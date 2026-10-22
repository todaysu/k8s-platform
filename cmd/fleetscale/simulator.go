package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

type simulator struct {
	pods, nodes, workloads int
	lists, objects, bytes  atomic.Int64
	podActive, podMax      atomic.Int64
}
type countingWriter struct {
	http.ResponseWriter
	bytes *atomic.Int64
}

func (w countingWriter) Write(data []byte) (int, error) {
	n, err := w.ResponseWriter.Write(data)
	w.bytes.Add(int64(n))
	return n, err
}
func metadata(name string, index int) metav1.ObjectMeta {
	return metav1.ObjectMeta{Name: name, Namespace: fmt.Sprintf("app-%02d", index%99), UID: types.UID(name), ResourceVersion: "1", CreationTimestamp: metav1.NewTime(time.Unix(1700000000, 0))}
}
func (s *simulator) server() *httptest.Server {
	return httptest.NewTLSServer(http.HandlerFunc(s.serve))
}
func (s *simulator) serve(raw http.ResponseWriter, r *http.Request) {
	raw.Header().Set("Content-Type", "application/json")
	path := r.URL.Path
	if r.URL.Query().Get("watch") == "true" {
		raw.WriteHeader(200)
		raw.(http.Flusher).Flush()
		<-r.Context().Done()
		return
	}
	w := countingWriter{raw, &s.bytes}
	if strings.HasSuffix(path, "/query") {
		json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"resultType": "vector", "result": []any{map[string]any{"metric": map[string]string{}, "value": []any{time.Now().Unix(), "0.25"}}}}})
		return
	}
	if strings.HasSuffix(path, "/query_range") {
		start, _ := strconv.ParseInt(r.URL.Query().Get("start"), 10, 64)
		end, _ := strconv.ParseInt(r.URL.Query().Get("end"), 10, 64)
		step, _ := strconv.ParseInt(r.URL.Query().Get("step"), 10, 64)
		if step < 1 {
			step = 60
		}
		values := []any{}
		for at := start; at <= end && len(values) < 600; at += step {
			values = append(values, []any{at, "0.25"})
		}
		json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"resultType": "matrix", "result": []any{map[string]any{"metric": map[string]string{"instance": "synthetic"}, "values": values}}}})
		return
	}
	if path == "/version" {
		json.NewEncoder(w).Encode(map[string]string{"gitVersion": "v1.32.0"})
		return
	}
	count := 0
	kind := ""
	api := "v1"
	var factory func(int) any
	switch path {
	case "/api/v1/nodes":
		count = s.nodes
		kind = "NodeList"
		factory = func(i int) any {
			return corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("node-%03d", i)}, Status: corev1.NodeStatus{Allocatable: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("8"), corev1.ResourceMemory: resource.MustParse("32Gi")}, Capacity: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("8"), corev1.ResourceMemory: resource.MustParse("32Gi")}, Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}}}}
		}
	case "/api/v1/namespaces":
		count = 100
		kind = "NamespaceList"
		factory = func(i int) any {
			name := fmt.Sprintf("app-%02d", i)
			if i == 99 {
				name = "kube-system"
			}
			return corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
		}
	case "/api/v1/pods", "/api/v1/namespaces/kube-system/pods":
		count = s.pods
		if path != "/api/v1/pods" {
			count = 6
		} else {
			active := s.podActive.Add(1)
			defer s.podActive.Add(-1)
			for {
				old := s.podMax.Load()
				if active <= old || s.podMax.CompareAndSwap(old, active) {
					break
				}
			}
		}
		kind = "PodList"
		factory = func(i int) any {
			owner := i % s.workloads
			m := metadata(fmt.Sprintf("java-api-%06d", i), owner)
			m.OwnerReferences = []metav1.OwnerReference{{Kind: "ReplicaSet", Name: fmt.Sprintf("rs-%06d", owner), UID: types.UID(fmt.Sprintf("rs-%06d", owner)), Controller: boolPtr(true)}}
			if path != "/api/v1/pods" {
				m.Namespace = "kube-system"
			}
			return corev1.Pod{ObjectMeta: m, Spec: corev1.PodSpec{NodeName: fmt.Sprintf("node-%03d", i%s.nodes), Containers: []corev1.Container{{Name: "java", Image: "example/java:test", Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("50m"), corev1.ResourceMemory: resource.MustParse("128Mi")}}}}}, Status: corev1.PodStatus{Phase: corev1.PodRunning, Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}}}
		}
	case "/apis/apps/v1/deployments":
		count = s.workloads
		kind = "DeploymentList"
		api = "apps/v1"
		factory = func(i int) any {
			return appsv1.Deployment{ObjectMeta: metadata(fmt.Sprintf("deploy-%06d", i), i), Spec: appsv1.DeploymentSpec{Replicas: int32Ptr(int32(s.pods / s.workloads))}, Status: appsv1.DeploymentStatus{ReadyReplicas: int32(s.pods / s.workloads)}}
		}
	case "/apis/apps/v1/statefulsets":
		kind = "StatefulSetList"
		api = "apps/v1"
	case "/apis/apps/v1/daemonsets":
		kind = "DaemonSetList"
		api = "apps/v1"
	case "/apis/apps/v1/replicasets":
		count = s.workloads * 2
		kind = "ReplicaSetList"
		api = "apps/v1"
		factory = func(i int) any {
			m := metadata(fmt.Sprintf("rs-%06d", i), i%s.workloads)
			m.OwnerReferences = []metav1.OwnerReference{{Kind: "Deployment", Name: fmt.Sprintf("deploy-%06d", i%s.workloads), UID: types.UID(fmt.Sprintf("deploy-%06d", i%s.workloads)), Controller: boolPtr(true)}}
			return appsv1.ReplicaSet{ObjectMeta: m}
		}
	case "/api/v1/services":
		count = 1
		kind = "ServiceList"
		factory = func(int) any {
			return corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "prometheus", Namespace: "monitoring"}, Spec: corev1.ServiceSpec{Ports: []corev1.ServicePort{{Name: "web", Port: 9090}}}}
		}
	case "/apis/events.k8s.io/v1/events":
		kind = "EventList"
		api = "events.k8s.io/v1"
	case "/api/v1/events":
		kind = "EventList"
	default:
		http.Error(w, "unsupported synthetic endpoint", 404)
		return
	}
	from, _ := strconv.Atoi(r.URL.Query().Get("continue"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 {
		limit = count
	}
	if from < 0 || from > count {
		http.Error(w, "invalid continue token", 400)
		return
	}
	to := min(from+limit, count)
	items := make([]any, 0, to-from)
	for i := from; i < to; i++ {
		items = append(items, factory(i))
	}
	next := ""
	if to < count {
		next = strconv.Itoa(to)
	}
	s.lists.Add(1)
	s.objects.Add(int64(len(items)))
	json.NewEncoder(w).Encode(map[string]any{"apiVersion": api, "kind": kind, "metadata": map[string]string{"resourceVersion": "1", "continue": next}, "items": items})
}
func boolPtr(value bool) *bool    { return &value }
func int32Ptr(value int32) *int32 { return &value }
