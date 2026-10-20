package cluster

import (
	"context"
	"crypto/tls"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

func TestEffectivePodResourcesUsesInitMaximumAndOverhead(t *testing.T) {
	pod := corev1.Pod{Spec: corev1.PodSpec{
		Containers: []corev1.Container{
			{Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("500m"), corev1.ResourceMemory: resource.MustParse("256Mi")}, Limits: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1"), corev1.ResourceMemory: resource.MustParse("512Mi")}}},
			{Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("500m"), corev1.ResourceMemory: resource.MustParse("256Mi")}}},
		},
		InitContainers: []corev1.Container{{Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("2"), corev1.ResourceMemory: resource.MustParse("128Mi")}}}},
		Overhead:       corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m"), corev1.ResourceMemory: resource.MustParse("64Mi")},
	}}

	got := effectivePodResources(pod)
	expectedMemoryRequest := resource.MustParse("576Mi")
	expectedMemoryLimit := resource.MustParse("512Mi")

	if got.CPURequestsMilli != 2100 || got.MemoryRequests != expectedMemoryRequest.Value() {
		t.Fatalf("unexpected effective requests: %#v", got)
	}
	if got.CPULimitsMilli != 1000 || got.MemoryLimits != expectedMemoryLimit.Value() {
		t.Fatalf("unexpected effective limits: %#v", got)
	}
}

func TestKubernetesConnectorReturnsServerVersion(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/version" {
			http.NotFound(writer, request)
			return
		}
		if request.Header.Get("Authorization") != "Bearer test-token" {
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"major":"1","minor":"32","gitVersion":"v1.32.4"}`))
	}))
	t.Cleanup(server.Close)

	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	version, err := NewKubernetesConnector(time.Second).Version(context.Background(), ParsedKubeconfig{
		APIServer: server.URL,
		CAData:    ca,
		Token:     "test-token",
	})
	if err != nil {
		t.Fatalf("Version returned error: %v", err)
	}
	if version != "v1.32.4" {
		t.Fatalf("expected v1.32.4, got %q", version)
	}
}

func TestKubernetesConnectorUsesClientCertificate(t *testing.T) {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.TLS == nil || len(request.TLS.PeerCertificates) == 0 {
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"major":"1","minor":"32","gitVersion":"v1.32.4"}`))
	}))
	server.TLS = &tls.Config{ClientAuth: tls.RequireAnyClientCert}
	server.StartTLS()
	t.Cleanup(server.Close)

	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	clientCertificate, clientKey := testClientCredentialPEM(t)
	version, err := NewKubernetesConnector(time.Second).Version(context.Background(), ParsedKubeconfig{
		APIServer:             server.URL,
		CAData:                ca,
		ClientCertificateData: clientCertificate,
		ClientKeyData:         clientKey,
	})
	if err != nil {
		t.Fatalf("Version returned error: %v", err)
	}
	if version != "v1.32.4" {
		t.Fatalf("expected v1.32.4, got %q", version)
	}
}

func TestKubernetesConnectorMapsUnauthorized(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusUnauthorized)
		_, _ = writer.Write([]byte(`{"kind":"Status","apiVersion":"v1","status":"Failure","reason":"Unauthorized","code":401}`))
	}))
	t.Cleanup(server.Close)
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})

	_, err := NewKubernetesConnector(time.Second).Version(context.Background(), ParsedKubeconfig{
		APIServer: server.URL,
		CAData:    ca,
		Token:     "bad-token",
	})
	if got := ErrorCode(err); got != CodeAuthenticationFailed {
		t.Fatalf("expected %s, got %s (%v)", CodeAuthenticationFailed, got, err)
	}
}

func TestKubernetesConnectorCollectsOverviewAndPrometheusMetrics(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api/v1/nodes":
			_, _ = writer.Write([]byte(`{"items":[{"metadata":{"name":"worker-1"},"status":{"capacity":{"cpu":"8","memory":"16Gi"},"conditions":[{"type":"Ready","status":"True"}]}}]}`))
		case "/api/v1/pods":
			_, _ = writer.Write([]byte(`{"items":[{"metadata":{"namespace":"kube-system","name":"coredns"},"status":{"phase":"Running","conditions":[{"type":"Ready","status":"True"}]}},{"metadata":{"namespace":"app","name":"web"},"status":{"phase":"Running","conditions":[{"type":"Ready","status":"False"}]}}]}`))
		case "/api/v1/services":
			_, _ = writer.Write([]byte(`{"items":[{"metadata":{"namespace":"monitoring","name":"prometheus","labels":{"app":"prometheus"}},"spec":{"ports":[{"name":"web","port":9090}]}}]}`))
		case "/api/v1/namespaces/monitoring/services/http:prometheus:web/proxy/api/v1/query":
			value := "2"
			if strings.Contains(request.URL.Query().Get("query"), "memory_working_set") {
				value = "8589934592"
			}
			_, _ = writer.Write([]byte(`{"status":"success","data":{"result":[{"value":[1,"` + value + `"]}]}}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})

	snapshot, err := NewKubernetesConnector(time.Second).Health(context.Background(), ParsedKubeconfig{
		APIServer: server.URL,
		CAData:    ca,
		Token:     "test-token",
	})
	if err != nil {
		t.Fatalf("Health returned error: %v", err)
	}
	if snapshot.NodesReady != 1 || snapshot.NodesTotal != 1 || snapshot.InstancesReady != 1 || snapshot.InstancesTotal != 2 {
		t.Fatalf("unexpected readiness snapshot: %#v", snapshot)
	}
	if snapshot.CPUUtilization == nil || *snapshot.CPUUtilization != 25 || snapshot.MemoryUtilization == nil || *snapshot.MemoryUtilization != 50 {
		t.Fatalf("unexpected Prometheus metrics: %#v", snapshot)
	}
}

func TestKubernetesConnectorListsNodeDetails(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.URL.Path != "/api/v1/nodes" {
			http.NotFound(writer, request)
			return
		}
		_, _ = writer.Write([]byte(`{"items":[{"metadata":{"name":"worker-1","creationTimestamp":"2026-10-01T00:00:00Z","labels":{"node-role.kubernetes.io/worker":"","topology.kubernetes.io/region":"cn-east","topology.kubernetes.io/zone":"cn-east-1a","node.kubernetes.io/instance-type":"physical"}},"spec":{},"status":{"nodeInfo":{"kubeletVersion":"v1.32.4"},"capacity":{"cpu":"8","memory":"16Gi"},"conditions":[{"type":"Ready","status":"True"}]}}]}`))
	}))
	t.Cleanup(server.Close)
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})

	nodes, err := NewKubernetesConnector(time.Second).Nodes(context.Background(), ParsedKubeconfig{
		APIServer: server.URL,
		CAData:    ca,
		Token:     "test-token",
	})
	if err != nil {
		t.Fatalf("Nodes returned error: %v", err)
	}
	if len(nodes) != 1 || nodes[0].Name != "worker-1" || nodes[0].Status != "Ready" {
		t.Fatalf("unexpected nodes: %#v", nodes)
	}
	if len(nodes[0].Roles) != 1 || nodes[0].Roles[0] != "worker" || nodes[0].CPUCores != 8 {
		t.Fatalf("unexpected node role or capacity: %#v", nodes[0])
	}
	if nodes[0].Region != "cn-east" || nodes[0].Zone != "cn-east-1a" || nodes[0].MemoryBytes != 16*1024*1024*1024 {
		t.Fatalf("unexpected node topology or memory: %#v", nodes[0])
	}
}

func TestKubernetesConnectorListsWorkloadsAndResolvesOwnedPods(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/apis/apps/v1/deployments":
			_, _ = writer.Write([]byte(`{"items":[{"metadata":{"namespace":"app","name":"web","uid":"dep-uid","generation":2,"creationTimestamp":"2026-10-01T00:00:00Z","labels":{"app":"web"}},"spec":{"replicas":3,"template":{"spec":{"containers":[{"name":"web","image":"registry.example/web:v2"}]}}},"status":{"observedGeneration":2,"replicas":3,"updatedReplicas":3,"readyReplicas":2,"availableReplicas":2,"conditions":[{"type":"Progressing","status":"True","lastUpdateTime":"2026-10-16T01:00:00Z"}]}}]}`))
		case "/apis/apps/v1/statefulsets":
			_, _ = writer.Write([]byte(`{"items":[{"metadata":{"namespace":"data","name":"cache","uid":"sts-uid","generation":1,"creationTimestamp":"2026-10-02T00:00:00Z"},"spec":{"replicas":2,"template":{"spec":{"containers":[{"name":"cache","image":"redis:7"}]}}},"status":{"observedGeneration":1,"replicas":2,"readyReplicas":2,"availableReplicas":2,"updatedReplicas":2,"currentRevision":"cache-a","updateRevision":"cache-a"}}]}`))
		case "/apis/apps/v1/daemonsets":
			_, _ = writer.Write([]byte(`{"items":[{"metadata":{"namespace":"monitoring","name":"node-agent","uid":"ds-uid","generation":1,"creationTimestamp":"2026-10-03T00:00:00Z"},"spec":{"template":{"spec":{"containers":[{"name":"agent","image":"agent:v1"}]}}},"status":{"observedGeneration":1,"desiredNumberScheduled":3,"updatedNumberScheduled":3,"numberReady":2,"numberAvailable":2,"numberUnavailable":1}}]}`))
		case "/apis/apps/v1/replicasets", "/apis/apps/v1/namespaces/app/replicasets":
			_, _ = writer.Write([]byte(`{"items":[{"metadata":{"namespace":"app","name":"web-abc","uid":"rs-uid","ownerReferences":[{"apiVersion":"apps/v1","kind":"Deployment","name":"web","uid":"dep-uid","controller":true}]}}]}`))
		case "/api/v1/pods":
			_, _ = writer.Write([]byte(`{"items":[{"metadata":{"namespace":"app","name":"web-abc-1","uid":"pod-uid","creationTimestamp":"2026-10-16T00:30:00Z","ownerReferences":[{"apiVersion":"apps/v1","kind":"ReplicaSet","name":"web-abc","uid":"rs-uid","controller":true}]},"spec":{"nodeName":"worker-1","containers":[{"name":"web","image":"registry.example/web:v2"}]},"status":{"phase":"Running","podIP":"10.0.0.10","startTime":"2026-10-16T00:31:00Z","conditions":[{"type":"Ready","status":"False"}],"containerStatuses":[{"name":"web","ready":false,"restartCount":4,"state":{"waiting":{"reason":"CrashLoopBackOff"}}}]}},{"metadata":{"namespace":"app","name":"manual-pod","uid":"manual-pod-uid","creationTimestamp":"2026-10-16T00:40:00Z","labels":{"run":"manual"}},"spec":{"nodeName":"worker-1","containers":[{"name":"manual","image":"busybox:1.36"}]},"status":{"phase":"Running","podIP":"10.0.0.11","startTime":"2026-10-16T00:41:00Z","conditions":[{"type":"Ready","status":"True"}],"containerStatuses":[{"name":"manual","ready":true,"restartCount":0,"state":{"running":{}}}]}}]}`))
		case "/api/v1/namespaces/app/pods/web-abc-1":
			_, _ = writer.Write([]byte(`{"metadata":{"namespace":"app","name":"web-abc-1","uid":"pod-uid","creationTimestamp":"2026-10-16T00:30:00Z","labels":{"app":"web"},"ownerReferences":[{"apiVersion":"apps/v1","kind":"ReplicaSet","name":"web-abc","uid":"rs-uid","controller":true}]},"spec":{"nodeName":"worker-1","serviceAccountName":"web-sa","containers":[{"name":"web","image":"registry.example/web:v2"}]},"status":{"phase":"Running","hostIP":"192.168.1.10","podIP":"10.0.0.10","qosClass":"Burstable","startTime":"2026-10-16T00:31:00Z","conditions":[{"type":"Ready","status":"False","reason":"ContainersNotReady","message":"container is waiting"}],"containerStatuses":[{"name":"web","ready":false,"restartCount":4,"state":{"waiting":{"reason":"CrashLoopBackOff"}}}]}}`))
		case "/api/v1/namespaces/app/pods/web-abc-1/log":
			query := request.URL.Query()
			if query.Get("container") != "web" || query.Get("timestamps") != "true" {
				t.Fatalf("unexpected log query: %s", request.URL.RawQuery)
			}
			if query.Get("sinceTime") != "" {
				if query.Get("tailLines") != "" || query.Get("sinceTime") != "2026-10-16T00:30:00Z" {
					t.Fatalf("unexpected ranged log query: %s", request.URL.RawQuery)
				}
				_, _ = writer.Write([]byte("2026-10-16T01:00:00Z server started\n2026-10-16T02:00:00Z later line\n"))
				return
			}
			if query.Get("tailLines") != "200" {
				t.Fatalf("unexpected tailed log query: %s", request.URL.RawQuery)
			}
			_, _ = writer.Write([]byte("2026-10-16T01:00:00Z server started\n"))
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	parsed := ParsedKubeconfig{APIServer: server.URL, CAData: ca, Token: "test-token"}

	workloads, err := NewKubernetesConnector(time.Second).Workloads(context.Background(), parsed)
	if err != nil {
		t.Fatalf("Workloads returned error: %v", err)
	}
	if len(workloads) != 3 {
		t.Fatalf("expected three controller workloads, got %#v", workloads)
	}
	byName := make(map[string]ClusterWorkload, len(workloads))
	for _, workload := range workloads {
		byName[workload.Name] = workload
	}
	if byName["web"].Status != "progressing" || byName["web"].PodsTotal != 1 || byName["web"].RestartCount != 4 {
		t.Fatalf("unexpected deployment summary: %#v", byName["web"])
	}
	if byName["cache"].Status != "healthy" || byName["node-agent"].Status != "degraded" {
		t.Fatalf("unexpected controller states: %#v", byName)
	}
	detail, err := NewKubernetesConnector(time.Second).Workload(context.Background(), parsed, "Deployment", "app", "web")
	if err != nil {
		t.Fatalf("Workload returned error: %v", err)
	}
	if detail.Labels["app"] != "web" || len(detail.Pods) != 1 || detail.Pods[0].StatusReason != "CrashLoopBackOff" {
		t.Fatalf("unexpected workload detail: %#v", detail)
	}
	pods, err := NewKubernetesConnector(time.Second).Pods(context.Background(), parsed)
	if err != nil {
		t.Fatalf("Pods returned error: %v", err)
	}
	if len(pods) != 2 {
		t.Fatalf("expected two Pods, got %#v", pods)
	}
	byPodName := make(map[string]ClusterPod, len(pods))
	for _, pod := range pods {
		byPodName[pod.Name] = pod
	}
	if byPodName["web-abc-1"].OwnerKind != "Deployment" || byPodName["web-abc-1"].OwnerName != "web" {
		t.Fatalf("unexpected owned Pod: %#v", byPodName["web-abc-1"])
	}
	if byPodName["manual-pod"].OwnerKind != "" || byPodName["manual-pod"].OwnerName != "" {
		t.Fatalf("unexpected unowned Pod: %#v", byPodName["manual-pod"])
	}
	podDetail, err := NewKubernetesConnector(time.Second).Pod(context.Background(), parsed, "app", "web-abc-1")
	if err != nil {
		t.Fatalf("Pod returned error: %v", err)
	}
	if podDetail.OwnerKind != "Deployment" || podDetail.OwnerName != "web" || podDetail.ServiceAccount != "web-sa" {
		t.Fatalf("unexpected Pod detail: %#v", podDetail)
	}
	if len(podDetail.Containers) != 1 || podDetail.Containers[0].StatusReason != "CrashLoopBackOff" || len(podDetail.Conditions) != 1 {
		t.Fatalf("unexpected Pod runtime detail: %#v", podDetail)
	}
	logs, err := NewKubernetesConnector(time.Second).PodLogs(context.Background(), parsed, "app", "web-abc-1", PodLogOptions{Container: "web", TailLines: 200})
	if err != nil {
		t.Fatalf("PodLogs returned error: %v", err)
	}
	if logs.Container != "web" || !strings.Contains(logs.Text, "server started") || logs.TailLines != 200 {
		t.Fatalf("unexpected Pod logs: %#v", logs)
	}
	since := time.Date(2026, 10, 16, 0, 30, 0, 0, time.UTC)
	until := time.Date(2026, 10, 16, 1, 30, 0, 0, time.UTC)
	rangedLogs, err := NewKubernetesConnector(time.Second).PodLogs(context.Background(), parsed, "app", "web-abc-1", PodLogOptions{Container: "web", TailLines: 200, SinceTime: &since, UntilTime: &until})
	if err != nil {
		t.Fatalf("ranged PodLogs returned error: %v", err)
	}
	if !strings.Contains(rangedLogs.Text, "server started") || strings.Contains(rangedLogs.Text, "later line") {
		t.Fatalf("unexpected ranged Pod logs: %#v", rangedLogs)
	}
}

func TestKubernetesConnectorClassifiesBusinessAndAllOtherNamespacesAsPlatform(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api/v1/nodes":
			_, _ = writer.Write([]byte(`{"items":[{"metadata":{"name":"worker-1"},"spec":{},"status":{"allocatable":{"cpu":"8","memory":"16Gi"}}}]}`))
		case "/api/v1/namespaces":
			_, _ = writer.Write([]byte(`{"items":[{"metadata":{"name":"kube-system"}},{"metadata":{"name":"orders"}},{"metadata":{"name":"new-addon"}},{"metadata":{"name":"myapp"}},{"metadata":{"name":"empty-platform"}},{"metadata":{"name":"empty-business"}},{"metadata":{"name":"pending-only"}},{"metadata":{"name":"completed-only"}}]}`))
		case "/api/v1/pods":
			_, _ = writer.Write([]byte(`{"items":[
				{"metadata":{"namespace":"kube-system","name":"dns"},"spec":{"nodeName":"worker-1","containers":[{"name":"dns","resources":{"requests":{"cpu":"100m","memory":"128Mi"}}}]},"status":{"phase":"Running"}},
				{"metadata":{"namespace":"orders","name":"api"},"spec":{"nodeName":"worker-1","containers":[{"name":"api","resources":{"requests":{"cpu":"500m","memory":"512Mi"}}}]},"status":{"phase":"Running"}},
				{"metadata":{"namespace":"new-addon","name":"agent"},"spec":{"nodeName":"worker-1","containers":[{"name":"agent","resources":{"requests":{"cpu":"200m","memory":"256Mi"}}}]},"status":{"phase":"Running"}},
				{"metadata":{"namespace":"pending-only","name":"pending"},"spec":{"containers":[{"name":"app","resources":{"requests":{"cpu":"300m"}}}]},"status":{"phase":"Pending"}},
				{"metadata":{"namespace":"completed-only","name":"done"},"spec":{"nodeName":"worker-1","containers":[{"name":"app","resources":{"requests":{"cpu":"900m"}}}]},"status":{"phase":"Succeeded"}},
				{"metadata":{"namespace":"completed-only","name":"failed"},"spec":{"nodeName":"worker-1","containers":[{"name":"app","resources":{"requests":{"cpu":"900m"}}}]},"status":{"phase":"Failed"}}
			]}`))
		case "/api/v1/services":
			_, _ = writer.Write([]byte(`{"items":[]}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	snapshot, err := NewKubernetesConnector(time.Second).Capacity(context.Background(), ParsedKubeconfig{APIServer: server.URL, CAData: ca, Token: "token"}, CapacityRule{VersionID: 7, PlatformNamespaces: []string{"kube-system", "empty-platform", "orders"}, BusinessNamespaces: []string{"orders", "empty-business"}})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Platform.CPURequestsMilli != 300 || snapshot.Business.CPURequestsMilli != 500 || snapshot.Unclassified.CPURequestsMilli != 0 {
		t.Fatalf("unexpected classified capacity: platform=%#v business=%#v unclassified=%#v", snapshot.Platform, snapshot.Business, snapshot.Unclassified)
	}
	if snapshot.ClassificationVersionID != 7 || len(snapshot.Namespaces) != 8 {
		t.Fatalf("unexpected capacity metadata: %#v", snapshot)
	}
	if snapshot.Pending.CPURequestsMilli != 300 {
		t.Fatalf("pending requests should remain separate: %#v", snapshot.Pending)
	}
	byName := map[string]NamespaceCapacity{}
	for _, namespace := range snapshot.Namespaces {
		byName[namespace.Namespace] = namespace
	}
	for name, class := range map[string]string{"myapp": "platform", "empty-platform": "platform", "empty-business": "business", "pending-only": "platform", "completed-only": "platform"} {
		namespace, ok := byName[name]
		if !ok || namespace.Class != class || namespace.Pods != 0 || namespace.CPURequestsMilli != 0 || namespace.MemoryRequests != 0 || namespace.PodsWithoutCPURequests != 0 || namespace.PodsWithoutMemoryRequests != 0 {
			t.Errorf("expected discovered namespace %s with class %s and zero scheduled resources, got %#v (exists=%v)", name, class, namespace, ok)
		}
	}
}

func TestCapacityMetricsUseBusinessListAndPlatformComplement(t *testing.T) {
	queries := []string{}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/namespaces":
			w.Write([]byte(`{"items":[{"metadata":{"name":"myapp"}},{"metadata":{"name":"addon"}}]}`))
		case "/api/v1/nodes", "/api/v1/pods":
			w.Write([]byte(`{"items":[]}`))
		case "/api/v1/services":
			w.Write([]byte(`{"items":[{"metadata":{"namespace":"monitoring","name":"prometheus","labels":{"app":"prometheus"}},"spec":{"ports":[{"name":"web","port":9090}]}}]}`))
		case "/api/v1/namespaces/monitoring/services/http:prometheus:web/proxy/api/v1/query":
			query := r.URL.Query().Get("query")
			queries = append(queries, query)
			value := "1"
			if strings.Contains(query, "namespace!~") {
				value = "2"
			}
			if strings.Contains(query, "memory_working_set") {
				value += "000"
			}
			w.Write([]byte(`{"status":"success","data":{"result":[{"value":[1,"` + value + `"]}]}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	snapshot, err := NewKubernetesConnector(time.Second).Capacity(context.Background(), ParsedKubeconfig{APIServer: server.URL, CAData: ca, Token: "token"}, CapacityRule{BusinessNamespaces: []string{"myapp"}})
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.MetricsAvailable || len(queries) != 4 || *snapshot.Business.CPUUsageMilli != 1000 || *snapshot.Platform.CPUUsageMilli != 2000 || *snapshot.Business.MemoryUsage != 1000 || *snapshot.Platform.MemoryUsage != 2000 {
		t.Fatalf("wrong business/platform metrics: %#v, queries=%v", snapshot, queries)
	}
	for _, query := range queries {
		if !strings.Contains(query, "myapp") {
			t.Errorf("query must use global business list: %s", query)
		}
	}
}

func TestKubernetesConnectorRejectsIncompleteNamespaceDiscovery(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.URL.Path == "/api/v1/namespaces" {
			writer.WriteHeader(http.StatusForbidden)
			_, _ = writer.Write([]byte(`{"kind":"Status","apiVersion":"v1","status":"Failure","reason":"Forbidden","code":403,"message":"namespaces is forbidden"}`))
			return
		}
		_, _ = writer.Write([]byte(`{"items":[]}`))
	}))
	t.Cleanup(server.Close)
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	_, err := NewKubernetesConnector(time.Second).Capacity(context.Background(), ParsedKubeconfig{APIServer: server.URL, CAData: ca, Token: "token"}, CapacityRule{})
	if ErrorCode(err) != CodeAuthorizationFailed {
		t.Fatalf("namespace discovery permission errors must not become an empty result: %v", err)
	}
}

type fakeConnector struct {
	version   string
	err       error
	health    HealthSnapshot
	nodes     []ClusterNode
	pods      []ClusterPod
	pod       ClusterPodDetail
	podLogs   PodLogSnapshot
	workloads []ClusterWorkload
	workload  ClusterWorkloadDetail
	capacity  ClusterCapacity
	healthFn  func(context.Context, ParsedKubeconfig) (HealthSnapshot, error)
}

func (connector fakeConnector) Version(context.Context, ParsedKubeconfig) (string, error) {
	return connector.version, connector.err
}

func (connector fakeConnector) Health(ctx context.Context, parsed ParsedKubeconfig) (HealthSnapshot, error) {
	if connector.healthFn != nil {
		return connector.healthFn(ctx, parsed)
	}
	return connector.health, connector.err
}

func (connector fakeConnector) Nodes(context.Context, ParsedKubeconfig) ([]ClusterNode, error) {
	return connector.nodes, connector.err
}

func (connector fakeConnector) Pods(context.Context, ParsedKubeconfig) ([]ClusterPod, error) {
	return connector.pods, connector.err
}

func (connector fakeConnector) Pod(context.Context, ParsedKubeconfig, string, string) (ClusterPodDetail, error) {
	return connector.pod, connector.err
}

func (connector fakeConnector) PodLogs(context.Context, ParsedKubeconfig, string, string, PodLogOptions) (PodLogSnapshot, error) {
	return connector.podLogs, connector.err
}

func TestLogsUntilFiltersTimestampedLines(t *testing.T) {
	content := "2026-10-16T01:00:00Z first\n2026-10-16T01:30:00Z second\n2026-10-16T02:00:00Z third\n"
	until := time.Date(2026, 10, 16, 1, 30, 0, 0, time.UTC)

	got := logsUntil(content, until)

	if got != "2026-10-16T01:00:00Z first\n2026-10-16T01:30:00Z second\n" {
		t.Fatalf("unexpected filtered logs: %q", got)
	}
}

func (connector fakeConnector) Workloads(context.Context, ParsedKubeconfig) ([]ClusterWorkload, error) {
	return connector.workloads, connector.err
}

func (connector fakeConnector) Workload(context.Context, ParsedKubeconfig, string, string, string) (ClusterWorkloadDetail, error) {
	return connector.workload, connector.err
}

func (connector fakeConnector) Capacity(context.Context, ParsedKubeconfig, CapacityRule) (ClusterCapacity, error) {
	return connector.capacity, connector.err
}
