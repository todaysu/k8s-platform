package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"sync"
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
		container := request.URL.Query().Get("container")
		shell := request.URL.Query().Get("shell")
		if shell == "" {
			shell = "sh"
		}
		if len(validation.IsDNS1123Label(container)) > 0 || (shell != "sh" && shell != "bash") {
			writeAPIError(writer, http.StatusBadRequest, cluster.CodeContainerNotFound)
			return
		}
		upgrader := websocket.Upgrader{CheckOrigin: sameOrigin, EnableCompression: false, ReadBufferSize: 4096, WriteBufferSize: 4096}
		connection, err := upgrader.Upgrade(writer, request, nil)
		if err != nil {
			return
		}
		defer connection.Close()
		connection.SetReadLimit(64 * 1024)
		execContext, cancel := context.WithCancel(request.Context())
		defer cancel()
		stdinReader, stdinWriter := io.Pipe()
		defer stdinReader.Close()
		defer stdinWriter.Close()
		sizes := make(chan cluster.TerminalSize, 8)
		defer close(sizes)
		socket := &terminalSocket{connection: connection}
		finished := make(chan error, 1)
		go func() {
			finished <- manager.ExecPod(execContext, request.PathValue("id"), request.PathValue("namespace"), request.PathValue("name"), cluster.PodExecOptions{Container: container, Shell: shell, Stdin: stdinReader, Stdout: socket, Stderr: socket, Sizes: sizes})
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
					_ = socket.writeJSON(map[string]string{"type": "error", "message": errorMessage(cluster.ErrorCode(execErr))})
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

func sameOrigin(request *http.Request) bool {
	origin := request.Header.Get("Origin")
	if origin == "" {
		return false
	}
	parsed, err := url.Parse(origin)
	return err == nil && parsed.Host == request.Host && (parsed.Scheme == "http" || parsed.Scheme == "https")
}
