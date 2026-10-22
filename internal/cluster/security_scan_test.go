package cluster

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestKubeBenchImageSelectionUsesNodeArchitecture(t *testing.T) {
	connector := &KubernetesConnector{kubeBenchImage: "registry/kube-bench:multi", kubeBenchImageAMD64: "registry/kube-bench:amd64", kubeBenchImageARM64: "registry/kube-bench:arm64"}
	if got := connector.kubeBenchImageForArch("amd64"); got != "registry/kube-bench:amd64" {
		t.Fatalf("unexpected amd64 image: %s", got)
	}
	if got := connector.kubeBenchImageForArch("arm64"); got != "registry/kube-bench:arm64" {
		t.Fatalf("unexpected arm64 image: %s", got)
	}
	if got := connector.kubeBenchImageForArch("s390x"); got != "registry/kube-bench:multi" {
		t.Fatalf("unexpected fallback image: %s", got)
	}
}

func TestSecurityPodStartupFailureDetectsImagePullBackOff(t *testing.T) {
	pod := &corev1.Pod{Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ImagePullBackOff", Message: "registry unavailable"}}}}}}
	if got := securityPodStartupFailure(pod); !strings.Contains(got, "ImagePullBackOff") || !strings.Contains(got, "registry unavailable") {
		t.Fatalf("unexpected startup failure: %q", got)
	}
}

func TestWaitForSecurityJobTimesOutWhenScanPodDoesNotStart(t *testing.T) {
	job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "scan", Namespace: "kube-system"}}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "scan-pod", Namespace: "kube-system", Labels: map[string]string{"job-name": "scan"}},
		Status: corev1.PodStatus{
			Phase:   corev1.PodPending,
			Reason:  "Unschedulable",
			Message: "0/3 nodes are available",
			ContainerStatuses: []corev1.ContainerStatus{{
				Name:  "kube-bench",
				State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ContainerCreating"}},
			}},
		},
	}
	client := fake.NewSimpleClientset(job, pod)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := waitForSecurityJob(ctx, client, "kube-system", "scan", "node-a", "kube-bench", 1024, 50*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "未能启动") || !strings.Contains(err.Error(), "Unschedulable") || !strings.Contains(err.Error(), "ContainerCreating") {
		t.Fatalf("unexpected startup timeout error: %v", err)
	}
}

func TestParseKubeBenchReportsSkipsVersionDetectionWarning(t *testing.T) {
	output := []byte("Warning: Kubernetes version was not auto-detected because kubectl could not connect to the Kubernetes server.\n" +
		`{"Controls":[{"id":"4","tests":[{"section":"4.1","results":[` +
		`{"test_number":"4.1.1","test_desc":"Ensure file permissions","status":"FAIL","remediation":"chmod 600"},` +
		`{"test_number":"4.1.2","test_desc":"Ensure ownership","status":"PASS"}` +
		`]}]}],"Totals":{"total_pass":1,"total_fail":1}}`)
	report, err := parseKubeBenchReports([]SecurityHostBenchmarkOutput{{Node: "node-a", Data: output}})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Findings) != 2 || report.Summary.Controls != 2 || report.Summary.Passed != 1 || report.Summary.Failed != 1 {
		t.Fatalf("unexpected report: %#v", report)
	}
	if report.Findings[0].ControlID != "CIS-4.1.1" || report.Findings[0].Status != "FAIL" || report.Findings[0].Resources[0] != "Node/node-a" {
		t.Fatalf("unexpected finding: %#v", report.Findings[0])
	}
}

func TestNormalizeSecurityScanModeOnlyAllowsKubeBench(t *testing.T) {
	for _, input := range []string{"", "full", "cis", "host", "kube-bench"} {
		got, err := NormalizeSecurityScanMode(input)
		if err != nil || got != SecurityScanModeFull {
			t.Fatalf("normalize %q: got %q, err=%v", input, got, err)
		}
	}
	for _, input := range []string{"api", "readonly", "kubescape", "unknown"} {
		if _, err := NormalizeSecurityScanMode(input); ErrorCode(err) != CodeSecurityScanModeInvalid {
			t.Fatalf("expected %q to be rejected, got %v", input, err)
		}
	}
}

func TestManualKubeBenchScanPersistsResultAndHistory(t *testing.T) {
	repository := newSecurityTestRepository("cluster-1")
	connector := &securityTestConnector{hostOutputs: kubeBenchOutput("FAIL")}
	service := NewOnboardingService(connector, plainCipher{}, repository)
	service.now = func() time.Time { return time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC) }

	started, err := service.StartSecurityBaselineScan(context.Background(), "cluster-1", SecurityScanModeFull)
	if err != nil {
		t.Fatal(err)
	}
	if started.Status != SecurityScanStatusQueued || started.Scanner != "kube-bench" {
		t.Fatalf("unexpected queued record: %#v", started)
	}
	service.WaitSecurityScans()
	saved, _ := repository.GetSecurityBaseline(context.Background(), "cluster-1")
	if saved == nil || saved.Status != SecurityScanStatusCompleted || saved.Report.Summary.Failed != 1 {
		t.Fatalf("unexpected completed record: %#v", saved)
	}
	if connector.hostCalls.Load() != 1 || len(repository.history) != 1 {
		t.Fatalf("expected one scan and one history row, calls=%d history=%d", connector.hostCalls.Load(), len(repository.history))
	}
}

func TestManualKubeBenchScanRejectsConcurrentRun(t *testing.T) {
	repository := newSecurityTestRepository("cluster-1")
	release := make(chan struct{})
	connector := &securityTestConnector{hostOutputs: kubeBenchOutput("PASS"), release: release}
	service := NewOnboardingService(connector, plainCipher{}, repository)
	if _, err := service.StartSecurityBaselineScan(context.Background(), "cluster-1", SecurityScanModeFull); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for connector.hostCalls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if _, err := service.StartSecurityBaselineScan(context.Background(), "cluster-1", SecurityScanModeFull); ErrorCode(err) != CodeSecurityScanInProgress {
		t.Fatalf("expected scan in progress, got %v", err)
	}
	close(release)
	service.WaitSecurityScans()
}

func TestManualKubeBenchScanPersistsFailure(t *testing.T) {
	repository := newSecurityTestRepository("cluster-1")
	// The connector error stands in for a client-go failure, whose raw text
	// carries the API Server address, request path and ServiceAccount name.
	service := NewOnboardingService(&securityTestConnector{err: errors.New(`Get "https://prod-apiserver:6443/api/v1/nodes?limit=500": dial tcp 10.1.2.3:6443: connect: connection refused`)}, plainCipher{}, repository)
	if _, err := service.StartSecurityBaselineScan(context.Background(), "cluster-1", SecurityScanModeFull); err != nil {
		t.Fatal(err)
	}
	service.WaitSecurityScans()
	saved, _ := repository.GetSecurityBaseline(context.Background(), "cluster-1")
	if saved == nil || saved.Status != SecurityScanStatusFailed || len(repository.history) != 1 {
		t.Fatalf("expected persisted failure, got %#v", saved)
	}
	// The failure is still reported and still actionable, but the raw upstream
	// text must not reach the persisted record or any API response.
	if !strings.Contains(saved.Error, "无法连接集群 API Server") {
		t.Fatalf("expected a report-safe reason, got %q", saved.Error)
	}
	for _, leaked := range []string{"prod-apiserver", "10.1.2.3", "api/v1/nodes", "connection refused"} {
		if strings.Contains(saved.Error, leaked) {
			t.Fatalf("persisted error leaked %q: %q", leaked, saved.Error)
		}
	}
}

func TestSecurityHistoryComparesLatestSuccessfulScans(t *testing.T) {
	repository := newSecurityTestRepository("cluster-1")
	previous := SecurityBaselineRecord{ID: 1, ClusterID: "cluster-1", Status: SecurityScanStatusCompleted, Report: SecurityBaselineReport{Findings: []SecurityBaselineFinding{{ControlID: "CIS-1.1", Status: "FAIL", Title: "one"}, {ControlID: "CIS-1.2", Status: "PASS", Title: "two"}}}}
	current := SecurityBaselineRecord{ID: 2, ClusterID: "cluster-1", Status: SecurityScanStatusCompleted, Report: SecurityBaselineReport{Findings: []SecurityBaselineFinding{{ControlID: "CIS-1.1", Status: "PASS", Title: "one"}, {ControlID: "CIS-1.2", Status: "WARN", Title: "two"}}}}
	repository.history = []SecurityBaselineRecord{current, previous}
	service := NewOnboardingService(&securityTestConnector{}, plainCipher{}, repository)
	history, err := service.GetSecurityBaselineHistory(context.Background(), "cluster-1", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(history.Comparison.Resolved) != 1 || len(history.Comparison.NewRisks) != 1 || history.Comparison.CurrentScanID != 2 {
		t.Fatalf("unexpected comparison: %#v", history.Comparison)
	}
}

func TestRecoverInterruptedSecurityScanMarksFailedAndCleans(t *testing.T) {
	repository := newSecurityTestRepository("cluster-1")
	repository.scans["cluster-1"] = SecurityBaselineRecord{ClusterID: "cluster-1", Status: SecurityScanStatusRunning, StartedAt: time.Now().Add(-time.Minute)}
	connector := &securityTestConnector{cleanup: make(chan struct{}, 1)}
	service := NewOnboardingService(connector, plainCipher{}, repository)
	service.RecoverInterruptedSecurityScans(context.Background())
	saved, _ := repository.GetSecurityBaseline(context.Background(), "cluster-1")
	if saved == nil || saved.Status != SecurityScanStatusFailed || !strings.Contains(saved.Error, "平台重启") {
		t.Fatalf("expected interrupted scan failure: %#v", saved)
	}
	select {
	case <-connector.cleanup:
	case <-time.After(time.Second):
		t.Fatal("expected orphan cleanup")
	}
}

func kubeBenchOutput(status string) []SecurityHostBenchmarkOutput {
	return []SecurityHostBenchmarkOutput{{Node: "worker-1", Data: []byte(`{"Controls":[{"groups":[{"checks":[{"test_number":"4.1.1","test_desc":"Kubelet file permissions","status":"` + status + `","remediation":"chmod 600"}]}]}]}`)}}
}

type securityTestConnector struct {
	fakeConnector
	hostOutputs []SecurityHostBenchmarkOutput
	err         error
	release     chan struct{}
	hostCalls   atomic.Int32
	cleanup     chan struct{}
}

func (connector *securityTestConnector) RunSecurityHostBenchmark(context.Context, ParsedKubeconfig) ([]SecurityHostBenchmarkOutput, error) {
	connector.hostCalls.Add(1)
	if connector.release != nil {
		<-connector.release
	}
	return connector.hostOutputs, connector.err
}

func (connector *securityTestConnector) CleanupSecurityScanResources(context.Context, ParsedKubeconfig) error {
	if connector.cleanup != nil {
		connector.cleanup <- struct{}{}
	}
	return nil
}

type securityTestRepository struct {
	cluster   ClusterRecord
	mu        sync.Mutex
	scans     map[string]SecurityBaselineRecord
	history   []SecurityBaselineRecord
	whitelist []SecurityWhitelistEntry
}

func newSecurityTestRepository(clusterID string) *securityTestRepository {
	return &securityTestRepository{cluster: ClusterRecord{ManagedCluster: ManagedCluster{ID: clusterID, Name: "test-cluster"}, EncryptedCredentials: `{"ca_data":"Y2E=","token":"token"}`}, scans: make(map[string]SecurityBaselineRecord)}
}

func (repository *securityTestRepository) Create(context.Context, ClusterRecord) (ManagedCluster, error) {
	return repository.cluster.ManagedCluster, nil
}
func (repository *securityTestRepository) Delete(context.Context, string) error { return nil }
func (repository *securityTestRepository) GetRecord(context.Context, string) (ClusterRecord, error) {
	return repository.cluster, nil
}
func (repository *securityTestRepository) ListRecords(context.Context) ([]ClusterRecord, error) {
	return []ClusterRecord{repository.cluster}, nil
}
func (repository *securityTestRepository) SaveSecurityBaseline(_ context.Context, value SecurityBaselineRecord) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.scans[value.ClusterID] = value
	return nil
}
func (repository *securityTestRepository) AppendSecurityBaselineHistory(_ context.Context, value SecurityBaselineRecord) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	value.ID = int64(len(repository.history) + 1)
	repository.history = append([]SecurityBaselineRecord{value}, repository.history...)
	return nil
}
func (repository *securityTestRepository) GetSecurityBaseline(_ context.Context, id string) (*SecurityBaselineRecord, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	value, ok := repository.scans[id]
	if !ok {
		return nil, nil
	}
	return &value, nil
}
func (repository *securityTestRepository) ListSecurityBaselines(context.Context) (map[string]SecurityBaselineRecord, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	values := make(map[string]SecurityBaselineRecord, len(repository.scans))
	for key, value := range repository.scans {
		values[key] = value
	}
	return values, nil
}
func (repository *securityTestRepository) ListSecurityBaselineHistory(context.Context, string, int) ([]SecurityBaselineRecord, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	return append([]SecurityBaselineRecord(nil), repository.history...), nil
}
func (repository *securityTestRepository) ListSecurityWhitelist(context.Context, string) ([]SecurityWhitelistEntry, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	return append([]SecurityWhitelistEntry(nil), repository.whitelist...), nil
}
func (repository *securityTestRepository) AddSecurityWhitelist(_ context.Context, value SecurityWhitelistEntry) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.whitelist = append(repository.whitelist, value)
	return nil
}
func (repository *securityTestRepository) DeleteSecurityWhitelist(_ context.Context, clusterID, controlID string) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	for index, value := range repository.whitelist {
		if value.ClusterID == clusterID && value.ControlID == controlID {
			repository.whitelist = append(repository.whitelist[:index], repository.whitelist[index+1:]...)
			break
		}
	}
	return nil
}

func TestKubeBenchArgsUsesSelectedBenchmark(t *testing.T) {
	args := kubeBenchArgs(WithSecurityBenchmark(context.Background(), "cis-1.24"))
	if len(args) != 4 || args[0] != "run" || args[1] != "--json" || args[2] != "--benchmark" || args[3] != "cis-1.24" {
		t.Fatalf("unexpected args: %#v", args)
	}
	if auto := kubeBenchArgs(context.Background()); len(auto) != 2 {
		t.Fatalf("auto-detect should not add --benchmark: %#v", auto)
	}
}

func TestNormalizeSecurityBenchmark(t *testing.T) {
	for _, value := range []string{"cis-1.24", "cis-1.23", "cis-1.20", "cis-1.12"} {
		if got, err := NormalizeSecurityBenchmark(value); err != nil || got != value {
			t.Fatalf("normalize %q: got %q, err=%v", value, got, err)
		}
	}
	if got, err := NormalizeSecurityBenchmark(""); err != nil || got != "" {
		t.Fatalf("empty benchmark should auto-detect: %q %v", got, err)
	}
	for _, value := range []string{"cis-1.11", "cis-1.6", "eks-1.8.0", "cis-9.99"} {
		if _, err := NormalizeSecurityBenchmark(value); ErrorCode(err) != CodeSecurityBenchmarkInvalid {
			t.Fatalf("expected %q to be rejected, got %v", value, err)
		}
	}
}

func TestSupportedSecurityBenchmarksOnlyExposeCIS12AndLater(t *testing.T) {
	profiles := SupportedSecurityBenchmarks()
	if len(profiles) != 4 {
		t.Fatalf("expected four CIS benchmark versions, got %#v", profiles)
	}
	want := []string{"cis-1.24", "cis-1.23", "cis-1.20", "cis-1.12"}
	for i, profile := range profiles {
		if profile.ID != want[i] || profile.CISVersion == "" {
			t.Fatalf("unexpected profile %d: %#v", i, profile)
		}
	}
}

func TestApplySecurityWhitelistRemovesItemFromScore(t *testing.T) {
	report := SecurityBaselineReport{Findings: []SecurityBaselineFinding{
		{ControlID: "CIS-1.1.1", Status: "PASS"},
		{ControlID: "CIS-1.1.2", Status: "FAIL"},
		{ControlID: "CIS-1.1.3", Status: "WARN"},
	}}
	applySecurityWhitelist(&report, []SecurityWhitelistEntry{{ControlID: "CIS-1.1.2", Reason: "accepted"}})
	if report.Findings[1].Status != "WHITELISTED" || report.Findings[1].WhitelistReason != "accepted" {
		t.Fatalf("unexpected finding: %#v", report.Findings[1])
	}
	if report.Summary.Whitelisted != 1 || report.Summary.Failed != 0 || report.Summary.Passed != 2 || report.Summary.Score != 66 {
		t.Fatalf("unexpected summary: %#v", report.Summary)
	}
}

func TestCompareSecurityReportsMarksRemediationResult(t *testing.T) {
	previous := SecurityBaselineRecord{ID: 1, Report: SecurityBaselineReport{Findings: []SecurityBaselineFinding{
		{ControlID: "CIS-1.1.1", Status: "FAIL"},
		{ControlID: "CIS-1.1.2", Status: "PASS"},
		{ControlID: "CIS-1.1.3", Status: "WARN"},
	}}}
	current := SecurityBaselineRecord{ID: 2, Report: SecurityBaselineReport{Findings: []SecurityBaselineFinding{
		{ControlID: "CIS-1.1.1", Status: "PASS"},
		{ControlID: "CIS-1.1.2", Status: "FAIL"},
		{ControlID: "CIS-1.1.3", Status: "WARN"},
	}}}
	result := compareSecurityReports(previous, current)
	byID := map[string]SecurityBaselineRemediation{}
	for _, item := range result.Remediation {
		byID[item.ControlID] = item
	}
	if byID["CIS-1.1.1"].Result != "remediated" || byID["CIS-1.1.2"].Result != "new_risk" || byID["CIS-1.1.3"].Result != "not_remediated" {
		t.Fatalf("unexpected remediation: %#v", result.Remediation)
	}
}

func TestFleetSecurityBaselineAppliesWhitelistOnRead(t *testing.T) {
	repository := newSecurityTestRepository("cluster-1")
	repository.scans["cluster-1"] = SecurityBaselineRecord{
		ClusterID: "cluster-1", Status: SecurityScanStatusCompleted, Scanner: "kube-bench", Framework: "cis",
		Report: SecurityBaselineReport{Findings: []SecurityBaselineFinding{{ControlID: "CIS-1.1.1", Status: "FAIL", Title: "test"}}},
	}
	repository.whitelist = []SecurityWhitelistEntry{{ClusterID: "cluster-1", ControlID: "CIS-1.1.1", Reason: "accepted"}}
	service := NewOnboardingService(&securityTestConnector{}, plainCipher{}, repository)
	fleet, err := service.GetFleetSecurityBaselines(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(fleet.Clusters) != 1 || fleet.Clusters[0].Scan == nil || fleet.Clusters[0].Scan.Report.Findings[0].Status != "WHITELISTED" {
		t.Fatalf("whitelist not applied: %#v", fleet)
	}
}

func TestSecurityWhitelistValidation(t *testing.T) {
	repository := newSecurityTestRepository("cluster-1")
	service := NewOnboardingService(&securityTestConnector{}, plainCipher{}, repository)
	if _, err := service.AddSecurityWhitelist(context.Background(), "cluster-1", "not-a-control", "reason"); ErrorCode(err) != CodeSecurityWhitelistInvalid {
		t.Fatalf("expected invalid control error, got %v", err)
	}
	if _, err := service.AddSecurityWhitelist(context.Background(), "cluster-1", "CIS-1.1.1", ""); ErrorCode(err) != CodeSecurityWhitelistInvalid {
		t.Fatalf("expected missing reason error, got %v", err)
	}
	entry, err := service.AddSecurityWhitelist(context.Background(), "cluster-1", "CIS-1.1.1", "accepted for test")
	if err != nil || entry.ControlID != "CIS-1.1.1" {
		t.Fatalf("unexpected whitelist entry: %#v %v", entry, err)
	}
}
