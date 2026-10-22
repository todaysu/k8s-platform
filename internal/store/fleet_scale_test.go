package store

import (
	"context"
	"example.com/k8s-platform/internal/cluster"
	"testing"
	"time"
)

func TestCapacityReadOnlyDecodesSelectedCluster(t *testing.T) {
	db, err := OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	createIndexCluster(t, db, "one", "one", "test", "TCS")
	createIndexCluster(t, db, "two", "two", "test", "TCS")
	if err = db.SaveFleetCapacitySuccess(ctx, cluster.FleetCapacityItem{ClusterID: "one"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	// Unrelated invalid payload proves the single-cluster path does not load the fleet.
	if _, err = db.db.Exec(`INSERT INTO fleet_capacity_snapshots(cluster_id,last_attempt_at,payload,failure_code) VALUES('two',?,'invalid-json','')`, time.Now().UTC().Format(timeLayout)); err != nil {
		t.Fatal(err)
	}
	service := cluster.NewOnboardingService(nil, nil, db)
	if _, err = service.GetCachedCapacity(ctx, "one"); err != nil {
		t.Fatal(err)
	}
	if _, err = service.GetCachedCapacity(ctx, "missing"); cluster.ErrorCode(err) != cluster.CodeClusterNotFound {
		t.Fatalf("missing cluster: %v", err)
	}
}

func TestColdIndexAndCapacityNeverContactRemoteCluster(t *testing.T) {
	db, err := OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	createIndexCluster(t, db, "one", "one", "test", "TCS")
	// Nil connector would panic if a polling request tried to collect live data.
	service := cluster.NewOnboardingService(nil, nil, db)
	result, err := service.SearchResources(context.Background(), "checkout")
	if err != nil || !result.IndexPending || len(result.Items) != 0 {
		t.Fatalf("cold index: %+v %v", result, err)
	}
	if _, err = service.GetCachedCapacity(context.Background(), "one"); cluster.ErrorCode(err) != cluster.CodeCapacityPending {
		t.Fatalf("cold capacity: %v", err)
	}
}
