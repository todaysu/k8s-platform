package store

import (
	"context"
	"errors"
	"example.com/k8s-platform/internal/cluster"
	"fmt"
	"testing"
	"time"
)

func settleShards(t *testing.T, db *SQLite, owners []string, count int) map[string][]cluster.CollectionLease {
	t.Helper()
	result := map[string][]cluster.CollectionLease{}
	for n := 0; n < 3; n++ {
		for _, owner := range owners {
			leases, err := db.SyncCollectionShards(context.Background(), owner, count, 30*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			result[owner] = leases
		}
	}
	return result
}
func TestCollectionShardBalanceTakeoverAndEpochFencing(t *testing.T) {
	db := newJobTestStore(t)
	ctx := context.Background()
	leases := settleShards(t, db, []string{"a", "b"}, 16)
	if len(leases["a"]) != 8 || len(leases["b"]) != 8 {
		t.Fatalf("unbalanced shards: %v", leases)
	}
	old := leases["a"][0]
	id := ""
	for i := 0; ; i++ {
		id = fmt.Sprint("cluster-", i)
		if cluster.CollectionShard(id, 16) == old.Shard {
			break
		}
	}
	createIndexCluster(t, db, id, id, "test", "TCS")
	stale := cluster.WithCollectionLease(ctx, old)
	if err := db.SaveFleetCapacitySuccess(stale, cluster.FleetCapacityItem{ClusterID: id}, time.Now()); err != nil {
		t.Fatal(err)
	}
	// Simulate process death, without a graceful release.
	if _, err := db.db.Exec(`UPDATE collection_members SET lease_until=0 WHERE owner='a'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.Exec(`UPDATE collection_shards SET lease_until=0 WHERE owner='a'`); err != nil {
		t.Fatal(err)
	}
	replacement, err := db.SyncCollectionShards(ctx, "b", 16, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if len(replacement) != 16 {
		t.Fatal("survivor did not take over")
	}
	var next cluster.CollectionLease
	for _, lease := range replacement {
		if lease.Shard == old.Shard {
			next = lease
		}
	}
	if next.Epoch <= old.Epoch {
		t.Fatal("epoch did not advance")
	}
	if err = db.ReplaceClusterResources(stale, id, []cluster.IndexedResource{{ClusterID: id, Kind: "Pod", Name: "stale"}}); !errors.Is(err, cluster.ErrCollectionLeaseLost) {
		t.Fatalf("stale index write: %v", err)
	}
	if err = db.UpsertEvents(stale, []cluster.ClusterEvent{{ClusterID: id, EventUID: "old", FirstObservedAt: time.Now(), LastObservedAt: time.Now()}}); !errors.Is(err, cluster.ErrCollectionLeaseLost) {
		t.Fatalf("stale event write: %v", err)
	}
	if err = db.SaveFleetCapacityFailure(stale, cluster.FleetCapacityFailure{ClusterID: id, Code: "old"}, time.Now()); err == nil {
		t.Fatal("stale capacity failure accepted")
	}
	if err = db.UpdateClusterHealth(stale, cluster.ManagedCluster{ID: id, ConnectionStatus: "stale", LastCheckedAt: time.Now()}); err == nil {
		t.Fatal("stale health accepted")
	}
	if err = db.SetEventCollectorStatus(stale, cluster.EventCollectorStatus{ClusterID: id, State: "stale", UpdatedAt: time.Now()}); err == nil {
		t.Fatal("stale checkpoint accepted")
	}
	current := cluster.WithCollectionLease(ctx, next)
	if err = db.ReplaceClusterResources(current, id, []cluster.IndexedResource{{ClusterID: id, Kind: "Pod", Name: "current"}}); err != nil {
		t.Fatal(err)
	}
	if _, err = db.SyncCollectionShards(ctx, "other", 8, 30*time.Second); !errors.Is(err, cluster.ErrCollectionShardCount) {
		t.Fatal("live shard mapping changed")
	}
}
func TestCollectionShardRejectsWrongClusterAndExpiresBeforeCommit(t *testing.T) {
	db := newJobTestStore(t)
	leases, err := db.SyncCollectionShards(context.Background(), "owner", 16, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	lease := leases[0]
	id := "outside"
	for cluster.CollectionShard(id, 16) == lease.Shard {
		id += "x"
	}
	createIndexCluster(t, db, id, id, "test", "TCS")
	scoped := cluster.WithCollectionLease(context.Background(), lease)
	if err = db.ReplaceClusterResources(scoped, id, nil); !errors.Is(err, cluster.ErrCollectionLeaseLost) {
		t.Fatal("write crossed shard boundary")
	}
}

func TestCollectionTransactionRechecksLeaseBeforeCommit(t *testing.T) {
	db := newJobTestStore(t)
	ctx := context.Background()
	leases, err := db.SyncCollectionShards(ctx, "owner", 1, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	createIndexCluster(t, db, "one", "one", "test", "TCS")
	scoped := cluster.WithCollectionLease(ctx, leases[0])
	tx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = checkCollectionLease(scoped, tx, true, "one"); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`UPDATE clusters SET connection_status='late' WHERE id='one'`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`UPDATE collection_members SET lease_until=0 WHERE owner='owner'`); err != nil {
		t.Fatal(err)
	}
	if err = commitCollectionTx(scoped, tx, true); !errors.Is(err, cluster.ErrCollectionLeaseLost) {
		t.Fatalf("expired transaction committed: %v", err)
	}
	tx.Rollback()
	record, err := db.GetRecord(ctx, "one")
	if err != nil || record.ConnectionStatus == "late" {
		t.Fatal("expired mutation was published")
	}
}

func TestCollectionModeRejectsAccidentalLegacyWriter(t *testing.T) {
	db := newJobTestStore(t)
	ctx := context.Background()
	if err := db.VerifyCollectionMode(ctx, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SyncCollectionShards(ctx, "owner", 16, 30*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := db.VerifyCollectionMode(ctx, 0); !errors.Is(err, cluster.ErrCollectionShardCount) {
		t.Fatal("legacy collector could bypass active shard ownership")
	}
	if err := db.VerifyCollectionMode(ctx, 16); err != nil {
		t.Fatal(err)
	}
}
