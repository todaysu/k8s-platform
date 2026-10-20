package cluster

import (
	"context"
	"encoding/base64"
	"fmt"
	"testing"
)

func TestValidationServiceReturnsSafePreview(t *testing.T) {
	ca := base64.StdEncoding.EncodeToString(testCertificatePEM(t))
	kubeconfig := []byte(fmt.Sprintf(`apiVersion: v1
kind: Config
current-context: ctx
clusters:
- name: cluster
  cluster:
    server: https://cluster.example.test
    certificate-authority-data: %s
users:
- name: user
  user:
    token: super-secret-token
contexts:
- name: ctx
  context:
    cluster: cluster
    user: user
`, ca))

	preview, err := NewValidationService(fakeConnector{version: "v1.32.4"}).Validate(context.Background(), " prd-01 ", kubeconfig)
	if err != nil {
		t.Fatalf("Validate returned error: %v", err)
	}
	if preview.Name != "prd-01" || preview.ContextName != "ctx" || preview.KubernetesVersion != "v1.32.4" {
		t.Fatalf("unexpected preview: %#v", preview)
	}
	if preview.AuthenticationType != "Bearer Token" {
		t.Fatalf("expected Bearer Token preview, got %q", preview.AuthenticationType)
	}
}
