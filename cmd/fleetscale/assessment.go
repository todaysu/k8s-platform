package main

import (
	"fmt"
	"sort"
)

// Assess only the short synthetic HTTP/takeover checks. Passing these checks
// never implies the unmeasured production or 24h acceptance matrix has passed.
func (m *measurements) assessment(adopted bool) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	failures := []string{}
	for status, count := range m.statuses {
		if count > 0 && (status < 200 || status >= 300) {
			failures = append(failures, fmt.Sprintf("HTTP %d: %d requests", status, count))
		}
	}
	limits := map[string][2]float64{"clusters": {1000, 3000}, "capacity": {2000, 5000}, "search": {2000, 5000}, "events": {2000, 5000}, "monitoring": {5000, 8000}}
	for route, bounds := range limits {
		values := append([]float64(nil), m.latency[route]...)
		if len(values) == 0 {
			failures = append(failures, route+": no samples")
			continue
		}
		sort.Float64s(values)
		p95, p99 := values[int(float64(len(values)-1)*.95)], values[int(float64(len(values)-1)*.99)]
		if p95 > bounds[0] || p99 > bounds[1] {
			failures = append(failures, fmt.Sprintf("%s latency exceeds synthetic threshold: P95 %.1f/P99 %.1f ms", route, p95, p99))
		}
	}
	if !adopted {
		failures = append(failures, "graceful collector takeover did not complete")
	}
	sort.Strings(failures)
	return failures
}
