package store

import (
	"context"
	"os"
	"testing"
	"time"

	"example.com/k8s-platform/internal/cluster"
)

func TestMySQLAutomaticMigrationAndClusterSnapshot(t *testing.T) {
	dsn := os.Getenv("MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("MYSQL_TEST_DSN is not configured")
	}
	store, err := OpenMySQL(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	id := "integration-health"
	_, _ = store.db.Exec(`DELETE FROM clusters WHERE id=?`, id)
	now := time.Now().UTC().Truncate(time.Microsecond)
	_, err = store.Create(context.Background(), cluster.ClusterRecord{ManagedCluster: cluster.ManagedCluster{ID: id, Name: id, Environment: "stp", ClusterType: "G01", TechnologyStack: "TCS", APIServer: "https://example.invalid", ContextName: "ctx", KubernetesVersion: "v1.32", AuthenticationType: "token", ConnectionStatus: "connected", CreatedAt: now, LastCheckedAt: now}, EncryptedCredentials: "encrypted", Fingerprint: id})
	if err != nil {
		t.Fatal(err)
	}
	nodes := 200
	if err := store.UpdateClusterHealth(context.Background(), cluster.ManagedCluster{ID: id, ConnectionStatus: "connected", LastCheckedAt: now, NodesReady: &nodes, NodesTotal: &nodes}); err != nil {
		t.Fatal(err)
	}
	record, err := store.GetRecord(context.Background(), id)
	if err != nil || record.NodesTotal == nil || *record.NodesTotal != 200 {
		t.Fatalf("unexpected mysql snapshot: %#v err=%v", record, err)
	}
	if err := store.Delete(context.Background(), id); err != nil {
		t.Fatal(err)
	}
}
