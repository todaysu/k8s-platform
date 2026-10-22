package httpapi

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"example.com/k8s-platform/internal/arthasdist"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"example.com/k8s-platform/internal/cluster"
	"github.com/gorilla/websocket"
	"k8s.io/apimachinery/pkg/util/validation"
)

type terminalClientMessage struct {
	Type string `json:"type"`
	Data string `json:"data,omitempty"`
	Cols uint16 `json:"cols,omitempty"`
	Rows uint16 `json:"rows,omitempty"`
}

type terminalSocket struct {
	connection *websocket.Conn
	mutex      sync.Mutex
}

func (socket *terminalSocket) Write(data []byte) (int, error) {
	socket.mutex.Lock()
	defer socket.mutex.Unlock()
	_ = socket.connection.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if err := socket.connection.WriteMessage(websocket.BinaryMessage, data); err != nil {
		return 0, err
	}
	return len(data), nil
}
func (socket *terminalSocket) writeJSON(value any) error {
	socket.mutex.Lock()
	defer socket.mutex.Unlock()
	_ = socket.connection.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return socket.connection.WriteJSON(value)
}

func podExecHandler(manager clusterExecManager, enabled bool) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		if !enabled || manager == nil {
			writeAPIError(writer, http.StatusServiceUnavailable, cluster.CodePodExecUnavailable)
			return
		}
		mode := request.URL.Query().Get("mode")
		if mode != "" && mode != "arthas" {
			writeAPIError(writer, http.StatusBadRequest, "INVALID_TERMINAL_MODE")
			return
		}
		container := request.URL.Query().Get("container")
		shell := request.URL.Query().Get("shell")
		if shell == "" {
			shell = "sh"
		}
		// Validate the shell unconditionally: the arthas branch overwrites
		// Command today, but leaving one mode unchecked would silently turn
		// this argument into an injection surface if that ever changes.
		if len(validation.IsDNS1123Label(container)) > 0 || shell != "sh" && shell != "bash" {
			writeAPIError(writer, http.StatusBadRequest, cluster.CodeContainerNotFound)
			return
		}
		// Validate the path-derived object names here instead of relying on
		// client-go to reject them: a percent-encoded separator would decode
		// into "../../namespaces/<other>" and reach another namespace.
		namespace := request.PathValue("namespace")
		podName := request.PathValue("name")
		if len(validation.IsDNS1123Label(namespace)) > 0 || len(validation.IsDNS1123Subdomain(podName)) > 0 {
			writeAPIError(writer, http.StatusBadRequest, cluster.CodePodNotFound)
			return
		}
		prompt := ""
		if mode != "arthas" && podName != "" {
			prompt = podName + " $ "
		}
		options := cluster.PodExecOptions{Container: container, Shell: shell, Prompt: prompt, Stdin: nil, Stdout: nil, Stderr: nil, Sizes: nil}
		sessionToken := ""
		if mode == "arthas" {
			random := make([]byte, 16)
			if _, err := rand.Read(random); err != nil {
				writeAPIError(writer, http.StatusInternalServerError, "ARTHAS_SESSION_FAILED")
				return
			}
			sessionToken = hex.EncodeToString(random)
			options.Command = arthasSessionCommand(sessionToken)
			// Values are validated as DNS names above, so they cannot carry
			// newlines and forge extra log lines.
			log.Printf("arthas session cluster=%s pod=%s/%s container=%s", request.PathValue("id"), namespace, podName, container)
		}
		upgrader := websocket.Upgrader{CheckOrigin: sameOrigin, EnableCompression: false, ReadBufferSize: 4096, WriteBufferSize: 4096}
		connection, err := upgrader.Upgrade(writer, request, nil)
		if err != nil {
			return
		}
		defer connection.Close()
		connection.SetReadLimit(64 * 1024)
		var execContext context.Context
		var cancel context.CancelFunc
		if mode == "arthas" {
			execContext, cancel = context.WithTimeout(request.Context(), 30*time.Minute)
		} else {
			execContext, cancel = context.WithCancel(request.Context())
		}
		defer cancel()
		stdinReader, stdinWriter := io.Pipe()
		defer stdinReader.Close()
		defer stdinWriter.Close()
		sizes := make(chan cluster.TerminalSize, 8)
		defer close(sizes)
		socket := &terminalSocket{connection: connection}
		finished := make(chan error, 1)
		executionDone := make(chan struct{})
		if sessionToken != "" {
			defer func() {
				cancel()
				_ = stdinWriter.Close()
				select {
				case <-executionDone:
				case <-time.After(2 * time.Second):
				}
				cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 8*time.Second)
				defer cleanupCancel()
				_ = cleanupArthasClient(cleanupCtx, manager, request.PathValue("id"), namespace, podName, container, sessionToken)
			}()
		}
		go func() {
			defer close(executionDone)
			options.Stdin = stdinReader
			options.Stdout = socket
			options.Stderr = socket
			options.Sizes = sizes
			if mode == "arthas" {
				if err := prepareArthas(execContext, manager, request.PathValue("id"), namespace, podName, container, socket); err != nil {
					finished <- err
					return
				}
			}
			finished <- manager.ExecPod(execContext, request.PathValue("id"), namespace, podName, options)
		}()
		messages := make(chan terminalClientMessage, 16)
		disconnected := make(chan struct{})
		go func() {
			defer close(disconnected)
			for {
				_, content, readErr := connection.ReadMessage()
				if readErr != nil {
					return
				}
				var message terminalClientMessage
				if json.Unmarshal(content, &message) == nil {
					select {
					case messages <- message:
					case <-execContext.Done():
						return
					}
				}
			}
		}()

		for {
			select {
			case execErr := <-finished:
				if execErr != nil && execContext.Err() == nil {
					message := errorMessage(cluster.ErrorCode(execErr))
					if mode == "arthas" && cluster.ErrorCode(execErr) == cluster.CodeConnectionFailed {
						message = "Arthas 进程已退出或终端通道中断，请查看终端输出后重新连接。"
					}
					_ = socket.writeJSON(map[string]string{"type": "error", "message": message})
				}
				_ = socket.writeJSON(map[string]string{"type": "closed"})
				return
			case <-disconnected:
				cancel()
				return
			case message := <-messages:
				switch message.Type {
				case "input":
					if len(message.Data) > 16*1024 {
						continue
					}
					if _, err := io.WriteString(stdinWriter, message.Data); err != nil {
						cancel()
						return
					}
				case "resize":
					if message.Cols < 20 || message.Cols > 500 || message.Rows < 5 || message.Rows > 200 {
						continue
					}
					select {
					case sizes <- cluster.TerminalSize{Width: message.Cols, Height: message.Rows}:
					default:
					}
				case "close":
					cancel()
					return
				}
			}
		}
	}
}

// loopbackWebSocketOrigin is set once at startup for local development. It is
// false by default so a browser page served from any loopback port cannot open
// an authenticated terminal channel to a platform that happens to run locally.
var loopbackWebSocketOrigin atomic.Bool

// SetLoopbackWebSocketOrigin configures the loopback exception. Callers pass
// cfg.AllowLoopbackWSOrigin; production deployments must leave it disabled.
func SetLoopbackWebSocketOrigin(enabled bool) { loopbackWebSocketOrigin.Store(enabled) }

func sameOrigin(request *http.Request) bool {
	origin := request.Header.Get("Origin")
	if origin == "" {
		return false
	}
	parsed, err := url.Parse(origin)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return false
	}
	if parsed.Host == request.Host {
		return true
	}
	// Local development serves the SPA from Vite (for example :5173) and
	// proxies /api to the platform (:8080). That exception is opt-in because a
	// WebSocket handshake is not covered by the browser same-origin policy.
	if !loopbackWebSocketOrigin.Load() {
		return false
	}
	return isLoopbackHost(parsed.Hostname()) && isLoopbackHost(hostWithoutPort(request.Host))
}

func isLoopbackHost(host string) bool {
	host = strings.Trim(strings.TrimSpace(host), "[]")
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func hostWithoutPort(host string) string {
	if parsed, _, err := net.SplitHostPort(host); err == nil {
		return parsed
	}
	return host
}

func prepareArthas(ctx context.Context, manager clusterExecManager, id, namespace, pod, container string, output io.Writer) error {
	payload, err := arthasdist.Payload()
	if err != nil {
		return err
	}
	_, _ = io.WriteString(output, "[platform] 正在通过 Kubernetes Exec 传输内置 Arthas "+arthasdist.Version+"，无需容器联网…\r\n")
	script := `set -eu
umask 077
command -v java >/dev/null 2>&1 || { echo '目标容器缺少 java'; exit 1; }
command -v tar >/dev/null 2>&1 || { echo '目标容器缺少 tar，无法传入离线包'; exit 1; }
case "$(uname -m)" in x86_64|amd64|aarch64|arm64) ;; *) echo '仅支持 Linux x86_64 / ARM64'; exit 1 ;; esac
ROOT=` + shellQuote(arthasdist.Home) + `
if [ -s "$ROOT/arthas-core.jar" ] && [ -s "$ROOT/arthas-boot.jar" ]; then cat >/dev/null; exit 0; fi
STAGE=$(mktemp -d /tmp/ftc-arthas-stage.XXXXXX)
trap 'rm -rf "$STAGE"' EXIT
tar -xzf - -C "$STAGE"
test -s "$STAGE/arthas-core.jar"
mkdir -p "$ROOT"
cp -R "$STAGE"/. "$ROOT"/
`
	transferCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	return manager.ExecPod(transferCtx, id, namespace, pod, cluster.PodExecOptions{Container: container, Command: []string{"/bin/sh", "-c", script}, DisableTTY: true, Stdin: bytes.NewReader(payload), Stdout: output, Stderr: output})
}

func arthasCommand() []string {
	script := `set -eu
export HOME=/tmp
echo "[platform] 启动内置 Arthas ` + arthasdist.Version + `；请选择目标 Java 进程的编号。"
exec java -Duser.home=/tmp -jar ` + shellQuote(arthasdist.Home+"/arthas-boot.jar") + ` --arthas-home ` + shellQuote(arthasdist.Home) + ` --use-version ` + arthasdist.Version + ` --target-ip 127.0.0.1 --http-port -1
`
	return []string{"/bin/sh", "-c", script}
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

// Each WebSocket owns one launcher process. A dropped exec stream does not reliably
// terminate it in Kubernetes; kill only a PID whose command line carries our token.
func arthasSessionCommand(token string) []string {
	command := arthasCommand()
	command[2] = strings.Replace(command[2], "exec java -Duser.home=/tmp", "echo $$ > "+shellQuote(arthasdist.Home+"/session-"+token+".pid")+"\nexec java -Dftc.arthas.session="+token+" -Duser.home=/tmp", 1)
	return command
}
func cleanupArthasClient(ctx context.Context, manager clusterExecManager, id, namespace, pod, container, token string) error {
	script := `set -eu
FILE=` + shellQuote(arthasdist.Home+"/session-"+token+".pid") + `
TOKEN=` + shellQuote(token) + `
[ -f "$FILE" ] || exit 0
PID=$(cat "$FILE")
case "$PID" in ''|*[!0-9]*) rm -f "$FILE"; exit 0 ;; esac
if [ -r "/proc/$PID/cmdline" ]; then
  COMMAND=$(tr '\000' ' ' < "/proc/$PID/cmdline")
  case "$COMMAND" in *"-Dftc.arthas.session=$TOKEN "*) kill -TERM "$PID" 2>/dev/null || true ;; esac
fi
rm -f "$FILE"
`
	return manager.ExecPod(ctx, id, namespace, pod, cluster.PodExecOptions{Container: container, Command: []string{"/bin/sh", "-c", script}, DisableTTY: true, Stdin: strings.NewReader(""), Stdout: io.Discard, Stderr: io.Discard})
}
