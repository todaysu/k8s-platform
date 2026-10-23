package setup

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	fakediscovery "k8s.io/client-go/discovery/fake"
	"k8s.io/client-go/kubernetes/fake"
)

func testNode(name, arch string, taints ...corev1.Taint) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: map[string]string{"kubernetes.io/arch": arch, "kubernetes.io/os": "linux"},
		},
		Spec: corev1.NodeSpec{Taints: taints},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("8"),
				corev1.ResourceMemory: resource.MustParse("16Gi"),
			},
			Addresses: []corev1.NodeAddress{{Type: corev1.NodeInternalIP, Address: "10.0.0." + name[len(name)-1:]}},
		},
	}
}

func testStorageClass(name string, isDefault bool) *storagev1.StorageClass {
	annotations := map[string]string{}
	if isDefault {
		annotations["storageclass.kubernetes.io/is-default-class"] = "true"
	}
	expansion := true
	mode := storagev1.VolumeBindingWaitForFirstConsumer
	return &storagev1.StorageClass{
		ObjectMeta:           metav1.ObjectMeta{Name: name, Annotations: annotations},
		Provisioner:          "kubernetes.io/no-provisioner",
		AllowVolumeExpansion: &expansion,
		VolumeBindingMode:    &mode,
	}
}

func testIngressClass(name string, isDefault bool) *networkingv1.IngressClass {
	annotations := map[string]string{}
	if isDefault {
		annotations["ingressclass.kubernetes.io/is-default-class"] = "true"
	}
	return &networkingv1.IngressClass{
		ObjectMeta: metav1.ObjectMeta{Name: name, Annotations: annotations},
		Spec:       networkingv1.IngressClassSpec{Controller: "k8s.io/ingress-nginx"},
	}
}

func discoveryWithPXC(client *fake.Clientset) {
	discovery, ok := client.Discovery().(*fakediscovery.FakeDiscovery)
	if !ok {
		panic("unexpected discovery client")
	}
	discovery.Resources = []*metav1.APIResourceList{{
		GroupVersion: pxcGroupVersion,
		APIResources: []metav1.APIResource{{Name: pxcResource, Kind: "PerconaXtraDBCluster"}},
	}}
}

func TestPreflightReportsClusterCapabilities(t *testing.T) {
	client := fake.NewSimpleClientset(
		testNode("node1", "amd64"),
		testNode("node2", "amd64"),
		testNode("node3", "arm64"),
		testStorageClass("ftc-platform-local", true),
		testIngressClass("nginx", true),
	)
	discoveryWithPXC(client)
	server := &Server{config: Config{Namespace: "ftc-platform", RuntimeSecret: "ftc-platform-runtime"}, client: client}

	result := server.preflight(context.Background())
	if len(result.Nodes) != 3 {
		t.Fatalf("expected 3 nodes, got %d", len(result.Nodes))
	}
	if !result.Nodes[0].Schedulable || result.Nodes[0].Arch != "amd64" || result.Nodes[0].CPUMillis != 8000 {
		t.Fatalf("unexpected first node: %#v", result.Nodes[0])
	}
	if len(result.StorageClasses) != 1 || !result.StorageClasses[0].Default || !result.StorageClasses[0].AllowVolumeExpansion {
		t.Fatalf("unexpected storage classes: %#v", result.StorageClasses)
	}
	if len(result.IngressClasses) != 1 || !result.IngressClasses[0].Default {
		t.Fatalf("unexpected ingress classes: %#v", result.IngressClasses)
	}
	if !result.PXCAvailable {
		t.Fatal("PXC availability was not detected")
	}
	if result.RuntimeSecretExists {
		t.Fatal("unexpected runtime secret")
	}
	if len(result.Errors) != 0 {
		t.Fatalf("unexpected preflight errors: %v", result.Errors)
	}
}

func TestPreflightAllowsSmallExternalMySQLCluster(t *testing.T) {
	client := fake.NewSimpleClientset(
		testNode("node1", "amd64"),
		testNode("node2", "amd64", corev1.Taint{Key: "dedicated", Effect: corev1.TaintEffectNoSchedule}),
	)
	server := &Server{config: Config{Namespace: "ftc-platform"}, client: client}

	result := server.preflight(context.Background())
	if result.PXCAvailable {
		t.Fatal("PXC should not be required for an external-MySQL install")
	}
	if len(result.Errors) != 0 {
		t.Fatalf("external MySQL with NodePort must not require IngressClass: %v", result.Errors)
	}
	if result.Nodes[1].Schedulable {
		t.Fatal("tainted node must not be schedulable")
	}
}

func TestPreflightWarnsWhenAlreadyInitialized(t *testing.T) {
	client := fake.NewSimpleClientset(
		testNode("node1", "amd64"),
		testNode("node2", "amd64"),
		testNode("node3", "amd64"),
		testStorageClass("standard", true),
		testIngressClass("nginx", true),
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "ftc-platform-runtime", Namespace: "ftc-platform"}},
	)
	discoveryWithPXC(client)
	server := &Server{config: Config{Namespace: "ftc-platform", RuntimeSecret: "ftc-platform-runtime"}, client: client}

	result := server.preflight(context.Background())
	if !result.RuntimeSecretExists {
		t.Fatal("runtime secret was not detected")
	}
	if len(result.Warnings) == 0 {
		t.Fatal("expected an already-initialized warning")
	}
}

func TestPreflightEndpointRequiresToken(t *testing.T) {
	client := fake.NewSimpleClientset()
	discoveryWithPXC(client)
	server := &Server{config: Config{Token: "setup-token-value", Namespace: "ftc-platform"}, client: client}

	request := httptest.NewRequest(http.MethodPost, "/api/v1/setup/preflight", strings.NewReader(`{"token":"setup-token-value"}`))
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Data  PreflightResult `json:"data"`
		Error any             `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Data.Errors) == 0 {
		t.Fatal("expected preflight errors for an empty cluster")
	}

	bad := httptest.NewRequest(http.MethodPost, "/api/v1/setup/preflight", strings.NewReader(`{"token":"wrong"}`))
	badRecorder := httptest.NewRecorder()
	server.ServeHTTP(badRecorder, bad)
	if badRecorder.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", badRecorder.Code)
	}
}
