package httpapi

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestSPAHandlerServesAssetsAndFallsBackToIndex(t *testing.T) {
	directory := t.TempDir()
	if err := os.MkdirAll(filepath.Join(directory, "assets"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "index.html"), []byte("FTC UI"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "assets", "app.js"), []byte("asset"), 0o600); err != nil {
		t.Fatal(err)
	}
	handler := NewSPAHandler(directory)
	for _, test := range []struct {
		path, body string
		status     int
	}{{"/assets/app.js", "asset", http.StatusOK}, {"/clusters/example/workloads", "FTC UI", http.StatusOK}, {"/../../etc/passwd", "invalid URL path\n", http.StatusBadRequest}} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, test.path, nil))
		if response.Code != test.status || response.Body.String() != test.body {
			t.Fatalf("%s returned %d %q", test.path, response.Code, response.Body.String())
		}
	}
}
