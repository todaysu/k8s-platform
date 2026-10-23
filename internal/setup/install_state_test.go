package setup

import (
	"context"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestInstallStateSurvivesRestartWithoutCredentials(t *testing.T) {
	client := fake.NewSimpleClientset()
	server := &Server{config: Config{Namespace: "ftc-platform"}, client: client}
	server.install = newInstallStatus()
	server.install.State = "failed"
	server.install.Error = "数据库不可达"
	server.install.Domain = "platform.example.test"
	if err := server.saveInstallState(context.Background()); err != nil {
		t.Fatal(err)
	}
	stored, err := client.CoreV1().ConfigMaps("ftc-platform").Get(context.Background(), installStateName, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stored.Data["status"], "setup-token") || strings.Contains(stored.Data["status"], "password") {
		t.Fatal("credentials leaked to progress ConfigMap")
	}
	restarted := &Server{config: Config{Namespace: "ftc-platform"}, client: client}
	if err := restarted.loadInstallState(context.Background()); err != nil {
		t.Fatal(err)
	}
	if restarted.installSnapshot().State != "failed" || restarted.installSnapshot().Domain != "platform.example.test" {
		t.Fatalf("status not restored: %#v", restarted.installSnapshot())
	}
}

func TestInterruptedInstallCanResumeAfterLeaseEnds(t *testing.T) {
	client := fake.NewSimpleClientset()
	server := &Server{config: Config{Namespace: "ftc-platform"}, client: client, install: newInstallStatus()}
	if err := server.saveInstallState(context.Background()); err != nil {
		t.Fatal(err)
	}
	restarted := &Server{config: Config{Namespace: "ftc-platform"}, client: client}
	if err := restarted.loadInstallState(context.Background()); err != nil {
		t.Fatal(err)
	}
	if status := restarted.installSnapshot(); status.State != "failed" {
		t.Fatalf("lost lease should make installation resumable: %#v", status)
	}
}
