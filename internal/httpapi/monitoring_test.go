package httpapi

import (
	"context"
	"example.com/k8s-platform/internal/cluster"
	"net/http/httptest"
	"strings"
	"testing"
)

type monitorFixture struct{}

func (monitorFixture) GetMonitoring(context.Context, string, cluster.MonitoringQuery) (cluster.MonitoringSnapshot, error) {
	return cluster.MonitoringSnapshot{}, nil
}
func TestMonitoringRejectsUnboundedQueries(t *testing.T) {
	r := httptest.NewRequest("GET", "/clusters/a/monitoring?view=pods", nil)
	w := httptest.NewRecorder()
	monitoringHandler(monitorFixture{}).ServeHTTP(w, r)
	if w.Code != 400 {
		t.Fatal(w.Code)
	}
}

type missingMonitorFixture struct{}

func (missingMonitorFixture) GetMonitoring(context.Context, string, cluster.MonitoringQuery) (cluster.MonitoringSnapshot, error) {
	return cluster.MonitoringSnapshot{}, &cluster.MonitoringError{Code: "MONITORING_NOT_CONFIGURED", Message: "该集群未发现 Prometheus 数据源。"}
}
func TestMonitoringPreservesSpecificFailure(t *testing.T) {
	r := httptest.NewRequest("GET", "/clusters/a/monitoring?view=overview&start=2026-10-18T00:00:00Z&end=2026-10-19T00:00:00Z", nil)
	w := httptest.NewRecorder()
	monitoringHandler(missingMonitorFixture{}).ServeHTTP(w, r)
	if w.Code != 503 || !strings.Contains(w.Body.String(), "MONITORING_NOT_CONFIGURED") {
		t.Fatal(w.Code, w.Body.String())
	}
}
