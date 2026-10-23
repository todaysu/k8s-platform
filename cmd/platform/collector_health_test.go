package main

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
)

type fakePinger struct{ err error }

func (db fakePinger) PingContext(context.Context) error { return db.err }
func TestCollectorNeverServesBusinessRoutes(t *testing.T) {
	for _, path := range []string{"/api/v1/clusters", "/api/v1/auth/login", "/tools", "/"} {
		w := httptest.NewRecorder()
		collectorHealth(fakePinger{}).ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 404 {
			t.Fatalf("business route exposed: %s %d", path, w.Code)
		}
	}
	for _, test := range []struct {
		path string
		err  error
		want int
	}{{"/healthz", nil, 200}, {"/readyz", nil, 200}, {"/readyz", errors.New("offline"), 503}, {"/healthz", errors.New("offline"), 200}} {
		w := httptest.NewRecorder()
		collectorHealth(fakePinger{test.err}).ServeHTTP(w, httptest.NewRequest("GET", test.path, nil))
		if w.Code != test.want {
			t.Fatalf("probe %s got %d", test.path, w.Code)
		}
	}
}
