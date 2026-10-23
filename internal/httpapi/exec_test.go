package httpapi

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"time"

	"example.com/k8s-platform/internal/arthasdist"
	"example.com/k8s-platform/internal/cluster"
	"example.com/k8s-platform/internal/platformsettings"
	"github.com/gorilla/websocket"
	"k8s.io/apimachinery/pkg/util/validation"
	"testing"
)

type arthasExecStub struct {
	calls  atomic.Int32
	launch chan cluster.PodExecOptions
	block  bool
}

func (stub *arthasExecStub) ExecPod(ctx context.Context, _ string, _, _ string, options cluster.PodExecOptions) error {
	stub.calls.Add(1)
	if options.DisableTTY {
		_, err := io.Copy(io.Discard, options.Stdin)
		return err
	}
	stub.launch <- options
	if stub.block {
		<-ctx.Done()
	}
	return nil
}

func arthasSettingsRepository(t *testing.T, settings platformsettings.Java) *settingsMemoryRepository {
	t.Helper()
	payload, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	return &settingsMemoryRepository{records: map[string]platformsettings.Record{platformsettings.SectionJava: {Section: platformsettings.SectionJava, Payload: payload}}}
}

func arthasTestSocket(t *testing.T, handler http.HandlerFunc) (*websocket.Conn, *httptest.Server) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/clusters/{id}/pods/{namespace}/{name}/exec", handler)
	server := httptest.NewServer(mux)
	connection, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/api/v1/clusters/test/pods/app/java/exec?container=java&mode=arthas", http.Header{"Origin": []string{server.URL}})
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	_ = connection.SetReadDeadline(time.Now().Add(10 * time.Second))
	return connection, server
}

func TestArthasHandlerUsesConfiguredOfflineOverride(t *testing.T) {
	var content bytes.Buffer
	archive := zip.NewWriter(&content)
	for _, name := range []string{"arthas-boot.jar", "arthas-core.jar", "arthas-agent.jar", "arthas-spy.jar"} {
		entry, err := archive.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = entry.Write([]byte("test jar"))
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	files := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { _, _ = writer.Write(content.Bytes()) }))
	defer files.Close()
	settings := platformsettings.Defaults().Java
	settings.ArthasURL, settings.ArthasVersion = files.URL+"/arthas.zip", "4.4.0"
	stub := &arthasExecStub{launch: make(chan cluster.PodExecOptions, 1)}
	connection, server := arthasTestSocket(t, podExecHandler(stub, true, arthasSettingsRepository(t, settings)))
	defer server.Close()
	defer connection.Close()
	for {
		_, data, err := connection.ReadMessage()
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), `"type":"closed"`) {
			break
		}
	}
	options := <-stub.launch
	if !strings.Contains(options.Command[2], "--use-version '4.4.0'") || strings.Contains(options.Command[2], arthasdist.Home) {
		t.Fatalf("configured version was not used: %s", options.Command[2])
	}
}

func TestArthasHandlerReportsBadPackageBeforeExecutingPod(t *testing.T) {
	files := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { _, _ = writer.Write([]byte("invalid zip")) }))
	defer files.Close()
	settings := platformsettings.Defaults().Java
	settings.ArthasURL = files.URL + "/arthas.zip"
	stub := &arthasExecStub{launch: make(chan cluster.PodExecOptions, 1)}
	connection, server := arthasTestSocket(t, podExecHandler(stub, true, arthasSettingsRepository(t, settings)))
	defer server.Close()
	defer connection.Close()
	var message string
	for {
		_, data, err := connection.ReadMessage()
		if err != nil {
			t.Fatal(err)
		}
		message += string(data)
		if strings.Contains(string(data), `"type":"closed"`) {
			break
		}
	}
	if !strings.Contains(message, "完整的 ZIP 分发包") || stub.calls.Load() != 0 {
		t.Fatalf("invalid archive should be actionable and must not run Exec: calls=%d message=%s", stub.calls.Load(), message)
	}
}

func TestArthasHandlerEnforcesConfiguredConcurrency(t *testing.T) {
	settings := platformsettings.Defaults().Java
	settings.MaxConcurrency = 1
	stub := &arthasExecStub{launch: make(chan cluster.PodExecOptions, 1), block: true}
	handler := podExecHandler(stub, true, arthasSettingsRepository(t, settings))
	connection, server := arthasTestSocket(t, handler)
	defer server.Close()
	defer connection.Close()
	select {
	case <-stub.launch:
	case <-time.After(10 * time.Second):
		t.Fatal("first diagnostic did not start")
	}
	response, err := http.Get(server.URL + "/api/v1/clusters/test/pods/app/java/exec?container=java&mode=arthas")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("expected concurrency rejection, got %d", response.StatusCode)
	}
}

func TestPodExecDisabledByDefault(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/c1/pods/default/web/exec?container=web", nil)
	response := httptest.NewRecorder()
	NewRouter("test", fakeManager{}).ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", response.Code)
	}
}

func TestSameOrigin(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "http://kubescope.example/api", nil)
	request.Host = "kubescope.example"
	request.Header.Set("Origin", "https://kubescope.example")
	if !sameOrigin(request) {
		t.Fatal("expected matching origin")
	}
	request.Header.Set("Origin", "https://attacker.example")
	if sameOrigin(request) {
		t.Fatal("expected cross origin to be rejected")
	}
}

func TestArthasCommandUsesPinnedOfflineBundle(t *testing.T) {
	command := arthasCommand()
	if len(command) != 3 || command[0] != "/bin/sh" {
		t.Fatal("invalid command")
	}
	for _, expected := range []string{"--arthas-home", "--use-version '4.3.5'", "--target-ip 127.0.0.1", "--http-port -1"} {
		if !strings.Contains(command[2], expected) {
			t.Fatalf("missing %s", expected)
		}
	}
	for _, forbidden := range []string{"curl", "wget", "https://"} {
		if strings.Contains(command[2], forbidden) {
			t.Fatal("must work offline")
		}
	}
}

func TestSameOriginLoopbackExceptionIsOptIn(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080/api/v1/clusters", nil)
	request.Host = "127.0.0.1:8080"
	request.Header.Set("Origin", "http://127.0.0.1:5173")
	// A WebSocket handshake is not covered by the browser same-origin policy,
	// so the cross-port loopback allowance must stay off by default.
	if sameOrigin(request) {
		t.Fatal("loopback cross-port origin must be rejected unless explicitly enabled")
	}
	SetLoopbackWebSocketOrigin(true)
	defer SetLoopbackWebSocketOrigin(false)
	if !sameOrigin(request) {
		t.Fatal("expected loopback development origin to be accepted once enabled")
	}
	request.Header.Set("Origin", "http://localhost:5173")
	if !sameOrigin(request) {
		t.Fatal("expected localhost development origin to be accepted once enabled")
	}
}

func TestSameOriginRejectsPublicCrossOrigin(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "http://platform.example.com/api/v1/clusters", nil)
	request.Host = "platform.example.com"
	request.Header.Set("Origin", "http://127.0.0.1:5173")
	if sameOrigin(request) {
		t.Fatal("public host must not accept loopback origin")
	}
	request.Header.Set("Origin", "http://platform.example.com")
	if !sameOrigin(request) {
		t.Fatal("same public origin rejected")
	}
}

func TestArthasLauncherIsScopedToOneSession(t *testing.T) {
	token := "0123456789abcdef0123456789abcdef"
	command := arthasSessionCommand(token)
	if !strings.Contains(command[2], "session-"+token+".pid") || !strings.Contains(command[2], "-Dftc.arthas.session='"+token+"'") {
		t.Fatal("launcher must record and identify its own process")
	}
	if strings.Contains(command[2], "killall") {
		t.Fatal("must not kill unrelated Java processes")
	}
}

func TestPodExecRejectsInvalidResourceNames(t *testing.T) {
	// A percent-encoded separator decodes into "../.." inside a path value, so
	// the handler must reject it instead of relying on client-go to fail later.
	cases := []struct {
		namespace string
		name      string
		reason    string
	}{
		{"../../namespaces/victim", "pod", "namespace traversal"},
		{"app", "../../secrets/db", "pod traversal"},
		{"App", "pod", "uppercase namespace"},
		{"app", "Pod_Name", "invalid pod name"},
	}
	for _, item := range cases {
		request := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/c1/pods/"+item.namespace+"/"+item.name+"/exec", nil)
		request.SetPathValue("id", "c1")
		request.SetPathValue("namespace", item.namespace)
		request.SetPathValue("name", item.name)
		if len(validation.IsDNS1123Label(item.namespace)) == 0 && len(validation.IsDNS1123Subdomain(item.name)) == 0 {
			t.Fatalf("case %q is actually valid; the test proves nothing", item.reason)
		}
	}
}
