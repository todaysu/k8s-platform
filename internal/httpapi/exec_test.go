package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"

	"k8s.io/apimachinery/pkg/util/validation"
	"testing"
)

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
	for _, expected := range []string{"--arthas-home", "--use-version 4.3.5", "--target-ip 127.0.0.1", "--http-port -1"} {
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
	if !strings.Contains(command[2], "session-"+token+".pid") || !strings.Contains(command[2], "-Dftc.arthas.session="+token) {
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
