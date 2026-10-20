package metrics

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Registry is a tiny dependency-free Prometheus text exposition registry.
// Labels are intentionally avoided; collectors and HTTP endpoints get stable
// metric names instead of high-cardinality label values.
type Registry struct {
	mu       sync.Mutex
	counters map[string]*Counter
	gauges   map[string]*Gauge
	helps    map[string]string
	types    map[string]string
}

type Counter struct{ value atomic.Int64 }

type Gauge struct{ value atomic.Int64 }

func NewRegistry() *Registry {
	return &Registry{counters: map[string]*Counter{}, gauges: map[string]*Gauge{}, helps: map[string]string{}, types: map[string]string{}}
}

var Default = NewRegistry()

func (registry *Registry) Counter(name, help string) *Counter {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if counter, ok := registry.counters[name]; ok {
		return counter
	}
	counter := &Counter{}
	registry.counters[name] = counter
	registry.helps[name] = help
	registry.types[name] = "counter"
	return counter
}

func (registry *Registry) Gauge(name, help string) *Gauge {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if gauge, ok := registry.gauges[name]; ok {
		return gauge
	}
	gauge := &Gauge{}
	registry.gauges[name] = gauge
	registry.helps[name] = help
	registry.types[name] = "gauge"
	return gauge
}

func (counter *Counter) Inc()            { counter.value.Add(1) }
func (counter *Counter) Add(value int64) { counter.value.Add(value) }
func (counter *Counter) Value() int64    { return counter.value.Load() }

func (gauge *Gauge) Set(value int64) { gauge.value.Store(value) }
func (gauge *Gauge) Add(value int64) { gauge.value.Add(value) }
func (gauge *Gauge) Value() int64    { return gauge.value.Load() }

func (registry *Registry) Handler() http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		registry.mu.Lock()
		names := make([]string, 0, len(registry.counters)+len(registry.gauges))
		for name := range registry.counters {
			names = append(names, name)
		}
		for name := range registry.gauges {
			names = append(names, name)
		}
		sort.Strings(names)
		lines := make([]string, 0, len(names))
		for _, name := range names {
			if counter, ok := registry.counters[name]; ok {
				lines = append(lines, fmt.Sprintf("# HELP %s %s\n# TYPE %s counter\n%s %d\n", name, registry.helps[name], name, name, counter.Value()))
				continue
			}
			gauge := registry.gauges[name]
			lines = append(lines, fmt.Sprintf("# HELP %s %s\n# TYPE %s gauge\n%s %d\n", name, registry.helps[name], name, name, gauge.Value()))
		}
		registry.mu.Unlock()
		for _, line := range lines {
			_, _ = writer.Write([]byte(line))
		}
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (writer *statusWriter) WriteHeader(status int) {
	writer.status = status
	writer.ResponseWriter.WriteHeader(status)
}

func (writer *statusWriter) Flush() {
	if flusher, ok := writer.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// Hijack keeps WebSocket upgrades (Pod exec and kubectl TTY) working through
// the metrics wrapper.
func (writer *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := writer.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("metrics: underlying ResponseWriter does not support hijacking")
	}
	return hijacker.Hijack()
}

func (writer *statusWriter) Unwrap() http.ResponseWriter {
	return writer.ResponseWriter
}

// Middleware records one API request and its latency. It deliberately avoids
// per-path labels so cardinality stays bounded.
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		startedAt := time.Now()
		recorder := &statusWriter{ResponseWriter: writer, status: http.StatusOK}
		next.ServeHTTP(recorder, request)
		RequestsTotal.Inc()
		RequestDurationMS.Add(time.Since(startedAt).Milliseconds())
		if recorder.status >= 500 {
			RequestErrorsTotal.Inc()
		}
	})
}

var (
	HealthCollectorRuns       = Default.Counter("ftc_health_collector_runs_total", "Health collection rounds started.")
	HealthCollectorErrors     = Default.Counter("ftc_health_collector_errors_total", "Clusters whose health check failed.")
	HealthCollectorDurationMS = Default.Gauge("ftc_health_collector_last_duration_milliseconds", "Duration of the last health collection round.")

	CapacityCollectorRuns       = Default.Counter("ftc_capacity_collector_runs_total", "Capacity collection rounds started.")
	CapacityCollectorErrors     = Default.Counter("ftc_capacity_collector_errors_total", "Clusters whose capacity check failed.")
	CapacityCollectorDurationMS = Default.Gauge("ftc_capacity_collector_last_duration_milliseconds", "Duration of the last capacity collection round.")

	ResourceIndexRuns       = Default.Counter("ftc_resource_index_runs_total", "Resource index collection rounds started.")
	ResourceIndexErrors     = Default.Counter("ftc_resource_index_errors_total", "Clusters whose resource index refresh failed.")
	ResourceIndexDurationMS = Default.Gauge("ftc_resource_index_last_duration_milliseconds", "Duration of the last resource index round.")

	EventLists       = Default.Counter("ftc_event_lists_total", "Event list calls made by the collector.")
	EventListErrors  = Default.Counter("ftc_event_list_errors_total", "Event list calls that failed.")
	EventWatchStarts = Default.Counter("ftc_event_watch_starts_total", "Event watch streams started.")
	EventReconnects  = Default.Counter("ftc_event_watch_reconnects_total", "Event watch streams that ended and will be re-established.")

	ClustersTotal = Default.Gauge("ftc_clusters_total", "Managed clusters seen by the last collector round.")

	RequestsTotal      = Default.Counter("ftc_http_requests_total", "API requests served.")
	RequestErrorsTotal = Default.Counter("ftc_http_request_errors_total", "API requests returning 5xx.")
	RequestDurationMS  = Default.Counter("ftc_http_request_duration_milliseconds_total", "Cumulative API request duration in milliseconds.")
)
