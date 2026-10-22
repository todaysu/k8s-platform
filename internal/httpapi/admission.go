package httpapi

import (
	"context"
	"errors"
	"example.com/k8s-platform/internal/cluster"
	"net/http"
	"strings"
	"sync"
	"time"

	"example.com/k8s-platform/internal/metrics"
)

// AdmissionLimits bound each process. When a shared quota repository is present,
// realtime, monitoring and session limits are ALSO enforced across API replicas.
// Separate lanes ensure a slow monitor or WebSocket cannot consume normal read slots.
type AdmissionLimits struct{ Queries, Realtime, Monitoring, Sessions, PerCluster int }

type admissionLane struct {
	active   int
	limit    int
	clusters map[string]int
}
type admission struct {
	shared     cluster.RequestQuotaRepository
	mu         sync.Mutex
	lanes      [4]admissionLane
	perCluster int
}

var admissionRejected = metrics.Default.Counter("platform_admission_rejected_total", "Requests rejected by bounded per-process admission")

func newAdmission(limits AdmissionLimits) *admission {
	defaults := []int{64, 20, 8, 10}
	values := []int{limits.Queries, limits.Realtime, limits.Monitoring, limits.Sessions}
	a := &admission{perCluster: limits.PerCluster}
	if a.perCluster <= 0 {
		a.perCluster = 2
	}
	for i, n := range values {
		if n <= 0 {
			n = defaults[i]
		}
		a.lanes[i] = admissionLane{limit: n, clusters: map[string]int{}}
	}
	return a
}

func requestLane(r *http.Request) (int, string) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 4 || parts[0] != "api" || parts[1] != "v1" || parts[2] != "clusters" {
		return 0, ""
	}
	id := parts[3]
	suffix := strings.Join(parts[4:], "/")
	if strings.HasSuffix(suffix, "/exec") || suffix == "kubectl/tty" {
		return 3, id
	}
	if suffix == "monitoring" {
		return 2, id
	}
	if suffix == "nodes" || suffix == "pods" || strings.HasPrefix(suffix, "pods/") || strings.HasPrefix(suffix, "workloads") || suffix == "kubectl" || r.URL.Query().Get("refresh") == "1" {
		return 1, id
	}
	return 0, ""
}

func (a *admission) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/health" {
			next.ServeHTTP(w, r)
			return
		}
		lane, id := requestLane(r)
		a.mu.Lock()
		l := &a.lanes[lane]
		// Short requests are rejected rather than enqueued without a bound.
		if l.active >= l.limit || (id != "" && l.clusters[id] >= a.perCluster) {
			a.mu.Unlock()
			admissionRejected.Inc()
			w.Header().Set("Retry-After", "2")
			writeAPIError(w, http.StatusTooManyRequests, "PLATFORM_BUSY")
			return
		}
		l.active++
		if id != "" {
			l.clusters[id]++
		}
		a.mu.Unlock()
		defer func() {
			a.mu.Lock()
			l.active--
			if id != "" {
				l.clusters[id]--
				if l.clusters[id] == 0 {
					delete(l.clusters, id)
				}
			}
			a.mu.Unlock()
		}()
		if a.shared != nil && lane > 0 {
			name := []string{"", "realtime", "monitoring", "session"}[lane]
			acquireCtx, stop := context.WithTimeout(r.Context(), 2*time.Second)
			token, err := a.shared.AcquireRequestQuota(acquireCtx, name, id, l.limit, a.perCluster, 15*time.Second)
			stop()
			if err != nil {
				if errors.Is(err, cluster.ErrRequestQuotaExceeded) {
					admissionRejected.Inc()
					w.Header().Set("Retry-After", "2")
					writeAPIError(w, http.StatusTooManyRequests, "PLATFORM_BUSY")
				} else {
					writeAPIError(w, http.StatusServiceUnavailable, cluster.CodePersistenceFailed)
				}
				return
			}
			// A lost lease cancels remote work; requests never silently bypass the shared budget.
			ctx, cancel := context.WithCancel(r.Context())
			r = r.WithContext(ctx)
			done := make(chan struct{})
			go func() {
				defer close(done)
				ticker := time.NewTicker(5 * time.Second)
				defer ticker.Stop()
				for {
					select {
					case <-ctx.Done():
						return
					case <-ticker.C:
						renewal, stop := context.WithTimeout(ctx, 2*time.Second)
						err := a.shared.RenewRequestQuota(renewal, token, 15*time.Second)
						stop()
						if err != nil {
							cancel()
							return
						}
					}
				}
			}()
			defer func() {
				cancel()
				<-done
				releaseCtx, stop := context.WithTimeout(context.Background(), 2*time.Second)
				defer stop()
				_ = a.shared.ReleaseRequestQuota(releaseCtx, token)
			}()
		}
		// Passing the original writer preserves WebSocket Hijacker support.
		next.ServeHTTP(w, r)
	})
}
