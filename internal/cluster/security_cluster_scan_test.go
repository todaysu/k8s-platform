package cluster

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestScanClusterSecurityFindsWorkloadRBACAndExposureRisks(t *testing.T) {
	privileged := true
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "api-0", Namespace: "app"},
		Spec: corev1.PodSpec{
			HostNetwork: true,
			Containers: []corev1.Container{{
				Name: "api", Image: "registry.example.com/api:latest",
				SecurityContext: &corev1.SecurityContext{Privileged: &privileged, AllowPrivilegeEscalation: &privileged},
			}},
		},
	}
	namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "app"}}
	role := &rbacv1.ClusterRole{ObjectMeta: metav1.ObjectMeta{Name: "custom-admin"}, Rules: []rbacv1.PolicyRule{{Verbs: []string{"*"}, Resources: []string{"*"}, APIGroups: []string{"*"}}}}
	service := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "app"}, Spec: corev1.ServiceSpec{Type: corev1.ServiceTypeNodePort}}
	client := fake.NewSimpleClientset(pod, namespace, role, service)

	report, err := scanClusterSecurity(context.Background(), client)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]SecurityBaselineFinding{}
	for _, finding := range report.Findings {
		byID[finding.ControlID] = finding
	}
	for _, id := range []string{"CLUSTER-001", "CLUSTER-002", "CLUSTER-007", "CLUSTER-008", "CLUSTER-009", "CLUSTER-010"} {
		finding, ok := byID[id]
		if !ok || finding.Status == "PASS" || finding.Affected == 0 {
			t.Fatalf("%s should have findings: %#v", id, finding)
		}
	}
	if report.Pods != 1 || report.Namespaces != 1 {
		t.Fatalf("unexpected counts: %#v", report)
	}
}
