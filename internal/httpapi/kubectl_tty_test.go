package httpapi

import (
	"context"
	"example.com/k8s-platform/internal/cluster"
	"github.com/gorilla/websocket"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type ttyFixture struct{ stopped chan struct{} }

func (f ttyFixture) RunKubectlTTY(ctx context.Context, _ string, _ []string, input io.Reader, out io.Writer, _ <-chan cluster.TerminalSize) error {
	defer close(f.stopped)
	io.WriteString(out, "stream-ready")
	<-ctx.Done()
	return ctx.Err()
}
func TestTTYSocketStreamsAndCancels(t *testing.T) {
	f := ttyFixture{make(chan struct{})}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /clusters/{id}/tty", clusterKubectlTTY(f))
	server := httptest.NewServer(mux)
	defer server.Close()
	header := http.Header{"Origin": []string{server.URL}}
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/clusters/a/tty", header)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, _, err = conn.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	conn.WriteJSON(map[string]any{"type": "run", "args": []string{"get", "pods", "-w"}})
	kind, data, err := conn.ReadMessage()
	if err != nil || kind != websocket.BinaryMessage || string(data) != "stream-ready" {
		t.Fatal(kind, string(data), err)
	}
	conn.Close()
	select {
	case <-f.stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("session not canceled")
	}
}
