package store

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"example.com/k8s-platform/internal/cluster"
)

func newJobTestStore(t *testing.T) *SQLite {
	t.Helper()
	db, err := OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}
func enqueueJob(t *testing.T, db *SQLite, id string) cluster.SecurityBaselineRecord {
	t.Helper()
	createIndexCluster(t, db, id, id, "test", "TCS")
	record := cluster.SecurityBaselineRecord{ClusterID: id, Status: cluster.SecurityScanStatusQueued, Scanner: "kube-bench", Framework: "cis", StartedAt: time.Now().UTC()}
	if err := db.EnqueueSecurityJob(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	return record
}
func TestSecurityJobClaimIsExclusiveAcrossWorkers(t *testing.T) {
	db := newJobTestStore(t)
	enqueueJob(t, db, "one")
	enqueueJob(t, db, "two")
	var claimed atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(owner int) {
			defer wg.Done()
			job, err := db.ClaimSecurityJob(context.Background(), fmt.Sprint(owner), 1, 30*time.Second)
			if err != nil {
				t.Error(err)
			}
			if job != nil {
				claimed.Add(1)
			}
		}(i)
	}
	wg.Wait()
	if claimed.Load() != 1 {
		t.Fatalf("global running limit bypassed: %d", claimed.Load())
	}
}
func TestSecurityJobsFenceExpiredAndOldAttempts(t *testing.T) {
	db := newJobTestStore(t)
	record := enqueueJob(t, db, "one")
	ctx := context.Background()
	old, err := db.ClaimSecurityJob(ctx, "old", 2, 30*time.Second)
	if err != nil || old == nil {
		t.Fatal(err)
	}
	if err = db.EnqueueSecurityJob(ctx, record); cluster.ErrorCode(err) != cluster.CodeSecurityScanInProgress {
		t.Fatalf("duplicate accepted: %v", err)
	}
	if _, err = db.db.Exec(`UPDATE security_jobs SET lease_until=0 WHERE cluster_id='one'`); err != nil {
		t.Fatal(err)
	}
	if err = db.RenewSecurityJob(ctx, *old, 30*time.Second); !errors.Is(err, cluster.ErrSecurityJobLeaseLost) {
		t.Fatalf("expired lease revived: %v", err)
	}
	if err = db.SaveSecurityJobRecord(ctx, *old, record); !errors.Is(err, cluster.ErrSecurityJobLeaseLost) {
		t.Fatalf("expired write accepted: %v", err)
	}
	if err = db.ExpireSecurityJobs(ctx); err != nil {
		t.Fatal(err)
	}
	failed, err := db.GetSecurityBaseline(ctx, "one")
	if err != nil || failed.Status != cluster.SecurityScanStatusFailed {
		t.Fatalf("interruption not exposed: %+v %v", failed, err)
	}
	record.StartedAt = time.Now().UTC()
	if err = db.EnqueueSecurityJob(ctx, record); err != nil {
		t.Fatal(err)
	}
	next, err := db.ClaimSecurityJob(ctx, "new", 2, 30*time.Second)
	if err != nil || next == nil {
		t.Fatal(err)
	}
	if err = db.SaveSecurityJobRecord(ctx, *old, old.Record); !errors.Is(err, cluster.ErrSecurityJobLeaseLost) {
		t.Fatalf("old attempt overwrote new: %v", err)
	}
	done := next.Record
	done.Status = cluster.SecurityScanStatusCompleted
	now := time.Now().UTC()
	done.FinishedAt = &now
	if err = db.SaveSecurityJobRecord(ctx, *next, done); err != nil {
		t.Fatal(err)
	}
	history, err := db.ListSecurityBaselineHistory(ctx, "one", 10)
	if err != nil || len(history) != 2 {
		t.Fatalf("atomic history: %d %v", len(history), err)
	}
	if err = db.SaveSecurityJobRecord(ctx, *next, next.Record); !errors.Is(err, cluster.ErrSecurityJobLeaseLost) {
		t.Fatal("late progress overwrote completion")
	}
}
func TestSecurityQueueIsBounded(t *testing.T) {
	db := newJobTestStore(t)
	for i := 0; i < securityQueueLimit; i++ {
		enqueueJob(t, db, fmt.Sprintf("c%03d", i))
	}
	createIndexCluster(t, db, "overflow", "overflow", "test", "TCS")
	err := db.EnqueueSecurityJob(context.Background(), cluster.SecurityBaselineRecord{ClusterID: "overflow", Status: cluster.SecurityScanStatusQueued, StartedAt: time.Now().UTC()})
	if cluster.ErrorCode(err) != cluster.CodeSecurityScanQueueFull {
		t.Fatalf("unbounded queue: %v", err)
	}
	if value, _ := db.GetSecurityBaseline(context.Background(), "overflow"); value != nil {
		t.Fatal("rejected task changed visible state")
	}
}

type queuedScanConnector struct {
	cluster.Connector
	calls   atomic.Int32
	started chan struct{}
}

func (c *queuedScanConnector) RunSecurityHostBenchmark(ctx context.Context, _ cluster.ParsedKubeconfig) ([]cluster.SecurityHostBenchmarkOutput, error) {
	c.calls.Add(1)
	if c.started != nil {
		close(c.started)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return []cluster.SecurityHostBenchmarkOutput{{Node: "one", Data: []byte(`{"Controls":[{"id":"4","tests":[{"section":"4.1","results":[{"test_number":"4.1.1","test_desc":"permissions","status":"PASS"}]}]}]}`)}}, nil
}
func TestQueuedScanSurvivesAPIRestartAndRunsOnWorker(t *testing.T) {
	db := newJobTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	if _, err := db.Create(ctx, cluster.ClusterRecord{ManagedCluster: cluster.ManagedCluster{ID: "one", Name: "one", APIServer: "https://one", CreatedAt: now, LastCheckedAt: now}, EncryptedCredentials: `{"token":"test"}`, Fingerprint: "one"}); err != nil {
		t.Fatal(err)
	}
	connector := &queuedScanConnector{}
	api := cluster.NewOnboardingService(connector, indexCipher{}, db)
	api.EnableQueuedSecurityScans()
	queued, err := api.StartSecurityBaselineScan(ctx, "one", cluster.SecurityScanModeFull)
	if err != nil {
		t.Fatal(err)
	}
	if queued.Status != cluster.SecurityScanStatusQueued || connector.calls.Load() != 0 {
		t.Fatal("API executed remote scan")
	}
	// A fresh API process sees the same durable job; local in-memory maps are irrelevant.
	api2 := cluster.NewOnboardingService(connector, indexCipher{}, db)
	api2.EnableQueuedSecurityScans()
	if _, err = api2.StartSecurityBaselineScan(ctx, "one", cluster.SecurityScanModeFull); cluster.ErrorCode(err) != cluster.CodeSecurityScanInProgress {
		t.Fatal("duplicate across API replicas accepted")
	}
	worker := cluster.NewOnboardingService(connector, indexCipher{}, db)
	run, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); worker.RunSecurityWorker(run, "worker") }()
	defer func() { cancel(); <-done }()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		value, e := db.GetSecurityBaseline(ctx, "one")
		if e != nil {
			t.Fatal(e)
		}
		if value != nil && value.Status == cluster.SecurityScanStatusCompleted {
			if connector.calls.Load() != 1 {
				t.Fatal("duplicate execution")
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("queued job did not finish")
}

func TestWorkerShutdownCancelsRemoteScanAndPersistsFailure(t *testing.T) {
	db := newJobTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	if _, err := db.Create(ctx, cluster.ClusterRecord{ManagedCluster: cluster.ManagedCluster{ID: "one", Name: "one", APIServer: "https://one", CreatedAt: now, LastCheckedAt: now}, EncryptedCredentials: `{"token":"test"}`, Fingerprint: "one"}); err != nil {
		t.Fatal(err)
	}
	connector := &queuedScanConnector{started: make(chan struct{})}
	api := cluster.NewOnboardingService(connector, indexCipher{}, db)
	api.EnableQueuedSecurityScans()
	if _, err := api.StartSecurityBaselineScan(ctx, "one", cluster.SecurityScanModeFull); err != nil {
		t.Fatal(err)
	}
	worker := cluster.NewOnboardingService(connector, indexCipher{}, db)
	run, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); worker.RunSecurityWorker(run, "worker") }()
	select {
	case <-connector.started:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("worker leaked running scan")
	}
	value, err := db.GetSecurityBaseline(ctx, "one")
	if err != nil || value.Status != cluster.SecurityScanStatusFailed {
		t.Fatalf("shutdown status=%+v error=%v", value, err)
	}
}
