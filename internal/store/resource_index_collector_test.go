package store

import (
	"context"
	"testing"
	"time"

	"example.com/k8s-platform/internal/cluster"
)

func TestSearchResourcesUsesPersistentIndex(t *testing.T) {
	ctx := context.Background()
	database, err := OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	createIndexCluster(t, database, "c1", "shanghai-01", "production", "TCS")
	if err = database.ReplaceClusterResources(ctx, "c1", []cluster.IndexedResource{{
		ClusterID: "c1", Kind: "Deployment", Namespace: "payments", Name: "checkout-api",
		Status: "healthy", Images: []string{"registry/checkout:v3"}, UpdatedAt: time.Now().UTC(),
	}}); err != nil {
		t.Fatal(err)
	}
	// A nil connector proves the search path never falls back to a live cluster.
	service := cluster.NewOnboardingService(nil, nil, database)
	result, err := service.SearchResources(ctx, "checkout")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 1 || result.Items[0].Name != "checkout-api" || len(result.FailedClusters) != 0 {
		t.Fatalf("unexpected index search result: %#v", result)
	}
}

type indexConnector struct {
	cluster.Connector
}

func (indexConnector) Workloads(context.Context, cluster.ParsedKubeconfig) ([]cluster.ClusterWorkload, error) {
	return []cluster.ClusterWorkload{{Kind: "Deployment", Namespace: "payments", Name: "checkout-api", Status: "healthy", Images: []string{"registry/checkout:v3"}}}, nil
}

func (indexConnector) Pods(context.Context, cluster.ParsedKubeconfig) ([]cluster.ClusterPod, error) {
	return []cluster.ClusterPod{{Namespace: "payments", Name: "checkout-api-abc", Phase: "Running", OwnerKind: "Deployment", OwnerName: "checkout-api", NodeName: "node-1"}}, nil
}

type indexCipher struct{}

func (indexCipher) Encrypt(value []byte) (string, error) { return string(value), nil }
func (indexCipher) Decrypt(value string) ([]byte, error) { return []byte(value), nil }

func TestResourceIndexCollectorBuildsSearchableRows(t *testing.T) {
	ctx := context.Background()
	database, err := OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	now := time.Now().UTC().Truncate(time.Second)
	if _, err = database.Create(ctx, cluster.ClusterRecord{ManagedCluster: cluster.ManagedCluster{ID: "c1", Name: "c1", APIServer: "https://c1", CreatedAt: now, LastCheckedAt: now}, EncryptedCredentials: `{"token":"t"}`, Fingerprint: "c1"}); err != nil {
		t.Fatal(err)
	}
	service := cluster.NewOnboardingService(indexConnector{}, indexCipher{}, database)
	collector := cluster.NewResourceIndexCollector(service, database, time.Minute, 1, time.Second)
	collector.CollectOnce(ctx)
	result, err := database.SearchClusterResources(ctx, "checkout", 800)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 2 {
		t.Fatalf("collector did not index workload and pod: %#v", result)
	}
}
