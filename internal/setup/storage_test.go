package setup

import (
	"context"
	"strings"
	"testing"

	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func validVolumes() []LocalVolumeRequest {
	return []LocalVolumeRequest{
		{Node: "node1", Path: "/data1/ftc-platform/mysql", SizeGiB: 200},
		{Node: "node2", Path: "/data2/ftc-platform/mysql", SizeGiB: 200},
		{Node: "node3", Path: "/data3/ftc-platform/mysql", SizeGiB: 200},
	}
}

func TestPlanLocalStorageBuildsStaticPVs(t *testing.T) {
	plan, err := PlanLocalStorage(validVolumes(), "")
	if err != nil {
		t.Fatal(err)
	}
	if plan.ClassName != localStorageClass {
		t.Fatalf("unexpected class name: %s", plan.ClassName)
	}
	if plan.Class.Provisioner != "kubernetes.io/no-provisioner" || plan.Class.VolumeBindingMode == nil || *plan.Class.VolumeBindingMode != storagev1.VolumeBindingWaitForFirstConsumer {
		t.Fatalf("unexpected storage class: %#v", plan.Class)
	}
	if plan.Class.AllowVolumeExpansion == nil || *plan.Class.AllowVolumeExpansion {
		t.Fatal("local static volumes must not advertise expansion")
	}
	if len(plan.PVs) != 3 {
		t.Fatalf("expected 3 PVs, got %d", len(plan.PVs))
	}
	for index, pv := range plan.PVs {
		if pv.Spec.Local == nil || pv.Spec.Local.Path != validVolumes()[index].Path {
			t.Fatalf("unexpected PV path: %#v", pv.Spec.Local)
		}
		if pv.Spec.StorageClassName != localStorageClass {
			t.Fatalf("unexpected PV class: %s", pv.Spec.StorageClassName)
		}
		if pv.Spec.PersistentVolumeReclaimPolicy != "Retain" {
			t.Fatalf("unexpected reclaim policy: %s", pv.Spec.PersistentVolumeReclaimPolicy)
		}
		if pv.Spec.NodeAffinity == nil || len(pv.Spec.NodeAffinity.Required.NodeSelectorTerms) != 1 {
			t.Fatalf("missing node affinity: %#v", pv.Spec.NodeAffinity)
		}
	}
}

func TestPlanLocalStorageRejectsUnsafeInput(t *testing.T) {
	tests := []struct {
		name    string
		volumes []LocalVolumeRequest
		want    string
	}{
		{name: "wrong count", volumes: validVolumes()[:2], want: "恰好 3 个"},
		{name: "duplicate node", volumes: []LocalVolumeRequest{{Node: "node1", Path: "/data1", SizeGiB: 100}, {Node: "node1", Path: "/data2", SizeGiB: 100}, {Node: "node3", Path: "/data3", SizeGiB: 100}}, want: "重复"},
		{name: "root path", volumes: []LocalVolumeRequest{{Node: "node1", Path: "/", SizeGiB: 100}, {Node: "node2", Path: "/data2", SizeGiB: 100}, {Node: "node3", Path: "/data3", SizeGiB: 100}}, want: "根目录"},
		{name: "kubelet path", volumes: []LocalVolumeRequest{{Node: "node1", Path: "/var/lib/kubelet/pods", SizeGiB: 100}, {Node: "node2", Path: "/data2", SizeGiB: 100}, {Node: "node3", Path: "/data3", SizeGiB: 100}}, want: "系统目录"},
		{name: "small volume", volumes: []LocalVolumeRequest{{Node: "node1", Path: "/data1", SizeGiB: 5}, {Node: "node2", Path: "/data2", SizeGiB: 100}, {Node: "node3", Path: "/data3", SizeGiB: 100}}, want: "不能小于"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := PlanLocalStorage(test.volumes, "")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected error containing %q, got %v", test.want, err)
			}
		})
	}
}

func TestApplyLocalStorageCreatesAndReusesObjects(t *testing.T) {
	client := fake.NewSimpleClientset(testNode("node1", "amd64"), testNode("node2", "amd64"), testNode("node3", "amd64"))
	server := &Server{client: client}
	plan, err := PlanLocalStorage(validVolumes(), "")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := server.ApplyLocalStorage(ctx, plan); err != nil {
		t.Fatal(err)
	}
	if _, err := client.StorageV1().StorageClasses().Get(ctx, localStorageClass, metav1.GetOptions{}); err != nil {
		t.Fatal(err)
	}
	list, err := client.CoreV1().PersistentVolumes().List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 3 {
		t.Fatalf("expected 3 PVs, got %d", len(list.Items))
	}
	if err := server.ApplyLocalStorage(ctx, plan); err != nil {
		t.Fatalf("second apply should be idempotent: %v", err)
	}
}

func TestApplyLocalStorageRejectsNodeAndClassConflicts(t *testing.T) {
	plan, err := PlanLocalStorage(validVolumes(), "")
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{client: fake.NewSimpleClientset(testNode("node1", "amd64"), testNode("node2", "amd64"))}
	if err := server.ApplyLocalStorage(context.Background(), plan); err == nil {
		t.Fatal("missing node must be rejected")
	}

	client := fake.NewSimpleClientset(
		testNode("node1", "amd64"), testNode("node2", "amd64"), testNode("node3", "amd64"),
		&storagev1.StorageClass{ObjectMeta: metav1.ObjectMeta{Name: localStorageClass}, Provisioner: "kubernetes.io/aws-ebs"},
	)
	server = &Server{client: client}
	if err := server.ApplyLocalStorage(context.Background(), plan); err == nil {
		t.Fatal("conflicting StorageClass must be rejected")
	}
}
