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

// 前端发版后 index.html 必须重新验证，否则浏览器会一直加载旧入口，
// 用户看到的是上一版界面并误报功能缺失。
func TestSPAHandlerCacheHeaders(t *testing.T) {
	directory := t.TempDir()
	if err := os.MkdirAll(filepath.Join(directory, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "index.html"), []byte("<html>entry</html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "assets", "index-abc123.js"), []byte("console.log(1)"), 0o644); err != nil {
		t.Fatal(err)
	}
	handler := NewSPAHandler(directory)

	cases := []struct {
		path string
		want string
	}{
		// http.FileServer 会把 /index.html 规范化为 301 跳转到 /，
		// 两种情况都必须带 no-cache，浏览器才会重新验证入口文档。
		{"/", "no-cache"},
		{"/index.html", "no-cache"},
		{"/clusters/abc", "no-cache"},
		{"/assets/index-abc123.js", "public, max-age=31536000, immutable"},
	}
	for _, item := range cases {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, item.path, nil))
		if got := recorder.Header().Get("Cache-Control"); got != item.want {
			t.Errorf("%s: Cache-Control = %q, want %q", item.path, got, item.want)
		}
		if recorder.Code != http.StatusOK && recorder.Code != http.StatusMovedPermanently {
			t.Errorf("%s: unexpected status %d", item.path, recorder.Code)
		}
	}
}
