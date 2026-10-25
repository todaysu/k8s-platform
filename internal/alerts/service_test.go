package alerts

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"example.com/k8s-platform/internal/cluster"
	_ "modernc.org/sqlite"
)

type testSource struct {
	snapshot cluster.AlertSnapshot
	err      error
}

func (source *testSource) ListRecords(context.Context) ([]cluster.ClusterRecord, error) {
	return []cluster.ClusterRecord{{ManagedCluster: cluster.ManagedCluster{ID: "c1"}}}, nil
}
func (source *testSource) GetClusterAlerts(context.Context, string) (cluster.AlertSnapshot, error) {
	return source.snapshot, source.err
}

func TestAlertStateTransitionsAndCollectionFailure(t *testing.T) {
	db, err := sql.Open("sqlite", "file:alert-test?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	defer db.Close()
	ctx := context.Background()
	started := time.Date(2026, 10, 24, 10, 0, 0, 0, time.UTC)
	source := &testSource{snapshot: cluster.AlertSnapshot{Source: "monitoring/alertmanager", CollectedAt: started.Add(time.Minute), Alerts: []cluster.Alert{{Fingerprint: "fp1", Labels: map[string]string{"alertname": "PodCrashLooping", "severity": "warning", "namespace": "myapp", "pod": "web-1"}, StartsAt: started, State: "active"}}}}
	service, err := New(ctx, db, source)
	if err != nil {
		t.Fatal(err)
	}
	service.CollectOnce(ctx)
	current, err := service.List(ctx, Query{View: "current"})
	if err != nil || current.Total != 1 || current.Items[0].ResourceName != "web-1" {
		t.Fatalf("current = %+v, %v", current, err)
	}
	service.CollectOnce(ctx)
	current, err = service.List(ctx, Query{View: "current"})
	if err != nil || current.Total != 1 {
		t.Fatalf("duplicate current = %+v, %v", current, err)
	}
	source.snapshot.Alerts[0].State = "suppressed"
	source.snapshot.CollectedAt = started.Add(2 * time.Minute)
	service.CollectOnce(ctx)
	current, err = service.List(ctx, Query{View: "current"})
	if err != nil || current.Total != 1 || current.Items[0].State != "suppressed" {
		t.Fatalf("suppressed alert = %+v, %v", current, err)
	}
	source.err = errors.New("proxy unavailable")
	service.CollectOnce(ctx)
	current, err = service.List(ctx, Query{View: "current"})
	if err != nil || current.Total != 1 || current.Coverage[0].Status != "error" {
		t.Fatalf("failure changed state = %+v, %v", current, err)
	}
	source.err = nil
	source.snapshot = cluster.AlertSnapshot{Source: "monitoring/alertmanager", CollectedAt: started.Add(3 * time.Minute), Alerts: []cluster.Alert{}}
	service.CollectOnce(ctx)
	history, err := service.List(ctx, Query{View: "history", ClusterID: "c1", Severity: "warning"})
	if err != nil || history.Total != 1 || history.Items[0].EndsAt == nil {
		t.Fatalf("history = %+v, %v", history, err)
	}
}

func TestListFiltersByTriggerOrRecoveryTime(t *testing.T) {
	db, err := sql.Open("sqlite", "file:alert-range-test?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	defer db.Close()
	ctx := context.Background()
	service, err := New(ctx, db, nil)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	rows := []struct {
		id, state  string
		start, end time.Time
	}{
		{"recent", "firing", now.Add(-30 * time.Minute), time.Time{}},
		{"old", "firing", now.Add(-48 * time.Hour), time.Time{}},
		{"resolved-recent", "resolved", now.Add(-72 * time.Hour), now.Add(-20 * time.Minute)},
		{"resolved-old", "resolved", now.Add(-96 * time.Hour), now.Add(-48 * time.Hour)},
	}
	for _, row := range rows {
		end := ""
		if !row.end.IsZero() {
			end = stamp(row.end)
		}
		_, err = db.ExecContext(ctx, `INSERT INTO alert_instances(id,cluster_id,fingerprint,name,severity,namespace,resource_kind,resource_name,summary,state,source,starts_at,last_seen_at,ends_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, row.id, "c1", row.id, row.id, "warning", "", "", "", "", row.state, "test", stamp(row.start), stamp(now), end)
		if err != nil {
			t.Fatal(err)
		}
	}
	current, err := service.List(ctx, Query{View: "current", Since: now.Add(-time.Hour)})
	if err != nil || current.Total != 1 || current.Items[0].ID != "recent" {
		t.Fatalf("current range = %+v, %v", current, err)
	}
	history, err := service.List(ctx, Query{View: "history", Since: now.Add(-time.Hour)})
	if err != nil || history.Total != 1 || history.Items[0].ID != "resolved-recent" {
		t.Fatalf("history range = %+v, %v", history, err)
	}
}

func TestRetentionOnlyPrunesResolvedAlerts(t *testing.T) {
	db, err := sql.Open("sqlite", "file:alert-retention-test?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	defer db.Close()
	ctx := context.Background()
	service, err := New(ctx, db, nil)
	if err != nil {
		t.Fatal(err)
	}
	service.ConfigureRetention(30, 100)
	if service.resolvedRetentionDays != 30 || service.cleanupBatchSize != 100 {
		t.Fatalf("configuration not applied: %+v", service)
	}
	old := time.Now().UTC().Add(-31 * 24 * time.Hour)
	for _, state := range []string{"resolved", "firing", "suppressed"} {
		_, err = db.ExecContext(ctx, `INSERT INTO alert_instances(id,cluster_id,fingerprint,name,severity,namespace,resource_kind,resource_name,summary,state,source,starts_at,last_seen_at,ends_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, state, "c1", state, state, "warning", "", "", "", "", state, "test", stamp(old), stamp(old), stamp(old))
		if err != nil {
			t.Fatal(err)
		}
	}
	if err = service.pruneResolved(ctx, time.Now().UTC().Add(-30*24*time.Hour), 100); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM alert_instances`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("expected active alerts to remain, got %d rows", count)
	}
}
