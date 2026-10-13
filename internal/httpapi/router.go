package httpapi

import (
	"encoding/json"
	"net/http"
)

type response struct {
	Data  any `json:"data"`
	Error any `json:"error"`
}

func NewRouter() http.Handler {
	router := http.NewServeMux()
	router.HandleFunc("GET /api/v1/health", health)
	return router
}

func health(writer http.ResponseWriter, _ *http.Request) {
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(response{
		Data: map[string]string{"status": "ok"},
	})
}
