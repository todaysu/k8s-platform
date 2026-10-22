package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestAdmissionSeparatesSessionsFromReadsAndReleasesSlots(t *testing.T) {
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	done := make(chan struct{})
	a := newAdmission(AdmissionLimits{Sessions: 1})
	handler := a.wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/clusters/c1/kubectl/tty" {
			entered <- struct{}{}
			<-release
		}
		w.WriteHeader(http.StatusOK)
	}))
	go func() {
		defer close(done)
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/v1/clusters/c1/kubectl/tty", nil))
	}()
	<-entered
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/clusters/c2/kubectl/tty", nil))
	if w.Code != 429 || w.Header().Get("Retry-After") == "" {
		t.Fatalf("missing backpressure: %d", w.Code)
	}
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/clusters", nil))
	if w.Code != 200 {
		t.Fatal("session consumed normal read slots")
	}
	close(release)
	<-done
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.lanes[3].active != 0 || len(a.lanes[3].clusters) != 0 {
		t.Fatal("session quota leaked")
	}
}

func TestAdmissionPerClusterIsolation(t *testing.T) {
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	done := make(chan struct{})
	handler := newAdmission(AdmissionLimits{Monitoring: 2, PerCluster: 1}).wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/clusters/slow/monitoring" {
			entered <- struct{}{}
			<-release
		}
		w.WriteHeader(200)
	}))
	go func() {
		defer close(done)
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/v1/clusters/slow/monitoring", nil))
	}()
	<-entered
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/clusters/slow/monitoring", nil))
	if w.Code != 429 {
		t.Fatal("same cluster was not bounded")
	}
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/clusters/healthy/monitoring", nil))
	if w.Code != 200 {
		t.Fatal("healthy cluster blocked")
	}
	close(release)
	<-done
}

type unavailableQuota struct{ calls atomic.Int32 }

func (q *unavailableQuota) AcquireRequestQuota(context.Context, string, string, int, int, time.Duration) (string, error) {
	q.calls.Add(1)
	return "", errors.New("database offline")
}
func (*unavailableQuota) RenewRequestQuota(context.Context, string, time.Duration) error {
	return errors.New("database offline")
}
func (*unavailableQuota) ReleaseRequestQuota(context.Context, string) error { return nil }
func TestSharedQuotaFailsClosedOnlyForExpensiveRequests(t *testing.T) {
	q := &unavailableQuota{}
	a := newAdmission(AdmissionLimits{})
	a.shared = q
	var calls atomic.Int32
	handler := a.wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(200) }))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/clusters/one/monitoring", nil))
	if w.Code != 503 || calls.Load() != 0 {
		t.Fatal("expensive request bypassed unavailable shared quota")
	}
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/clusters", nil))
	if w.Code != 200 || calls.Load() != 1 || q.calls.Load() != 1 {
		t.Fatal("normal reads used expensive quota")
	}
}
