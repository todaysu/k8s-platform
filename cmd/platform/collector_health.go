package main

import (
	"context"
	"log"
	"net/http"
	"time"
)

type databasePinger interface{ PingContext(context.Context) error }

// collectorHealth intentionally has no business API, SPA or authentication routes.
func collectorHealth(database databasePinger) http.Handler {
	health := http.NewServeMux()
	health.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	health.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if database.PingContext(ctx) != nil {
			http.Error(w, "database unavailable", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	return health
}

func startInternalHealth(ctx context.Context, addr string, database databasePinger) {
	handler := http.NewServeMux()
	handler.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	handler.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		if database != nil {
			check, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			defer cancel()
			if database.PingContext(check) != nil {
				http.Error(w, "database unavailable", 503)
				return
			}
		}
		w.WriteHeader(http.StatusOK)
	})
	server := &http.Server{Addr: addr, Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		stop, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(stop)
	}()
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("internal health server: %v", err)
		}
	}()
}
