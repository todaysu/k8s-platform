package cluster

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"log"
	"time"

	"example.com/k8s-platform/internal/metrics"
)

var ErrCollectionLeaseLost = errors.New("collection shard lease lost")
var ErrCollectionShardCount = errors.New("collection shard count differs from persisted mapping")

type CollectionLease struct {
	Shard int
	Count int
	Owner string
	Epoch int64
}
type collectionLeaseKey struct{}

func WithCollectionLease(ctx context.Context, lease CollectionLease) context.Context {
	return context.WithValue(ctx, collectionLeaseKey{}, lease)
}
func CollectionLeaseFromContext(ctx context.Context) (CollectionLease, bool) {
	lease, ok := ctx.Value(collectionLeaseKey{}).(CollectionLease)
	return lease, ok
}
func CollectionShard(id string, count int) int {
	if count <= 0 {
		return 0
	}
	hash := sha256.Sum256([]byte(id))
	return int(binary.BigEndian.Uint64(hash[:8]) % uint64(count))
}

type CollectionShardRepository interface {
	VerifyCollectionMode(context.Context, int) error
	SyncCollectionShards(context.Context, string, int, time.Duration) ([]CollectionLease, error)
	ReleaseCollectionShards(context.Context, string) error
}

var collectionOwned = metrics.Default.Gauge("platform_collection_shards_owned", "Collection shards owned by this process")
var collectionLeaseErrors = metrics.Default.Counter("platform_collection_lease_errors_total", "Collection lease renewal errors")

// RunCollectionShards owns multiple fixed logical shards and cancels their work
// immediately on loss of database coordination. All writes are fenced separately.
func RunCollectionShards(ctx context.Context, repo CollectionShardRepository, count int, run func(context.Context)) {
	owner, err := randomID()
	if err != nil {
		return
	}
	type running struct {
		lease  CollectionLease
		cancel context.CancelFunc
		done   chan struct{}
	}
	current := map[int]running{}
	stopAll := func() {
		for _, job := range current {
			job.cancel()
		}
		for _, job := range current {
			<-job.done
		}
		clear(current)
		collectionOwned.Set(0)
	}
	defer func() {
		stopAll()
		release, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = repo.ReleaseCollectionShards(release, owner)
	}()
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		attempt, cancel := context.WithTimeout(ctx, 15*time.Second)
		leases, syncErr := repo.SyncCollectionShards(attempt, owner, count, 45*time.Second)
		cancel()
		if syncErr != nil {
			if ctx.Err() == nil {
				collectionLeaseErrors.Inc()
				log.Printf("collection shard coordination failed: %v", syncErr)
			}
			stopAll()
		} else {
			wanted := map[int]CollectionLease{}
			for _, lease := range leases {
				wanted[lease.Shard] = lease
			}
			for id, job := range current {
				lease, ok := wanted[id]
				if !ok || lease.Epoch != job.lease.Epoch {
					job.cancel()
					<-job.done
					delete(current, id)
				}
			}
			for id, lease := range wanted {
				if _, exists := current[id]; exists {
					continue
				}
				work, cancel := context.WithCancel(WithCollectionLease(ctx, lease))
				done := make(chan struct{})
				current[id] = running{lease: lease, cancel: cancel, done: done}
				go func() { defer close(done); run(work) }()
			}
			collectionOwned.Set(int64(len(current)))
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func (service *OnboardingService) collectionRecords(ctx context.Context) ([]ClusterRecord, error) {
	records, err := service.repository.ListRecords(ctx)
	if err != nil {
		return nil, err
	}
	metrics.ClustersTotal.Set(int64(len(records)))
	if lease, ok := CollectionLeaseFromContext(ctx); ok {
		filtered := make([]ClusterRecord, 0, len(records)/lease.Count+1)
		for _, record := range records {
			if CollectionShard(record.ID, lease.Count) == lease.Shard {
				filtered = append(filtered, record)
			}
		}
		return filtered, nil
	}
	return records, nil
}
