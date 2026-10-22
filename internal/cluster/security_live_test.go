package cluster

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestSecurityLiveKind(t *testing.T) {
	if os.Getenv("FTC_SECURITY_LIVE_TEST") != "1" {
		t.Skip("local opt-in")
	}
	image := os.Getenv("FTC_SECURITY_IMAGE")
	if image == "" {
		image = "ftc-kind-registry:5000/ftc/kube-bench:v0.16.0-arm64"
	}
	raw, err := exec.Command("/usr/local/bin/kubectl", "--context", "kind-k8s-test", "config", "view", "--raw", "--minify").Output()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseKubeconfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	connector := NewKubernetesConnector(8 * time.Minute)
	connector.ConfigureHostSecurityScanner(image, "", "", "kube-system", "", 1)
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	outputs, err := connector.RunSecurityHostBenchmark(ctx, parsed)
	if err != nil {
		t.Fatalf("scan failed: %v", err)
	}
	report, err := parseKubeBenchReports(outputs)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	_ = os.MkdirAll("../../data/kind-test", 0o755)
	payload, _ := json.MarshalIndent(report, "", "  ")
	_ = os.WriteFile("../../data/kind-test/security-live.json", payload, 0o600)
	t.Logf("image=%s nodes=%d controls=%d passed=%d failed=%d warnings=%d score=%d", image, len(outputs), report.Summary.Controls, report.Summary.Passed, report.Summary.Failed, report.Summary.Warnings, report.Summary.Score)
}
