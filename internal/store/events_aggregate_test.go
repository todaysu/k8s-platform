package store

import (
	"context"
	"testing"
	"time"

	"example.com/k8s-platform/internal/cluster"
)

func TestSQLiteEventAggregatesByCategoryAndObject(t *testing.T) {
	ctx := context.Background()
	database, err := OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	now := time.Now().UTC().Truncate(time.Microsecond)
	if _, err = database.Create(ctx, cluster.ClusterRecord{ManagedCluster: cluster.ManagedCluster{ID: "c1", Name: "c1", APIServer: "https://c1", CreatedAt: now, LastCheckedAt: now}, Fingerprint: "c1"}); err != nil {
		t.Fatal(err)
	}
	events := []cluster.ClusterEvent{
		{ClusterID: "c1", EventUID: "1", Type: "Warning", Reason: "FailedScheduling", RegardingKind: "Pod", RegardingNamespace: "app", RegardingName: "p1", RegardingUID: "u1", Message: "0/3 nodes available", OccurrenceCount: 2, FirstObservedAt: now, LastObservedAt: now},
		{ClusterID: "c1", EventUID: "2", Type: "Warning", Reason: "FailedMount", RegardingKind: "Pod", RegardingNamespace: "app", RegardingName: "p2", RegardingUID: "u2", Message: "mount failed", OccurrenceCount: 1, FirstObservedAt: now, LastObservedAt: now},
		{ClusterID: "c1", EventUID: "3", Type: "Warning", Reason: "Failed", RegardingKind: "Pod", RegardingNamespace: "app", RegardingName: "p3", RegardingUID: "u3", Message: "Error: ImagePullBackOff", OccurrenceCount: 1, FirstObservedAt: now, LastObservedAt: now},
		{ClusterID: "c1", EventUID: "4", Type: "Warning", Reason: "Unhealthy", RegardingKind: "Pod", RegardingNamespace: "app", RegardingName: "p4", RegardingUID: "u4", Message: "probe failed", OccurrenceCount: 1, FirstObservedAt: now, LastObservedAt: now},
		{ClusterID: "c1", EventUID: "5", Type: "Warning", Reason: "BackOff", RegardingKind: "Pod", RegardingNamespace: "app", RegardingName: "p5", RegardingUID: "u5", Message: "back-off restarting", OccurrenceCount: 1, FirstObservedAt: now, LastObservedAt: now},
		{ClusterID: "c1", EventUID: "6", Type: "Warning", Reason: "Evicted", RegardingKind: "Pod", RegardingNamespace: "app", RegardingName: "p6", RegardingUID: "u6", Message: "evicted", OccurrenceCount: 1, FirstObservedAt: now, LastObservedAt: now},
		{ClusterID: "c1", EventUID: "7", Type: "Warning", Reason: "NodeNotReady", RegardingKind: "Node", RegardingNamespace: "", RegardingName: "node-1", RegardingUID: "n1", Message: "node not ready", OccurrenceCount: 1, FirstObservedAt: now, LastObservedAt: now},
		{ClusterID: "c1", EventUID: "8", Type: "Warning", Reason: "CustomWarning", RegardingKind: "Pod", RegardingNamespace: "app", RegardingName: "p8", RegardingUID: "u8", Message: "custom", OccurrenceCount: 1, FirstObservedAt: now, LastObservedAt: now},
	}
	if err = database.UpsertEvents(ctx, events); err != nil {
		t.Fatal(err)
	}
	rows, err := database.AggregateEvents(ctx, "c1", cluster.EventQuery{Since: now.Add(-time.Hour), Until: now.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	categories := map[string]int64{}
	for _, row := range rows {
		categories[row.Category] = row.EventCount
	}
	want := map[string]int64{"scheduling": 1, "storage": 1, "image-pull": 1, "probe": 1, "restart": 1, "eviction": 1, "node": 1, "other-customwarning": 1}
	if len(categories) != len(want) {
		t.Fatalf("unexpected categories: %#v", categories)
	}
	for category, count := range want {
		if categories[category] != count {
			t.Fatalf("category %s = %d, want %d (%#v)", category, categories[category], count, categories)
		}
	}
	objects, err := database.AggregateEventObjects(ctx, "c1", cluster.EventQuery{Since: now.Add(-time.Hour), Until: now.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if len(objects) != len(events) {
		t.Fatalf("expected one object row per object, got %d", len(objects))
	}
	if len(objects[0].Types) == 0 || len(objects[0].Types[0]) == 0 {
		t.Fatalf("object facets were not aggregated: %#v", objects[0])
	}
	if err = database.SetEventCollectorStatus(ctx, cluster.EventCollectorStatus{ClusterID: "c1", State: "watching", UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	statuses, err := database.ListEventCollectorStatuses(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(statuses) != 1 || statuses[0].ClusterID != "c1" || statuses[0].State != "watching" {
		t.Fatalf("unexpected collector statuses: %#v", statuses)
	}
}
