package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealth(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	response := httptest.NewRecorder()

	NewRouter("test-version").ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, response.Code)
	}

	if got := response.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("expected JSON content type, got %q", got)
	}

	want := "{\"data\":{\"status\":\"ok\"},\"error\":null}\n"
	if got := response.Body.String(); got != want {
		t.Fatalf("expected body %q, got %q", want, got)
	}
}

func TestVersion(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/v1/version", nil)
	response := httptest.NewRecorder()

	NewRouter("test-version").ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, response.Code)
	}

	want := "{\"data\":{\"version\":\"test-version\"},\"error\":null}\n"
	if got := response.Body.String(); got != want {
		t.Fatalf("expected body %q, got %q", want, got)
	}
}
