package cluster

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"math/big"
	"testing"
	"time"
)

func TestParseKubeconfigUsesCurrentContext(t *testing.T) {
	ca := testCertificatePEM(t)
	data := []byte(fmt.Sprintf(`apiVersion: v1
kind: Config
current-context: selected
clusters:
- name: ignored-cluster
  cluster:
    server: https://ignored.example.test
    certificate-authority-data: %s
- name: selected-cluster
  cluster:
    server: https://selected.example.test/
    certificate-authority-data: %s
users:
- name: ignored-user
  user:
    token: ignored-token
- name: selected-user
  user:
    token: selected-token
contexts:
- name: ignored
  context:
    cluster: ignored-cluster
    user: ignored-user
- name: selected
  context:
    cluster: selected-cluster
    user: selected-user
`, base64.StdEncoding.EncodeToString(ca), base64.StdEncoding.EncodeToString(ca)))

	parsed, err := ParseKubeconfig(data)
	if err != nil {
		t.Fatalf("ParseKubeconfig returned error: %v", err)
	}
	if parsed.ContextName != "selected" {
		t.Fatalf("expected selected context, got %q", parsed.ContextName)
	}
	if parsed.APIServer != "https://selected.example.test" {
		t.Fatalf("expected normalized selected server, got %q", parsed.APIServer)
	}
	if parsed.Token != "selected-token" {
		t.Fatal("expected token from selected context")
	}
}

func TestParseKubeconfigAcceptsEmbeddedClientCertificate(t *testing.T) {
	ca := base64.StdEncoding.EncodeToString(testCertificatePEM(t))
	clientCertificate, clientKey := testClientCredentialPEM(t)
	data := []byte(fmt.Sprintf(`apiVersion: v1
kind: Config
current-context: admin
clusters:
- name: cluster
  cluster:
    server: https://cluster.example.test
    certificate-authority-data: %s
users:
- name: admin
  user:
    client-certificate-data: %s
    client-key-data: %s
contexts:
- name: admin
  context:
    cluster: cluster
    user: admin
`, ca, base64.StdEncoding.EncodeToString(clientCertificate), base64.StdEncoding.EncodeToString(clientKey)))

	parsed, err := ParseKubeconfig(data)
	if err != nil {
		t.Fatalf("ParseKubeconfig returned error: %v", err)
	}
	if parsed.AuthenticationType != "客户端证书" {
		t.Fatalf("expected client certificate authentication, got %q", parsed.AuthenticationType)
	}
	if len(parsed.ClientCertificateData) == 0 || len(parsed.ClientKeyData) == 0 {
		t.Fatal("expected embedded client certificate credentials")
	}
	if parsed.Token != "" {
		t.Fatal("did not expect a token for client certificate authentication")
	}
}

func TestParseKubeconfigRejectsUnsupportedInputs(t *testing.T) {
	ca := base64.StdEncoding.EncodeToString(testCertificatePEM(t))
	tests := []struct {
		name        string
		current     string
		server      string
		clusterAuth string
		userAuth    string
		wantCode    string
	}{
		{name: "missing current context", server: "https://cluster.test", clusterAuth: "certificate-authority-data: " + ca, userAuth: "token: token", wantCode: CodeCurrentContextMissing},
		{name: "http server", current: "ctx", server: "http://cluster.test", clusterAuth: "certificate-authority-data: " + ca, userAuth: "token: token", wantCode: CodeHTTPSRequired},
		{name: "missing inline ca", current: "ctx", server: "https://cluster.test", clusterAuth: "certificate-authority: /tmp/ca.crt", userAuth: "token: token", wantCode: CodeInlineCARequired},
		{name: "missing credentials", current: "ctx", server: "https://cluster.test", clusterAuth: "certificate-authority-data: " + ca, userAuth: "{}", wantCode: CodeAuthenticationRequired},
		{name: "exec auth", current: "ctx", server: "https://cluster.test", clusterAuth: "certificate-authority-data: " + ca, userAuth: "exec:\n      command: kubectl", wantCode: CodeExecAuthUnsupported},
		{name: "client certificate paths", current: "ctx", server: "https://cluster.test", clusterAuth: "certificate-authority-data: " + ca, userAuth: "client-certificate: /tmp/client.crt\n    client-key: /tmp/client.key", wantCode: CodeInlineClientCertRequired},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			data := []byte(fmt.Sprintf(`apiVersion: v1
kind: Config
current-context: %s
clusters:
- name: cluster
  cluster:
    server: %s
    %s
users:
- name: user
  user:
    %s
contexts:
- name: ctx
  context:
    cluster: cluster
    user: user
`, test.current, test.server, test.clusterAuth, test.userAuth))

			_, err := ParseKubeconfig(data)
			if got := ErrorCode(err); got != test.wantCode {
				t.Fatalf("expected code %s, got %s (error %v)", test.wantCode, got, err)
			}
		})
	}
}

func testClientCredentialPEM(t *testing.T) ([]byte, []byte) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "test-client"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certificatePEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	return certificatePEM, keyPEM
}

func testCertificatePEM(t *testing.T) []byte {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}
