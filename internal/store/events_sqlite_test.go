package store

import (
	"context"
	"testing"
	"time"

	"example.com/k8s-platform/internal/cluster"
)

func TestSQLiteEventsUpsertFilterAndSummary(t *testing.T) {
	database, err := OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	now := time.Now().UTC().Truncate(time.Microsecond)
	_, err = database.Create(context.Background(), cluster.ClusterRecord{ManagedCluster: cluster.ManagedCluster{ID: "c1", Name: "test", Environment: "DEV", APIServer: "https://test", ContextName: "ctx", KubernetesVersion: "v1.32", AuthenticationType: "客户端证书", ConnectionStatus: "connected", CreatedAt: now, LastCheckedAt: now}, EncryptedCredentials: "secret", Fingerprint: "fp"})
	if err != nil {
		t.Fatal(err)
	}
	event := cluster.ClusterEvent{ClusterID: "c1", EventUID: "e1", Type: "Warning", Reason: "FailedScheduling", Message: "no nodes available", RegardingKind: "Pod", RegardingNamespace: "kube-system", RegardingName: "dns", RegardingUID: "pod1", ReportingController: "default-scheduler", OccurrenceCount: 1, FirstObservedAt: now.Add(-time.Minute), LastObservedAt: now, ResourceVersion: "1"}
	if err = database.UpsertEvents(context.Background(), []cluster.ClusterEvent{event}); err != nil {
		t.Fatal(err)
	}
	event.OccurrenceCount = 4
	event.LastObservedAt = now.Add(time.Minute)
	event.ResourceVersion = "2"
	if err = database.UpsertEvents(context.Background(), []cluster.ClusterEvent{event}); err != nil {
		t.Fatal(err)
	}
	page, err := database.QueryEvents(context.Background(), "c1", cluster.EventQuery{Since: now.Add(-time.Hour), Until: now.Add(time.Hour), ComponentOnly: true, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].OccurrenceCount != 4 {
		t.Fatalf("unexpected page: %#v", page)
	}
	stats, err := database.EventStorageStats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats.RawRows != 1 || stats.HourlyRows != 1 {
		t.Fatalf("unexpected storage stats: %#v", stats)
	}
	if err = database.UpsertEvents(context.Background(), []cluster.ClusterEvent{event}); err != nil {
		t.Fatal(err)
	}
	stableStats, err := database.EventStorageStats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stableStats.HourlyRows != 1 {
		t.Fatalf("replayed event created duplicate rollup: %#v", stableStats)
	}
	summary, err := database.EventSummary(context.Background(), "c1", now.Add(-time.Hour), now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if summary.Warning != 1 || summary.Attention != 1 || summary.ComponentWarnings != 1 || summary.AffectedObjects != 1 {
		t.Fatalf("unexpected summary: %#v", summary)
	}
	facets, err := database.EventFacets(context.Background(), "c1", now.Add(-time.Hour), now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(facets.Types) != 1 || facets.Types[0] != "Warning" || len(facets.Namespaces) != 1 || facets.Namespaces[0] != "kube-system" || len(facets.Kinds) != 1 || facets.Kinds[0] != "Pod" {
		t.Fatalf("unexpected facets: %#v", facets)
	}
}

func TestSQLiteEventsRetentionCleanup(t *testing.T) {
	database, err := OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	old := time.Now().UTC().Add(-31 * 24 * time.Hour)
	if err = database.UpsertEvents(context.Background(), []cluster.ClusterEvent{{ClusterID: "gone", EventUID: "old", Type: "Normal", FirstObservedAt: old, LastObservedAt: old}}); err != nil {
		t.Fatal(err)
	}
	deleted, err := database.DeleteEventsByRetention(context.Background(), time.Now().UTC().Add(-7*24*time.Hour), time.Now().UTC().Add(-30*24*time.Hour), 5000)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1 {
		t.Fatalf("expected one expired event, got %d", deleted)
	}
}
