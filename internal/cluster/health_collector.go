package cluster

import (
	"context"
	"log"
	"sync"
	"time"
)

const (
	defaultHealthCollectionInterval = time.Minute
	defaultHealthCollectionTimeout  = 20 * time.Second
	defaultHealthConcurrency        = 5
)

// HealthCollector refreshes fleet summary data independently from browser
// requests. Detail pages still use live Kubernetes API calls.
type HealthCollector struct {
	service     *OnboardingService
	interval    time.Duration
	timeout     time.Duration
	concurrency int
}

func NewHealthCollector(service *OnboardingService, interval time.Duration, concurrency int, timeout time.Duration) *HealthCollector {
	if interval <= 0 {
		interval = defaultHealthCollectionInterval
	}
	if concurrency <= 0 {
		concurrency = defaultHealthConcurrency
	}
	if timeout <= 0 {
		timeout = defaultHealthCollectionTimeout
	}
	return &HealthCollector{service: service, interval: interval, concurrency: concurrency, timeout: timeout}
}

func (collector *HealthCollector) Run(ctx context.Context) {
	for {
		startedAt := time.Now()
		collector.collect(ctx, true)
		if ctx.Err() != nil {
			return
		}
		remaining := collector.interval - time.Since(startedAt)
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

// CollectOnce is useful for startup verification and deterministic tests. It
// skips staggering but still enforces the configured concurrency and timeout.
func (collector *HealthCollector) CollectOnce(ctx context.Context) {
	collector.collect(ctx, false)
}

func (collector *HealthCollector) collect(ctx context.Context, stagger bool) {
	if collector == nil || collector.service == nil {
		return
	}
	if _, ok := collector.service.repository.(ClusterHealthRepository); !ok {
		return
	}
	records, err := collector.service.repository.ListRecords(ctx)
	if err != nil {
		log.Printf("health collector: list managed clusters: %v", err)
		return
	}
	if len(records) == 0 {
		return
	}

	jobs := make(chan ClusterRecord)
	workers := collector.concurrency
	if workers > len(records) {
		workers = len(records)
	}
	var waitGroup sync.WaitGroup
	waitGroup.Add(workers)
	for range workers {
		go func() {
			defer waitGroup.Done()
			for record := range jobs {
				requestContext, cancel := context.WithTimeout(ctx, collector.timeout)
				if err := collector.service.refreshHealth(requestContext, record); err != nil {
					log.Printf("health collector: persist cluster %s snapshot: %v", record.ID, err)
				}
				cancel()
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
			return
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
				return
			case <-timer.C:
			}
		}
	}
	close(jobs)
	waitGroup.Wait()
}
