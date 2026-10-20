package cluster

import (
	"context"
	"encoding/json"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestMonitoringQueryBoundaries(t *testing.T) {
	now := time.Now()
	base := MonitoringQuery{View: "pods", Start: now.Add(-time.Hour), End: now, Namespace: "myapp", Pod: "web"}
	if err := ValidateMonitoringQuery(base); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*MonitoringQuery){func(q *MonitoringQuery) { q.Start = now.Add(-8 * 24 * time.Hour) }, func(q *MonitoringQuery) { q.Pod = "pod"; q.Namespace = "" }, func(q *MonitoringQuery) { q.Namespace = `app"},x="` }, func(q *MonitoringQuery) { q.View = "arbitrary" }, func(q *MonitoringQuery) { q.End = q.Start }} {
		q := base
		mutate(&q)
		if ValidateMonitoringQuery(q) == nil {
			t.Fatal(q)
		}
	}
	specs := monitoringSpecs(base)
	if !strings.Contains(specs[0].query, `namespace="myapp"`) || !strings.Contains(specs[0].query, `pod="web"`) {
		t.Fatal(specs[0].query)
	}
}
func TestMonitoringPreservesZeroAndGaps(t *testing.T) {
	rows, err := decodeMonitoring([]byte(`{"status":"success","data":{"resultType":"matrix","result":[{"metric":{"pod":"web"},"values":[[1,"0"],[2,"NaN"],[3,"0.5"]]}]}}`))
	if err != nil || len(rows) != 1 || rows[0].Points[0].Value == nil || *rows[0].Points[0].Value != 0 || rows[0].Points[1].Value != nil {
		t.Fatal(rows, err)
	}
	rows, err = decodeMonitoring([]byte(`{"status":"success","data":{"resultType":"matrix","result":[{"metric":{},"values":[[1,"NaN"]]}]}}`))
	if err != nil || len(rows) != 0 {
		t.Fatal(rows, err)
	}
}
func TestMonitoringSeriesLimit(t *testing.T) {
	var items []any
	for i := 0; i < 41; i++ {
		items = append(items, map[string]any{"metric": map[string]string{}, "values": [][]any{{1, "1"}}})
	}
	raw, _ := json.Marshal(map[string]any{"status": "success", "data": map[string]any{"resultType": "matrix", "result": items}})
	if _, err := decodeMonitoring(raw); err == nil {
		t.Fatal("expected curve limit")
	}
}
func TestMonitoringUsesServiceProxyAndHistory(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/services/http:prometheus:web/proxy/api/v1/query_range") || r.URL.Query().Get("step") != "30" || r.URL.Query().Get("start") == "" {
			t.Errorf("wrong request: %s", r.URL)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[{"metric":{"instance":"api"},"values":[[1,"0"]]}]}}`))
	}))
	defer server.Close()
	client, err := kubernetes.NewForConfig(&rest.Config{Host: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	panel := queryMonitoringPanel(context.Background(), client, prometheusService{namespace: "monitoring", proxyName: "http:prometheus:web"}, monitoringSpec{id: "test", query: "up"}, MonitoringQuery{Start: time.Now().Add(-time.Hour), End: time.Now()}, 30)
	if panel.Status != "available" {
		t.Fatal(panel)
	}
}
func TestPodMonitoringUsesIOInsteadOfStaticRequests(t *testing.T) {
	specs := monitoringSpecs(MonitoringQuery{View: "pods", Namespace: "myapp", Pod: "web"})
	if len(specs) != 6 {
		t.Fatal(len(specs))
	}
	for _, spec := range specs {
		if strings.Contains(spec.query, "kube_pod_container_resource_") {
			t.Fatal("static requests/limits should not have separate panels")
		}
		if !strings.Contains(spec.query, `namespace="myapp"`) || !strings.Contains(spec.query, `pod="web"`) {
			t.Fatal(spec.query)
		}
	}
	if specs[2].unit != "bytes/s" || specs[4].unit != "IOPS" {
		t.Fatal(specs)
	}
}
