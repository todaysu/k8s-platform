package store

import (
	"strings"
	"testing"
	"time"

	"example.com/k8s-platform/internal/cluster"
)

func TestEventUpsertStatementBuildsOneMultiRowStatement(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	events := []cluster.ClusterEvent{
		{ClusterID: "a", EventUID: "1", Type: "Warning", Message: "one", OccurrenceCount: 1, FirstObservedAt: now, LastObservedAt: now},
		{ClusterID: "b", EventUID: "2", Type: "Normal", Message: "two", OccurrenceCount: 2, FirstObservedAt: now, LastObservedAt: now},
	}
	query, args := eventUpsertStatement(events)
	if strings.Count(query, "?") != 18*len(events) {
		t.Fatalf("expected 18 placeholders per event, got %d", strings.Count(query, "?"))
	}
	if len(args) != 18*len(events) {
		t.Fatalf("expected 18 args per event, got %d", len(args))
	}
	if strings.Count(query, "),(") != 1 {
		t.Fatalf("expected two value tuples in one statement: %s", query)
	}
	if !strings.Contains(query, "ON DUPLICATE KEY UPDATE") {
		t.Fatalf("missing upsert clause: %s", query)
	}
}

func TestRollupUpsertStatementBuildsOneMultiRowStatement(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	rows := []eventRollupRow{
		{bucket: now, clusterID: "a", fingerprint: "f1", eventType: "Warning", count: 3, lastObserved: now},
		{bucket: now, clusterID: "b", fingerprint: "f2", eventType: "Warning", count: 1, lastObserved: now},
	}
	query, args := rollupUpsertStatement(rows)
	if strings.Count(query, "?") != 9*len(rows) || len(args) != 9*len(rows) {
		t.Fatalf("unexpected rollup statement size: %d placeholders %d args", strings.Count(query, "?"), len(args))
	}
	if !strings.Contains(query, "occurrence_count=occurrence_count+VALUES(occurrence_count)") {
		t.Fatalf("missing rollup accumulation: %s", query)
	}
}

func TestLatestEventVersionsKeepsHighestOccurrence(t *testing.T) {
	events := []cluster.ClusterEvent{
		{ClusterID: "a", EventUID: "1", OccurrenceCount: 2, Message: "old"},
		{ClusterID: "a", EventUID: "1", OccurrenceCount: 5, Message: "new"},
		{ClusterID: "a", EventUID: "1", OccurrenceCount: 3, Message: "middle"},
		{ClusterID: "a", EventUID: "2", OccurrenceCount: 1, Message: "other"},
	}
	result := latestEventVersions(events)
	if len(result) != 2 {
		t.Fatalf("expected two distinct events, got %d", len(result))
	}
	if result[0].OccurrenceCount != 5 || result[0].Message != "new" {
		t.Fatalf("kept the wrong version: %#v", result[0])
	}
}

func TestBuildEventRollupsUsesOccurrenceDelta(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 34, 0, 0, time.UTC)
	events := []cluster.ClusterEvent{
		{ClusterID: "a", EventUID: "1", Type: "Warning", Reason: "Failed", RegardingKind: "Pod", Message: "boom", OccurrenceCount: 4, LastObservedAt: now},
		{ClusterID: "a", EventUID: "2", Type: "Warning", Reason: "Failed", RegardingKind: "Pod", Message: "boom", OccurrenceCount: 1, LastObservedAt: now},
	}
	previous := map[string]int64{"a\x001": 1}
	rows := buildEventRollups(events, previous)
	if len(rows) != 1 {
		t.Fatalf("expected one hourly bucket, got %d", len(rows))
	}
	if rows[0].count != 4 {
		t.Fatalf("expected delta 3 for the existing event and 1 for the new event, got %d", rows[0].count)
	}
}
