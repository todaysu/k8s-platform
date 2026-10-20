package cluster

import (
	"container/heap"
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
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
		fallback := strings.ToLower(strings.TrimSpace(e.Reason))
		if fallback == "" {
			fallback = strings.ToLower(strings.TrimSpace(e.RegardingKind))
		}
		return "other-" + fallback, "其他异常 · " + fallback
	}
}

func eventCategoryTitle(category string) string {
	switch category {
	case "scheduling":
		return "调度异常"
	case "storage":
		return "存储挂载异常"
	case "image-pull":
		return "镜像拉取异常"
	case "probe":
		return "健康探针异常"
	case "restart":
		return "容器退出或重启"
	case "eviction":
		return "资源驱逐"
	case "node":
		return "节点异常"
	}
	if strings.HasPrefix(category, "other-") {
		return "其他异常 · " + strings.TrimPrefix(category, "other-")
	}
	return category
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

func normalizeFleetEventQuery(q *FleetEventQuery) error {
	if q.Page < 1 {
		q.Page = 1
	}
	if q.Page > 10000 {
		return newError(CodeSearchQueryInvalid)
	}
	if q.Limit < 1 || q.Limit > 100 {
		q.Limit = 25
	}
	if q.View == "" {
		q.View = "issues"
	}
	if q.View != "issues" && q.View != "events" && q.View != "objects" && q.View != "resources" {
		return newError(CodeSearchQueryInvalid)
	}
	if q.Scope != "" && q.Scope != "business" && q.Scope != "platform" {
		return newError(CodeSearchQueryInvalid)
	}
	if q.View != "events" && q.View != "resources" {
		q.IncludeNormal = false
	}
	return nil
}

func fleetRecordMatches(record ClusterRecord, q FleetEventQuery) bool {
	if q.Environment != "" && record.Environment != q.Environment {
		return false
	}
	if q.TechnologyStack != "" && record.TechnologyStack != q.TechnologyStack {
		return false
	}
	if q.ClusterID != "" && record.ID != q.ClusterID {
		return false
	}
	return true
}

func fleetScanQuery(q FleetEventQuery, classification CapacityClassification) EventQuery {
	scanQuery := q.EventQuery
	scanQuery.BusinessNamespaces = classification.BusinessNamespaces
	scanQuery.AttentionOnly = !q.IncludeNormal
	scanQuery.ComponentOnly = false
	return scanQuery
}

func namespaceSet(values []string) map[string]bool {
	result := make(map[string]bool, len(values))
	for _, value := range values {
		result[value] = true
	}
	return result
}

func fleetScope(kind, namespace string, business map[string]bool) string {
	if kind != "Node" && business[namespace] {
		return "business"
	}
	return "platform"
}

func appendFleetFailure(result *FleetEventOverview, record ClusterRecord, err error) {
	result.Failures = append(result.Failures, FleetEventFailure{ClusterID: record.ID, ClusterName: record.Name, Code: ErrorCode(err)})
	result.Collectors = append(result.Collectors, EventCollectorStatus{ClusterID: record.ID, ClusterName: record.Name, State: "error", LastError: "持久化事件读取失败"})
	result.Summary.CollectorsFailed++
}

func (service *OnboardingService) appendFleetCollector(record ClusterRecord, statuses map[string]EventCollectorStatus, result *FleetEventOverview) {
	status, ok := statuses[record.ID]
	if !ok {
		status = EventCollectorStatus{ClusterID: record.ID, State: "starting"}
	}
	status.ClusterName = record.Name
	result.Collectors = append(result.Collectors, status)
	if status.State == "watching" {
		result.Summary.CollectorsHealthy++
	} else {
		result.Summary.CollectorsFailed++
	}
}

func fleetIssueGroups(buckets map[string]*eventBucket) []FleetEventIssue {
	groups := make([]FleetEventIssue, 0, len(buckets))
	for _, bucket := range buckets {
		bucket.issue.ClusterIDs = sortedKeys(bucket.clusters)
		bucket.issue.AffectedClusters = len(bucket.clusters)
		groups = append(groups, bucket.issue)
	}
	sort.Slice(groups, func(i, j int) bool {
		if groups[i].LastObservedAt.Equal(groups[j].LastObservedAt) {
			return groups[i].Fingerprint < groups[j].Fingerprint
		}
		return groups[i].LastObservedAt.After(groups[j].LastObservedAt)
	})
	return groups
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
	if err := normalizeFleetEventQuery(&q); err != nil {
		return FleetEventOverview{}, err
	}
	result := FleetEventOverview{
		Items:              []FleetEvent{},
		Issues:             []FleetEventIssue{},
		Objects:            []FleetEventObject{},
		Collectors:         []EventCollectorStatus{},
		Failures:           []FleetEventFailure{},
		Since:              q.Since,
		Until:              q.Until,
		BusinessConfigured: len(classification.BusinessNamespaces) > 0,
	}
	useDaily := q.Until.Sub(q.Since) > 6*time.Hour
	statuses := map[string]EventCollectorStatus{}
	if values, statusErr := service.eventRepository.ListEventCollectorStatuses(ctx); statusErr == nil {
		for _, value := range values {
			statuses[value.ClusterID] = value
		}
	}
	switch q.View {
	case "events":
		return service.getFleetEventRows(ctx, q, records, classification, statuses, result)
	case "objects", "resources":
		return service.getFleetEventObjects(ctx, q, records, classification, statuses, useDaily, result)
	default:
		return service.getFleetEventIssues(ctx, q, records, classification, statuses, useDaily, result)
	}
}

// getFleetEventIssues answers the fleet issue view with grouped SQL. It never
// materializes raw events or calls a target cluster from the request path.
func (service *OnboardingService) getFleetEventIssues(ctx context.Context, q FleetEventQuery, records []ClusterRecord, classification CapacityClassification, statuses map[string]EventCollectorStatus, useDaily bool, result FleetEventOverview) (FleetEventOverview, error) {
	buckets := map[string]*eventBucket{}
	types, namespaces, kinds := map[string]bool{}, map[string]bool{}, map[string]bool{}
	var total, affectedObjects int64
	affectedClusters := 0
	for _, record := range records {
		if !fleetRecordMatches(record, q) {
			continue
		}
		result.Summary.TotalClusters++
		scanQuery := fleetScanQuery(q, classification)
		groups, err := service.aggregateEventGroups(ctx, record.ID, scanQuery, useDaily)
		if err != nil {
			appendFleetFailure(&result, record, err)
			continue
		}
		localEvents := int64(0)
		for _, group := range groups {
			if q.Issue != "" && group.Category != q.Issue {
				continue
			}
			if group.EventCount == 0 {
				continue
			}
			localEvents += group.EventCount
			total += group.EventCount
			affectedObjects += group.AffectedObjects
			types[group.Type] = true
			kinds[group.RegardingKind] = true
			for _, namespace := range group.Namespaces {
				namespaces[namespace] = true
			}
			bucket := buckets[group.Category]
			if bucket == nil {
				bucket = &eventBucket{
					issue: FleetEventIssue{
						Title:           eventCategoryTitle(group.Category),
						Fingerprint:     group.Category,
						Severity:        "warning",
						Type:            group.Type,
						Reason:          group.Reason,
						RegardingKind:   group.RegardingKind,
						Message:         group.Message,
						FirstObservedAt: group.FirstObservedAt,
						LastObservedAt:  group.LastObservedAt,
					},
					clusters: map[string]bool{},
					objects:  map[string]bool{},
				}
				buckets[group.Category] = bucket
			}
			bucket.issue.EventCount += int(group.EventCount)
			bucket.issue.OccurrenceCount += group.OccurrenceCount
			bucket.issue.AffectedObjects += int(group.AffectedObjects)
			if group.FirstObservedAt.Before(bucket.issue.FirstObservedAt) {
				bucket.issue.FirstObservedAt = group.FirstObservedAt
			}
			if group.LastObservedAt.After(bucket.issue.LastObservedAt) {
				bucket.issue.LastObservedAt = group.LastObservedAt
			}
			if len(group.Message) > len(bucket.issue.Message) {
				bucket.issue.Message = group.Message
			}
			bucket.clusters[record.ID] = true
		}
		if localEvents > 0 {
			affectedClusters++
		}
		service.appendFleetCollector(record, statuses, &result)
	}
	if ctx.Err() != nil {
		return FleetEventOverview{}, ctx.Err()
	}
	groups := fleetIssueGroups(buckets)
	start, end := eventPageBounds(len(groups), q.Page, q.Limit)
	result.Issues = groups[start:end]
	result.Total = total
	result.Summary.EventRecords = total
	result.Summary.ProblemGroups = len(groups)
	result.Summary.WarningGroups = len(groups)
	result.Summary.AffectedObjects = int(affectedObjects)
	result.Summary.AffectedClusters = affectedClusters
	result.Facets = EventFacets{Types: sortedKeys(types), Namespaces: sortedKeys(namespaces), Kinds: sortedKeys(kinds)}
	result.Pagination = EventPagination{Page: q.Page, PageSize: q.Limit, TotalItems: len(groups), TotalPages: max(1, (len(groups)+q.Limit-1)/q.Limit)}
	storage, err := service.eventStorageStats(ctx)
	if err != nil {
		return FleetEventOverview{}, err
	}
	result.Storage = storage
	return result, nil
}

// getFleetEventObjects returns one row per object (and category) grouped by
// SQL. Owner grouping is only attempted when the caller narrowed the query to
// one cluster; fleet-wide requests never fan out to every API server.
func (service *OnboardingService) getFleetEventObjects(ctx context.Context, q FleetEventQuery, records []ClusterRecord, classification CapacityClassification, statuses map[string]EventCollectorStatus, useDaily bool, result FleetEventOverview) (FleetEventOverview, error) {
	business := namespaceSet(classification.BusinessNamespaces)
	objects := map[string]FleetEventObject{}
	categories := map[string]bool{}
	types, namespaces, kinds := map[string]bool{}, map[string]bool{}, map[string]bool{}
	var total int64
	affectedClusters := 0
	for _, record := range records {
		if !fleetRecordMatches(record, q) {
			continue
		}
		result.Summary.TotalClusters++
		scanQuery := fleetScanQuery(q, classification)
		rows, err := service.aggregateEventObjects(ctx, record.ID, scanQuery, useDaily)
		if err != nil {
			appendFleetFailure(&result, record, err)
			continue
		}
		localObjects := map[string]FleetEventObject{}
		localEvents := int64(0)
		for _, row := range rows {
			if q.Issue != "" && row.Category != q.Issue {
				continue
			}
			if row.EventCount == 0 {
				continue
			}
			localEvents += row.EventCount
			total += row.EventCount
			categories[row.Category] = true
			namespaces[row.RegardingNamespace] = true
			for _, eventType := range row.Types {
				types[eventType] = true
			}
			kind := row.RegardingKind
			kinds[kind] = true
			key := eventObjectKey(ClusterEvent{ClusterID: record.ID, RegardingKind: kind, RegardingNamespace: row.RegardingNamespace, RegardingUID: row.RegardingUID, RegardingName: row.RegardingName})
			object, exists := localObjects[key]
			if !exists {
				object = FleetEventObject{
					ClusterID:   record.ID,
					ClusterName: record.Name,
					Namespace:   row.RegardingNamespace,
					Kind:        kind,
					Name:        row.RegardingName,
					UID:         row.RegardingUID,
					Scope:       fleetScope(kind, row.RegardingNamespace, business),
				}
			}
			object.EventCount += int(row.EventCount)
			object.OccurrenceCount += row.OccurrenceCount
			if row.LastObservedAt.After(object.LastObservedAt) {
				object.LastObservedAt = row.LastObservedAt
				object.Message = row.Message
			}
			localObjects[key] = object
		}
		if localEvents > 0 {
			affectedClusters++
		}
		if q.ClusterID != "" && len(localObjects) > 0 {
			localObjects = service.eventWorkloadGroups(ctx, record, localObjects)
		}
		for _, object := range localObjects {
			key := eventObjectKey(ClusterEvent{ClusterID: object.ClusterID, RegardingKind: object.Kind, RegardingNamespace: object.Namespace, RegardingUID: object.UID, RegardingName: object.Name})
			objects[key] = object
		}
		service.appendFleetCollector(record, statuses, &result)
	}
	if ctx.Err() != nil {
		return FleetEventOverview{}, ctx.Err()
	}
	values := make([]FleetEventObject, 0, len(objects))
	for _, object := range objects {
		values = append(values, object)
	}
	sort.Slice(values, func(i, j int) bool {
		if values[i].LastObservedAt.Equal(values[j].LastObservedAt) {
			return fmt.Sprint(values[i].ClusterID, values[i].Namespace, values[i].Kind, values[i].UID, values[i].Name) < fmt.Sprint(values[j].ClusterID, values[j].Namespace, values[j].Kind, values[j].UID, values[j].Name)
		}
		return values[i].LastObservedAt.After(values[j].LastObservedAt)
	})
	start, end := eventPageBounds(len(values), q.Page, q.Limit)
	result.Objects = values[start:end]
	result.Total = total
	result.Summary.EventRecords = total
	result.Summary.ProblemGroups = len(categories)
	result.Summary.WarningGroups = len(categories)
	result.Summary.AffectedObjects = len(objects)
	result.Summary.AffectedClusters = affectedClusters
	result.Facets = EventFacets{Types: sortedKeys(types), Namespaces: sortedKeys(namespaces), Kinds: sortedKeys(kinds)}
	result.Pagination = EventPagination{Page: q.Page, PageSize: q.Limit, TotalItems: len(values), TotalPages: max(1, (len(values)+q.Limit-1)/q.Limit)}
	storage, err := service.eventStorageStats(ctx)
	if err != nil {
		return FleetEventOverview{}, err
	}
	result.Storage = storage
	return result, nil
}

// getFleetEventRows keeps the raw-event path for evidence queries. The caller
// normally scopes it to one cluster and a short time window.
func (service *OnboardingService) getFleetEventRows(ctx context.Context, q FleetEventQuery, records []ClusterRecord, classification CapacityClassification, statuses map[string]EventCollectorStatus, result FleetEventOverview) (FleetEventOverview, error) {
	business := namespaceSet(classification.BusinessNamespaces)
	all := newFleetAccumulator(q)
	clusterSet := map[string]bool{}
	for _, record := range records {
		if !fleetRecordMatches(record, q) {
			continue
		}
		result.Summary.TotalClusters++
		scanQuery := fleetScanQuery(q, classification)
		local := newFleetAccumulator(q)
		visit := func(event ClusterEvent) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			scope := fleetScope(event.RegardingKind, event.RegardingNamespace, business)
			if q.Scope != "" && q.Scope != scope {
				return nil
			}
			local.add(FleetEvent{ClusterEvent: event, ClusterName: record.Name, Environment: record.Environment, TechnologyStack: record.TechnologyStack, Scope: scope})
			return nil
		}
		var err error
		if scanner, ok := service.eventRepository.(EventScanner); ok {
			err = scanner.ScanEvents(ctx, record.ID, scanQuery, visit)
		} else {
			err = service.scanEventPages(ctx, record.ID, scanQuery, visit)
		}
		if err != nil {
			appendFleetFailure(&result, record, err)
			continue
		}
		if local.total > 0 {
			clusterSet[record.ID] = true
		}
		all.merge(local)
		service.appendFleetCollector(record, statuses, &result)
	}
	if ctx.Err() != nil {
		return FleetEventOverview{}, ctx.Err()
	}
	groups := []FleetEventIssue{}
	for _, bucket := range all.groups {
		bucket.issue.ClusterIDs = sortedKeys(bucket.clusters)
		bucket.issue.AffectedClusters = len(bucket.clusters)
		bucket.issue.AffectedObjects = len(bucket.objects)
		groups = append(groups, bucket.issue)
	}
	sort.Slice(groups, func(i, j int) bool {
		if groups[i].LastObservedAt.Equal(groups[j].LastObservedAt) {
			return groups[i].Fingerprint < groups[j].Fingerprint
		}
		return groups[i].LastObservedAt.After(groups[j].LastObservedAt)
	})
	objects := make([]FleetEventObject, 0, len(all.objects))
	for _, object := range all.objects {
		objects = append(objects, object)
	}
	sort.Slice(objects, func(i, j int) bool {
		if objects[i].LastObservedAt.Equal(objects[j].LastObservedAt) {
			return fmt.Sprint(objects[i].ClusterID, objects[i].Namespace, objects[i].Kind, objects[i].UID, objects[i].Name) < fmt.Sprint(objects[j].ClusterID, objects[j].Namespace, objects[j].Kind, objects[j].UID, objects[j].Name)
		}
		return objects[i].LastObservedAt.After(objects[j].LastObservedAt)
	})
	rows := []FleetEvent(all.top)
	sort.Slice(rows, func(i, j int) bool { return eventNewer(rows[i], rows[j]) })
	start, end := eventPageBounds(len(rows), q.Page, q.Limit)
	result.Items = rows[start:end]
	result.Total = int64(all.total)
	result.Summary.EventRecords = int64(all.total)
	result.Summary.ProblemGroups = len(groups)
	result.Summary.WarningGroups = len(groups)
	result.Summary.AffectedObjects = len(objects)
	result.Summary.AffectedClusters = len(clusterSet)
	result.Facets = EventFacets{Types: sortedKeys(all.types), Namespaces: sortedKeys(all.namespaces), Kinds: sortedKeys(all.kinds)}
	result.Pagination = EventPagination{Page: q.Page, PageSize: q.Limit, TotalItems: all.total, TotalPages: max(1, (all.total+q.Limit-1)/q.Limit)}
	storage, err := service.eventStorageStats(ctx)
	if err != nil {
		return FleetEventOverview{}, err
	}
	result.Storage = storage
	return result, nil
}

func (service *OnboardingService) aggregateEventGroups(ctx context.Context, id string, q EventQuery, useDaily bool) ([]EventGroupAggregate, error) {
	if useDaily {
		rows, err := service.eventRepository.AggregateDailyEvents(ctx, id, q)
		if err == nil && len(rows) > 0 {
			return rows, nil
		}
	}
	return service.eventRepository.AggregateEvents(ctx, id, q)
}

func (service *OnboardingService) aggregateEventObjects(ctx context.Context, id string, q EventQuery, useDaily bool) ([]EventObjectAggregate, error) {
	if useDaily {
		rows, err := service.eventRepository.AggregateDailyEventObjects(ctx, id, q)
		if err == nil && len(rows) > 0 {
			return rows, nil
		}
	}
	return service.eventRepository.AggregateEventObjects(ctx, id, q)
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
