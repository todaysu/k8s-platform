package cluster

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

var eventVariableToken = regexp.MustCompile(`(?i)(?:\b(?:\d{1,3}\.){3}\d{1,3}\b|\b[0-9a-f]{8,}\b|\b\d+\b)`)

func (service *OnboardingService) SetEventRepository(repository EventRepository) {
	service.eventRepository = repository
}

func (service *OnboardingService) GetEvents(ctx context.Context, id string, query EventQuery) (EventPage, error) {
	if _, err := service.repository.GetRecord(ctx, id); err != nil {
		return EventPage{}, err
	}
	if service.eventRepository == nil {
		return EventPage{}, newError(CodePersistenceFailed)
	}
	if query.Limit <= 0 || query.Limit > 200 {
		query.Limit = 100
	}
	return service.eventRepository.QueryEvents(ctx, id, query)
}

func (service *OnboardingService) GetEventSummary(ctx context.Context, id string, since, until time.Time) (EventSummary, error) {
	if _, err := service.repository.GetRecord(ctx, id); err != nil {
		return EventSummary{}, err
	}
	if service.eventRepository == nil {
		return EventSummary{}, newError(CodePersistenceFailed)
	}
	return service.eventRepository.EventSummary(ctx, id, since, until)
}

func (service *OnboardingService) GetEventFacets(ctx context.Context, id string, since, until time.Time) (EventFacets, error) {
	if _, err := service.repository.GetRecord(ctx, id); err != nil {
		return EventFacets{}, err
	}
	if service.eventRepository == nil {
		return EventFacets{}, newError(CodePersistenceFailed)
	}
	return service.eventRepository.EventFacets(ctx, id, since, until)
}

func isComponentEvent(event ClusterEvent) bool {
	return event.RegardingKind == "Node" || event.RegardingNamespace == "kube-system" || strings.HasPrefix(event.RegardingNamespace, "openshift-")
}

func groupFleetEvents(events []FleetEvent) []FleetEventIssue {
	groups := map[string]*FleetEventIssue{}
	clusterSets, objectSets := map[string]map[string]bool{}, map[string]map[string]bool{}
	for _, event := range events {
		fingerprint := EventFingerprint(event.ClusterEvent)
		issue := groups[fingerprint]
		if issue == nil {
			issue = &FleetEventIssue{Fingerprint: fingerprint, Severity: eventSeverity(event.ClusterEvent), Type: event.Type, Reason: event.Reason, RegardingKind: event.RegardingKind, Message: event.Message, FirstObservedAt: event.FirstObservedAt, LastObservedAt: event.LastObservedAt}
			groups[fingerprint], clusterSets[fingerprint], objectSets[fingerprint] = issue, map[string]bool{}, map[string]bool{}
		}
		issue.EventCount++
		issue.OccurrenceCount += max(event.OccurrenceCount, 1)
		if event.FirstObservedAt.Before(issue.FirstObservedAt) {
			issue.FirstObservedAt = event.FirstObservedAt
		}
		if event.LastObservedAt.After(issue.LastObservedAt) {
			issue.LastObservedAt = event.LastObservedAt
		}
		clusterSets[fingerprint][event.ClusterID] = true
		objectSets[fingerprint][event.ClusterID+"/"+event.RegardingUID+"/"+event.RegardingName] = true
	}
	result := make([]FleetEventIssue, 0, len(groups))
	for fingerprint, issue := range groups {
		issue.ClusterIDs = sortedKeys(clusterSets[fingerprint])
		issue.AffectedClusters, issue.AffectedObjects = len(clusterSets[fingerprint]), len(objectSets[fingerprint])
		result = append(result, *issue)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Severity != result[j].Severity {
			return severityRank(result[i].Severity) > severityRank(result[j].Severity)
		}
		return result[i].LastObservedAt.After(result[j].LastObservedAt)
	})
	return result
}

func EventFingerprint(event ClusterEvent) string {
	normalized := strings.ToLower(eventVariableToken.ReplaceAllString(event.Message, "<value>"))
	raw := strings.ToLower(event.Type + "|" + event.Reason + "|" + event.RegardingKind + "|" + normalized)
	return fmt.Sprintf("%x", sha256.Sum256([]byte(raw)))[:16]
}

func eventSeverity(event ClusterEvent) string {
	reason := strings.ToLower(event.Reason)
	if strings.Contains(reason, "failed") || reason == "evicted" || reason == "nodeunavailable" {
		return "critical"
	}
	if strings.ToLower(event.Type) == "warning" || reason == "backoff" || reason == "unhealthy" {
		return "warning"
	}
	return "normal"
}
func severityRank(value string) int {
	if value == "critical" {
		return 2
	}
	if value == "warning" {
		return 1
	}
	return 0
}
func sortedKeys(values map[string]bool) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		if value != "" {
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}

func EncodeEventCursor(observed time.Time, uid string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(observed.UTC().Format(time.RFC3339Nano) + "\n" + uid))
}

func DecodeEventCursor(value string) (*time.Time, string, error) {
	if value == "" {
		return nil, "", nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return nil, "", err
	}
	parts := strings.SplitN(string(decoded), "\n", 2)
	if len(parts) != 2 {
		return nil, "", errors.New("invalid cursor")
	}
	parsed, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return nil, "", err
	}
	return &parsed, parts[1], nil
}

// EventCollector follows the Kubernetes API's list-then-watch pattern. It runs
// centrally, so managed clusters do not need an additional agent.
type EventCollector struct {
	service *OnboardingService
	repo    EventRepository
	events  EventConnector
	mu      sync.Mutex
	workers map[string]context.CancelFunc
}

func NewEventCollector(service *OnboardingService, repo EventRepository) *EventCollector {
	events, _ := service.connector.(EventConnector)
	return &EventCollector{service: service, repo: repo, events: events, workers: map[string]context.CancelFunc{}}
}

func (collector *EventCollector) Run(ctx context.Context) {
	if collector.repo == nil || collector.events == nil {
		return
	}
	_ = collector.repo.EnsureEventRollups(ctx)
	reconcile := time.NewTicker(15 * time.Second)
	cleanup := time.NewTicker(time.Hour)
	defer reconcile.Stop()
	defer cleanup.Stop()
	collector.reconcile(ctx)
	for {
		select {
		case <-ctx.Done():
			collector.stopAll()
			return
		case <-reconcile.C:
			collector.reconcile(ctx)
		case <-cleanup.C:
			collector.cleanup(ctx)
		}
	}
}

func (collector *EventCollector) reconcile(ctx context.Context) {
	records, err := collector.service.repository.ListRecords(ctx)
	if err != nil {
		return
	}
	wanted := map[string]ClusterRecord{}
	for _, record := range records {
		wanted[record.ID] = record
	}
	collector.mu.Lock()
	defer collector.mu.Unlock()
	for id, cancel := range collector.workers {
		if _, ok := wanted[id]; !ok {
			cancel()
			delete(collector.workers, id)
		}
	}
	for id, record := range wanted {
		if _, ok := collector.workers[id]; ok {
			continue
		}
		workerCtx, cancel := context.WithCancel(ctx)
		collector.workers[id] = cancel
		go collector.collect(workerCtx, record)
	}
}

func (collector *EventCollector) collect(ctx context.Context, record ClusterRecord) {
	parsed, err := collector.service.parsedCredentials(record)
	if err != nil {
		collector.status(ctx, record.ID, "error", "", err)
		return
	}
	backoff := time.Second
	for ctx.Err() == nil {
		batch, err := collector.events.ListEvents(ctx, parsed)
		if err != nil {
			collector.status(ctx, record.ID, "error", "", err)
			waitContext(ctx, backoff)
			backoff = minDuration(backoff*2, 30*time.Second)
			continue
		}
		for index := range batch.Items {
			batch.Items[index].ClusterID = record.ID
		}
		if err := collector.repo.UpsertEvents(ctx, batch.Items); err != nil {
			collector.status(ctx, record.ID, "error", batch.ResourceVersion, err)
			waitContext(ctx, backoff)
			continue
		}
		collector.status(ctx, record.ID, "watching", batch.ResourceVersion, nil)
		backoff = time.Second
		err = collector.watchBatched(ctx, record.ID, parsed, batch.APIVersion, batch.ResourceVersion)
		if ctx.Err() != nil {
			return
		}
		if err != nil && !errors.Is(err, ErrEventWatchExpired) {
			collector.status(ctx, record.ID, "retrying", batch.ResourceVersion, err)
			waitContext(ctx, backoff)
			backoff = minDuration(backoff*2, 30*time.Second)
		}
	}
}

func (collector *EventCollector) watchBatched(ctx context.Context, clusterID string, parsed ParsedKubeconfig, apiVersion, resourceVersion string) error {
	watchContext, cancel := context.WithCancel(ctx)
	defer cancel()
	events := make(chan ClusterEvent, 1000)
	errorsChannel := make(chan error, 1)
	go func() {
		errorsChannel <- collector.events.WatchEvents(watchContext, parsed, apiVersion, resourceVersion, func(event ClusterEvent) error {
			event.ClusterID = clusterID
			select {
			case events <- event:
				return nil
			case <-watchContext.Done():
				return watchContext.Err()
			}
		})
	}()

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	buffer := make([]ClusterEvent, 0, 200)
	flush := func() error {
		if len(buffer) == 0 {
			return nil
		}
		if err := collector.repo.UpsertEvents(ctx, buffer); err != nil {
			return err
		}
		collector.status(ctx, clusterID, "watching", buffer[len(buffer)-1].ResourceVersion, nil)
		buffer = buffer[:0]
		return nil
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event := <-events:
			buffer = append(buffer, event)
			if len(buffer) >= 200 {
				if err := flush(); err != nil {
					return err
				}
			}
		case <-ticker.C:
			if err := flush(); err != nil {
				return err
			}
		case watchErr := <-errorsChannel:
			if err := flush(); err != nil {
				return err
			}
			return watchErr
		}
	}
}

func (collector *EventCollector) status(ctx context.Context, id, state, rv string, cause error) {
	now := time.Now().UTC()
	status := EventCollectorStatus{ClusterID: id, State: state, ResourceVersion: rv, UpdatedAt: now}
	if cause == nil {
		status.LastSuccessAt = &now
	} else {
		status.LastError = cause.Error()
	}
	_ = collector.repo.SetEventCollectorStatus(ctx, status)
}

func (collector *EventCollector) cleanup(ctx context.Context) {
	now := time.Now().UTC()
	normalCutoff := now.Add(-7 * 24 * time.Hour)
	warningCutoff := now.Add(-30 * 24 * time.Hour)
	for {
		deleted, err := collector.repo.DeleteEventsByRetention(ctx, normalCutoff, warningCutoff, 5000)
		if err != nil || deleted < 5000 {
			break
		}
	}
	rollupCutoff := now.Add(-365 * 24 * time.Hour)
	for {
		deleted, err := collector.repo.DeleteEventRollupsBefore(ctx, rollupCutoff, 5000)
		if err != nil || deleted < 5000 {
			return
		}
	}
}

func (collector *EventCollector) stopAll() {
	collector.mu.Lock()
	defer collector.mu.Unlock()
	for _, cancel := range collector.workers {
		cancel()
	}
	collector.workers = map[string]context.CancelFunc{}
}
func waitContext(ctx context.Context, delay time.Duration) {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}
func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
