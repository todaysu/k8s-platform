package cluster

import (
	"container/heap"
	"context"
	"fmt"
	"sort"
	"strings"
)

// Categories are navigation indexes, never claims of a common root cause.
func eventCategory(e ClusterEvent) (string, string) {
	reason, message := strings.ToLower(e.Reason), strings.ToLower(e.Message)
	switch {
	case reason == "failedscheduling":
		return "scheduling", "调度异常"
	case strings.Contains(reason, "mount") || strings.Contains(reason, "attachvolume"):
		return "storage", "存储挂载异常"
	case strings.Contains(message, "imagepull") || strings.Contains(message, "pull image") || strings.Contains(message, "pulling image") || reason == "errimagepull" || reason == "imagepullbackoff":
		return "image-pull", "镜像拉取异常"
	case reason == "unhealthy":
		return "probe", "健康探针异常"
	case reason == "backoff" || strings.Contains(message, "crashloop") || strings.Contains(message, "restarting failed container"):
		return "restart", "容器退出或重启"
	case reason == "evicted":
		return "eviction", "资源驱逐"
	case e.RegardingKind == "Node":
		return "node", "节点异常"
	default:
		return "other-" + EventFingerprint(e), "其他异常 · " + e.Reason
	}
}
func eventObjectKey(e ClusterEvent) string {
	identity := e.RegardingUID
	if identity == "" {
		identity = "name:" + e.RegardingName
	}
	return strings.Join([]string{e.ClusterID, e.RegardingKind, e.RegardingNamespace, identity}, "/")
}
func eventRowKey(e FleetEvent) string { return e.ClusterID + "/" + e.EventUID }
func eventNewer(a, b FleetEvent) bool {
	if a.LastObservedAt.Equal(b.LastObservedAt) {
		return eventRowKey(a) > eventRowKey(b)
	}
	return a.LastObservedAt.After(b.LastObservedAt)
}

type eventHeap []FleetEvent

func (h eventHeap) Len() int           { return len(h) }
func (h eventHeap) Less(i, j int) bool { return eventNewer(h[j], h[i]) }
func (h eventHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *eventHeap) Push(x any)        { *h = append(*h, x.(FleetEvent)) }
func (h *eventHeap) Pop() any          { old := *h; n := len(old); v := old[n-1]; *h = old[:n-1]; return v }

type eventBucket struct {
	issue             FleetEventIssue
	clusters, objects map[string]bool
}
type fleetAccumulator struct {
	query                    FleetEventQuery
	total                    int
	groups                   map[string]*eventBucket
	objects                  map[string]FleetEventObject
	top                      eventHeap
	types, namespaces, kinds map[string]bool
}

func newFleetAccumulator(q FleetEventQuery) *fleetAccumulator {
	return &fleetAccumulator{query: q, groups: map[string]*eventBucket{}, objects: map[string]FleetEventObject{}, types: map[string]bool{}, namespaces: map[string]bool{}, kinds: map[string]bool{}}
}
func (a *fleetAccumulator) retain(e FleetEvent) {
	if a.query.View != "events" {
		return
	}
	capacity := a.query.Page * a.query.Limit
	if a.top.Len() < capacity {
		heap.Push(&a.top, e)
	} else if eventNewer(e, a.top[0]) {
		heap.Pop(&a.top)
		heap.Push(&a.top, e)
	}
}
func (a *fleetAccumulator) add(e FleetEvent) {
	attention := eventSeverity(e.ClusterEvent) != "normal"
	category, title := eventCategory(e.ClusterEvent)
	if a.query.Issue != "" && category != a.query.Issue {
		return
	}
	if !a.query.IncludeNormal && !attention {
		return
	}
	a.total++
	a.types[e.Type] = true
	a.namespaces[e.RegardingNamespace] = true
	a.kinds[e.RegardingKind] = true
	key := eventObjectKey(e.ClusterEvent)
	object := a.objects[key]
	if object.ClusterID == "" {
		object = FleetEventObject{ClusterID: e.ClusterID, ClusterName: e.ClusterName, Namespace: e.RegardingNamespace, Kind: e.RegardingKind, Name: e.RegardingName, UID: e.RegardingUID, Scope: e.Scope}
	}
	object.EventCount++
	object.OccurrenceCount += max(e.OccurrenceCount, 1)
	if e.LastObservedAt.After(object.LastObservedAt) {
		object.LastObservedAt = e.LastObservedAt
		object.Message = e.Message
	}
	a.objects[key] = object
	a.retain(e)
	if !attention {
		return
	}
	bucket := a.groups[category]
	if bucket == nil {
		bucket = &eventBucket{issue: FleetEventIssue{Fingerprint: category, Title: title, Severity: "warning", Type: e.Type, Reason: e.Reason, RegardingKind: e.RegardingKind, Message: e.Message, FirstObservedAt: e.FirstObservedAt}, clusters: map[string]bool{}, objects: map[string]bool{}}
		a.groups[category] = bucket
	}
	bucket.issue.EventCount++
	bucket.issue.OccurrenceCount += max(e.OccurrenceCount, 1)
	if e.FirstObservedAt.Before(bucket.issue.FirstObservedAt) {
		bucket.issue.FirstObservedAt = e.FirstObservedAt
	}
	if e.LastObservedAt.After(bucket.issue.LastObservedAt) {
		bucket.issue.LastObservedAt = e.LastObservedAt
	}
	// Prefer a useful full error over a short BackOff symptom; all variants remain in object evidence.
	if len(e.Message) > len(bucket.issue.Message) {
		bucket.issue.Message = e.Message
	}
	bucket.clusters[e.ClusterID] = true
	bucket.objects[key] = true
}
func (a *fleetAccumulator) merge(b *fleetAccumulator) {
	a.total += b.total
	for k, v := range b.objects {
		a.objects[k] = v
	}
	for k := range b.types {
		a.types[k] = true
	}
	for k := range b.namespaces {
		a.namespaces[k] = true
	}
	for k := range b.kinds {
		a.kinds[k] = true
	}
	for _, e := range b.top {
		a.retain(e)
	}
	for k, other := range b.groups {
		bucket := a.groups[k]
		if bucket == nil {
			a.groups[k] = other
			continue
		}
		bucket.issue.EventCount += other.issue.EventCount
		bucket.issue.OccurrenceCount += other.issue.OccurrenceCount
		if other.issue.FirstObservedAt.Before(bucket.issue.FirstObservedAt) {
			bucket.issue.FirstObservedAt = other.issue.FirstObservedAt
		}
		if other.issue.LastObservedAt.After(bucket.issue.LastObservedAt) {
			bucket.issue.LastObservedAt = other.issue.LastObservedAt
		}
		if len(other.issue.Message) > len(bucket.issue.Message) {
			bucket.issue.Message = other.issue.Message
		}
		for key := range other.clusters {
			bucket.clusters[key] = true
		}
		for key := range other.objects {
			bucket.objects[key] = true
		}
	}
}
func eventPageBounds(total, page, size int) (int, int) {
	start := min((page-1)*size, total)
	return start, min(start+size, total)
}

func (service *OnboardingService) GetFleetEvents(ctx context.Context, q FleetEventQuery) (FleetEventOverview, error) {
	if service.eventRepository == nil {
		return FleetEventOverview{}, newError(CodePersistenceFailed)
	}
	records, err := service.repository.ListRecords(ctx)
	if err != nil {
		return FleetEventOverview{}, err
	}
	classification, err := service.activeCapacityClassification(ctx)
	if err != nil {
		return FleetEventOverview{}, err
	}
	if q.Page < 1 {
		q.Page = 1
	}
	if q.Page > 10000 {
		return FleetEventOverview{}, newError(CodeSearchQueryInvalid)
	}
	if q.Limit < 1 || q.Limit > 100 {
		q.Limit = 25
	}
	if q.View == "" {
		q.View = "issues"
	}
	if q.View != "issues" && q.View != "events" && q.View != "objects" && q.View != "resources" {
		return FleetEventOverview{}, newError(CodeSearchQueryInvalid)
	}
	if q.Scope != "" && q.Scope != "business" && q.Scope != "platform" {
		return FleetEventOverview{}, newError(CodeSearchQueryInvalid)
	}
	if q.View != "events" && q.View != "resources" {
		q.IncludeNormal = false
	}
	result := FleetEventOverview{Items: []FleetEvent{}, Issues: []FleetEventIssue{}, Objects: []FleetEventObject{}, Collectors: []EventCollectorStatus{}, Failures: []FleetEventFailure{}, Since: q.Since, Until: q.Until, BusinessConfigured: len(classification.BusinessNamespaces) > 0}
	all := newFleetAccumulator(q)
	business := map[string]bool{}
	for _, n := range classification.BusinessNamespaces {
		business[n] = true
	}
	for _, record := range records {
		if q.Environment != "" && record.Environment != q.Environment || q.TechnologyStack != "" && record.TechnologyStack != q.TechnologyStack || q.ClusterID != "" && record.ID != q.ClusterID {
			continue
		}
		result.Summary.TotalClusters++
		local := newFleetAccumulator(q)
		scanQuery := q.EventQuery
		scanQuery.BusinessNamespaces = classification.BusinessNamespaces
		scanQuery.AttentionOnly = !q.IncludeNormal
		scanQuery.ComponentOnly = false
		visit := func(e ClusterEvent) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			scope := "platform"
			if e.RegardingKind != "Node" && business[e.RegardingNamespace] {
				scope = "business"
			}
			// Defensive filtering also makes alternative repositories respect the same contract.
			if q.Scope != "" && q.Scope != scope {
				return nil
			}
			local.add(FleetEvent{ClusterEvent: e, ClusterName: record.Name, Environment: record.Environment, TechnologyStack: record.TechnologyStack, Scope: scope})
			return nil
		}
		if scanner, ok := service.eventRepository.(EventScanner); ok {
			err = scanner.ScanEvents(ctx, record.ID, scanQuery, visit)
		} else {
			err = service.scanEventPages(ctx, record.ID, scanQuery, visit)
		}
		if err != nil {
			result.Failures = append(result.Failures, FleetEventFailure{ClusterID: record.ID, ClusterName: record.Name, Code: ErrorCode(err)})
			result.Collectors = append(result.Collectors, EventCollectorStatus{ClusterID: record.ID, ClusterName: record.Name, State: "error", LastError: "持久化事件读取失败"})
			result.Summary.CollectorsFailed++
			continue
		}
		if q.View == "resources" || q.View == "objects" {
			local.objects = service.eventWorkloadGroups(ctx, record, local.objects)
		}
		all.merge(local)
		summary, summaryErr := service.eventRepository.EventSummary(ctx, record.ID, q.Since, q.Until)
		status := EventCollectorStatus{ClusterID: record.ID, ClusterName: record.Name, State: summary.CollectorState, LastSuccessAt: summary.LastSuccessAt, LastError: summary.LastError}
		if summaryErr != nil {
			status.State = "error"
			status.LastError = "采集状态读取失败"
		}
		result.Collectors = append(result.Collectors, status)
		if status.State == "watching" {
			result.Summary.CollectorsHealthy++
		} else {
			result.Summary.CollectorsFailed++
		}
	}
	if ctx.Err() != nil {
		return FleetEventOverview{}, ctx.Err()
	}
	groups := []FleetEventIssue{}
	for _, b := range all.groups {
		b.issue.ClusterIDs = sortedKeys(b.clusters)
		b.issue.AffectedClusters = len(b.clusters)
		b.issue.AffectedObjects = len(b.objects)
		groups = append(groups, b.issue)
	}
	sort.Slice(groups, func(i, j int) bool {
		if groups[i].LastObservedAt.Equal(groups[j].LastObservedAt) {
			return groups[i].Fingerprint < groups[j].Fingerprint
		}
		return groups[i].LastObservedAt.After(groups[j].LastObservedAt)
	})
	objects := []FleetEventObject{}
	clusterSet := map[string]bool{}
	for _, o := range all.objects {
		objects = append(objects, o)
		clusterSet[o.ClusterID] = true
	}
	sort.Slice(objects, func(i, j int) bool {
		if objects[i].LastObservedAt.Equal(objects[j].LastObservedAt) {
			return fmt.Sprint(objects[i].ClusterID, objects[i].Namespace, objects[i].Kind, objects[i].UID, objects[i].Name) < fmt.Sprint(objects[j].ClusterID, objects[j].Namespace, objects[j].Kind, objects[j].UID, objects[j].Name)
		}
		return objects[i].LastObservedAt.After(objects[j].LastObservedAt)
	})
	result.Total = int64(all.total)
	result.Summary.EventRecords = int64(all.total)
	result.Summary.ProblemGroups = len(groups)
	result.Summary.WarningGroups = len(groups)
	result.Summary.AffectedObjects = len(objects)
	result.Summary.AffectedClusters = len(clusterSet)
	total := len(groups)
	switch q.View {
	case "objects", "resources":
		total = len(objects)
		start, end := eventPageBounds(total, q.Page, q.Limit)
		result.Objects = objects[start:end]
	case "events":
		total = all.total
		rows := []FleetEvent(all.top)
		sort.Slice(rows, func(i, j int) bool { return eventNewer(rows[i], rows[j]) })
		start, end := eventPageBounds(len(rows), q.Page, q.Limit)
		result.Items = rows[start:end]
	default:
		start, end := eventPageBounds(total, q.Page, q.Limit)
		result.Issues = groups[start:end]
	}
	result.Pagination = EventPagination{Page: q.Page, PageSize: q.Limit, TotalItems: total, TotalPages: max(1, (total+q.Limit-1)/q.Limit)}
	result.Facets = EventFacets{Types: sortedKeys(all.types), Namespaces: sortedKeys(all.namespaces), Kinds: sortedKeys(all.kinds)}
	result.Storage, err = service.eventStorageStats(ctx)
	if err != nil {
		return FleetEventOverview{}, err
	}
	return result, nil
}
func (service *OnboardingService) scanEventPages(ctx context.Context, id string, q EventQuery, visit func(ClusterEvent) error) error {
	q.Limit = 200
	for {
		page, err := service.eventRepository.QueryEvents(ctx, id, q)
		if err != nil {
			return err
		}
		for _, e := range page.Items {
			if err = visit(e); err != nil {
				return err
			}
		}
		if page.NextCursor == "" {
			return nil
		}
		next, uid, err := DecodeEventCursor(page.NextCursor)
		if err != nil {
			return err
		}
		if q.CursorTime != nil && next.Equal(*q.CursorTime) && uid == q.CursorUID {
			return fmt.Errorf("event cursor did not advance")
		}
		q.CursorTime, q.CursorUID = next, uid
	}
}
