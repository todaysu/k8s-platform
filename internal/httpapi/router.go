package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"example.com/k8s-platform/internal/cluster"
)

type response struct {
	Data  any `json:"data"`
	Error any `json:"error"`
}

type clusterValidator interface {
	Validate(context.Context, string, []byte) (cluster.ValidationPreview, error)
}

// requestTimeout bounds live Kubernetes calls made by a single HTTP request.
// The connector also has a total per-call timeout; this deadline stops a
// handler that needs several sequential calls.
func requestTimeout(request *http.Request, timeout time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(request.Context(), timeout)
}

type clusterRefresher interface {
	Refresh(context.Context, string) (cluster.ManagedCluster, error)
}

type clusterCapacitySnapshotter interface {
	GetCachedCapacity(context.Context, string) (cluster.ClusterCapacity, error)
}

type clusterCapacityRefresher interface {
	RefreshCapacity(context.Context, string) (cluster.ClusterCapacity, error)
}

type clusterManager interface {
	Delete(context.Context, string) error
	GetCapacity(context.Context, string) (cluster.ClusterCapacity, error)
	GetFleetCapacity(context.Context) (cluster.FleetCapacity, error)
	GetCapacityClassification(context.Context) (cluster.CapacityClassificationOverview, error)
	Get(context.Context, string) (cluster.ManagedCluster, error)
	GetNodes(context.Context, string) ([]cluster.ClusterNode, error)
	GetPod(context.Context, string, string, string) (cluster.ClusterPodDetail, error)
	GetPodLogs(context.Context, string, string, string, cluster.PodLogOptions) (cluster.PodLogSnapshot, error)
	GetPods(context.Context, string) ([]cluster.ClusterPod, error)
	GetWorkload(context.Context, string, string, string, string) (cluster.ClusterWorkloadDetail, error)
	GetWorkloads(context.Context, string) ([]cluster.ClusterWorkload, error)
	List(context.Context) ([]cluster.ManagedCluster, error)
	LockCapacityBaseline(context.Context, string) (cluster.CapacityBaseline, error)
	KeepCapacityBaseline(context.Context, string, string) (cluster.CapacityBaselineDecision, error)
	Onboard(context.Context, string, string, string, string, []byte) (cluster.ManagedCluster, error)
	UpdateClassification(context.Context, string, string, string) (cluster.ManagedCluster, error)
	SaveCapacityClassification(context.Context, cluster.CapacityClassification) (cluster.CapacityClassification, error)
}

type clusterEventManager interface {
	GetFleetEvents(context.Context, cluster.FleetEventQuery) (cluster.FleetEventOverview, error)
	GetEvents(context.Context, string, cluster.EventQuery) (cluster.EventPage, error)
	GetEventSummary(context.Context, string, time.Time, time.Time) (cluster.EventSummary, error)
	GetEventFacets(context.Context, string, time.Time, time.Time) (cluster.EventFacets, error)
}

type clusterExecManager interface {
	ExecPod(context.Context, string, string, string, cluster.PodExecOptions) error
}

type globalResourceSearcher interface {
	SearchResources(context.Context, string) (cluster.GlobalResourceSearch, error)
}
type workloadYAMLManager interface {
	GetWorkloadYAML(context.Context, string, string, string, string) (cluster.WorkloadYAML, error)
}

type RouterOptions struct{ PodExecEnabled bool }

type errorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func NewRouter(version string, validators ...clusterValidator) http.Handler {
	return NewRouterWithOptions(version, RouterOptions{}, validators...)
}

func NewRouterWithOptions(version string, options RouterOptions, validators ...clusterValidator) http.Handler {
	var validator clusterValidator
	if len(validators) > 0 {
		validator = validators[0]
	}
	var manager clusterManager
	if candidate, ok := validator.(clusterManager); ok {
		manager = candidate
	}
	var events clusterEventManager
	if candidate, ok := validator.(clusterEventManager); ok {
		events = candidate
	}
	var execManager clusterExecManager
	if candidate, ok := validator.(clusterExecManager); ok {
		execManager = candidate
	}
	searcher, _ := validator.(globalResourceSearcher)
	yamlManager, _ := validator.(workloadYAMLManager)
	router := http.NewServeMux()
	router.HandleFunc("GET /api/v1/health", health)
	router.HandleFunc("GET /api/v1/version", func(writer http.ResponseWriter, _ *http.Request) {
		writeVersion(writer, version)
	})
	router.HandleFunc("GET /api/v1/capabilities", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(writer, http.StatusOK, response{Data: map[string]bool{"pod_exec_enabled": options.PodExecEnabled}})
	})
	router.HandleFunc("POST /api/v1/clusters/validate", validateCluster(validator))
	router.HandleFunc("GET /api/v1/clusters/{id}/monitoring", monitoringHandler(validator))
	router.HandleFunc("POST /api/v1/clusters/{id}/kubectl", runClusterKubectl(validator))
	router.HandleFunc("GET /api/v1/clusters/{id}/kubectl/tty", clusterKubectlTTY(validator))
	router.HandleFunc("GET /api/v1/capacity", getFleetCapacity(manager))
	router.HandleFunc("GET /api/v1/capacity/classification", getCapacityClassification(manager))
	router.HandleFunc("PUT /api/v1/capacity/classification", saveCapacityClassification(manager))
	router.HandleFunc("GET /api/v1/clusters", listClusters(manager))
	router.HandleFunc("POST /api/v1/clusters", onboardCluster(manager))
	router.HandleFunc("GET /api/v1/clusters/{id}", getCluster(manager))
	router.HandleFunc("PATCH /api/v1/clusters/{id}/classification", updateClusterClassification(manager))
	router.HandleFunc("GET /api/v1/clusters/{id}/nodes", getClusterNodes(manager))
	router.HandleFunc("GET /api/v1/clusters/{id}/capacity", getClusterCapacity(manager))
	router.HandleFunc("POST /api/v1/clusters/{id}/capacity/baselines", lockClusterCapacityBaseline(manager))
	router.HandleFunc("POST /api/v1/clusters/{id}/capacity/baseline-decisions", keepClusterCapacityBaseline(manager))
	router.HandleFunc("GET /api/v1/clusters/{id}/pods", getClusterPods(manager))
	router.HandleFunc("GET /api/v1/clusters/{id}/pods/{namespace}/{name}", getClusterPod(manager))
	router.HandleFunc("GET /api/v1/clusters/{id}/pods/{namespace}/{name}/logs", getClusterPodLogs(manager))
	router.HandleFunc("GET /api/v1/clusters/{id}/pods/{namespace}/{name}/exec", podExecHandler(execManager, options.PodExecEnabled))
	router.HandleFunc("GET /api/v1/clusters/{id}/workloads", getClusterWorkloads(manager))
	router.HandleFunc("GET /api/v1/clusters/{id}/workloads/{kind}/{namespace}/{name}", getClusterWorkload(manager))
	router.HandleFunc("GET /api/v1/clusters/{id}/workloads/{kind}/{namespace}/{name}/yaml", getClusterWorkloadYAML(yamlManager))
	router.HandleFunc("GET /api/v1/search/resources", searchGlobalResources(searcher))
	router.HandleFunc("GET /api/v1/events", getFleetEvents(events))
	router.HandleFunc("GET /api/v1/events/resource", getEventResource(manager))
	router.HandleFunc("GET /api/v1/clusters/{id}/events", getClusterEvents(events))
	router.HandleFunc("GET /api/v1/clusters/{id}/events/summary", getClusterEventSummary(events))
	router.HandleFunc("GET /api/v1/clusters/{id}/events/options", getClusterEventFacets(events))
	router.HandleFunc("DELETE /api/v1/clusters/{id}", deleteCluster(manager))
	return router
}

func getFleetEvents(manager clusterEventManager) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		if manager == nil {
			writeAPIError(writer, http.StatusServiceUnavailable, cluster.CodePersistenceFailed)
			return
		}
		since, until := eventRange(request)
		limit, _ := strconv.Atoi(request.URL.Query().Get("limit"))
		if request.URL.Query().Get("since") != "" || request.URL.Query().Get("until") != "" {
			var err error
			since, err = time.Parse(time.RFC3339Nano, request.URL.Query().Get("since"))
			if err != nil {
				writeAPIError(writer, http.StatusBadRequest, "INVALID_EVENT_RANGE")
				return
			}
			until, err = time.Parse(time.RFC3339Nano, request.URL.Query().Get("until"))
			if err != nil || !until.After(since) || until.Sub(since) > 31*24*time.Hour {
				writeAPIError(writer, http.StatusBadRequest, "INVALID_EVENT_RANGE")
				return
			}
		}
		page := 1
		if value := request.URL.Query().Get("page"); value != "" {
			var err error
			page, err = strconv.Atoi(value)
			if err != nil || page < 1 || page > 10000 {
				writeAPIError(writer, http.StatusBadRequest, "INVALID_EVENT_PAGE")
				return
			}
		}
		includeNormal, _ := strconv.ParseBool(request.URL.Query().Get("include_normal"))
		ctx, cancel := context.WithTimeout(request.Context(), 30*time.Second)
		defer cancel()
		result, err := manager.GetFleetEvents(ctx, cluster.FleetEventQuery{
			View: request.URL.Query().Get("view"), Issue: request.URL.Query().Get("issue"), Page: page, IncludeNormal: includeNormal,
			EventQuery:  cluster.EventQuery{Since: since, Until: until, Type: request.URL.Query().Get("type"), Namespace: request.URL.Query().Get("namespace"), Kind: request.URL.Query().Get("kind"), Search: request.URL.Query().Get("q"), Scope: request.URL.Query().Get("scope"), ObjectUID: request.URL.Query().Get("object_uid"), ObjectName: request.URL.Query().Get("object_name"), Limit: limit},
			Environment: request.URL.Query().Get("environment"), TechnologyStack: request.URL.Query().Get("technology_stack"), ClusterID: request.URL.Query().Get("cluster_id"),
		})
		if err != nil {
			writeClusterError(writer, err)
			return
		}
		writeJSON(writer, http.StatusOK, response{Data: result})
	}
}

func getCapacityClassification(manager clusterManager) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		if manager == nil {
			writeAPIError(writer, http.StatusServiceUnavailable, cluster.CodePersistenceFailed)
			return
		}
		ctx, cancel := context.WithTimeout(request.Context(), 25*time.Second)
		defer cancel()
		value, err := manager.GetCapacityClassification(ctx)
		if err != nil {
			writeClusterError(writer, err)
			return
		}
		writeJSON(writer, http.StatusOK, response{Data: value})
	}
}

func saveCapacityClassification(manager clusterManager) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		if manager == nil {
			writeAPIError(writer, http.StatusServiceUnavailable, cluster.CodePersistenceFailed)
			return
		}
		var value cluster.CapacityClassification
		decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 256*1024))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&value); err != nil {
			writeAPIError(writer, http.StatusBadRequest, cluster.CodeCapacityRulesInvalid)
			return
		}
		created, err := manager.SaveCapacityClassification(request.Context(), value)
		if err != nil {
			writeClusterError(writer, err)
			return
		}
		writeJSON(writer, http.StatusCreated, response{Data: created})
	}
}

func keepClusterCapacityBaseline(manager clusterManager) http.HandlerFunc {
	type requestBody struct {
		Reason string `json:"reason"`
	}
	return func(writer http.ResponseWriter, request *http.Request) {
		if manager == nil {
			writeAPIError(writer, http.StatusServiceUnavailable, cluster.CodePersistenceFailed)
			return
		}
		var body requestBody
		decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 64*1024))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&body); err != nil {
			writeAPIError(writer, http.StatusBadRequest, cluster.CodeBaselineReasonRequired)
			return
		}
		decision, err := manager.KeepCapacityBaseline(request.Context(), request.PathValue("id"), body.Reason)
		if err != nil {
			writeClusterError(writer, err)
			return
		}
		writeJSON(writer, http.StatusCreated, response{Data: decision})
	}
}

func getFleetCapacity(manager clusterManager) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		if manager == nil {
			writeAPIError(writer, http.StatusServiceUnavailable, cluster.CodeConnectionFailed)
			return
		}
		ctx, cancel := context.WithTimeout(request.Context(), 20*time.Second)
		defer cancel()
		capacity, err := manager.GetFleetCapacity(ctx)
		if err != nil {
			writeClusterError(writer, err)
			return
		}
		writeJSON(writer, http.StatusOK, response{Data: capacity})
	}
}

func getClusterCapacity(manager clusterManager) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		if manager == nil {
			writeAPIError(writer, http.StatusServiceUnavailable, cluster.CodeConnectionFailed)
			return
		}
		ctx, cancel := context.WithTimeout(request.Context(), 30*time.Second)
		defer cancel()
		refresh, _ := strconv.ParseBool(request.URL.Query().Get("refresh"))
		var capacity cluster.ClusterCapacity
		var err error
		if refresh {
			if refresher, ok := manager.(clusterCapacityRefresher); ok {
				capacity, err = refresher.RefreshCapacity(ctx, request.PathValue("id"))
			} else {
				capacity, err = manager.GetCapacity(ctx, request.PathValue("id"))
			}
		} else if snapshotter, ok := manager.(clusterCapacitySnapshotter); ok {
			capacity, err = snapshotter.GetCachedCapacity(ctx, request.PathValue("id"))
		} else {
			capacity, err = manager.GetCapacity(ctx, request.PathValue("id"))
		}
		if err != nil {
			writeClusterError(writer, err)
			return
		}
		writeJSON(writer, http.StatusOK, response{Data: capacity})
	}
}

func lockClusterCapacityBaseline(manager clusterManager) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		if manager == nil {
			writeAPIError(writer, http.StatusServiceUnavailable, cluster.CodePersistenceFailed)
			return
		}
		ctx, cancel := context.WithTimeout(request.Context(), 30*time.Second)
		defer cancel()
		baseline, err := manager.LockCapacityBaseline(ctx, request.PathValue("id"))
		if err != nil {
			writeClusterError(writer, err)
			return
		}
		writeJSON(writer, http.StatusCreated, response{Data: baseline})
	}
}

func searchGlobalResources(searcher globalResourceSearcher) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		if searcher == nil {
			writeAPIError(writer, http.StatusServiceUnavailable, cluster.CodeConnectionFailed)
			return
		}
		ctx, cancel := context.WithTimeout(request.Context(), 30*time.Second)
		defer cancel()
		result, err := searcher.SearchResources(ctx, request.URL.Query().Get("q"))
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				writeAPIError(writer, http.StatusGatewayTimeout, cluster.CodeConnectionTimeout)
				return
			}
			writeClusterError(writer, err)
			return
		}
		writeJSON(writer, http.StatusOK, response{Data: result})
	}
}

func getClusterWorkloadYAML(manager workloadYAMLManager) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		if manager == nil {
			writeAPIError(writer, http.StatusServiceUnavailable, cluster.CodeConnectionFailed)
			return
		}
		ctx, cancel := requestTimeout(request, 15*time.Second)
		defer cancel()
		result, err := manager.GetWorkloadYAML(ctx, request.PathValue("id"), request.PathValue("kind"), request.PathValue("namespace"), request.PathValue("name"))
		if err != nil {
			writeClusterError(writer, err)
			return
		}
		writeJSON(writer, http.StatusOK, response{Data: result})
	}
}

func eventRange(request *http.Request) (time.Time, time.Time) {
	until := time.Now().UTC()
	duration := 24 * time.Hour
	switch request.URL.Query().Get("range") {
	case "1h":
		duration = time.Hour
	case "6h":
		duration = 6 * time.Hour
	case "7d":
		duration = 7 * 24 * time.Hour
	case "30d":
		duration = 30 * 24 * time.Hour
	}
	return until.Add(-duration), until
}

func getClusterEvents(manager clusterEventManager) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		if manager == nil {
			writeAPIError(writer, http.StatusServiceUnavailable, cluster.CodePersistenceFailed)
			return
		}
		since, until := eventRange(request)
		limit, _ := strconv.Atoi(request.URL.Query().Get("limit"))
		cursorTime, cursorUID, err := cluster.DecodeEventCursor(request.URL.Query().Get("cursor"))
		if err != nil {
			writeAPIError(writer, http.StatusBadRequest, "INVALID_EVENT_CURSOR")
			return
		}
		componentOnly, _ := strconv.ParseBool(request.URL.Query().Get("component"))
		ctx, cancel := requestTimeout(request, 20*time.Second)
		defer cancel()
		page, err := manager.GetEvents(ctx, request.PathValue("id"), cluster.EventQuery{Since: since, Until: until, Type: request.URL.Query().Get("type"), Namespace: request.URL.Query().Get("namespace"), Kind: request.URL.Query().Get("kind"), Search: request.URL.Query().Get("q"), ComponentOnly: componentOnly, Limit: limit, CursorTime: cursorTime, CursorUID: cursorUID})
		if err != nil {
			writeClusterError(writer, err)
			return
		}
		writeJSON(writer, http.StatusOK, response{Data: page})
	}
}

func getClusterEventSummary(manager clusterEventManager) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		if manager == nil {
			writeAPIError(writer, http.StatusServiceUnavailable, cluster.CodePersistenceFailed)
			return
		}
		since, until := eventRange(request)
		ctx, cancel := requestTimeout(request, 15*time.Second)
		defer cancel()
		summary, err := manager.GetEventSummary(ctx, request.PathValue("id"), since, until)
		if err != nil {
			writeClusterError(writer, err)
			return
		}
		writeJSON(writer, http.StatusOK, response{Data: summary})
	}
}

func getClusterEventFacets(manager clusterEventManager) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		if manager == nil {
			writeAPIError(writer, http.StatusServiceUnavailable, cluster.CodePersistenceFailed)
			return
		}
		since, until := eventRange(request)
		ctx, cancel := requestTimeout(request, 15*time.Second)
		defer cancel()
		facets, err := manager.GetEventFacets(ctx, request.PathValue("id"), since, until)
		if err != nil {
			writeClusterError(writer, err)
			return
		}
		writeJSON(writer, http.StatusOK, response{Data: facets})
	}
}

func getClusterPodLogs(manager clusterManager) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		if manager == nil {
			writeAPIError(writer, http.StatusServiceUnavailable, cluster.CodePersistenceFailed)
			return
		}
		tailLines := int64(200)
		if raw := request.URL.Query().Get("tail_lines"); raw != "" {
			if parsed, err := strconv.ParseInt(raw, 10, 64); err == nil {
				tailLines = parsed
			}
		}
		if tailLines < 50 {
			tailLines = 50
		}
		if tailLines > 1000 {
			tailLines = 1000
		}
		previous, _ := strconv.ParseBool(request.URL.Query().Get("previous"))
		sinceTime, err := optionalRFC3339Time(request.URL.Query().Get("since"))
		if err != nil {
			writeAPIError(writer, http.StatusBadRequest, cluster.CodeInvalidLogTimeRange)
			return
		}
		untilTime, err := optionalRFC3339Time(request.URL.Query().Get("until"))
		if err != nil || (sinceTime != nil && untilTime != nil && !sinceTime.Before(*untilTime)) {
			writeAPIError(writer, http.StatusBadRequest, cluster.CodeInvalidLogTimeRange)
			return
		}
		ctx, cancel := requestTimeout(request, 20*time.Second)
		defer cancel()
		logs, err := manager.GetPodLogs(
			ctx, request.PathValue("id"), request.PathValue("namespace"), request.PathValue("name"),
			cluster.PodLogOptions{
				Container: request.URL.Query().Get("container"), TailLines: tailLines, Previous: previous,
				SinceTime: sinceTime, UntilTime: untilTime,
			},
		)
		if err != nil {
			writeClusterError(writer, err)
			return
		}
		writeJSON(writer, http.StatusOK, response{Data: logs})
	}
}

func optionalRFC3339Time(value string) (*time.Time, error) {
	if value == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func getClusterPod(manager clusterManager) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		if manager == nil {
			writeAPIError(writer, http.StatusServiceUnavailable, cluster.CodePersistenceFailed)
			return
		}
		ctx, cancel := requestTimeout(request, 15*time.Second)
		defer cancel()
		pod, err := manager.GetPod(ctx, request.PathValue("id"), request.PathValue("namespace"), request.PathValue("name"))
		if err != nil {
			writeClusterError(writer, err)
			return
		}
		writeJSON(writer, http.StatusOK, response{Data: pod})
	}
}

func getClusterPods(manager clusterManager) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		if manager == nil {
			writeAPIError(writer, http.StatusServiceUnavailable, cluster.CodePersistenceFailed)
			return
		}
		ctx, cancel := requestTimeout(request, 30*time.Second)
		defer cancel()
		pods, err := manager.GetPods(ctx, request.PathValue("id"))
		if err != nil {
			writeClusterError(writer, err)
			return
		}
		writeJSON(writer, http.StatusOK, response{Data: pods})
	}
}

func getClusterWorkloads(manager clusterManager) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		if manager == nil {
			writeAPIError(writer, http.StatusServiceUnavailable, cluster.CodePersistenceFailed)
			return
		}
		ctx, cancel := requestTimeout(request, 30*time.Second)
		defer cancel()
		workloads, err := manager.GetWorkloads(ctx, request.PathValue("id"))
		if err != nil {
			writeClusterError(writer, err)
			return
		}
		writeJSON(writer, http.StatusOK, response{Data: workloads})
	}
}

func getClusterWorkload(manager clusterManager) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		if manager == nil {
			writeAPIError(writer, http.StatusServiceUnavailable, cluster.CodePersistenceFailed)
			return
		}
		ctx, cancel := requestTimeout(request, 30*time.Second)
		defer cancel()
		workload, err := manager.GetWorkload(
			ctx,
			request.PathValue("id"),
			request.PathValue("kind"),
			request.PathValue("namespace"),
			request.PathValue("name"),
		)
		if err != nil {
			writeClusterError(writer, err)
			return
		}
		writeJSON(writer, http.StatusOK, response{Data: workload})
	}
}

func getClusterNodes(manager clusterManager) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		if manager == nil {
			writeAPIError(writer, http.StatusServiceUnavailable, cluster.CodePersistenceFailed)
			return
		}
		ctx, cancel := requestTimeout(request, 20*time.Second)
		defer cancel()
		nodes, err := manager.GetNodes(ctx, request.PathValue("id"))
		if err != nil {
			writeClusterError(writer, err)
			return
		}
		writeJSON(writer, http.StatusOK, response{Data: nodes})
	}
}

func getCluster(manager clusterManager) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		if manager == nil {
			writeAPIError(writer, http.StatusServiceUnavailable, cluster.CodePersistenceFailed)
			return
		}
		ctx, cancel := requestTimeout(request, 20*time.Second)
		defer cancel()
		refresh, _ := strconv.ParseBool(request.URL.Query().Get("refresh"))
		var managedCluster cluster.ManagedCluster
		var err error
		if refresh {
			if refresher, ok := manager.(clusterRefresher); ok {
				managedCluster, err = refresher.Refresh(ctx, request.PathValue("id"))
			} else {
				managedCluster, err = manager.Get(ctx, request.PathValue("id"))
			}
		} else {
			managedCluster, err = manager.Get(ctx, request.PathValue("id"))
		}
		if err != nil {
			writeClusterError(writer, err)
			return
		}
		writeJSON(writer, http.StatusOK, response{Data: managedCluster})
	}
}

func updateClusterClassification(manager clusterManager) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		if manager == nil {
			writeAPIError(writer, http.StatusServiceUnavailable, cluster.CodePersistenceFailed)
			return
		}
		var payload struct {
			ClusterType     string `json:"cluster_type"`
			TechnologyStack string `json:"technology_stack"`
		}
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			writeAPIError(writer, http.StatusBadRequest, cluster.CodeClusterTypeInvalid)
			return
		}
		managedCluster, err := manager.UpdateClassification(request.Context(), request.PathValue("id"), payload.ClusterType, payload.TechnologyStack)
		if err != nil {
			writeClusterError(writer, err)
			return
		}
		writeJSON(writer, http.StatusOK, response{Data: managedCluster})
	}
}

func deleteCluster(manager clusterManager) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		if manager == nil {
			writeAPIError(writer, http.StatusServiceUnavailable, cluster.CodePersistenceFailed)
			return
		}
		ctx, cancel := requestTimeout(request, 20*time.Second)
		defer cancel()
		if err := manager.Delete(ctx, request.PathValue("id")); err != nil {
			writeClusterError(writer, err)
			return
		}
		writeJSON(writer, http.StatusOK, response{Data: map[string]string{"id": request.PathValue("id")}})
	}
}

func onboardCluster(manager clusterManager) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		if manager == nil {
			writeAPIError(writer, http.StatusServiceUnavailable, cluster.CodePersistenceFailed)
			return
		}
		payload, ok := decodeClusterRequest(writer, request)
		if !ok {
			return
		}
		ctx, cancel := requestTimeout(request, 30*time.Second)
		defer cancel()
		managedCluster, err := manager.Onboard(ctx, payload.Name, payload.Environment, payload.ClusterType, payload.TechnologyStack, []byte(payload.Kubeconfig))
		if err != nil {
			writeClusterError(writer, err)
			return
		}
		writeJSON(writer, http.StatusCreated, response{Data: managedCluster})
	}
}

func listClusters(manager clusterManager) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		if manager == nil {
			writeAPIError(writer, http.StatusServiceUnavailable, cluster.CodePersistenceFailed)
			return
		}
		clusters, err := manager.List(request.Context())
		if err != nil {
			writeClusterError(writer, err)
			return
		}
		writeJSON(writer, http.StatusOK, response{Data: clusters})
	}
}

func health(writer http.ResponseWriter, _ *http.Request) {
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(response{
		Data: map[string]string{"status": "ok"},
	})
}

func writeVersion(writer http.ResponseWriter, version string) {
	writeJSON(writer, http.StatusOK, response{
		Data: map[string]string{"version": version},
	})
}

type validateClusterRequest struct {
	Name            string `json:"name"`
	Environment     string `json:"environment,omitempty"`
	ClusterType     string `json:"cluster_type,omitempty"`
	TechnologyStack string `json:"technology_stack,omitempty"`
	Kubeconfig      string `json:"kubeconfig"`
}

func validateCluster(validator clusterValidator) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		if validator == nil {
			writeAPIError(writer, http.StatusServiceUnavailable, cluster.CodeConnectionFailed)
			return
		}

		payload, ok := decodeClusterRequest(writer, request)
		if !ok {
			return
		}

		ctx, cancel := requestTimeout(request, 30*time.Second)
		defer cancel()
		preview, err := validator.Validate(ctx, payload.Name, []byte(payload.Kubeconfig))
		if err != nil {
			writeClusterError(writer, err)
			return
		}

		writeJSON(writer, http.StatusOK, response{Data: preview})
	}
}

func decodeClusterRequest(writer http.ResponseWriter, request *http.Request) (validateClusterRequest, bool) {
	request.Body = http.MaxBytesReader(writer, request.Body, cluster.MaxKubeconfigSize+64*1024)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var payload validateClusterRequest
	if err := decoder.Decode(&payload); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			writeAPIError(writer, http.StatusRequestEntityTooLarge, cluster.CodeKubeconfigTooLarge)
			return validateClusterRequest{}, false
		}
		writeAPIError(writer, http.StatusBadRequest, cluster.CodeKubeconfigInvalid)
		return validateClusterRequest{}, false
	}
	return payload, true
}

func writeClusterError(writer http.ResponseWriter, err error) {
	code := cluster.ErrorCode(err)
	status := http.StatusUnprocessableEntity
	switch code {
	case cluster.CodeNameRequired, cluster.CodeEnvironmentRequired, cluster.CodeClusterTypeRequired, cluster.CodeClusterTypeInvalid, cluster.CodeClusterTypeMismatch, cluster.CodeTechnologyStackRequired, cluster.CodeTechnologyStackInvalid, cluster.CodeKubeconfigRequired, cluster.CodeKubeconfigTooLarge, cluster.CodeKubeconfigInvalid, cluster.CodeSearchQueryInvalid, cluster.CodeCapacityRulesInvalid, cluster.CodeBaselineReasonRequired:
		status = http.StatusBadRequest
	case cluster.CodeNameDuplicate, cluster.CodeClusterDuplicate:
		status = http.StatusConflict
	case cluster.CodeClusterNotFound, cluster.CodeWorkloadNotFound, cluster.CodePodNotFound:
		status = http.StatusNotFound
	case cluster.CodeEncryptionFailed, cluster.CodePersistenceFailed:
		status = http.StatusInternalServerError
	case cluster.CodePodNotRunning, cluster.CodeContainerNotFound:
		status = http.StatusConflict
	case cluster.CodePodExecUnavailable, cluster.CodeCapacityPending:
		status = http.StatusServiceUnavailable
	}
	writeAPIError(writer, status, code)
}

func writeAPIError(writer http.ResponseWriter, status int, code string) {
	writeJSON(writer, status, response{
		Error: errorResponse{Code: code, Message: errorMessage(code)},
	})
}

func errorMessage(code string) string {
	messages := map[string]string{
		cluster.CodeNameRequired:             "请输入集群名称。",
		cluster.CodeEnvironmentRequired:      "请选择或输入集群环境。",
		cluster.CodeClusterTypeRequired:      "请选择集群类型。",
		cluster.CodeClusterTypeInvalid:       "集群类型只支持 G01 或 OCP。",
		cluster.CodeClusterTypeMismatch:      "集群类型与技术栈不匹配。G01 支持 TCS/CMBK8S，OCP 只支持 OCP。",
		cluster.CodeTechnologyStackRequired:  "请选择技术栈。",
		cluster.CodeTechnologyStackInvalid:   "技术栈只支持 TCS、CMBK8S 或 OCP。",
		cluster.CodeKubeconfigRequired:       "请上传或粘贴 Kubeconfig。",
		cluster.CodeKubeconfigTooLarge:       "Kubeconfig 不能超过 1 MiB。",
		cluster.CodeKubeconfigInvalid:        "Kubeconfig 格式无效。",
		cluster.CodeCurrentContextMissing:    "Kubeconfig 没有设置 current-context。",
		cluster.CodeCurrentContextInvalid:    "current-context 引用的集群或用户不存在。",
		cluster.CodeHTTPSRequired:            "API Server 必须使用 HTTPS。",
		cluster.CodeInlineCARequired:         "第一版要求 Kubeconfig 内嵌 CA 证书。",
		cluster.CodeInlineCAInvalid:          "Kubeconfig 中的 CA 证书无效。",
		cluster.CodeTokenAuthRequired:        "Kubeconfig 中没有可用的 Bearer Token。",
		cluster.CodeUnsupportedAuth:          "Kubeconfig 同时包含多种凭据或凭据不完整。",
		cluster.CodeAuthenticationRequired:   "Kubeconfig 中没有可用的 Token 或客户端证书。",
		cluster.CodeInlineClientCertRequired: "客户端证书和私钥必须内嵌在 Kubeconfig 中，不能引用本地文件。",
		cluster.CodeClientCertificateInvalid: "Kubeconfig 中的客户端证书或私钥无效。",
		cluster.CodeExecAuthUnsupported:      "暂不支持 exec 插件认证；平台容器无法安全执行外部登录命令。",
		cluster.CodeAuthProviderUnsupported:  "暂不支持 auth-provider 插件认证。",
		cluster.CodeBasicAuthUnsupported:     "暂不支持用户名和密码认证。",
		cluster.CodeNameDuplicate:            "集群名称已经存在，请使用其他名称。",
		cluster.CodeClusterDuplicate:         "该 Kubernetes 集群已经被纳管。",
		cluster.CodeClusterNotFound:          "该集群不存在或已经解除纳管。",
		cluster.CodeWorkloadNotFound:         "该工作负载不存在或已经被删除。",
		cluster.CodePodNotFound:              "该 Pod 不存在或已经被删除。",
		cluster.CodeInvalidLogTimeRange:      "日志时间范围无效，开始时间必须早于结束时间。",
		cluster.CodePodNotRunning:            "Pod 不是 Running 状态，无法打开终端。",
		cluster.CodeContainerNotFound:        "选择的容器不存在或已发生变化。",
		cluster.CodePodExecUnavailable:       "Pod 终端未启用，请由管理员完成安全配置。",
		cluster.CodePodExecForbidden:         "纳管凭据缺少 pods/exec 权限，请为目标命名空间授予 pods/get 和 pods/exec/create。",
		cluster.CodeSearchQueryInvalid:       "请至少输入 2 个字符搜索跨集群资源。",
		cluster.CodeEncryptionFailed:         "集群凭据加密失败。",
		cluster.CodePersistenceFailed:        "集群保存失败，请稍后重试。",
		cluster.CodeConnectionTimeout:        "连接 Kubernetes API Server 超时。",
		cluster.CodeTLSVerificationFailed:    "API Server 的 TLS 证书校验失败。",
		cluster.CodeAuthenticationFailed:     "Kubeconfig 中的连接凭据认证失败。",
		cluster.CodeAuthorizationFailed:      "Kubeconfig 中的连接凭据没有读取集群版本的权限。",
		cluster.CodeConnectionFailed:         "无法连接 Kubernetes API Server。",
		cluster.CodeCapacityRulesInvalid:     "容量分类规则无效或存在 Namespace 冲突。",
		cluster.CodeCapacityPending:          "容量数据尚未采集完成，请稍后重试。",
		cluster.CodeBaselineReasonRequired:   "暂不更新基线时必须填写原因。",
	}
	if message, ok := messages[code]; ok {
		return message
	}
	return "请求处理失败。"
}

func writeJSON(writer http.ResponseWriter, status int, payload response) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(payload)
}

func getEventResource(manager clusterManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		live, ok := manager.(interface {
			GetEventResource(context.Context, cluster.EventResourceQuery) (cluster.EventResourceSnapshot, error)
		})
		if !ok {
			writeAPIError(w, http.StatusServiceUnavailable, "EVENT_RESOURCE_UNAVAILABLE")
			return
		}
		q := r.URL.Query()
		if q.Get("cluster_id") == "" || q.Get("kind") == "" || q.Get("name") == "" {
			writeAPIError(w, http.StatusBadRequest, "INVALID_EVENT_RESOURCE")
			return
		}
		ctx, cancel := requestTimeout(r, 15*time.Second)
		defer cancel()
		result, err := live.GetEventResource(ctx, cluster.EventResourceQuery{ClusterID: q.Get("cluster_id"), Namespace: q.Get("namespace"), Kind: q.Get("kind"), Name: q.Get("name"), UID: q.Get("uid")})
		if err != nil {
			writeClusterError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, response{Data: result})
	}
}
