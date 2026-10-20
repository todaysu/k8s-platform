package store

import (
	"context"
	"testing"
	"time"

	"example.com/k8s-platform/internal/cluster"
)

func createIndexCluster(t *testing.T, database *SQLite, id, name, environment, stack string) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	_, err := database.Create(context.Background(), cluster.ClusterRecord{ManagedCluster: cluster.ManagedCluster{
		ID: id, Name: name, Environment: environment, ClusterType: "G01", TechnologyStack: stack,
		APIServer: "https://" + id, CreatedAt: now, LastCheckedAt: now,
	}, Fingerprint: id})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSQLiteResourceIndexReplaceSearchDelete(t *testing.T) {
	ctx := context.Background()
	database, err := OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	createIndexCluster(t, database, "c1", "shanghai-01", "production", "TCS")
	createIndexCluster(t, database, "c2", "beijing-01", "production", "OCP")
	now := time.Now().UTC().Truncate(time.Second)
	resources := []cluster.IndexedResource{
		{ClusterID: "c1", Kind: "Deployment", Namespace: "payments", Name: "checkout-api", Status: "healthy", Images: []string{"registry/checkout:v3"}, UpdatedAt: now},
		{ClusterID: "c1", Kind: "Pod", Namespace: "payments", Name: "checkout-api-abc", Status: "Running", OwnerKind: "Deployment", OwnerName: "checkout-api", NodeName: "node-1", Images: []string{"registry/checkout:v3"}, UpdatedAt: now},
		{ClusterID: "c2", Kind: "Pod", Namespace: "apps", Name: "order-api", Status: "Running", OwnerKind: "Deployment", OwnerName: "order-service", NodeName: "node-2", Images: []string{"registry/order:v1"}, UpdatedAt: now},
	}
	if err = database.ReplaceClusterResources(ctx, "c1", resources[:2]); err != nil {
		t.Fatal(err)
	}
	if err = database.ReplaceClusterResources(ctx, "c2", resources[2:]); err != nil {
		t.Fatal(err)
	}

	results, err := database.SearchClusterResources(ctx, "checkout", 800)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("expected workload and pod, got %#v", results)
	}
	if results[0].ClusterID != "c1" || results[0].ClusterName != "shanghai-01" || results[0].Environment != "production" || results[0].TechnologyStack != "TCS" {
		t.Fatalf("lost cluster metadata: %#v", results[0])
	}
	results, err = database.SearchClusterResources(ctx, "order-service", 800)
	if err != nil || len(results) != 1 || results[0].OwnerName != "order-service" {
		t.Fatalf("owner search failed: %#v %v", results, err)
	}
	results, err = database.SearchClusterResources(ctx, "node-1", 800)
	if err != nil || len(results) != 1 || results[0].Name != "checkout-api-abc" {
		t.Fatalf("node search failed: %#v %v", results, err)
	}
	results, err = database.SearchClusterResources(ctx, "%", 800)
	if err != nil || len(results) != 0 {
		t.Fatalf("wildcard must be escaped: %#v %v", results, err)
	}

	if err = database.ReplaceClusterResources(ctx, "c1", resources[:1]); err != nil {
		t.Fatal(err)
	}
	results, err = database.SearchClusterResources(ctx, "checkout", 800)
	if err != nil || len(results) != 1 || results[0].Kind != "Deployment" {
		t.Fatalf("replacement did not remove stale rows: %#v %v", results, err)
	}
	if err = database.DeleteClusterResources(ctx, "c1"); err != nil {
		t.Fatal(err)
	}
	results, err = database.SearchClusterResources(ctx, "checkout", 800)
	if err != nil || len(results) != 0 {
		t.Fatalf("delete did not remove rows: %#v %v", results, err)
	}
}
