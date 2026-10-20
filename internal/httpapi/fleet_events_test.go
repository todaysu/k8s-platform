package httpapi

import (
	"context"
	"example.com/k8s-platform/internal/cluster"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type investigationManager struct {
	fakeManager
	query cluster.FleetEventQuery
}

func (m *investigationManager) GetFleetEvents(_ context.Context, q cluster.FleetEventQuery) (cluster.FleetEventOverview, error) {
	m.query = q
	return cluster.FleetEventOverview{}, nil
}
func (m *investigationManager) GetEvents(context.Context, string, cluster.EventQuery) (cluster.EventPage, error) {
	return cluster.EventPage{}, nil
}
func (m *investigationManager) GetEventSummary(context.Context, string, time.Time, time.Time) (cluster.EventSummary, error) {
	return cluster.EventSummary{}, nil
}
func (m *investigationManager) GetEventFacets(context.Context, string, time.Time, time.Time) (cluster.EventFacets, error) {
	return cluster.EventFacets{}, nil
}
func TestEventInvestigationQueryValidationAndContext(t *testing.T) {
	manager := &investigationManager{}
	router := NewRouter("test", manager)
	for _, bad := range []string{"?page=-1", "?page=nope", "?since=bad&until=bad", "?since=2026-10-19T02:00:00Z&until=2026-10-19T01:00:00Z"} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/events"+bad, nil))
		if w.Code != 400 {
			t.Fatalf("bad query accepted %s: %d", bad, w.Code)
		}
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/events?view=events&page=2&scope=business&issue=image-pull&object_uid=old-uid&object_name=web&include_normal=true&since=2026-10-19T01:00:00Z&until=2026-10-19T02:00:00Z", nil))
	q := manager.query
	if w.Code != 200 || q.View != "events" || q.Page != 2 || q.Scope != "business" || q.ObjectUID != "old-uid" || q.ObjectName != "web" || !q.IncludeNormal || q.Issue != "image-pull" || q.Until.Sub(q.Since) != time.Hour {
		t.Fatalf("lost investigation context: %#v status %d", q, w.Code)
	}
}
