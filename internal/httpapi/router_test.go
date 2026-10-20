package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"example.com/k8s-platform/internal/cluster"
)

func TestHealth(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	response := httptest.NewRecorder()

	NewRouter("test-version").ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, response.Code)
	}

	if got := response.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("expected JSON content type, got %q", got)
	}

	want := "{\"data\":{\"status\":\"ok\"},\"error\":null}\n"
	if got := response.Body.String(); got != want {
		t.Fatalf("expected body %q, got %q", want, got)
	}
}

func TestCapabilitiesReportsPodExecState(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/v1/capabilities", nil)
	recorder := httptest.NewRecorder()
	NewRouterWithOptions("test", RouterOptions{PodExecEnabled: true}, fakeValidator{}).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), `"pod_exec_enabled":true`) {
		t.Fatalf("expected enabled capability, got %s", recorder.Body.String())
	}
}

type fakeValidator struct {
	preview cluster.ValidationPreview
	err     error
}

type fakeManager struct {
	fakeValidator
	managed             cluster.ManagedCluster
	capacity            cluster.ClusterCapacity
	fleetCapacity       cluster.FleetCapacity
	classification      cluster.CapacityClassificationOverview
	savedClassification cluster.CapacityClassification
	decision            cluster.CapacityBaselineDecision
	baseline            cluster.CapacityBaseline
	nodes               []cluster.ClusterNode
	pods                []cluster.ClusterPod
	pod                 cluster.ClusterPodDetail
	podLogs             cluster.PodLogSnapshot
	workloads           []cluster.ClusterWorkload
	workload            cluster.ClusterWorkloadDetail
	err                 error
}

func (manager fakeManager) Onboard(context.Context, string, string, string, string, []byte) (cluster.ManagedCluster, error) {
	return manager.managed, manager.err
}

func (manager fakeManager) UpdateClassification(context.Context, string, string, string) (cluster.ManagedCluster, error) {
	return manager.managed, manager.err
}

func (manager fakeManager) List(context.Context) ([]cluster.ManagedCluster, error) {
	if manager.err != nil {
		return nil, manager.err
	}
	return []cluster.ManagedCluster{manager.managed}, nil
}

func (manager fakeManager) Delete(context.Context, string) error {
	return manager.err
}

func (manager fakeManager) Get(context.Context, string) (cluster.ManagedCluster, error) {
	return manager.managed, manager.err
}

func (manager fakeManager) GetNodes(context.Context, string) ([]cluster.ClusterNode, error) {
	return manager.nodes, manager.err
}

func (manager fakeManager) GetCapacity(context.Context, string) (cluster.ClusterCapacity, error) {
	return manager.capacity, manager.err
}

func (manager fakeManager) GetFleetCapacity(context.Context) (cluster.FleetCapacity, error) {
	return manager.fleetCapacity, manager.err
}

func (manager fakeManager) GetCapacityClassification(context.Context) (cluster.CapacityClassificationOverview, error) {
	return manager.classification, manager.err
}
func (manager fakeManager) SaveCapacityClassification(_ context.Context, value cluster.CapacityClassification) (cluster.CapacityClassification, error) {
	if manager.err != nil {
		return cluster.CapacityClassification{}, manager.err
	}
	if manager.savedClassification.ID != 0 {
		return manager.savedClassification, nil
	}
	value.ID = 3
	return value, nil
}
func (manager fakeManager) KeepCapacityBaseline(context.Context, string, string) (cluster.CapacityBaselineDecision, error) {
	return manager.decision, manager.err
}

func (manager fakeManager) LockCapacityBaseline(context.Context, string) (cluster.CapacityBaseline, error) {
	return manager.baseline, manager.err
}

func (manager fakeManager) GetPods(context.Context, string) ([]cluster.ClusterPod, error) {
	return manager.pods, manager.err
}

func (manager fakeManager) GetPod(context.Context, string, string, string) (cluster.ClusterPodDetail, error) {
	return manager.pod, manager.err
}

func (manager fakeManager) GetPodLogs(context.Context, string, string, string, cluster.PodLogOptions) (cluster.PodLogSnapshot, error) {
	return manager.podLogs, manager.err
}

func (manager fakeManager) GetWorkloads(context.Context, string) ([]cluster.ClusterWorkload, error) {
	return manager.workloads, manager.err
}

func (manager fakeManager) GetWorkload(context.Context, string, string, string, string) (cluster.ClusterWorkloadDetail, error) {
	return manager.workload, manager.err
}

func (validator fakeValidator) Validate(context.Context, string, []byte) (cluster.ValidationPreview, error) {
	return validator.preview, validator.err
}

func TestValidateCluster(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/validate", strings.NewReader(`{"name":"prd-01","kubeconfig":"safe-config"}`))
	responseRecorder := httptest.NewRecorder()
	validator := fakeValidator{preview: cluster.ValidationPreview{
		Name:               "prd-01",
		APIServer:          "https://cluster.example.test",
		ContextName:        "default",
		KubernetesVersion:  "v1.32.4",
		AuthenticationType: "Bearer Token",
	}}

	NewRouter("test-version", validator).ServeHTTP(responseRecorder, request)

	if responseRecorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", responseRecorder.Code, responseRecorder.Body.String())
	}
	var payload struct {
		Data cluster.ValidationPreview `json:"data"`
	}
	if err := json.Unmarshal(responseRecorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Data.KubernetesVersion != "v1.32.4" {
		t.Fatalf("unexpected response: %#v", payload.Data)
	}
}

func TestValidateClusterReturnsSafeError(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/validate", strings.NewReader(`{"name":"prd-01","kubeconfig":"secret-token"}`))
	responseRecorder := httptest.NewRecorder()
	validator := fakeValidator{err: &cluster.Error{Code: cluster.CodeAuthenticationFailed}}

	NewRouter("test-version", validator).ServeHTTP(responseRecorder, request)

	if responseRecorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected status 422, got %d", responseRecorder.Code)
	}
	if strings.Contains(responseRecorder.Body.String(), "secret-token") {
		t.Fatal("response leaked request content")
	}
	if !strings.Contains(responseRecorder.Body.String(), cluster.CodeAuthenticationFailed) {
		t.Fatalf("expected safe error code, got %s", responseRecorder.Body.String())
	}
}

func TestOnboardClusterCreatesManagedCluster(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/api/v1/clusters", strings.NewReader(`{"name":"prd-01","environment":"PRD","cluster_type":"G01","technology_stack":"CMBK8S","kubeconfig":"safe-config"}`))
	responseRecorder := httptest.NewRecorder()
	manager := fakeManager{managed: cluster.ManagedCluster{
		ID: "cluster-1", Name: "prd-01", APIServer: "https://cluster.example.test",
		Environment: "PRD", ClusterType: "G01", TechnologyStack: "CMBK8S",
		ContextName: "default", KubernetesVersion: "v1.32.4",
		AuthenticationType: "客户端证书", ConnectionStatus: "connected",
	}}

	NewRouter("test-version", manager).ServeHTTP(responseRecorder, request)

	if responseRecorder.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", responseRecorder.Code, responseRecorder.Body.String())
	}
	if !strings.Contains(responseRecorder.Body.String(), `"id":"cluster-1"`) {
		t.Fatalf("unexpected response: %s", responseRecorder.Body.String())
	}
	if !strings.Contains(responseRecorder.Body.String(), `"technology_stack":"CMBK8S"`) {
		t.Fatalf("unexpected response: %s", responseRecorder.Body.String())
	}
}

func TestUpdateClusterClassification(t *testing.T) {
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/clusters/cluster-1/classification", strings.NewReader(`{"cluster_type":"G01","technology_stack":"TCS"}`))
	responseRecorder := httptest.NewRecorder()
	manager := fakeManager{managed: cluster.ManagedCluster{ID: "cluster-1", ClusterType: "G01", TechnologyStack: "TCS"}}

	NewRouter("test-version", manager).ServeHTTP(responseRecorder, request)

	if responseRecorder.Code != http.StatusOK || !strings.Contains(responseRecorder.Body.String(), `"cluster_type":"G01"`) {
		t.Fatalf("unexpected classification response: %d %s", responseRecorder.Code, responseRecorder.Body.String())
	}
}

func TestListClusters(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/v1/clusters", nil)
	responseRecorder := httptest.NewRecorder()
	manager := fakeManager{managed: cluster.ManagedCluster{ID: "cluster-1", Name: "prd-01", Environment: "PRD"}}

	NewRouter("test-version", manager).ServeHTTP(responseRecorder, request)

	if responseRecorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", responseRecorder.Code, responseRecorder.Body.String())
	}
	if !strings.Contains(responseRecorder.Body.String(), `"environment":"PRD"`) {
		t.Fatalf("unexpected response: %s", responseRecorder.Body.String())
	}
}

func TestDeleteCluster(t *testing.T) {
	request := httptest.NewRequest(http.MethodDelete, "/api/v1/clusters/cluster-1", nil)
	responseRecorder := httptest.NewRecorder()
	manager := fakeManager{managed: cluster.ManagedCluster{ID: "cluster-1"}}

	NewRouter("test-version", manager).ServeHTTP(responseRecorder, request)

	if responseRecorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", responseRecorder.Code, responseRecorder.Body.String())
	}
	if !strings.Contains(responseRecorder.Body.String(), `"id":"cluster-1"`) {
		t.Fatalf("unexpected response: %s", responseRecorder.Body.String())
	}
}

func TestGetCluster(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/cluster-1", nil)
	responseRecorder := httptest.NewRecorder()
	manager := fakeManager{managed: cluster.ManagedCluster{ID: "cluster-1", Name: "prd-01"}}

	NewRouter("test-version", manager).ServeHTTP(responseRecorder, request)

	if responseRecorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", responseRecorder.Code, responseRecorder.Body.String())
	}
	if !strings.Contains(responseRecorder.Body.String(), `"name":"prd-01"`) {
		t.Fatalf("unexpected response: %s", responseRecorder.Body.String())
	}
}

func TestGetClusterNodes(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/cluster-1/nodes", nil)
	responseRecorder := httptest.NewRecorder()
	manager := fakeManager{nodes: []cluster.ClusterNode{{Name: "worker-1", Status: "Ready", CPUCores: 8}}}

	NewRouter("test-version", manager).ServeHTTP(responseRecorder, request)

	if responseRecorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", responseRecorder.Code, responseRecorder.Body.String())
	}
	if !strings.Contains(responseRecorder.Body.String(), `"name":"worker-1"`) {
		t.Fatalf("unexpected response: %s", responseRecorder.Body.String())
	}
}

func TestGetClusterCapacityAndLockBaseline(t *testing.T) {
	manager := fakeManager{
		capacity: cluster.ClusterCapacity{WorkerNodes: 3, CPUAllocatableMilli: 24000, Comparison: cluster.CapacityComparison{Status: "stable"}},
		baseline: cluster.CapacityBaseline{ID: 7, ClusterID: "cluster-1", WorkerNodes: 3},
	}
	getRequest := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/cluster-1/capacity", nil)
	getResponse := httptest.NewRecorder()
	NewRouter("test-version", manager).ServeHTTP(getResponse, getRequest)
	if getResponse.Code != http.StatusOK || !strings.Contains(getResponse.Body.String(), `"cpu_allocatable_milli":24000`) {
		t.Fatalf("unexpected capacity response: %d %s", getResponse.Code, getResponse.Body.String())
	}

	postRequest := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/cluster-1/capacity/baselines", nil)
	postResponse := httptest.NewRecorder()
	NewRouter("test-version", manager).ServeHTTP(postResponse, postRequest)
	if postResponse.Code != http.StatusCreated || !strings.Contains(postResponse.Body.String(), `"id":7`) {
		t.Fatalf("unexpected baseline response: %d %s", postResponse.Code, postResponse.Body.String())
	}
}

func TestGetFleetCapacity(t *testing.T) {
	manager := fakeManager{fleetCapacity: cluster.FleetCapacity{Items: []cluster.FleetCapacityItem{{ClusterID: "cluster-1", ClusterName: "prd-1", Capacity: cluster.ClusterCapacity{CPUAllocatableMilli: 24000}}}}}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/capacity", nil)
	responseRecorder := httptest.NewRecorder()

	NewRouter("test-version", manager).ServeHTTP(responseRecorder, request)

	if responseRecorder.Code != http.StatusOK || !strings.Contains(responseRecorder.Body.String(), `"cluster_name":"prd-1"`) {
		t.Fatalf("unexpected fleet capacity response: %d %s", responseRecorder.Code, responseRecorder.Body.String())
	}
}

func TestCapacityClassificationAndBaselineDecisionEndpoints(t *testing.T) {
	manager := fakeManager{
		classification: cluster.CapacityClassificationOverview{Classification: cluster.CapacityClassification{ID: 2, BusinessNamespaces: []string{"orders"}}, Discovered: []cluster.DiscoveredNamespace{{TechnologyStack: "OCP", Namespace: "openshift-monitoring"}}},
		decision:       cluster.CapacityBaselineDecision{ID: 9, ClusterID: "cluster-1", ClassificationVersionID: 2, Decision: "keep", Reason: "gray release"},
	}
	router := NewRouter("test", manager)
	getResponse := httptest.NewRecorder()
	router.ServeHTTP(getResponse, httptest.NewRequest(http.MethodGet, "/api/v1/capacity/classification", nil))
	if getResponse.Code != http.StatusOK || !strings.Contains(getResponse.Body.String(), `"namespace":"openshift-monitoring"`) {
		t.Fatalf("unexpected get classification response: %d %s", getResponse.Code, getResponse.Body.String())
	}

	putResponse := httptest.NewRecorder()
	router.ServeHTTP(putResponse, httptest.NewRequest(http.MethodPut, "/api/v1/capacity/classification", strings.NewReader(`{"business_namespaces":["orders"],"common_platform_namespaces":["kube-system"],"stack_platform_namespaces":{"OCP":["openshift-monitoring"]},"note":"initial"}`)))
	if putResponse.Code != http.StatusCreated || !strings.Contains(putResponse.Body.String(), `"id":3`) {
		t.Fatalf("unexpected save classification response: %d %s", putResponse.Code, putResponse.Body.String())
	}

	decisionResponse := httptest.NewRecorder()
	router.ServeHTTP(decisionResponse, httptest.NewRequest(http.MethodPost, "/api/v1/clusters/cluster-1/capacity/baseline-decisions", strings.NewReader(`{"reason":"gray release"}`)))
	if decisionResponse.Code != http.StatusCreated || !strings.Contains(decisionResponse.Body.String(), `"reason":"gray release"`) {
		t.Fatalf("unexpected baseline decision response: %d %s", decisionResponse.Code, decisionResponse.Body.String())
	}
}

func TestGetClusterWorkloads(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/cluster-1/workloads", nil)
	responseRecorder := httptest.NewRecorder()
	manager := fakeManager{workloads: []cluster.ClusterWorkload{{Kind: "Deployment", Namespace: "app", Name: "web", Status: "healthy"}}}

	NewRouter("test-version", manager).ServeHTTP(responseRecorder, request)

	if responseRecorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", responseRecorder.Code, responseRecorder.Body.String())
	}
	if !strings.Contains(responseRecorder.Body.String(), `"kind":"Deployment"`) || !strings.Contains(responseRecorder.Body.String(), `"name":"web"`) {
		t.Fatalf("unexpected response: %s", responseRecorder.Body.String())
	}
}

func TestGetClusterPods(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/cluster-1/pods", nil)
	responseRecorder := httptest.NewRecorder()
	manager := fakeManager{pods: []cluster.ClusterPod{{Name: "web-abc-1", Namespace: "app", OwnerKind: "Deployment", OwnerName: "web"}}}

	NewRouter("test-version", manager).ServeHTTP(responseRecorder, request)

	if responseRecorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", responseRecorder.Code, responseRecorder.Body.String())
	}
	if !strings.Contains(responseRecorder.Body.String(), `"owner_kind":"Deployment"`) {
		t.Fatalf("unexpected response: %s", responseRecorder.Body.String())
	}
}

func TestGetClusterPodDetail(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/cluster-1/pods/app/web-abc-1", nil)
	responseRecorder := httptest.NewRecorder()
	manager := fakeManager{pod: cluster.ClusterPodDetail{
		ClusterPod: cluster.ClusterPod{Name: "web-abc-1", Namespace: "app", OwnerKind: "Deployment", OwnerName: "web"},
		Containers: []cluster.PodContainer{{Name: "web", StatusReason: "CrashLoopBackOff"}},
	}}

	NewRouter("test-version", manager).ServeHTTP(responseRecorder, request)

	if responseRecorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", responseRecorder.Code, responseRecorder.Body.String())
	}
	if !strings.Contains(responseRecorder.Body.String(), `"status_reason":"CrashLoopBackOff"`) {
		t.Fatalf("unexpected response: %s", responseRecorder.Body.String())
	}
}

func TestGetClusterPodLogs(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/cluster-1/pods/app/web-abc-1/logs?container=web&tail_lines=500&previous=true&since=2026-10-16T01%3A00%3A00Z&until=2026-10-16T02%3A00%3A00Z", nil)
	responseRecorder := httptest.NewRecorder()
	manager := fakeManager{podLogs: cluster.PodLogSnapshot{Container: "web", Text: "server started\n", TailLines: 500, Previous: true}}

	NewRouter("test-version", manager).ServeHTTP(responseRecorder, request)

	if responseRecorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", responseRecorder.Code, responseRecorder.Body.String())
	}
	if !strings.Contains(responseRecorder.Body.String(), `"text":"server started\n"`) || !strings.Contains(responseRecorder.Body.String(), `"previous":true`) {
		t.Fatalf("unexpected response: %s", responseRecorder.Body.String())
	}
}

func TestGetClusterPodLogsRejectsInvalidTimeRange(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/cluster-1/pods/app/web-abc-1/logs?since=2026-10-16T02%3A00%3A00Z&until=2026-10-16T01%3A00%3A00Z", nil)
	responseRecorder := httptest.NewRecorder()

	NewRouter("test-version", fakeManager{}).ServeHTTP(responseRecorder, request)

	if responseRecorder.Code != http.StatusBadRequest || !strings.Contains(responseRecorder.Body.String(), cluster.CodeInvalidLogTimeRange) {
		t.Fatalf("expected invalid time range response, got %d: %s", responseRecorder.Code, responseRecorder.Body.String())
	}
}

func TestGetClusterWorkloadDetail(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/cluster-1/workloads/Deployment/app/web", nil)
	responseRecorder := httptest.NewRecorder()
	manager := fakeManager{workload: cluster.ClusterWorkloadDetail{
		ClusterWorkload: cluster.ClusterWorkload{Kind: "Deployment", Namespace: "app", Name: "web"},
		Pods:            []cluster.WorkloadPod{{Name: "web-abc-1", Phase: "Running"}},
	}}

	NewRouter("test-version", manager).ServeHTTP(responseRecorder, request)

	if responseRecorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", responseRecorder.Code, responseRecorder.Body.String())
	}
	if !strings.Contains(responseRecorder.Body.String(), `"name":"web-abc-1"`) {
		t.Fatalf("unexpected response: %s", responseRecorder.Body.String())
	}
}

func TestDeleteMissingClusterReturnsNotFound(t *testing.T) {
	request := httptest.NewRequest(http.MethodDelete, "/api/v1/clusters/missing", nil)
	responseRecorder := httptest.NewRecorder()
	manager := fakeManager{err: &cluster.Error{Code: cluster.CodeClusterNotFound}}

	NewRouter("test-version", manager).ServeHTTP(responseRecorder, request)

	if responseRecorder.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d: %s", responseRecorder.Code, responseRecorder.Body.String())
	}
}

func TestVersion(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/v1/version", nil)
	response := httptest.NewRecorder()

	NewRouter("test-version").ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, response.Code)
	}

	want := "{\"data\":{\"version\":\"test-version\"},\"error\":null}\n"
	if got := response.Body.String(); got != want {
		t.Fatalf("expected body %q, got %q", want, got)
	}
}

type refreshingManager struct {
	fakeManager
	refreshed         bool
	capacityRefreshed bool
}

func (manager *refreshingManager) Refresh(context.Context, string) (cluster.ManagedCluster, error) {
	manager.refreshed = true
	return manager.managed, nil
}

func (manager *refreshingManager) RefreshCapacity(context.Context, string) (cluster.ClusterCapacity, error) {
	manager.capacityRefreshed = true
	return manager.capacity, nil
}

func TestSnapshotEndpointsUseLiveRefreshOnlyWhenRequested(t *testing.T) {
	manager := &refreshingManager{fakeManager: fakeManager{}}
	router := NewRouter("test", manager)

	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/clusters/cluster-1", nil))
	if manager.refreshed {
		t.Fatal("snapshot cluster read must not call live refresh")
	}
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/clusters/cluster-1?refresh=1", nil))
	if !manager.refreshed {
		t.Fatal("refresh=1 must call live refresh")
	}

	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/clusters/cluster-1/capacity", nil))
	if manager.capacityRefreshed {
		t.Fatal("snapshot capacity read must not call live refresh")
	}
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/clusters/cluster-1/capacity?refresh=1", nil))
	if !manager.capacityRefreshed {
		t.Fatal("capacity refresh=1 must call live refresh")
	}
}
