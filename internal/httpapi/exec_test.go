package httpapi

import (
	"net/http"
	"net/http/httptest"
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
