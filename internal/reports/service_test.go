package reports

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
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
		`CREATE TABLE alert_rollup_hourly(bucket_start TEXT,cluster_id TEXT,fingerprint TEXT,name TEXT,severity TEXT,firing_count INTEGER,resolved_count INTEGER,total_duration_seconds INTEGER,affected_resources INTEGER)`,
		`CREATE TABLE alert_collection_history(bucket_start TEXT,cluster_id TEXT,success_count INTEGER,failure_count INTEGER)`,
		`CREATE TABLE event_issue_hourly(bucket_start TEXT,cluster_id TEXT,fingerprint TEXT,type TEXT,reason TEXT,occurrence_count INTEGER)`,
	} {
		if _, err := map[bool]*sql.DB{true: platformDB, false: eventDB}[!strings.Contains(statement, "event_issue")].Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Date(2026, time.October, 23, 12, 0, 0, 0, reportZone)
	if _, err := eventDB.Exec(`INSERT INTO event_issue_hourly VALUES(?,?,?,?,?,?),(?,?,?,?,?,?),(?,?,?,?,?,?)`,
		"2026-10-19T01:00:00Z", "cluster-1", "problem-a", "Warning", "FailedScheduling", 5,
		"2026-10-20T02:00:00Z", "cluster-1", "normal-a", "Normal", "Pulled", 7,
		"2026-10-12T01:00:00Z", "cluster-1", "problem-a", "Warning", "FailedScheduling", 2); err != nil {
		t.Fatal(err)
	}
	if _, err := platformDB.Exec(`INSERT INTO security_baseline_history VALUES(?,?,?)`, "cluster-1", "completed", "2026-10-20T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if _, err := platformDB.Exec(`INSERT INTO alert_rollup_hourly VALUES(?,?,?,?,?,?,?,?,?)`, "2026-10-20T03:00:00Z", "cluster-1", "alert-a", "APIServerHighLatency", "critical", 3, 2, 900, 3); err != nil {
		t.Fatal(err)
	}
	if _, err := platformDB.Exec(`INSERT INTO alert_collection_history VALUES(?,?,?,?)`, "2026-10-20T03:00:00Z", "cluster-1", 9, 1); err != nil {
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
	if report.Evidence.ProblemClusters != 1 || report.Evidence.OngoingProblemClusters != 1 || report.Evidence.NewProblemClusters != 0 || report.Evidence.PreviousProblemClusters != 1 {
		t.Fatalf("unexpected problem-cluster evidence: %+v", report.Evidence)
	}
	if report.Evidence.Alerts.Critical != 3 || report.Evidence.Alerts.Resolved != 2 || report.Evidence.Alerts.ResolvedGroups != 1 || len(report.Evidence.Alerts.Top) != 1 || report.Evidence.Alerts.CollectionSuccessPct != 90 {
		t.Fatalf("unexpected alert evidence: %+v", report.Evidence.Alerts)
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

func TestDeleteRemovesReportAndReportsNotFoundAfterwards(t *testing.T) {
	db := openReportDB(t, "report-delete")
	service, err := New(context.Background(), db, db, true, true, reportSource{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.October, 23, 12, 0, 0, 0, reportZone)
	// Seed one report directly so the test does not depend on a full collection.
	report := Report{ID: "report-x", Title: "集群运行周报", Request: Request{Period: "weekly"}, GeneratedAt: now, AIStatus: "disabled", Analysis: ruleAnalysis(Evidence{})}
	payload, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO operational_reports(id,title,period,environment,cluster_type,generated_at,ai_status,payload) VALUES(?,?,?,?,?,?,?,?)`, report.ID, report.Title, report.Request.Period, "", "", now.UTC().Format(time.RFC3339Nano), report.AIStatus, string(payload)); err != nil {
		t.Fatal(err)
	}
	if err := service.Delete(context.Background(), report.ID); err != nil {
		t.Fatalf("delete = %v", err)
	}
	if _, err := service.Get(context.Background(), report.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
	if err := service.Delete(context.Background(), report.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound on second delete, got %v", err)
	}
}

func TestSubmitPersistsBackgroundJob(t *testing.T) {
	db := openReportDB(t, "report-jobs")
	service, err := New(context.Background(), db, db, true, true, reportSource{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.October, 23, 12, 0, 0, 0, reportZone)
	job, err := service.Submit(context.Background(), Request{Period: "weekly", Date: "2026-10-22", UseAI: true}, now)
	if err != nil {
		t.Fatal(err)
	}
	if job.Status != "queued" || job.ID == "" || !job.Request.UseAI {
		t.Fatalf("unexpected submitted job: %+v", job)
	}
	loaded, err := service.GetJob(context.Background(), job.ID)
	if err != nil || loaded.ID != job.ID || loaded.Message == "" {
		t.Fatalf("loaded job = %+v, %v", loaded, err)
	}
	jobs, err := service.ListJobs(context.Background())
	if err != nil || len(jobs) != 1 || jobs[0].ID != job.ID {
		t.Fatalf("jobs = %+v, %v", jobs, err)
	}
}

func TestModelFailureMessageIsActionable(t *testing.T) {
	message := ModelFailureMessage(errors.New("模型接口返回 401：invalid token"))
	if !strings.Contains(message, "API Key 无效或已过期") || !strings.Contains(message, "invalid token") {
		t.Fatalf("unexpected message: %s", message)
	}
}

// Aggregators answer an unknown model name with a 5xx channel message, which
// would otherwise read as an outage instead of a typo in the model field.
func TestModelFailureMessageNamesUnknownModel(t *testing.T) {
	message := ModelFailureMessage(errors.New("模型接口返回 500：分组 default 下模型 model-that-does-not-exist 无可用渠道（distributor）"))
	if !strings.Contains(message, "模型名称在该服务上不存在") {
		t.Fatalf("unknown model was not named: %s", message)
	}
	if strings.Contains(message, "服务端异常") {
		t.Fatalf("unknown model was misreported as an outage: %s", message)
	}
}

func TestModelFailureMessageKeepsTransportDetail(t *testing.T) {
	message := ModelFailureMessage(errors.New("无法连接模型接口"))
	if !strings.Contains(message, "无法连接模型服务") || !strings.Contains(message, "无法连接模型接口") {
		t.Fatalf("transport detail was dropped: %s", message)
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
			Warning: 18, ObservedDays: 7, ExpectedDays: 7, PreviousWarning: 9,
			ProblemClusters: 4, NewProblemClusters: 2, OngoingProblemClusters: 2, PreviousProblemClusters: 2,
			Reasons:         []Reason{{Reason: "FailedScheduling", Count: 12}, {Reason: "BackOff", Count: 6}},
			PreviousReasons: []Reason{{Reason: "FailedScheduling", Count: 6}},
			ReasonClusters: []ReasonCluster{
				{Reason: "FailedScheduling", ClusterID: "cluster-1", ClusterName: "stp-g01-01", Count: 9},
				{Reason: "FailedScheduling", ClusterID: "cluster-2", ClusterName: "prd-g01-01", Count: 3},
			},
			Clusters: []ClusterSummary{{ID: "cluster-1", Name: "stp-g01-01", Environment: "stp", ClusterType: "G01", TechnologyStack: "TCS", Warning: 14}},
		},
		Analysis: Analysis{Summary: "调度失败是本期最主要的重复问题。", Actions: []string{"检查节点资源与调度约束。"}},
	}
	page, err := RenderHTML(report)
	if err != nil {
		t.Fatal(err)
	}
	text := string(page)
	// The report must lead with the decision, not with ranked raw counts, and it
	// stays a self-contained reading surface instead of turning every datum into
	// a platform navigation link.
	for _, expected := range []string{
		"AI 集群运行报告", "本周期最严重的集群与问题", "调度容量不足，Pod 排不进去", "处置建议", "stp-g01-01 9", "prd-g01-01 3",
		"资源池容量风险", "本周期发现了什么", "支撑判断的数据", "历史问题与当前证据链", "净增 2 个", "独立问题簇", "本期新增", "跨周期持续", "#07111f", "#28d7ef",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("report does not contain %q", expected)
		}
	}
	if strings.Contains(text, "重复问题 TOP 5") || strings.Contains(text, "热点集群 TOP 5") {
		t.Fatal("frequency-only ranking is still the primary layout")
	}
	if strings.Contains(text, "href=\"") || strings.Contains(text, "target=\"_top\"") {
		t.Fatal("report still contains interactive drill-down links")
	}
}

func TestRenderHTMLAlertEvidenceIsReadableWithoutDrillDown(t *testing.T) {
	report := Report{
		ID: "bar", Title: "集群运行周报", GeneratedAt: time.Now(),
		Window: Window{Start: time.Now().Add(-7 * 24 * time.Hour), End: time.Now(), Label: "周报"},
		Evidence: Evidence{
			Warning: 10, ObservedDays: 7, ExpectedDays: 7,
			Alerts: AlertEvidence{
				Critical: 40, Warning: 60, CollectionSuccessPct: 100,
				Top: []AlertProblem{
					{Name: "KubePodCrashLooping", Severity: "critical", Occurrences: 90},
					{Name: "KubeNodeNotReady", Severity: "critical", Occurrences: 10},
				},
			},
		},
	}
	page, err := RenderHTML(report)
	if err != nil {
		t.Fatal(err)
	}
	text := string(page)
	for _, expected := range []string{"重点告警证据", "KubePodCrashLooping", "90", "KubeNodeNotReady", "10", "采集成功率 100.0%"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("alert evidence does not contain %q", expected)
		}
	}
	if strings.Contains(text, "/alerts?") {
		t.Fatal("alert evidence unexpectedly contains a drill-down URL")
	}
}
