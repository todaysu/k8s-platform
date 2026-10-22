package store

import (
	"context"
	"errors"
	"example.com/k8s-platform/internal/cluster"
	"sync"
	"testing"
	"time"
)

func TestRequestQuotasBoundAllReplicasAndRecoverExpiredSlots(t *testing.T) {
	db := newJobTestStore(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	tokens := make(chan string, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			token, err := db.AcquireRequestQuota(ctx, "monitoring", "same-cluster", 8, 2, 15*time.Second)
			if err != nil && !errors.Is(err, cluster.ErrRequestQuotaExceeded) {
				t.Error(err)
			}
			if err == nil {
				tokens <- token
			}
		}()
	}
	wg.Wait()
	close(tokens)
	if len(tokens) != 2 {
		t.Fatalf("per-cluster quota multiplied: %d", len(tokens))
	}
	for token := range tokens {
		if err := db.ReleaseRequestQuota(ctx, token); err != nil {
			t.Fatal(err)
		}
	}
	token, err := db.AcquireRequestQuota(ctx, "session", "one", 1, 1, 15*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.AcquireRequestQuota(ctx, "session", "two", 1, 1, 15*time.Second); !errors.Is(err, cluster.ErrRequestQuotaExceeded) {
		t.Fatal("global session quota bypassed")
	}
	if _, err = db.db.Exec(`UPDATE platform_request_quotas SET lease_until=0 WHERE token=?`, token); err != nil {
		t.Fatal(err)
	}
	if err = db.RenewRequestQuota(ctx, token, 15*time.Second); !errors.Is(err, cluster.ErrRequestQuotaLeaseLost) {
		t.Fatal("expired request revived")
	}
	replacement, err := db.AcquireRequestQuota(ctx, "session", "two", 1, 1, 15*time.Second)
	if err != nil || replacement == token {
		t.Fatalf("crashed request did not release capacity: %v", err)
	}
	// Releasing a stale token cannot remove another request's slot.
	if err = db.ReleaseRequestQuota(ctx, token); err != nil {
		t.Fatal(err)
	}
	if _, err = db.AcquireRequestQuota(ctx, "session", "three", 1, 1, 15*time.Second); !errors.Is(err, cluster.ErrRequestQuotaExceeded) {
		t.Fatal("stale release removed replacement")
	}
}
