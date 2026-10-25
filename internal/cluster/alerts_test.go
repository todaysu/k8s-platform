package cluster

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestDiscoverAlertmanagerServiceUsesInternalProxy(t *testing.T) {
	services := []corev1.Service{{ObjectMeta: metav1.ObjectMeta{Name: "alertmanager", Namespace: "monitoring"}, Spec: corev1.ServiceSpec{Ports: []corev1.ServicePort{{Name: "web", Port: 9093}}}}}
	result := discoverAlertmanagerServices(services)
	if len(result) != 1 || result[0].proxyName != "http:alertmanager:web" {
		t.Fatalf("services = %+v", result)
	}
}
