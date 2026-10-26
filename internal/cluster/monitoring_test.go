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
	for _, mutate := range []func(*MonitoringQuery){func(q *MonitoringQuery) { q.Start = now.Add(-8 * 24 * time.Hour) }, func(q *MonitoringQuery) { q.Pod = "pod"; q.Namespace = "" }, func(q *MonitoringQuery) { q.Namespace = `app"},x="` }, func(q *MonitoringQuery) { q.View = "arbitrary" }, func(q *MonitoringQuery) { q.Node = "worker-1" }, func(q *MonitoringQuery) { q.End = q.Start }} {
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
	rows, err := decodeMonitoring([]byte(`{"status":"success","data":{"resultType":"matrix","result":[{"metric":{"pod":"web"},"values":[[1,"0"],[2,"NaN"],[3,"0.5"]]}]}}`), 40, "pods")
	if err != nil || len(rows) != 1 || rows[0].Points[0].Value == nil || *rows[0].Points[0].Value != 0 || rows[0].Points[1].Value != nil {
		t.Fatal(rows, err)
	}
	rows, err = decodeMonitoring([]byte(`{"status":"success","data":{"resultType":"matrix","result":[{"metric":{},"values":[[1,"NaN"]]}]}}`), 40, "pods")
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
	if _, err := decodeMonitoring(raw, 40, "pods"); err == nil {
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

// 节点性能视图需要覆盖 CPU、内存、文件系统、磁盘 I/O 与网络，
// 并且按节点分组，否则多台主机会合并成一条无法定位的曲线。
func TestNodeMonitoringViewExposesHostPanels(t *testing.T) {
	now := time.Now()
	q := MonitoringQuery{View: "nodes", Start: now.Add(-time.Hour), End: now}
	if err := ValidateMonitoringQuery(q); err != nil {
		t.Fatalf("nodes view must be accepted: %v", err)
	}
	specs := monitoringSpecs(q)
	queries := map[string]string{}
	for _, spec := range specs {
		queries[spec.id] = spec.query
		if spec.title == "" || spec.unit == "" || spec.hint == "" || spec.query == "" {
			t.Errorf("panel %s must describe itself: %#v", spec.id, spec)
		}
		if !strings.Contains(spec.query, "by(node,instance)") {
			t.Errorf("panel %s must group by node and instance: %s", spec.id, spec.query)
		}
		// 不同发行版的 node-exporter job 名不一致，绑定 job 会让面板在健康集群上空白。
		if strings.Contains(spec.query, "job=") {
			t.Errorf("panel %s must not pin the job label: %s", spec.id, spec.query)
		}
	}
	for _, required := range []string{
		"node_cpu_usage", "node_load1", "node_memory_usage", "node_filesystem_usage",
		"node_disk_read", "node_disk_write", "node_disk_iops", "node_disk_utilization",
		"node_network_receive", "node_network_transmit", "node_network_errors",
	} {
		if _, ok := queries[required]; !ok {
			t.Errorf("missing host panel %s", required)
		}
	}
	// 虚拟文件系统与 CNI 设备参与计算会严重失真，排除条件必须存在。
	if !strings.Contains(queries["node_filesystem_usage"], "fstype!~") {
		t.Error("filesystem panel must exclude virtual filesystems")
	}
	for _, id := range []string{"node_network_receive", "node_network_transmit", "node_network_errors"} {
		if !strings.Contains(queries[id], "device!~") {
			t.Errorf("%s must exclude virtual network devices", id)
		}
	}
	if strings.Contains(queries["node_filesystem_usage"], "sum by(node,instance)") || !strings.Contains(queries["node_filesystem_usage"], "max by(node,instance)") {
		t.Error("filesystem panel must expose the fullest real filesystem per node")
	}
	if !strings.Contains(queries["node_network_errors"], "receive_drop_total") || !strings.Contains(queries["node_network_errors"], "transmit_drop_total") {
		t.Error("network error panel must include dropped packets")
	}
}

func TestNodeMonitoringCanFilterAtPrometheus(t *testing.T) {
	now := time.Now()
	q := MonitoringQuery{View: "nodes", Node: "worker-1", Start: now.Add(-time.Hour), End: now}
	if err := ValidateMonitoringQuery(q); err != nil {
		t.Fatal(err)
	}
	for _, spec := range monitoringSpecs(q) {
		if !strings.Contains(spec.query, `node="worker-1"`) {
			t.Errorf("panel %s does not filter selected node: %s", spec.id, spec.query)
		}
	}
	q.Node = `worker-1"}`
	if ValidateMonitoringQuery(q) == nil {
		t.Fatal("expected invalid node name")
	}
}
