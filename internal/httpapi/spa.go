package httpapi

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// NewSPAHandler serves the built React application and falls back to
// index.html for client-side routes. API traffic must be mounted separately.
func NewSPAHandler(directory string) http.Handler {
	files := http.FileServer(http.Dir(directory))
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		clean := strings.TrimPrefix(filepath.Clean("/"+strings.TrimPrefix(request.URL.Path, "/")), "/")
		if clean == "." {
			clean = "index.html"
		}
		path := filepath.Join(directory, clean)
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			files.ServeHTTP(writer, request)
			return
		}
		http.ServeFile(writer, request, filepath.Join(directory, "index.html"))
	})
}
