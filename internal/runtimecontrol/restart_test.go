package runtimecontrol

import (
	"context"
	"testing"

	"example.com/k8s-platform/internal/platformsettings"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestRestartPatchesPrimaryDeployment(t *testing.T) {
	client := fake.NewSimpleClientset(&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "ftc-platform-collector", Namespace: "ftc-platform"}})
	restarter := New(client, "ftc-platform")

	status, err := restarter.Restart(context.Background(), platformsettings.SectionCollection)
	if err != nil {
		t.Fatalf("restart collector: %v", err)
	}
	if status.Status != "restarting" || len(status.Targets) != 1 || status.Targets[0] != "ftc-platform-collector" {
		t.Fatalf("unexpected apply status: %#v", status)
	}
	deployment, err := client.AppsV1().Deployments("ftc-platform").Get(context.Background(), "ftc-platform-collector", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get deployment: %v", err)
	}
	if deployment.Spec.Template.Annotations[restartAnnotation] == "" {
		t.Fatal("restart annotation was not written")
	}
}

func TestRestartFallsBackToCombinedDeployment(t *testing.T) {
	client := fake.NewSimpleClientset(&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "ftc-platform", Namespace: "ftc-platform"}})
	restarter := New(client, "ftc-platform")

	status, err := restarter.Restart(context.Background(), platformsettings.SectionSecurity)
	if err != nil {
		t.Fatalf("restart worker fallback: %v", err)
	}
	if len(status.Targets) != 1 || status.Targets[0] != "ftc-platform" {
		t.Fatalf("expected combined deployment fallback, got %#v", status.Targets)
	}
}

func TestRestartSkipsSectionsWithoutExecutor(t *testing.T) {
	restarter := New(fake.NewSimpleClientset(), "ftc-platform")

	status, err := restarter.Restart(context.Background(), platformsettings.SectionJava)
	if err != nil {
		t.Fatalf("skip restart: %v", err)
	}
	if status.Status != "saved" || len(status.Targets) != 0 {
		t.Fatalf("unexpected skip result: %#v", status)
	}
}
