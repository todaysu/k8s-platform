package setup

import (
	"context"
	"testing"

	"k8s.io/client-go/kubernetes/fake"
)

func TestInstallLeasePreventsConcurrentControllers(t *testing.T) {
	client := fake.NewSimpleClientset()
	first := &Server{config: Config{Namespace: "ftc-platform"}, client: client}
	second := &Server{config: Config{Namespace: "ftc-platform"}, client: client}
	release, _, err := first.acquireInstallLease(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := second.acquireInstallLease(context.Background()); err == nil {
		t.Fatal("second controller acquired active installation")
	}
	release()
	cleanup, _, err := second.acquireInstallLease(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	cleanup()
}
