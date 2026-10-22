package setup

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestValidateInternalDomain(t *testing.T) {
	valid := []string{"ftc-platform.cmb.local", "FTC-Platform.CMB.Local", "ftc.internal"}
	for _, value := range valid {
		if _, err := ValidateInternalDomain(value); err != nil {
			t.Fatalf("valid domain %q rejected: %v", value, err)
		}
	}
	invalid := []string{"", "10.0.0.1", "*.cmb.local", "ftc_platform.cmb.local", "ftc-platform", "ftc..local", "-ftc.local", "ftc-.local"}
	for _, value := range invalid {
		if _, err := ValidateInternalDomain(value); err == nil {
			t.Fatalf("invalid domain %q accepted", value)
		}
	}
}

func TestGenerateSelfSignedTLS(t *testing.T) {
	bundle, err := GenerateSelfSignedTLS("ftc-platform.cmb.local")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tls.X509KeyPair(bundle.ServerCert, bundle.ServerKey); err != nil {
		t.Fatalf("server key pair is invalid: %v", err)
	}
	leaf := parsePEMCertificate(t, bundle.ServerCert)
	if !containsName(leaf.DNSNames, "ftc-platform.cmb.local") {
		t.Fatalf("server certificate lacks the domain: %v", leaf.DNSNames)
	}
	ca := parsePEMCertificate(t, bundle.CACert)
	if !ca.IsCA {
		t.Fatal("CA certificate is not marked as CA")
	}
	if err := leaf.CheckSignatureFrom(ca); err != nil {
		t.Fatalf("server certificate is not signed by the CA: %v", err)
	}
	if block, _ := pem.Decode(bundle.CAKey); block == nil {
		t.Fatal("CA key is not PEM encoded")
	}
}

func TestBuildIngress(t *testing.T) {
	plan := IngressPlan{Namespace: "ftc-platform", Host: "ftc-platform.cmb.local", IngressClass: "nginx"}
	ingress, err := BuildIngress(plan)
	if err != nil {
		t.Fatal(err)
	}
	if ingress.Spec.IngressClassName == nil || *ingress.Spec.IngressClassName != "nginx" {
		t.Fatalf("unexpected class: %#v", ingress.Spec.IngressClassName)
	}
	if len(ingress.Spec.Rules) != 1 || ingress.Spec.Rules[0].Host != "ftc-platform.cmb.local" {
		t.Fatalf("unexpected rules: %#v", ingress.Spec.Rules)
	}
	if len(ingress.Spec.TLS) != 1 || ingress.Spec.TLS[0].SecretName != platformTLSCert {
		t.Fatalf("unexpected TLS: %#v", ingress.Spec.TLS)
	}
	if ingress.Annotations["nginx.ingress.kubernetes.io/proxy-read-timeout"] != "3600" {
		t.Fatalf("missing WebSocket timeout annotation: %#v", ingress.Annotations)
	}
	if _, err := BuildIngress(IngressPlan{Host: "ftc-platform.cmb.local"}); err == nil {
		t.Fatal("empty IngressClass must be rejected")
	}
}

func TestApplyIngressCreatesAndReuses(t *testing.T) {
	client := fake.NewSimpleClientset(&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "ftc-platform"}})
	server := &Server{config: Config{Namespace: "ftc-platform"}, client: client}
	ctx := context.Background()
	plan := IngressPlan{Namespace: "ftc-platform", Host: "ftc-platform.cmb.local", IngressClass: "nginx"}

	if err := server.ApplyIngress(ctx, plan); err != nil {
		t.Fatal(err)
	}
	if _, err := client.CoreV1().Secrets("ftc-platform").Get(ctx, platformTLSCert, metav1.GetOptions{}); err != nil {
		t.Fatal(err)
	}
	ca, err := client.CoreV1().Secrets("ftc-platform").Get(ctx, platformCASecret, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(ca.Data["ca.crt"]) == 0 || len(ca.Data["ca.key"]) == 0 {
		t.Fatal("CA secret is incomplete")
	}
	if _, err := client.NetworkingV1().Ingresses("ftc-platform").Get(ctx, "ftc-platform", metav1.GetOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := server.ApplyIngress(ctx, plan); err != nil {
		t.Fatalf("second apply should be idempotent: %v", err)
	}

	other := plan
	other.Host = "other.cmb.local"
	if err := server.ApplyIngress(ctx, other); err == nil || !strings.Contains(err.Error(), "不包含域名") {
		t.Fatalf("changing the host must not silently reuse the certificate, got %v", err)
	}
}

func parsePEMCertificate(t *testing.T, data []byte) *x509.Certificate {
	t.Helper()
	block, _ := pem.Decode(data)
	if block == nil {
		t.Fatal("certificate is not PEM")
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return certificate
}
