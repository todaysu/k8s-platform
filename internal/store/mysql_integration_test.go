package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
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

func TestMySQLSecurityJobsAtomicClaimAndFencing(t *testing.T) {
	dsn := os.Getenv("MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("MYSQL_TEST_DSN is not configured; requires disposable test database")
	}
	db, err := OpenMySQL(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	token, err := jobToken()
	if err != nil {
		t.Fatal(err)
	}
	id := "worker-" + token
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	if _, err = db.Create(ctx, cluster.ClusterRecord{ManagedCluster: cluster.ManagedCluster{ID: id, Name: id, APIServer: "https://test.invalid", CreatedAt: now, LastCheckedAt: now}, Fingerprint: id}); err != nil {
		t.Fatal(err)
	}
	defer db.Delete(ctx, id)
	record := cluster.SecurityBaselineRecord{ClusterID: id, Status: cluster.SecurityScanStatusQueued, StartedAt: now}
	if err = db.EnqueueSecurityJob(ctx, record); err != nil {
		t.Fatal(err)
	}
	jobs := make(chan *cluster.SecurityJob, 10)
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			job, e := db.ClaimSecurityJob(ctx, fmt.Sprint(i), 1, 30*time.Second)
			if e != nil {
				t.Error(e)
			}
			if job != nil {
				jobs <- job
			}
		}(i)
	}
	wg.Wait()
	close(jobs)
	if len(jobs) != 1 {
		t.Fatalf("claim count=%d", len(jobs))
	}
	job := <-jobs
	if err = db.RenewSecurityJob(ctx, *job, 30*time.Second); err != nil {
		t.Fatal(err)
	}
	if _, err = db.db.ExecContext(ctx, `UPDATE security_jobs SET lease_until=0 WHERE cluster_id=?`, id); err != nil {
		t.Fatal(err)
	}
	if err = db.SaveSecurityJobRecord(ctx, *job, record); !errors.Is(err, cluster.ErrSecurityJobLeaseLost) {
		t.Fatalf("expired writer accepted: %v", err)
	}
	if err = db.ExpireSecurityJobs(ctx); err != nil {
		t.Fatal(err)
	}
	failed, err := db.GetSecurityBaseline(ctx, id)
	if err != nil || failed.Status != cluster.SecurityScanStatusFailed {
		t.Fatalf("expired job: %+v %v", failed, err)
	}
	record.StartedAt = time.Now().UTC().Truncate(time.Microsecond)
	if err = db.EnqueueSecurityJob(ctx, record); err != nil {
		t.Fatal(err)
	}
	next, err := db.ClaimSecurityJob(ctx, "replacement", 1, 30*time.Second)
	if err != nil || next == nil {
		t.Fatal(err)
	}
	if err = db.SaveSecurityJobRecord(ctx, *job, job.Record); !errors.Is(err, cluster.ErrSecurityJobLeaseLost) {
		t.Fatal("old attempt wrote into replacement")
	}
	done := next.Record
	done.Status = cluster.SecurityScanStatusCompleted
	finished := time.Now().UTC()
	done.FinishedAt = &finished
	if err = db.SaveSecurityJobRecord(ctx, *next, done); err != nil {
		t.Fatal(err)
	}
	history, err := db.ListSecurityBaselineHistory(ctx, id, 10)
	if err != nil || len(history) != 2 {
		t.Fatalf("history=%d error=%v", len(history), err)
	}
}

func TestMySQLSharedRequestQuota(t *testing.T) {
	dsn := os.Getenv("MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("MYSQL_TEST_DSN is not configured")
	}
	db, err := OpenMySQL(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	token, err := db.AcquireRequestQuota(ctx, "monitoring", "test", 1, 1, 15*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer db.ReleaseRequestQuota(ctx, token)
	if _, err = db.AcquireRequestQuota(ctx, "monitoring", "other", 1, 1, 15*time.Second); !errors.Is(err, cluster.ErrRequestQuotaExceeded) {
		t.Fatalf("shared quota not enforced: %v", err)
	}
	if err = db.RenewRequestQuota(ctx, token, 15*time.Second); err != nil {
		t.Fatal(err)
	}
	if _, err = db.db.Exec(`UPDATE platform_request_quotas SET lease_until=0 WHERE token=?`, token); err != nil {
		t.Fatal(err)
	}
	if err = db.RenewRequestQuota(ctx, token, 15*time.Second); !errors.Is(err, cluster.ErrRequestQuotaLeaseLost) {
		t.Fatal("expired quota revived")
	}
	next, err := db.AcquireRequestQuota(ctx, "monitoring", "other", 1, 1, 15*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer db.ReleaseRequestQuota(ctx, next)
}

func TestMySQLCollectionHeartbeatAndLateWriterFencing(t *testing.T) {
	dsn := os.Getenv("MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("MYSQL_TEST_DSN is not configured")
	}
	db, err := OpenMySQL(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	token, _ := jobToken()
	owner := "test-" + token
	defer db.ReleaseCollectionShards(ctx, owner)
	leases, err := db.SyncCollectionShards(ctx, owner, 16, 30*time.Second)
	if err != nil || len(leases) != 16 {
		t.Fatalf("initial ownership: %d %v", len(leases), err)
	}
	id := "cluster-" + token
	lease := leases[cluster.CollectionShard(id, 16)]
	now := time.Now().UTC()
	if _, err = db.Create(ctx, cluster.ClusterRecord{ManagedCluster: cluster.ManagedCluster{ID: id, Name: id, APIServer: "https://test.invalid", CreatedAt: now, LastCheckedAt: now}, Fingerprint: id}); err != nil {
		t.Fatal(err)
	}
	defer db.Delete(ctx, id)
	scoped := cluster.WithCollectionLease(ctx, lease)
	tx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = checkCollectionLease(scoped, tx, false, id); err != nil {
		t.Fatal(err)
	}
	// An open data transaction holds a shared epoch lock. Normal heartbeat must
	// use the independent member row, not wait for this data transaction.
	renewal, cancel := context.WithTimeout(ctx, 2*time.Second)
	updated, err := db.SyncCollectionShards(renewal, owner, 16, 30*time.Second)
	cancel()
	if err != nil || len(updated) != 16 {
		t.Fatalf("heartbeat blocked by snapshot transaction: %v", err)
	}
	if err = commitCollectionTx(scoped, tx, false); err != nil {
		t.Fatal(err)
	}
	if _, err = db.db.Exec(`UPDATE collection_members SET lease_until=0 WHERE owner=?`, owner); err != nil {
		t.Fatal(err)
	}
	replacement := "replacement-" + token
	defer db.ReleaseCollectionShards(ctx, replacement)
	adopted, err := db.SyncCollectionShards(ctx, replacement, 16, 30*time.Second)
	if err != nil || len(adopted) != 16 {
		t.Fatalf("takeover: %d %v", len(adopted), err)
	}
	if err = db.ReplaceClusterResources(scoped, id, nil); !errors.Is(err, cluster.ErrCollectionLeaseLost) {
		t.Fatalf("late index writer accepted: %v", err)
	}
	if err = db.UpsertEvents(scoped, []cluster.ClusterEvent{{ClusterID: id, EventUID: "late", FirstObservedAt: now, LastObservedAt: now}}); !errors.Is(err, cluster.ErrCollectionLeaseLost) {
		t.Fatalf("late event writer accepted: %v", err)
	}
}

func TestMySQLQuotaReleaseAndExpiryUseSameLockOrder(t *testing.T) {
	dsn := os.Getenv("MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("MYSQL_TEST_DSN is not configured")
	}
	db, err := OpenMySQL(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	failures := make(chan error, 8)
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for step := 0; step < 10; step++ {
				token, e := db.AcquireRequestQuota(ctx, "collection_list", fmt.Sprintf("quota-test-%d", worker), 64, 1, 15*time.Second)
				if e != nil {
					failures <- e
					return
				}
				if e = db.ReleaseRequestQuota(ctx, token); e != nil {
					failures <- e
					return
				}
			}
		}(worker)
	}
	wg.Wait()
	close(failures)
	for e := range failures {
		t.Error(e)
	}
}

func TestMySQLFleetGroupsMatchPerClusterDailyAndRaw(t *testing.T) {
	dsn := os.Getenv("MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("MYSQL_TEST_DSN is not configured")
	}
	db, err := OpenMySQL(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	suffix, _ := jobToken()
	ids := []string{"bulk-a-" + suffix, "bulk-b-" + suffix}
	now := time.Now().UTC().Truncate(time.Second)
	for _, id := range ids {
		if _, err = db.Create(ctx, cluster.ClusterRecord{ManagedCluster: cluster.ManagedCluster{ID: id, Name: id, APIServer: "https://test.invalid", CreatedAt: now, LastCheckedAt: now}, Fingerprint: id}); err != nil {
			t.Fatal(err)
		}
		defer db.Delete(ctx, id)
		if err = db.UpsertEvents(ctx, []cluster.ClusterEvent{{ClusterID: id, EventUID: "event", Type: "Warning", Reason: "FailedScheduling", Message: "no nodes", RegardingKind: "Pod", RegardingNamespace: "app", RegardingName: "p", RegardingUID: "uid", OccurrenceCount: 3, FirstObservedAt: now, LastObservedAt: now}}); err != nil {
			t.Fatal(err)
		}
	}
	// The second cluster exercises the per-cluster fallback when no rollup exists.
	if _, err = db.db.Exec(`DELETE FROM event_daily WHERE cluster_id=?`, ids[1]); err != nil {
		t.Fatal(err)
	}
	q := cluster.EventQuery{Since: now.Add(-time.Hour), Until: now.Add(time.Second), Namespace: "app"}
	for _, daily := range []bool{false, true} {
		all, e := db.AggregateFleetEventGroups(ctx, ids, q, daily)
		if e != nil {
			t.Fatal(e)
		}
		for _, id := range ids {
			expected, e := db.AggregateEvents(ctx, id, q)
			if daily {
				if rows, de := db.AggregateDailyEvents(ctx, id, q); de == nil && len(rows) > 0 {
					expected = rows
				}
			}
			if e != nil || len(all[id]) != len(expected) || len(expected) != 1 || all[id][0].EventCount != expected[0].EventCount || all[id][0].OccurrenceCount != expected[0].OccurrenceCount || all[id][0].Category != expected[0].Category {
				t.Fatalf("bulk mismatch daily=%v got=%+v want=%+v err=%v", daily, all[id], expected, e)
			}
		}
	}
}

func TestMySQLIndexedSearchPrefixAndSubstringFallback(t *testing.T) {
	dsn := os.Getenv("MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("MYSQL_TEST_DSN is not configured")
	}
	db, err := OpenMySQL(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	token, _ := jobToken()
	id := "search-" + token
	prefix := "java-" + token
	now := time.Now().UTC()
	if _, err = db.Create(ctx, cluster.ClusterRecord{ManagedCluster: cluster.ManagedCluster{ID: id, Name: id, APIServer: "https://test.invalid", CreatedAt: now, LastCheckedAt: now}, Fingerprint: id}); err != nil {
		t.Fatal(err)
	}
	defer db.Delete(ctx, id)
	resources := []cluster.IndexedResource{}
	for i := 0; i < 30; i++ {
		resources = append(resources, cluster.IndexedResource{ClusterID: id, Kind: "Pod", Namespace: "app", Name: fmt.Sprintf("%s-%03d", prefix, i), Status: "Running", Images: []string{"example/unique-image-" + token}})
	}
	if err = db.ReplaceClusterResources(ctx, id, resources); err != nil {
		t.Fatal(err)
	}
	rows, err := db.SearchClusterResources(ctx, prefix, 20)
	if err != nil || len(rows) != 20 {
		t.Fatalf("prefix path: count=%d err=%v", len(rows), err)
	}
	rows, err = db.SearchClusterResources(ctx, "unique-image-"+token, 20)
	if err != nil || len(rows) != 20 {
		t.Fatalf("image substring fallback: count=%d err=%v", len(rows), err)
	}
}
