package setup

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestEnsureTLSRecoversPartialWriteWithoutChangingCA(t *testing.T) {
	ctx := context.Background()
	client := fake.NewSimpleClientset()
	server := &Server{config: Config{Namespace: "ftc-platform"}, client: client}
	first, err := server.EnsureTLS(ctx, platformCASecret, platformTLSCert, "platform.example.test", nil)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := client.CoreV1().Secrets("ftc-platform").Get(ctx, platformCASecret, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.CoreV1().Secrets("ftc-platform").Delete(ctx, platformTLSCert, metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	rebuilt, err := server.EnsureTLS(ctx, platformCASecret, platformTLSCert, "platform.example.test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(first.Data["tls.crt"], rebuilt.Data["tls.crt"]) {
		t.Fatal("expected a new leaf certificate")
	}
	caAfter, err := client.CoreV1().Secrets("ftc-platform").Get(ctx, platformCASecret, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(ca.Data["ca.crt"], caAfter.Data["ca.crt"]) {
		t.Fatal("CA changed after partial write")
	}
	verifyCertificate(t, rebuilt, caAfter, "platform.example.test")
}

func TestEnsureTLSRenewsChangedHostAndIPSANUsingSameCA(t *testing.T) {
	ctx := context.Background()
	client := fake.NewSimpleClientset()
	server := &Server{config: Config{Namespace: "ftc-platform"}, client: client}
	_, err := server.EnsureTLS(ctx, platformCASecret, platformTLSCert, "setup.ftc.internal", []net.IP{net.ParseIP("10.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := server.EnsureTLS(ctx, platformCASecret, platformTLSCert, "setup.ftc.internal", []net.IP{net.ParseIP("10.0.0.2")})
	if err != nil {
		t.Fatal(err)
	}
	ca, err := client.CoreV1().Secrets("ftc-platform").Get(ctx, platformCASecret, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	verifyCertificate(t, updated, ca, "10.0.0.2")
}

func TestBootstrapTLSUsesNodeIPAndExcludesLoopback(t *testing.T) {
	ctx := context.Background()
	client := fake.NewSimpleClientset(
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: platformBootstrapSecret, Namespace: "ftc-platform"}},
		&corev1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: "worker-1"},
			Status: corev1.NodeStatus{Addresses: []corev1.NodeAddress{
				{Type: corev1.NodeInternalIP, Address: "127.0.0.1"},
				{Type: corev1.NodeInternalIP, Address: "10.20.30.40"},
			}},
		},
	)
	server := &Server{config: Config{Namespace: "ftc-platform"}, client: client}
	config, err := server.BootstrapTLS(ctx)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(config.Certificates[0].Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := leaf.VerifyHostname("10.20.30.40"); err != nil {
		t.Fatalf("node IP is missing from setup certificate: %v", err)
	}
	if err := leaf.VerifyHostname("127.0.0.1"); err == nil {
		t.Fatal("loopback address must not be included in setup certificate")
	}
}

func TestBootstrapTLSRejectsClusterWithoutNodeIP(t *testing.T) {
	client := fake.NewSimpleClientset(
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: platformBootstrapSecret, Namespace: "ftc-platform"}},
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "worker-1"}},
	)
	server := &Server{config: Config{Namespace: "ftc-platform"}, client: client}
	if _, err := server.BootstrapTLS(context.Background()); err == nil {
		t.Fatal("expected missing node IP to be rejected")
	}
}

func verifyCertificate(t *testing.T, secret, ca *corev1.Secret, host string) {
	t.Helper()
	pair, err := tls.X509KeyPair(secret.Data["tls.crt"], secret.Data["tls.key"])
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca.Data["ca.crt"]) {
		t.Fatal("CA invalid")
	}
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: roots, DNSName: host}); err != nil {
		t.Fatal(err)
	}
}
