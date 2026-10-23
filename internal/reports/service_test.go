package reports

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"example.com/k8s-platform/internal/cluster"
	_ "modernc.org/sqlite"
)

type reportSource struct {
	records  []cluster.ClusterRecord
	capacity cluster.FleetCapacity
}

func (source reportSource) ListRecords(context.Context) ([]cluster.ClusterRecord, error) {
	return source.records, nil
}

func (source reportSource) ListFleetCapacitySnapshots(context.Context) (cluster.FleetCapacity, error) {
	return source.capacity, nil
}

func openReportDB(t *testing.T, name string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+name+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestPeriodWindowUsesNaturalShanghaiPeriods(t *testing.T) {
	now := time.Date(2026, time.October, 23, 12, 0, 0, 0, reportZone)
	tests := []struct {
		period, date, start, end string
	}{
		{"weekly", "2026-10-22", "2026-10-19", "2026-10-26"},
		{"monthly", "2026-10-22", "2026-10-01", "2026-11-01"},
		{"quarterly", "2026-08-02", "2026-07-01", "2026-10-01"},
	}
	for _, test := range tests {
		window, err := PeriodWindow(test.period, test.date, now)
		if err != nil {
			t.Fatalf("%s: %v", test.period, err)
		}
		if got := window.Start.Format("2006-01-02"); got != test.start {
			t.Fatalf("%s start = %s", test.period, got)
		}
		if got := window.End.Format("2006-01-02"); got != test.end {
			t.Fatalf("%s end = %s", test.period, got)
		}
	}
}

func TestGeneratePersistsAggregatesWithoutClusterFanout(t *testing.T) {
	platformDB := openReportDB(t, "reports-platform")
	eventDB := openReportDB(t, "reports-events")
	for _, statement := range []string{
		`CREATE TABLE security_baseline_history(cluster_id TEXT,status TEXT,started_at TEXT)`,
		`CREATE TABLE event_issue_hourly(bucket_start TEXT,cluster_id TEXT,type TEXT,reason TEXT,occurrence_count INTEGER)`,
	} {
		if _, err := map[bool]*sql.DB{true: platformDB, false: eventDB}[strings.Contains(statement, "security_")].Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Date(2026, time.October, 23, 12, 0, 0, 0, reportZone)
	if _, err := eventDB.Exec(`INSERT INTO event_issue_hourly VALUES(?,?,?,?,?),(?,?,?,?,?)`,
		"2026-10-19T01:00:00Z", "cluster-1", "Warning", "FailedScheduling", 5,
		"2026-10-20T02:00:00Z", "cluster-1", "Normal", "Pulled", 7); err != nil {
		t.Fatal(err)
	}
	if _, err := platformDB.Exec(`INSERT INTO security_baseline_history VALUES(?,?,?)`, "cluster-1", "completed", "2026-10-20T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	source := reportSource{
		records:  []cluster.ClusterRecord{{ManagedCluster: cluster.ManagedCluster{ID: "cluster-1", Name: "stp-g01-01", Environment: "stp", ClusterType: "G01", TechnologyStack: "TCS", ConnectionStatus: "connected", LastCheckedAt: now}}},
		capacity: cluster.FleetCapacity{Items: []cluster.FleetCapacityItem{{ClusterID: "cluster-1", Capacity: cluster.ClusterCapacity{CollectedAt: now.Add(-time.Minute), CPUAllocatableMilli: 16000, MemoryAllocatable: 64 << 30, Platform: cluster.CapacityValues{CPURequestsMilli: 2000, MemoryRequests: 8 << 30}, Business: cluster.CapacityValues{CPURequestsMilli: 6000, MemoryRequests: 16 << 30}}}}},
	}
	service, err := New(context.Background(), platformDB, eventDB, true, true, source, nil)
	if err != nil {
		t.Fatal(err)
	}
	report, err := service.Generate(context.Background(), Request{Period: "weekly", Date: "2026-10-22", Environment: "stp", ClusterType: "G01"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if report.Evidence.Warning != 5 || report.Evidence.Normal != 7 || report.Evidence.SecurityCompleted != 1 {
		t.Fatalf("unexpected evidence: %+v", report.Evidence)
	}
	if len(report.Evidence.Pools) != 1 || report.Evidence.Pools[0].Samples != 1 || report.Evidence.Pools[0].CPURequested != 8 {
		t.Fatalf("unexpected pool: %+v", report.Evidence.Pools)
	}
	loaded, err := service.Get(context.Background(), report.ID)
	if err != nil || loaded.ID != report.ID || loaded.URL == "" {
		t.Fatalf("persisted report = %+v, %v", loaded, err)
	}
	items, err := service.List(context.Background())
	if err != nil || len(items) != 1 || items[0].ID != report.ID {
		t.Fatalf("items = %+v, %v", items, err)
	}
}

func TestRenderHTMLEscapesReportDataAndUsesNoExternalAssets(t *testing.T) {
	report := Report{
		ID: "abc", Title: `<script>alert(1)</script>`, GeneratedAt: time.Now(),
		Window:   Window{Start: time.Now().Add(-24 * time.Hour), End: time.Now(), Label: "日报"},
		Evidence: Evidence{Clusters: []ClusterSummary{{ID: "1", Name: `<img src=x onerror=alert(1)>`}}, Notes: []string{"test"}},
		Analysis: Analysis{Summary: `<b>unsafe</b>`},
	}
	page, err := RenderHTML(report)
	if err != nil {
		t.Fatal(err)
	}
	text := string(page)
	if strings.Contains(text, "<script>alert(1)</script>") || strings.Contains(text, "<img src=x") || strings.Contains(text, "<b>unsafe</b>") {
		t.Fatal("report data was rendered as active HTML")
	}
	if strings.Contains(text, "src=\"http") || strings.Contains(text, "href=\"http") {
		t.Fatal("standalone report unexpectedly references an external asset")
	}
}

func TestRenderHTMLPrioritizesRepeatedProblemsAndHotClusters(t *testing.T) {
	report := Report{
		ID: "def", Title: "集群运行周报", GeneratedAt: time.Now(),
		Window: Window{Start: time.Now().Add(-7 * 24 * time.Hour), End: time.Now(), Label: "周报"},
		Evidence: Evidence{
			Warning: 18, ObservedDays: 7, ExpectedDays: 7,
			Reasons:  []Reason{{Reason: "FailedScheduling", Count: 12}, {Reason: "BackOff", Count: 6}},
			Clusters: []ClusterSummary{{ID: "cluster-1", Name: "stp-g01-01", Environment: "stp", ClusterType: "G01", TechnologyStack: "TCS", Warning: 14}},
		},
		Analysis: Analysis{Summary: "调度失败是本期最主要的重复问题。", Actions: []string{"检查节点资源与调度约束。"}},
	}
	page, err := RenderHTML(report)
	if err != nil {
		t.Fatal(err)
	}
	text := string(page)
	for _, expected := range []string{"重复问题 TOP 5", "FailedScheduling", "12 次", "热点集群 TOP 5", "stp-g01-01", "建议先做"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("report does not contain %q", expected)
		}
	}
	if strings.Contains(text, "01 / Warning 每日趋势") || strings.Contains(text, "Warning 原因 TOP 12") {
		t.Fatal("old detail-first report layout is still rendered")
	}
}
