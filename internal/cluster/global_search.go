package cluster

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"
)

type cachedResources struct {
	workloads []ClusterWorkload
	pods      []ClusterPod
	loadedAt  time.Time
}

func (service *OnboardingService) SearchResources(ctx context.Context, rawQuery string) (GlobalResourceSearch, error) {
	query := strings.ToLower(strings.TrimSpace(rawQuery))
	if len([]rune(query)) < 2 {
		return GlobalResourceSearch{}, newError(CodeSearchQueryInvalid)
	}
	records, err := service.repository.ListRecords(ctx)
	if err != nil {
		return GlobalResourceSearch{}, err
	}
	result := GlobalResourceSearch{Items: []GlobalResourceResult{}, FailedClusters: []string{}}
	semaphore := make(chan struct{}, 10)
	var mutex sync.Mutex
	var wait sync.WaitGroup
	for _, current := range records {
		record := current
		wait.Add(1)
		go func() {
			defer wait.Done()
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-ctx.Done():
				return
			}
			snapshot, snapshotErr := service.resourcesForSearch(ctx, record)
			items := matchingResources(record.ManagedCluster, snapshot, query)
			mutex.Lock()
			result.Items = append(result.Items, items...)
			if snapshotErr != nil {
				result.FailedClusters = append(result.FailedClusters, record.Name)
			}
			mutex.Unlock()
		}()
	}
	wait.Wait()
	if ctx.Err() != nil {
		return GlobalResourceSearch{}, ctx.Err()
	}
	sort.Slice(result.Items, func(i, j int) bool {
		left, right := result.Items[i], result.Items[j]
		leftExact := strings.EqualFold(left.Name, rawQuery)
		rightExact := strings.EqualFold(right.Name, rawQuery)
		if leftExact != rightExact {
			return leftExact
		}
		leftPrefix := strings.HasPrefix(strings.ToLower(left.Name), query)
		rightPrefix := strings.HasPrefix(strings.ToLower(right.Name), query)
		if leftPrefix != rightPrefix {
			return leftPrefix
		}
		if left.Name != right.Name {
			return left.Name < right.Name
		}
		if left.ClusterName != right.ClusterName {
			return left.ClusterName < right.ClusterName
		}
		return left.Kind < right.Kind
	})
	if len(result.Items) > 200 {
		result.Items = result.Items[:200]
		result.Truncated = true
	}
	sort.Strings(result.FailedClusters)
	return result, nil
}

func (service *OnboardingService) resourcesForSearch(ctx context.Context, record ClusterRecord) (cachedResources, error) {
	service.resourceCacheMu.Lock()
	cached, ok := service.resourceCache[record.ID]
	service.resourceCacheMu.Unlock()
	if ok && service.now().Sub(cached.loadedAt) < 30*time.Second {
		return cached, nil
	}
	parsed, err := service.parsedCredentials(record)
	if err != nil {
		return cachedResources{}, err
	}
	type workloadResult struct {
		items []ClusterWorkload
		err   error
	}
	type podResult struct {
		items []ClusterPod
		err   error
	}
	workloads := make(chan workloadResult, 1)
	pods := make(chan podResult, 1)
	go func() {
		items, loadErr := service.connector.Workloads(ctx, parsed)
		workloads <- workloadResult{items, loadErr}
	}()
	go func() { items, loadErr := service.connector.Pods(ctx, parsed); pods <- podResult{items, loadErr} }()
	workloadData := <-workloads
	podData := <-pods
	snapshot := cachedResources{workloads: workloadData.items, pods: podData.items, loadedAt: service.now()}
	loadErr := errors.Join(workloadData.err, podData.err)
	if loadErr == nil {
		service.resourceCacheMu.Lock()
		service.resourceCache[record.ID] = snapshot
		service.resourceCacheMu.Unlock()
	}
	return snapshot, loadErr
}

func matchingResources(managed ManagedCluster, snapshot cachedResources, query string) []GlobalResourceResult {
	items := make([]GlobalResourceResult, 0)
	for _, workload := range snapshot.workloads {
		if matchesResource(query, workload.Name, workload.Namespace, workload.Kind, strings.Join(workload.Images, " ")) {
			items = append(items, GlobalResourceResult{ClusterID: managed.ID, ClusterName: managed.Name, Environment: managed.Environment, TechnologyStack: managed.TechnologyStack, Kind: workload.Kind, Namespace: workload.Namespace, Name: workload.Name, Status: workload.Status, Images: workload.Images})
		}
	}
	for _, pod := range snapshot.pods {
		if matchesResource(query, pod.Name, pod.Namespace, "Pod", pod.OwnerKind, pod.OwnerName, pod.NodeName, strings.Join(pod.Images, " ")) {
			items = append(items, GlobalResourceResult{ClusterID: managed.ID, ClusterName: managed.Name, Environment: managed.Environment, TechnologyStack: managed.TechnologyStack, Kind: "Pod", Namespace: pod.Namespace, Name: pod.Name, Status: pod.Phase, OwnerKind: pod.OwnerKind, OwnerName: pod.OwnerName, Images: pod.Images})
		}
	}
	return items
}
func matchesResource(query string, values ...string) bool {
	for _, value := range values {
		if strings.Contains(strings.ToLower(value), query) {
			return true
		}
	}
	return false
}
