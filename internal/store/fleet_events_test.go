package store

import (
	"context"
	"example.com/k8s-platform/internal/cluster"
	"fmt"
	"testing"
	"time"
)

func TestFleetInvestigationScansAllRecordsAndPaginatesWithoutLosingEvidence(t *testing.T) {
	ctx := context.Background()
	db, err := OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	now := time.Now().UTC().Truncate(time.Second)
	for _, id := range []string{"a", "b"} {
		_, err = db.Create(ctx, cluster.ClusterRecord{ManagedCluster: cluster.ManagedCluster{ID: id, Name: id, APIServer: "https://" + id, CreatedAt: now, LastCheckedAt: now}, Fingerprint: id})
		if err != nil {
			t.Fatal(err)
		}
	}
	_, err = db.CreateCapacityClassification(ctx, cluster.CapacityClassification{CreatedAt: now, BusinessNamespaces: []string{"myapp"}, CommonPlatformNamespaces: []string{}, StackPlatformNamespaces: map[string][]string{}})
	if err != nil {
		t.Fatal(err)
	}
	events := []cluster.ClusterEvent{}
	for i := 0; i < 650; i++ {
		id, namespace := "a", "myapp"
		if i%2 == 1 {
			id, namespace = "b", "addon"
		}
		events = append(events, cluster.ClusterEvent{ClusterID: id, EventUID: fmt.Sprint(i), Type: "Warning", Reason: "FailedScheduling", Message: "no available nodes", RegardingKind: "Pod", RegardingNamespace: namespace, RegardingName: fmt.Sprintf("pod-%d", i), RegardingUID: fmt.Sprintf("uid-%d", i), OccurrenceCount: 4, FirstObservedAt: now.Add(-time.Minute), LastObservedAt: now})
	}
	for i, message := range []string{"Failed to pull image: proxyconnect connection refused", "Error: ErrImagePull", "Error: ImagePullBackOff"} {
		events = append(events, cluster.ClusterEvent{ClusterID: "a", EventUID: fmt.Sprint("pull-", i), Type: "Warning", Reason: "Failed", Message: message, RegardingKind: "Pod", RegardingNamespace: "myapp", RegardingName: "pull", RegardingUID: "pull-uid", OccurrenceCount: 2, FirstObservedAt: now.Add(-time.Minute), LastObservedAt: now})
	}
	events = append(events, cluster.ClusterEvent{ClusterID: "a", EventUID: "normal", Type: "Normal", Reason: "Started", Message: "Started container", RegardingKind: "Pod", RegardingNamespace: "myapp", RegardingName: "pull", RegardingUID: "pull-uid", OccurrenceCount: 1, FirstObservedAt: now, LastObservedAt: now})
	// Same name, different UID must never be mistaken for the original resource.
	events = append(events, cluster.ClusterEvent{ClusterID: "a", EventUID: "replacement", Type: "Warning", Reason: "Failed", Message: "Error: ImagePullBackOff", RegardingKind: "Pod", RegardingNamespace: "myapp", RegardingName: "pull", RegardingUID: "replacement-uid", FirstObservedAt: now, LastObservedAt: now})
	if err = db.UpsertEvents(ctx, events); err != nil {
		t.Fatal(err)
	}
	svc := cluster.NewOnboardingService(nil, nil, db)
	svc.SetEventRepository(db)
	query := cluster.FleetEventQuery{EventQuery: cluster.EventQuery{Since: now.Add(-time.Hour), Until: now.Add(time.Second), Limit: 25}, View: "issues", Page: 1}
	overview, err := svc.GetFleetEvents(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	if overview.Total != 654 || overview.Summary.AffectedObjects != 652 || len(overview.Issues) != 2 || overview.Truncated {
		t.Fatalf("incomplete global aggregation: %#v", overview.Summary)
	}
	query.View = "events"
	query.Page = 2
	page2, err := svc.GetFleetEvents(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	if page2.Pagination.TotalItems != 654 || len(page2.Items) != 25 {
		t.Fatalf("bad raw page: %#v", page2.Pagination)
	}
	query.Page = 1
	page1, err := svc.GetFleetEvents(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, e := range page1.Items {
		seen[e.ClusterID+e.EventUID] = true
	}
	for _, e := range page2.Items {
		if seen[e.ClusterID+e.EventUID] {
			t.Fatal("pagination duplicated an event")
		}
	}
	query.Scope = "business"
	business, err := svc.GetFleetEvents(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	query.Scope = "platform"
	platform, err := svc.GetFleetEvents(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	if business.Total+platform.Total != overview.Total || platform.Total != 325 {
		t.Fatalf("scope totals inconsistent: %d+%d !=%d", business.Total, platform.Total, overview.Total)
	}
	query.Scope = ""
	query.View = "objects"
	query.Issue = "image-pull"
	objects, err := svc.GetFleetEvents(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	if objects.Pagination.TotalItems != 2 {
		t.Fatalf("image symptoms must share objects but distinct UIDs: %#v", objects)
	}
	query.View = "events"
	query.Issue = ""
	query.ObjectUID = "pull-uid"
	query.ObjectName = "pull"
	query.IncludeNormal = true
	evidence, err := svc.GetFleetEvents(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	if len(evidence.Items) != 4 {
		t.Fatalf("expected three symptoms plus Normal, without replacement: %#v", evidence.Items)
	}
	query.View = "resources"
	grouped, err := svc.GetFleetEvents(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	if grouped.Pagination.TotalItems != 1 || len(grouped.Objects) != 1 || grouped.Objects[0].EventCount != 4 || grouped.Objects[0].OccurrenceCount != 7 {
		t.Fatalf("resource events not merged: %#v", grouped)
	}
	query.ObjectUID = ""
	grouped, err = svc.GetFleetEvents(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	if grouped.Pagination.TotalItems != 2 {
		t.Fatalf("replacement UID must stay separate: %#v", grouped.Objects)
	}

	for _, e := range evidence.Items {
		if e.RegardingUID != "pull-uid" {
			t.Fatal("wrong UID in evidence")
		}
	}
}

type failingEventScanner struct{ *SQLite }

func (db failingEventScanner) ScanEvents(ctx context.Context, id string, q cluster.EventQuery, visit func(cluster.ClusterEvent) error) error {
	if id == "broken" {
		return fmt.Errorf("storage read failed")
	}
	return db.SQLite.ScanEvents(ctx, id, q, visit)
}
func TestFleetInvestigationReportsMissingClusterData(t *testing.T) {
	ctx := context.Background()
	db, err := OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UTC()
	_, err = db.Create(ctx, cluster.ClusterRecord{ManagedCluster: cluster.ManagedCluster{ID: "broken", Name: "broken", APIServer: "https://broken", CreatedAt: now, LastCheckedAt: now}, Fingerprint: "broken"})
	if err != nil {
		t.Fatal(err)
	}
	svc := cluster.NewOnboardingService(nil, nil, db)
	svc.SetEventRepository(failingEventScanner{db})
	result, err := svc.GetFleetEvents(ctx, cluster.FleetEventQuery{EventQuery: cluster.EventQuery{Since: now.Add(-time.Hour), Until: now}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Failures) != 1 || result.Collectors[0].State != "error" {
		t.Fatalf("missing data must not look healthy: %#v", result)
	}
}

func TestFleetUnknownAnomaliesBeyondTwelveArePageable(t *testing.T) {
	ctx := context.Background()
	db, err := OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UTC()
	_, err = db.Create(ctx, cluster.ClusterRecord{ManagedCluster: cluster.ManagedCluster{ID: "c", Name: "c", APIServer: "https://c", CreatedAt: now, LastCheckedAt: now}, Fingerprint: "c"})
	if err != nil {
		t.Fatal(err)
	}
	events := []cluster.ClusterEvent{}
	for i := 0; i < 27; i++ {
		events = append(events, cluster.ClusterEvent{ClusterID: "c", EventUID: fmt.Sprint(i), Type: "Warning", Reason: fmt.Sprintf("CustomWarning%d", i), Message: "custom evidence", RegardingKind: "Pod", RegardingUID: fmt.Sprint(i), FirstObservedAt: now, LastObservedAt: now})
	}
	if err = db.UpsertEvents(ctx, events); err != nil {
		t.Fatal(err)
	}
	svc := cluster.NewOnboardingService(nil, nil, db)
	svc.SetEventRepository(db)
	q := cluster.FleetEventQuery{View: "issues", Page: 2, EventQuery: cluster.EventQuery{Since: now.Add(-time.Hour), Until: now.Add(time.Second), Limit: 25}}
	result, err := svc.GetFleetEvents(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if result.Pagination.TotalItems != 27 || len(result.Issues) != 2 {
		t.Fatalf("unknown groups truncated: %#v", result.Pagination)
	}
}
