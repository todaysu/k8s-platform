package cluster

import (
	"context"
	"log"
	"sync"
	"time"

	"example.com/k8s-platform/internal/metrics"
)

type CapacityCollector struct {
	service     *OnboardingService
	interval    time.Duration
	timeout     time.Duration
	concurrency int
}

func NewCapacityCollector(service *OnboardingService, interval time.Duration, concurrency int, timeout time.Duration) *CapacityCollector {
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	if concurrency <= 0 {
		concurrency = 3
	}
	if timeout <= 0 {
		timeout = 45 * time.Second
	}
	return &CapacityCollector{service: service, interval: interval, concurrency: concurrency, timeout: timeout}
}

func (collector *CapacityCollector) Run(ctx context.Context) {
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

func (collector *CapacityCollector) CollectOnce(ctx context.Context) { collector.collect(ctx, false) }

func (collector *CapacityCollector) collect(ctx context.Context, stagger bool) {
	if collector == nil || collector.service == nil {
		return
	}
	metrics.CapacityCollectorRuns.Inc()
	startedAt := time.Now()
	defer func() { metrics.CapacityCollectorDurationMS.Set(time.Since(startedAt).Milliseconds()) }()
	repository, ok := collector.service.repository.(FleetCapacityRepository)
	if !ok {
		return
	}
	records, err := collector.service.repository.ListRecords(ctx)
	if err != nil || len(records) == 0 {
		if err != nil {
			log.Printf("capacity collector: list managed clusters: %v", err)
		}
		return
	}
	metrics.ClustersTotal.Set(int64(len(records)))
	classification, err := collector.service.activeCapacityClassification(ctx)
	if err != nil {
		log.Printf("capacity collector: read classification: %v", err)
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
				snapshot, collectErr := collector.service.capacityForRecordWithClassification(requestContext, record, classification)
				cancel()
				at := collector.service.now().UTC()
				metadata := record.ManagedCluster
				if collectErr != nil {
					metrics.CapacityCollectorErrors.Inc()
					failure := FleetCapacityFailure{ClusterID: metadata.ID, ClusterName: metadata.Name, Environment: metadata.Environment, ClusterType: metadata.ClusterType, TechnologyStack: metadata.TechnologyStack, Code: ErrorCode(collectErr)}
					if persistErr := repository.SaveFleetCapacityFailure(ctx, failure, at); persistErr != nil {
						log.Printf("capacity collector: persist cluster %s failure: %v", record.ID, persistErr)
					}
					continue
				}
				item := FleetCapacityItem{ClusterID: metadata.ID, ClusterName: metadata.Name, Environment: metadata.Environment, ClusterType: metadata.ClusterType, TechnologyStack: metadata.TechnologyStack, Capacity: snapshot}
				if persistErr := repository.SaveFleetCapacitySuccess(ctx, item, at); persistErr != nil {
					log.Printf("capacity collector: persist cluster %s snapshot: %v", record.ID, persistErr)
				}
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
