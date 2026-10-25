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
		applyStaticCacheHeaders(writer, clean)
		path := filepath.Join(directory, clean)
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			files.ServeHTTP(writer, request)
			return
		}
		http.ServeFile(writer, request, filepath.Join(directory, "index.html"))
	})
}

// applyStaticCacheHeaders separates hashed build assets from the entry
// document. Files under assets/ carry a content hash in their name and can be
// cached indefinitely. Everything else, especially index.html, must be
// revalidated on every request: a cached entry document keeps pointing at the
// previous build, so users never see newly deployed features and report them
// as missing.
func applyStaticCacheHeaders(writer http.ResponseWriter, clean string) {
	if strings.HasPrefix(clean, "assets/") {
		writer.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		return
	}
	writer.Header().Set("Cache-Control", "no-cache")
}
