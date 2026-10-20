package cluster

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	resourceCacheTTL    = 120 * time.Second
	resourceLoadTimeout = 25 * time.Second
)

type resourceLoad struct {
	done     chan struct{}
	snapshot cachedResources
	err      error
}

type cachedResources struct {
	workloads []ClusterWorkload
	pods      []ClusterPod
	loadedAt  time.Time
}

func (service *OnboardingService) SearchResources(ctx context.Context, rawQuery string) (GlobalResourceSearch, error) {
	if repository, ok := service.repository.(ResourceIndexRepository); ok {
		return service.searchResourceIndex(ctx, repository, rawQuery)
	}
	return service.searchResourcesLive(ctx, rawQuery)
}

// searchResourceIndex answers searches from the persisted index. The request
// path never calls a managed cluster, regardless of fleet size.
func (service *OnboardingService) searchResourceIndex(ctx context.Context, repository ResourceIndexRepository, rawQuery string) (GlobalResourceSearch, error) {
	query := strings.ToLower(strings.TrimSpace(rawQuery))
	if len([]rune(query)) < 2 {
		return GlobalResourceSearch{}, newError(CodeSearchQueryInvalid)
	}
	count, err := repository.CountClusterResources(ctx)
	if err != nil {
		return GlobalResourceSearch{}, err
	}
	if count == 0 {
		// The first index build can take a few minutes; keep search usable
		// during that window, then never fan out again.
		return service.searchResourcesLive(ctx, rawQuery)
	}
	items, err := repository.SearchClusterResources(ctx, query, 800)
	if err != nil {
		return GlobalResourceSearch{}, err
	}
	sortGlobalResourceResults(items, rawQuery, query)
	result := GlobalResourceSearch{Items: items, FailedClusters: []string{}}
	if len(result.Items) > 200 {
		result.Items = result.Items[:200]
		result.Truncated = true
	}
	return result, nil
}

func sortGlobalResourceResults(items []GlobalResourceResult, rawQuery, query string) {
	sort.Slice(items, func(i, j int) bool {
		left, right := items[i], items[j]
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
}

func (service *OnboardingService) searchResourcesLive(ctx context.Context, rawQuery string) (GlobalResourceSearch, error) {
	query := strings.ToLower(strings.TrimSpace(rawQuery))
	if len([]rune(query)) < 2 {
		return GlobalResourceSearch{}, newError(CodeSearchQueryInvalid)
	}
	records, err := service.repository.ListRecords(ctx)
	if err != nil {
		return GlobalResourceSearch{}, err
	}
	result := GlobalResourceSearch{Items: []GlobalResourceResult{}, FailedClusters: []string{}}
	semaphore := make(chan struct{}, 20)
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
	sortGlobalResourceResults(result.Items, rawQuery, query)
	if len(result.Items) > 200 {
		result.Items = result.Items[:200]
		result.Truncated = true
	}
	sort.Strings(result.FailedClusters)
	return result, nil
}

func (service *OnboardingService) resourcesForSearch(ctx context.Context, record ClusterRecord) (cachedResources, error) {
	service.resourceCacheMu.Lock()
	cached, hasCache := service.resourceCache[record.ID]
	if hasCache && service.now().Sub(cached.loadedAt) < resourceCacheTTL {
		service.resourceCacheMu.Unlock()
		return cached, nil
	}
	load, loading := service.resourceLoads[record.ID]
	if !loading {
		if service.resourceLoads == nil {
			service.resourceLoads = map[string]*resourceLoad{}
		}
		load = &resourceLoad{done: make(chan struct{})}
		service.resourceLoads[record.ID] = load
	}
	service.resourceCacheMu.Unlock()
	if loading {
		select {
		case <-load.done:
			return load.snapshot, load.err
		case <-ctx.Done():
			return cachedResources{}, ctx.Err()
		}
	}

	// Load once even when several users search at the same time. The loader is
	// detached from a single request so one cancellation does not fail peers.
	loadCtx, cancel := context.WithTimeout(context.Background(), resourceLoadTimeout)
	defer cancel()
	snapshot, err := service.loadResources(loadCtx, record)
	service.resourceCacheMu.Lock()
	if err == nil {
		service.resourceCache[record.ID] = snapshot
	}
	load.snapshot = snapshot
	load.err = err
	delete(service.resourceLoads, record.ID)
	close(load.done)
	service.resourceCacheMu.Unlock()
	return snapshot, err
}

func (service *OnboardingService) loadResources(ctx context.Context, record ClusterRecord) (cachedResources, error) {
	if service.connector == nil {
		return cachedResources{}, newError(CodeConnectionFailed)
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
	return snapshot, errors.Join(workloadData.err, podData.err)
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
