package cluster

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

func TestKubectlArgumentBoundary(t *testing.T) {
	for _, args := range [][]string{{"get", "pods", "-A"}, {"scale", "deployment/web", "-n", "app", "--replicas=2"}, {"rollout", "restart", "deployment/web"}, {"logs", "pod/web", "-f", "--tail=20"}, {"apply", "-f", "-"}, {"version", "--client=true", "-o", "json"}} {
		if err := ValidateKubectlArgs(args, "apiVersion: v1"); err != nil {
			t.Fatalf("valid command rejected: %v %v", args, err)
		}
	}
	for _, args := range [][]string{{"config", "view"}, {"proxy"}, {"get", "pods", "--kubeconfig=/etc/config"}, {"get", "pods", "--server", "https://other"}, {"get", "pods", "--token=secret"}, {"apply", "-f", "/etc/passwd"}, {"apply", "-f", "https://example.test/x"}, {"get", "pods", "-o", "jsonpath-file=/etc/passwd"}, {"create", "secret", "generic", "test", "--from-file=/etc/passwd"}, {"get", "pods", "--as=admin"}, {"unknown-plugin"}} {
		if err := ValidateKubectlArgs(args, ""); err == nil {
			t.Fatalf("unsafe command accepted: %v", args)
		}
	}
}
func TestKubectlClientUsesIsolatedConfig(t *testing.T) {
	if _, err := exec.LookPath("kubectl"); err != nil {
		t.Skip("kubectl unavailable")
	}
	result, err := executeKubectl(context.Background(), ParsedKubeconfig{APIServer: "https://127.0.0.1:1", Token: "test-secret-token"}, []string{"version", "--client=true", "-o", "json"}, "")
	if err != nil || result.ExitCode != 0 || !strings.Contains(result.Output, "clientVersion") || strings.Contains(result.Output, "test-secret-token") {
		t.Fatalf("client execution failed: %#v %v", result, err)
	}
}
