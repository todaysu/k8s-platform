package httpapi

import (
	"context"
	"encoding/json"
	"example.com/k8s-platform/internal/cluster"
	"github.com/gorilla/websocket"
	"io"
	"net/http"
	"time"
)

type kubectlTTYRunner interface {
	RunKubectlTTY(context.Context, string, []string, io.Reader, io.Writer, <-chan cluster.TerminalSize) error
}

func clusterKubectlTTY(manager any) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		runner, ok := manager.(kubectlTTYRunner)
		if !ok {
			http.Error(w, "TTY unavailable", 503)
			return
		}
		upgrader := websocket.Upgrader{CheckOrigin: sameOrigin}
		connection, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer connection.Close()
		connection.SetReadLimit(64 * 1024)
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		socket := &terminalSocket{connection: connection}
		messages := make(chan struct {
			Type string
			Data string
			Args []string
			Cols uint16
			Rows uint16
		}, 16)
		go func() {
			defer cancel()
			for {
				_, raw, e := connection.ReadMessage()
				if e != nil {
					return
				}
				var m struct {
					Type string
					Data string
					Args []string
					Cols uint16
					Rows uint16
				}
				if json.Unmarshal(raw, &m) != nil {
					continue
				}
				select {
				case messages <- m:
				case <-ctx.Done():
					return
				}
			}
		}()
		var input *io.PipeWriter
		var reader *io.PipeReader
		var sizes chan cluster.TerminalSize
		finished := make(chan error, 1)
		running := false
		var stopInput func() bool
		defer func() {
			cancel()
			if input != nil {
				input.Close()
			}
			if reader != nil {
				reader.Close()
			}
			if running {
				<-finished
			}
		}()
		_ = socket.writeJSON(map[string]string{"type": "ready"})
		ticker := time.NewTicker(25 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if connection.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second)) != nil {
					return
				}
			case err := <-finished:
				running = false
				if stopInput != nil {
					stopInput()
				}
				input.Close()
				reader.Close()
				input = nil
				reader = nil
				if err != nil {
					_ = socket.writeJSON(map[string]string{"type": "error", "message": "命令已结束，请检查终端输出。"})
				}
				_ = socket.writeJSON(map[string]string{"type": "ready"})
			case m := <-messages:
				switch m.Type {
				case "run":
					if running {
						continue
					}
					if cluster.ValidateTTYArgs(m.Args) != nil {
						_ = socket.writeJSON(map[string]string{"type": "error", "message": "不支持该命令或参数。"})
						_ = socket.writeJSON(map[string]string{"type": "ready"})
						continue
					}
					reader, input = io.Pipe()
					rd, wr := reader, input
					stopInput = context.AfterFunc(ctx, func() { rd.Close(); wr.Close() })
					sizes = make(chan cluster.TerminalSize, 8)
					running = true
					go func(args []string, rd *io.PipeReader, sz chan cluster.TerminalSize) {
						finished <- runner.RunKubectlTTY(ctx, r.PathValue("id"), args, rd, socket, sz)
					}(m.Args, reader, sizes)
				case "input":
					if running && len(m.Data) <= 16384 {
						if _, e := io.WriteString(input, m.Data); e != nil {
							return
						}
					}
				case "resize":
					if running && m.Cols >= 20 && m.Cols <= 500 && m.Rows >= 5 && m.Rows <= 200 {
						select {
						case sizes <- cluster.TerminalSize{Width: m.Cols, Height: m.Rows}:
						default:
						}
					}
				case "close":
					return
				}
			}
		}
	}
}
