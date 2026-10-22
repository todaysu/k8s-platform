package setup

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func runtimePlan() RuntimePlan {
	plan := DefaultRuntimePlan()
	plan.PlatformImage = "registry.local/ftc-platform:0.1.0"
	return plan
}

func TestBuildRuntimeResources(t *testing.T) {
	config := BuildRuntimeConfigMap(runtimePlan())
	if config.Namespace != "ftc-platform" || config.Data["SETUP_MODE"] != "false" || config.Data["TRUST_PROXY_HEADERS"] != "true" {
		t.Fatalf("unexpected runtime config: %#v", config.Data)
	}
	deployments, err := BuildRuntimeDeployments(runtimePlan())
	if err != nil {
		t.Fatal(err)
	}
	if len(deployments) != 3 {
		t.Fatalf("expected API/collector/worker, got %d", len(deployments))
	}
	api, collector, worker := deployments[0], deployments[1], deployments[2]
	if api.Name != "ftc-platform" || api.Spec.Replicas == nil || *api.Spec.Replicas != 2 {
		t.Fatalf("unexpected API deployment: %#v", api)
	}
	if collector.Name != "ftc-platform-collector" || worker.Name != "ftc-platform-worker" {
		t.Fatalf("unexpected role deployments: %s %s", collector.Name, worker.Name)
	}
	apiContainer := api.Spec.Template.Spec.Containers[0]
	if apiContainer.ReadinessProbe.HTTPGet.Path != "/api/v1/health" {
		t.Fatalf("unexpected API probe: %s", apiContainer.ReadinessProbe.HTTPGet.Path)
	}
	collectorContainer := collector.Spec.Template.Spec.Containers[0]
	if collectorContainer.ReadinessProbe.HTTPGet.Path != "/readyz" || collectorContainer.LivenessProbe.HTTPGet.Path != "/healthz" {
		t.Fatalf("unexpected collector probes: %#v", collectorContainer.ReadinessProbe)
	}
	if collector.Spec.Template.Spec.Affinity == nil {
		t.Fatal("collector is missing anti-affinity")
	}
	service := BuildRuntimeService("ftc-platform")
	if service.Spec.Selector["app.kubernetes.io/name"] != "ftc-platform" {
		t.Fatalf("unexpected service selector: %#v", service.Spec.Selector)
	}
}

func TestBuildRuntimeDeploymentsRequiresImage(t *testing.T) {
	plan := DefaultRuntimePlan()
	if _, err := BuildRuntimeDeployments(plan); err == nil {
		t.Fatal("empty image must be rejected")
	}
}

func TestApplyRuntimeCreatesAndUpdates(t *testing.T) {
	client := fake.NewSimpleClientset(&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "ftc-platform"}})
	server := &Server{config: Config{Namespace: "ftc-platform"}, client: client}
	ctx := context.Background()
	plan := runtimePlan()
	if err := server.ApplyRuntime(ctx, plan); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ftc-platform", "ftc-platform-collector", "ftc-platform-worker"} {
		if _, err := client.AppsV1().Deployments("ftc-platform").Get(ctx, name, metav1.GetOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := client.CoreV1().Services("ftc-platform").Get(ctx, "ftc-platform", metav1.GetOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := server.ApplyRuntime(ctx, plan); err != nil {
		t.Fatalf("second apply should update without error: %v", err)
	}
}
