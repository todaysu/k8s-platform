package cluster

import (
	"context"
	"testing"
	"time"
)

func TestGroupFleetEventsMergesVariablePodMessagesAcrossClusters(t *testing.T) {
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	events := []FleetEvent{
		{ClusterEvent: ClusterEvent{Type: "Warning", Reason: "FailedScheduling", Message: "0/3 nodes are available", RegardingKind: "Pod", RegardingName: "api-abc", RegardingUID: "one", OccurrenceCount: 2, FirstObservedAt: now.Add(-time.Hour), LastObservedAt: now}, ClusterName: "stp-a"},
		{ClusterEvent: ClusterEvent{Type: "Warning", Reason: "FailedScheduling", Message: "0/6 nodes are available", RegardingKind: "Pod", RegardingName: "api-def", RegardingUID: "two", OccurrenceCount: 4, FirstObservedAt: now.Add(-2 * time.Hour), LastObservedAt: now.Add(-time.Minute)}, ClusterName: "stp-b"},
	}
	events[0].ClusterID = "cluster-a"
	events[1].ClusterID = "cluster-b"

	issues := groupFleetEvents(events)
	if len(issues) != 1 {
		t.Fatalf("expected one normalized issue, got %d", len(issues))
	}
	if issues[0].AffectedClusters != 2 || issues[0].AffectedObjects != 2 || issues[0].OccurrenceCount != 6 {
		t.Fatalf("unexpected issue aggregation: %#v", issues[0])
	}
	if issues[0].Severity != "critical" {
		t.Fatalf("expected critical severity, got %q", issues[0].Severity)
	}
}

type storageStatsRepository struct {
	EventRepository
	calls int
	value EventStorageStats
}

func (repository *storageStatsRepository) EventStorageStats(context.Context) (EventStorageStats, error) {
	repository.calls++
	return repository.value, nil
}

func TestEventStorageStatsCacheAvoidsRepeatedCounts(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	repository := &storageStatsRepository{value: EventStorageStats{RawRows: 7}}
	service := &OnboardingService{eventRepository: repository, now: func() time.Time { return now }}

	first, err := service.eventStorageStats(context.Background())
	if err != nil || first.RawRows != 7 {
		t.Fatalf("unexpected first read: %#v %v", first, err)
	}
	now = now.Add(4 * time.Minute)
	second, err := service.eventStorageStats(context.Background())
	if err != nil || second.RawRows != 7 || repository.calls != 1 {
		t.Fatalf("expected cached read, got %#v calls=%d err=%v", second, repository.calls, err)
	}
	now = now.Add(2 * time.Minute)
	_, err = service.eventStorageStats(context.Background())
	if err != nil || repository.calls != 2 {
		t.Fatalf("expected refresh after ttl, calls=%d err=%v", repository.calls, err)
	}
}

func TestWatchConfigDisablesTotalTimeout(t *testing.T) {
	connector := NewKubernetesConnector(20 * time.Second)
	parsed := ParsedKubeconfig{APIServer: "https://example.invalid"}
	if timeout := connector.restConfig(parsed).Timeout; timeout != 20*time.Second {
		t.Fatalf("list timeout = %v, want 20s", timeout)
	}
	if timeout := connector.watchConfig(parsed).Timeout; timeout != 0 {
		t.Fatalf("watch timeout = %v, want 0", timeout)
	}
}

func TestStatusThrottleWritesOnChangeAndInterval(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	throttle := newStatusThrottle(10 * time.Second)
	if !throttle.allow("c1", "watching", "", now) {
		t.Fatal("first status must be written")
	}
	if throttle.allow("c1", "watching", "", now.Add(5*time.Second)) {
		t.Fatal("unchanged status must be throttled")
	}
	if !throttle.allow("c1", "error", "boom", now.Add(6*time.Second)) {
		t.Fatal("state change must be written immediately")
	}
	if !throttle.allow("c1", "watching", "", now.Add(20*time.Second)) {
		t.Fatal("recovery must be written immediately")
	}
	if throttle.allow("c1", "watching", "", now.Add(21*time.Second)) {
		t.Fatal("unchanged status must be throttled again")
	}
	throttle.forget("c1")
	if !throttle.allow("c1", "watching", "", now.Add(22*time.Second)) {
		t.Fatal("forgotten cluster must be written")
	}
}

func TestJitterStaysNearRequestedDelay(t *testing.T) {
	for i := 0; i < 200; i++ {
		value := jitter(time.Second)
		if value < 750*time.Millisecond || value > 1250*time.Millisecond {
			t.Fatalf("jitter out of bounds: %v", value)
		}
	}
	if jitter(0) != 0 {
		t.Fatal("zero delay must stay zero")
	}
}
