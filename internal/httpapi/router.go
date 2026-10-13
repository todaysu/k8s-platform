package httpapi

import (
	"encoding/json"
	"net/http"
)

type response struct {
	Data  any `json:"data"`
	Error any `json:"error"`
}

func NewRouter(version string) http.Handler {
	router := http.NewServeMux()
	router.HandleFunc("GET /api/v1/health", health)
	router.HandleFunc("GET /api/v1/version", func(writer http.ResponseWriter, _ *http.Request) {
		writeVersion(writer, version)
	})
	return router
}

func health(writer http.ResponseWriter, _ *http.Request) {
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(response{
		Data: map[string]string{"status": "ok"},
	})
}

func writeVersion(writer http.ResponseWriter, version string) {
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(response{
		Data: map[string]string{"version": version},
	})
}
