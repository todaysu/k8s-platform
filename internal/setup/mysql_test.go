package setup

import (
	"context"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
)

func testMySQLServer(t *testing.T) (*Server, *dynamicfake.FakeDynamicClient) {
	t.Helper()
	client := fake.NewSimpleClientset(&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "ftc-platform"}})
	dynamicClient := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
	return &Server{config: Config{Namespace: "ftc-platform"}, client: client, dynamic: dynamicClient}, dynamicClient
}

func TestBuildMySQLCluster(t *testing.T) {
	plan := DefaultMySQLPlan()
	object, err := BuildMySQLCluster(plan)
	if err != nil {
		t.Fatal(err)
	}
	if object.GetAPIVersion() != "pxc.percona.com/v1" || object.GetKind() != "PerconaXtraDBCluster" || object.GetName() != pxcClusterName {
		t.Fatalf("unexpected object: %#v", object.Object)
	}
	size, found, _ := unstructured.NestedInt64(object.Object, "spec", "pxc", "size")
	if !found || size != 3 {
		t.Fatalf("expected 3 PXC replicas, got %#v", object.Object["spec"])
	}
	class, _, _ := unstructured.NestedString(object.Object, "spec", "pxc", "volumeSpec", "persistentVolumeClaim", "storageClassName")
	if class != localStorageClass {
		t.Fatalf("unexpected storage class: %s", class)
	}
	tls, _, _ := unstructured.NestedBool(object.Object, "spec", "tls", "enabled")
	if tls {
		t.Fatal("PXC internal TLS should be disabled for the first delivery")
	}
	configuration, _, _ := unstructured.NestedString(object.Object, "spec", "pxc", "configuration")
	if !strings.Contains(configuration, "pxc_strict_mode=DISABLED") {
		t.Fatalf("PXC strict mode must be disabled for the migration lock: %q", configuration)
	}
	if plan.PXCImage == "" || plan.HAProxyImage == "" {
		t.Fatal("default images are missing")
	}
}

func TestApplyMySQLCreatesSecretAndCluster(t *testing.T) {
	server, dynamicClient := testMySQLServer(t)
	ctx := context.Background()
	plan := DefaultMySQLPlan()
	if err := server.ApplyMySQL(ctx, plan); err != nil {
		t.Fatal(err)
	}
	secret, err := server.client.CoreV1().Secrets("ftc-platform").Get(ctx, pxcSecretName, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"root", "xtrabackup", "monitor", "clustercheck", "proxyadmin", "operator"} {
		if len(secret.Data[key]) < 16 {
			t.Fatalf("secret key %s is missing or too short", key)
		}
	}
	object, err := dynamicClient.Resource(pxcGVR).Namespace("ftc-platform").Get(ctx, pxcClusterName, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if object.GetName() != pxcClusterName {
		t.Fatalf("unexpected cluster: %#v", object.Object)
	}
	if err := server.ApplyMySQL(ctx, plan); err == nil || !strings.Contains(err.Error(), "已存在") {
		t.Fatalf("second apply must be rejected, got %v", err)
	}
}

func TestWaitForMySQLReady(t *testing.T) {
	server, dynamicClient := testMySQLServer(t)
	ctx := context.Background()
	if err := server.ApplyMySQL(ctx, DefaultMySQLPlan()); err != nil {
		t.Fatal(err)
	}
	resource := dynamicClient.Resource(pxcGVR).Namespace("ftc-platform")
	object, err := resource.Get(ctx, pxcClusterName, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_ = unstructured.SetNestedField(object.Object, "ready", "status", "state")
	_ = unstructured.SetNestedField(object.Object, int64(3), "status", "pxc", "ready")
	_ = unstructured.SetNestedField(object.Object, int64(3), "status", "haproxy", "ready")
	if _, err := resource.Update(ctx, object, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := server.WaitForMySQL(ctx, pxcClusterName, "ftc-platform", time.Second); err != nil {
		t.Fatal(err)
	}
}

func TestBuildMySQLClusterRejectsInvalidPlan(t *testing.T) {
	plan := DefaultMySQLPlan()
	plan.Replicas = 1
	if _, err := BuildMySQLCluster(plan); err == nil {
		t.Fatal("single replica must be rejected for the HA delivery")
	}
	plan = DefaultMySQLPlan()
	plan.PXCImage = ""
	if _, err := BuildMySQLCluster(plan); err == nil {
		t.Fatal("empty image must be rejected")
	}
}
