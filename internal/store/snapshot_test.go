package store

import (
	"context"
	"testing"
	"time"

	"example.com/k8s-platform/internal/cluster"
)

func TestOnboardingReadsStoredSnapshots(t *testing.T) {
	ctx := context.Background()
	database, err := OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	now := time.Now().UTC().Truncate(time.Second)
	ready, total := 199, 200
	record := cluster.ClusterRecord{ManagedCluster: cluster.ManagedCluster{
		ID: "c1", Name: "c1", APIServer: "https://c1", CreatedAt: now, LastCheckedAt: now,
		NodesReady: &ready, NodesTotal: &total,
	}, Fingerprint: "c1"}
	if _, err = database.Create(ctx, record); err != nil {
		t.Fatal(err)
	}
	if err = database.UpdateClusterHealth(ctx, record.ManagedCluster); err != nil {
		t.Fatal(err)
	}
	service := cluster.NewOnboardingService(nil, nil, database)
	value, err := service.Get(ctx, "c1")
	if err != nil {
		t.Fatal(err)
	}
	if value.NodesTotal == nil || *value.NodesTotal != total || value.NodesReady == nil || *value.NodesReady != ready {
		t.Fatalf("expected stored health snapshot, got %#v", value)
	}

	item := cluster.FleetCapacityItem{ClusterID: "c1", ClusterName: "c1", Capacity: cluster.ClusterCapacity{CollectedAt: now, WorkerNodes: 3, CPUAllocatableMilli: 12000, SystemNamespaces: []string{"kube-system"}}}
	if err = database.SaveFleetCapacitySuccess(ctx, item, now); err != nil {
		t.Fatal(err)
	}
	capacity, err := service.GetCachedCapacity(ctx, "c1")
	if err != nil {
		t.Fatal(err)
	}
	if capacity.WorkerNodes != 3 || capacity.CPUAllocatableMilli != 12000 {
		t.Fatalf("expected stored capacity snapshot, got %#v", capacity)
	}
}
