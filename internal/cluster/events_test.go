package cluster

import (
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
