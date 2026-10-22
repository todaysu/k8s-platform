package main

import "testing"

func TestAssessmentRejectsHTTPFailuresMissingSamplesAndLatency(t *testing.T) {
	m := &measurements{statuses: map[int]int{200: 10}, latency: map[string][]float64{}}
	if len(m.assessment(true)) == 0 {
		t.Fatal("missing scenarios passed")
	}
	for _, r := range []string{"clusters", "capacity", "search", "events", "monitoring"} {
		m.latency[r] = []float64{1, 2, 3}
	}
	if failures := m.assessment(true); len(failures) != 0 {
		t.Fatal(failures)
	}
	m.statuses[500] = 1
	if len(m.assessment(true)) == 0 {
		t.Fatal("HTTP failure passed")
	}
	delete(m.statuses, 500)
	m.latency["search"] = []float64{6000, 6000, 6000}
	if len(m.assessment(true)) == 0 {
		t.Fatal("slow route passed")
	}
	if len(m.assessment(false)) == 0 {
		t.Fatal("takeover failure passed")
	}
}
