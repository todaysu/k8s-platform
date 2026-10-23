package tlsserver_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"os"
	"path/filepath"
	"testing"
	"time"

	"example.com/k8s-platform/internal/setup"
	"example.com/k8s-platform/internal/tlsserver"
)

func TestReloadKeepsLastValidCertificate(t *testing.T) {
	first, err := setup.GenerateSelfSignedTLS("first.example.test")
	if err != nil {
		t.Fatal(err)
	}
	second, err := setup.GenerateSelfSignedTLS("second.example.test")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certPath, keyPath := filepath.Join(dir, "tls.crt"), filepath.Join(dir, "tls.key")
	write := func(cert, key []byte) {
		t.Helper()
		if err := os.WriteFile(certPath, cert, 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(keyPath, key, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(first.ServerCert, first.ServerKey)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	config, err := tlsserver.FromFiles(ctx, certPath, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	host := func() string {
		pair, err := config.GetCertificate(&tls.ClientHelloInfo{})
		if err != nil {
			t.Fatal(err)
		}
		leaf, err := x509.ParseCertificate(pair.Certificate[0])
		if err != nil {
			t.Fatal(err)
		}
		return leaf.DNSNames[0]
	}
	write(second.ServerCert, []byte("broken key"))
	time.Sleep(5200 * time.Millisecond)
	if got := host(); got != "first.example.test" {
		t.Fatalf("invalid update replaced valid certificate: %s", got)
	}
	write(second.ServerCert, second.ServerKey)
	deadline := time.Now().Add(7 * time.Second)
	for time.Now().Before(deadline) {
		if host() == "second.example.test" {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("valid replacement was not loaded")
}
