package cluster

import (
	"context"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"example.com/k8s-platform/internal/metrics"
)

const (
	defaultResourceIndexInterval    = 10 * time.Minute
	defaultResourceIndexTimeout     = 30 * time.Second
	defaultResourceIndexConcurrency = 2
)

// ResourceIndexCollector maintains the persistent search index. It runs on the
// elected leader, so browser searches never fan out to managed clusters.
type ResourceIndexCollector struct {
	service     *OnboardingService
	repository  ResourceIndexRepository
	interval    time.Duration
	timeout     time.Duration
	concurrency int
}

func NewResourceIndexCollector(service *OnboardingService, repository ResourceIndexRepository, interval time.Duration, concurrency int, timeout time.Duration) *ResourceIndexCollector {
	if interval <= 0 {
		interval = defaultResourceIndexInterval
	}
	if concurrency <= 0 {
		concurrency = defaultResourceIndexConcurrency
	}
	if timeout <= 0 {
		timeout = defaultResourceIndexTimeout
	}
	return &ResourceIndexCollector{service: service, repository: repository, interval: interval, concurrency: concurrency, timeout: timeout}
}

func (collector *ResourceIndexCollector) Run(ctx context.Context) {
	first := true
	for {
		startedAt := time.Now()
		// Build the initial index without staggering so search stops falling
		// back to live cluster calls as soon as possible. Later rounds are
		// spread across the interval.
		complete := collector.collect(ctx, !first)
		first = !complete
		if ctx.Err() != nil {
			return
		}
		remaining := collector.interval - time.Since(startedAt)
		if !complete {
			remaining = 30 * time.Second
		}
		if remaining <= 0 {
			continue
		}
		timer := time.NewTimer(remaining)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return
		case <-timer.C:
		}
	}
}

func (collector *ResourceIndexCollector) CollectOnce(ctx context.Context) {
	collector.collect(ctx, false)
}

func (collector *ResourceIndexCollector) collect(ctx context.Context, stagger bool) bool {
	var failed atomic.Bool
	if collector == nil || collector.service == nil || collector.repository == nil {
		return false
	}
	metrics.ResourceIndexRuns.Inc()
	startedAt := time.Now()
	defer func() { metrics.ResourceIndexDurationMS.Set(time.Since(startedAt).Milliseconds()) }()
	records, err := collector.service.collectionRecords(ctx)
	if err != nil {
		log.Printf("resource index: list managed clusters: %v", err)
		return false
	}
	if len(records) == 0 {
		return false
	}
	if _, scoped := CollectionLeaseFromContext(ctx); !scoped {
		metrics.ClustersTotal.Set(int64(len(records)))
	}
	workers := collector.concurrency
	if workers > len(records) {
		workers = len(records)
	}
	jobs := make(chan ClusterRecord)
	var waitGroup sync.WaitGroup
	waitGroup.Add(workers)
	for range workers {
		go func() {
			defer waitGroup.Done()
			for record := range jobs {
				if ctx.Err() != nil {
					return
				}
				queueContext, queueCancel := context.WithTimeout(ctx, 5*time.Minute)
				budgetCtx, release, budgetErr := collector.service.collectionBudget(queueContext, ctx, record.ID, "collection_list", 4)
				if budgetErr != nil {
					failed.Store(true)
					queueCancel()
					if ctx.Err() == nil {
						log.Printf("collection admission expired for cluster %s: %v", record.ID, budgetErr)
					}
					continue
				}
				queueCancel()
				requestContext, cancel := context.WithTimeout(budgetCtx, collector.timeout)

				snapshot, loadErr := collector.service.loadResources(requestContext, record)
				if loadErr != nil {
					failed.Store(true)
					metrics.ResourceIndexErrors.Inc()
					log.Printf("resource index: load cluster %s: %v", record.ID, loadErr)
					release()
					cancel()
					queueCancel()
					continue
				}
				resources := buildIndexedResources(record.ID, snapshot, collector.service.now().UTC())
				if persistErr := collector.repository.ReplaceClusterResources(requestContext, record.ID, resources); persistErr != nil {
					failed.Store(true)
					metrics.ResourceIndexErrors.Inc()
					log.Printf("resource index: persist cluster %s: %v", record.ID, persistErr)
				}
				release()
				cancel()
				queueCancel()
			}
		}()
	}
	spacing := time.Duration(0)
	if stagger && len(records) > 1 {
		spacing = collector.interval / time.Duration(len(records))
	}
	for index, record := range records {
		select {
		case <-ctx.Done():
			close(jobs)
			waitGroup.Wait()
			return false
		case jobs <- record:
		}
		if spacing > 0 && index < len(records)-1 {
			timer := time.NewTimer(spacing)
			select {
			case <-ctx.Done():
				if !timer.Stop() {
					<-timer.C
				}
				close(jobs)
				waitGroup.Wait()
				return false
			case <-timer.C:
			}
		}
	}
	close(jobs)
	waitGroup.Wait()
	return !failed.Load() && ctx.Err() == nil
}

func buildIndexedResources(clusterID string, snapshot cachedResources, updatedAt time.Time) []IndexedResource {
	resources := make([]IndexedResource, 0, len(snapshot.workloads)+len(snapshot.pods))
	for _, workload := range snapshot.workloads {
		resources = append(resources, IndexedResource{
			ClusterID: clusterID,
			Kind:      workload.Kind,
			Namespace: workload.Namespace,
			Name:      workload.Name,
			Status:    workload.Status,
			Images:    workload.Images,
			UpdatedAt: updatedAt,
		})
	}
	for _, pod := range snapshot.pods {
		resources = append(resources, IndexedResource{
			ClusterID: clusterID,
			Kind:      "Pod",
			Namespace: pod.Namespace,
			Name:      pod.Name,
			Status:    pod.Phase,
			OwnerKind: pod.OwnerKind,
			OwnerName: pod.OwnerName,
			NodeName:  pod.NodeName,
			Images:    pod.Images,
			UpdatedAt: updatedAt,
		})
	}
	return resources
}
