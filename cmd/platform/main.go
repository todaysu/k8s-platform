package main

import (
	"errors"
	"log"
	"net/http"
	"time"

	"example.com/k8s-platform/internal/config"
	"example.com/k8s-platform/internal/httpapi"
)

const platformVersion = "0.1.0-dev"

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}

	server := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           httpapi.NewRouter(platformVersion),
		ReadHeaderTimeout: 5 * time.Second,
	}

	log.Printf("platform listening on %s", cfg.ListenAddr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
