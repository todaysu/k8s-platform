package httpapi

import (
	"bytes"
	"context"
	"example.com/k8s-platform/internal/cluster"
	"github.com/gorilla/websocket"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

type lockedLiveBuffer struct {
	bytes.Buffer
	mu sync.Mutex
}

func (b *lockedLiveBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Buffer.Write(p)
}

type arthasLiveManager struct {
	connector *cluster.KubernetesConnector
	parsed    cluster.ParsedKubeconfig
}

func (m arthasLiveManager) ExecPod(ctx context.Context, _ string, ns, pod string, opts cluster.PodExecOptions) error {
	return m.connector.ExecPod(ctx, m.parsed, ns, pod, opts)
}
func TestArthasLiveLocalDemo(t *testing.T) {
	if os.Getenv("FTC_ARTHAS_LIVE_TEST") != "1" {
		t.Skip("local opt-in")
	}
	raw, err := exec.Command("/usr/local/bin/kubectl", "--context", "kind-k8s-test", "config", "view", "--raw", "--minify").Output()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := cluster.ParseKubeconfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	name, err := exec.Command("/usr/local/bin/kubectl", "--context", "kind-k8s-test", "-n", "myapp", "get", "pods", "-l", "app=arthas-demo", "-o", "jsonpath={.items[0].metadata.name}").Output()
	if err != nil {
		t.Fatal(err)
	}
	manager := arthasLiveManager{cluster.NewKubernetesConnector(2 * time.Minute), parsed}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	var output lockedLiveBuffer
	if err := prepareArthas(ctx, manager, "", "myapp", string(name), "java", &output); err != nil {
		t.Fatalf("prepare: %v %s", err, output.String())
	}
	command := arthasCommand()
	command[2] = strings.TrimSpace(command[2]) + " 1 --command 'jvm;stop'\n"
	err = manager.ExecPod(ctx, "", "myapp", string(name), cluster.PodExecOptions{Container: "java", Command: command, DisableTTY: true, Stdin: strings.NewReader(""), Stdout: &output, Stderr: &output})
	_ = os.WriteFile("../../data/kind-test/arthas-demo-jvm.txt", output.Bytes(), 0600)
	if err != nil {
		t.Fatalf("attach: %v %s", err, output.String())
	}
	if !strings.Contains(output.String(), "RUNTIME") {
		t.Fatalf("missing JVM output: %s", output.String())
	}
}

func TestArthasLiveTTYLocalDemo(t *testing.T) {
	if os.Getenv("FTC_ARTHAS_LIVE_TEST") != "1" {
		t.Skip("local opt-in")
	}
	raw, err := exec.Command("/usr/local/bin/kubectl", "--context", "kind-k8s-test", "config", "view", "--raw", "--minify").Output()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := cluster.ParseKubeconfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	name, err := exec.Command("/usr/local/bin/kubectl", "--context", "kind-k8s-test", "-n", "myapp", "get", "pods", "-l", "app=arthas-demo", "-o", "jsonpath={.items[0].metadata.name}").Output()
	if err != nil {
		t.Fatal(err)
	}
	manager := arthasLiveManager{cluster.NewKubernetesConnector(2 * time.Minute), parsed}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/clusters/{id}/pods/{namespace}/{name}/exec", podExecHandler(manager, true))
	server := httptest.NewServer(mux)
	defer server.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/api/v1/clusters/local/pods/myapp/"+string(name)+"/exec?container=java&mode=arthas", http.Header{"Origin": []string{server.URL}})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(90 * time.Second))
	_ = conn.WriteJSON(terminalClientMessage{Type: "resize", Cols: 120, Rows: 32})
	ansi := regexp.MustCompile("\\x1b\\[[0-9;?]*[a-zA-Z]")
	var all strings.Builder
	pending := ""
	stage := 0
	selected := false
	for {
		typ, data, err := conn.ReadMessage()
		if err != nil {
			_ = os.WriteFile("../../data/kind-test/arthas-demo-tty.txt", []byte(all.String()), 0600)
			t.Fatalf("read stage %d: %v", stage, err)
		}
		if typ == websocket.TextMessage && strings.Contains(string(data), `"closed"`) {
			break
		}
		if typ != websocket.BinaryMessage {
			continue
		}
		value := ansi.ReplaceAllString(string(data), "")
		all.WriteString(value)
		pending += value
		if !selected && (strings.Contains(pending, "Input selection") || strings.Contains(pending, "[1]:")) {
			_ = conn.WriteJSON(terminalClientMessage{Type: "input", Data: "1\r"})
			selected = true
		}
		if !strings.Contains(pending, "[arthas@1]$") {
			continue
		}
		switch stage {
		case 0:
			_ = conn.WriteJSON(terminalClientMessage{Type: "input", Data: "trace demo.DiagnosticApp slowOperation '#cost > 100' -n 1\r"})
			stage = 1
			pending = ""
		case 1:
			if strings.Contains(pending, "demo.DiagnosticApp:slowOperation()") {
				_ = conn.WriteJSON(terminalClientMessage{Type: "input", Data: "watch demo.DiagnosticApp failingOperation throwExp -e -n 1 -x 1\r"})
				stage = 2
				pending = ""
			}
		case 2:
			if strings.Contains(pending, "IllegalStateException") {
				_ = conn.WriteJSON(terminalClientMessage{Type: "input", Data: "stop\r"})
				stage = 3
				pending = ""
			}
		}
	}
	_ = os.WriteFile("../../data/kind-test/arthas-demo-tty.txt", []byte(all.String()), 0600)
	if stage != 3 {
		t.Fatalf("expected trace and watch completion, stage=%d", stage)
	}
}
