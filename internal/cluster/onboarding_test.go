package cluster

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
	"time"
)

type capturingRepository struct {
	deletedID      string
	record         ClusterRecord
	baseline       *CapacityBaseline
	classification *CapacityClassification
	observations   []NamespaceObservation
	decision       *CapacityBaselineDecision
	health         *ManagedCluster
}

func (repository *capturingRepository) UpdateClusterHealth(_ context.Context, value ManagedCluster) error {
	repository.health = &value
	repository.record.ManagedCluster = value
	return nil
}

func (repository *capturingRepository) CreateCapacityClassification(_ context.Context, value CapacityClassification) (CapacityClassification, error) {
	value.ID = 2
	repository.classification = &value
	return value, nil
}
func (repository *capturingRepository) LatestCapacityClassification(context.Context) (*CapacityClassification, error) {
	return repository.classification, nil
}
func (repository *capturingRepository) ObserveNamespaces(_ context.Context, values []NamespaceObservation) error {
	repository.observations = append(repository.observations, values...)
	return nil
}
func (repository *capturingRepository) ListNamespaceObservations(context.Context) ([]NamespaceObservation, error) {
	return repository.observations, nil
}
func (repository *capturingRepository) CreateCapacityBaselineDecision(_ context.Context, value CapacityBaselineDecision) (CapacityBaselineDecision, error) {
	value.ID = 1
	repository.decision = &value
	return value, nil
}
func (repository *capturingRepository) LatestCapacityBaselineDecision(context.Context, string, int64) (*CapacityBaselineDecision, error) {
	return repository.decision, nil
}

func (repository *capturingRepository) Create(_ context.Context, record ClusterRecord) (ManagedCluster, error) {
	repository.record = record
	return record.ManagedCluster, nil
}

func (repository *capturingRepository) ListRecords(context.Context) ([]ClusterRecord, error) {
	return []ClusterRecord{repository.record}, nil
}

func (repository *capturingRepository) Delete(_ context.Context, id string) error {
	repository.deletedID = id
	return nil
}

func (repository *capturingRepository) GetRecord(context.Context, string) (ClusterRecord, error) {
	return repository.record, nil
}

func (repository *capturingRepository) CreateCapacityBaseline(_ context.Context, baseline CapacityBaseline) (CapacityBaseline, error) {
	baseline.ID = 1
	repository.baseline = &baseline
	return baseline, nil
}

func (repository *capturingRepository) LatestCapacityBaseline(context.Context, string) (*CapacityBaseline, error) {
	return repository.baseline, nil
}

func TestCapacityComparesPlatformRequestsWithDeliveryBaseline(t *testing.T) {
	cipher, err := NewAESGCMCipher(base64.StdEncoding.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := cipher.Encrypt([]byte(`{"ca_data":"Y2E=","token":"token"}`))
	if err != nil {
		t.Fatal(err)
	}
	repository := &capturingRepository{record: ClusterRecord{ManagedCluster: ManagedCluster{ID: "cluster-1", APIServer: "https://cluster.test"}, EncryptedCredentials: encrypted}, baseline: &CapacityBaseline{ClusterID: "cluster-1", CPUAllocatableMilli: 16000, MemoryAllocatable: 64 << 30, PlatformCPURequests: 2000, PlatformMemoryRequest: 4 << 30}}
	service := NewOnboardingService(fakeConnector{capacity: ClusterCapacity{CPUAllocatableMilli: 16000, MemoryAllocatable: 64 << 30, Platform: CapacityValues{CPURequestsMilli: 2400, MemoryRequests: 4 << 30}}}, cipher, repository)

	got, err := service.GetCapacity(context.Background(), "cluster-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Comparison.Status != "drift" || got.Comparison.PlatformCPURequestChange != 20 {
		t.Fatalf("unexpected comparison: %#v", got.Comparison)
	}
	created, err := service.LockCapacityBaseline(context.Background(), "cluster-1")
	if err != nil {
		t.Fatal(err)
	}
	if created.ID != 1 || created.PlatformCPURequests != 2400 {
		t.Fatalf("unexpected baseline: %#v", created)
	}
}

func TestFleetCapacityReturnsSuccessfulSnapshotsAndIsolatesFailures(t *testing.T) {
	cipher, err := NewAESGCMCipher(base64.StdEncoding.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := cipher.Encrypt([]byte(`{"ca_data":"Y2E=","token":"token"}`))
	if err != nil {
		t.Fatal(err)
	}
	repository := &multiRecordRepository{records: []ClusterRecord{
		{ManagedCluster: ManagedCluster{ID: "cluster-a", Name: "prd-a", Environment: "PRD", TechnologyStack: "TCS"}, EncryptedCredentials: encrypted},
		{ManagedCluster: ManagedCluster{ID: "cluster-b", Name: "dev-b", Environment: "DEV", TechnologyStack: "OCP"}, EncryptedCredentials: "broken"},
	}}
	service := NewOnboardingService(fakeConnector{capacity: ClusterCapacity{CPUAllocatableMilli: 8000}}, cipher, repository)
	service.now = func() time.Time { return time.Date(2026, 10, 17, 8, 0, 0, 0, time.UTC) }

	got, err := service.GetFleetCapacity(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Items) != 1 || got.Items[0].ClusterID != "cluster-a" || got.Items[0].Capacity.CPUAllocatableMilli != 8000 {
		t.Fatalf("unexpected capacity items: %#v", got.Items)
	}
	if len(got.Failures) != 1 || got.Failures[0].ClusterID != "cluster-b" {
		t.Fatalf("unexpected capacity failures: %#v", got.Failures)
	}
}

func TestCapacityClassificationOnlyUsesGlobalBusinessNamespaces(t *testing.T) {
	repository := &capturingRepository{record: ClusterRecord{ManagedCluster: ManagedCluster{ID: "cluster-1", TechnologyStack: "OCP"}}}
	service := NewOnboardingService(fakeConnector{}, nil, repository)
	for _, invalid := range []string{"Bad_Name", "app/team", "-app", strings.Repeat("a", 64)} {
		if _, err := service.SaveCapacityClassification(context.Background(), CapacityClassification{BusinessNamespaces: []string{invalid}}); ErrorCode(err) != CodeCapacityRulesInvalid {
			t.Fatalf("expected invalid namespace %q, got %v", invalid, err)
		}
	}
	created, err := service.SaveCapacityClassification(context.Background(), CapacityClassification{BusinessNamespaces: []string{" orders ", "orders", "future-app"}, CommonPlatformNamespaces: []string{"orders"}, StackPlatformNamespaces: map[string][]string{"OCP": {"orders"}}})
	if err != nil || created.ID != 2 || !sameStrings(created.BusinessNamespaces, []string{"future-app", "orders"}) || len(created.CommonPlatformNamespaces) != 0 || len(created.StackPlatformNamespaces) != 0 {
		t.Fatalf("unexpected classification: %#v err=%v", created, err)
	}
	for _, stack := range []string{"TCS", "CMBK8S", "OCP", "未标记"} {
		rule := capacityRuleForStack(created, stack)
		if !sameStrings(rule.BusinessNamespaces, created.BusinessNamespaces) || len(rule.PlatformNamespaces) != 0 {
			t.Fatalf("stack must not affect rule: %#v", rule)
		}
	}
	if _, err := service.KeepCapacityBaseline(context.Background(), "cluster-1", "  "); ErrorCode(err) != CodeBaselineReasonRequired {
		t.Fatalf("expected required reason, got %v", err)
	}
	decision, err := service.KeepCapacityBaseline(context.Background(), "cluster-1", "temporary add-on")
	if err != nil || decision.Reason != "temporary add-on" {
		t.Fatalf("unexpected decision: %#v, err=%v", decision, err)
	}
}

func TestCapacityReportsNewEmptyPlatformNamespaceWithoutChangingBaseline(t *testing.T) {
	cipher, _ := NewAESGCMCipher(base64.StdEncoding.EncodeToString(make([]byte, 32)))
	encrypted, _ := cipher.Encrypt([]byte(`{"ca_data":"Y2E=","token":"token"}`))
	delivered := time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC)
	baseline := &CapacityBaseline{ID: 1, ClusterID: "cluster-1", CapturedAt: delivered, SystemNamespaces: []string{"kube-system"}, CPUAllocatableMilli: 8000}
	repository := &capturingRepository{record: ClusterRecord{ManagedCluster: ManagedCluster{ID: "cluster-1"}, EncryptedCredentials: encrypted}, baseline: baseline}
	service := NewOnboardingService(fakeConnector{capacity: ClusterCapacity{CPUAllocatableMilli: 8000, SystemNamespaces: []string{"empty-addon", "kube-system", "legacy-platform"}, Namespaces: []NamespaceCapacity{{Namespace: "empty-addon", Class: "platform", CreatedAt: delivered.Add(time.Hour)}, {Namespace: "legacy-platform", Class: "platform", CreatedAt: delivered.Add(-time.Hour)}}}}, cipher, repository)
	snapshot, err := service.GetCapacity(context.Background(), "cluster-1")
	if err != nil {
		t.Fatal(err)
	}
	if !sameStrings(snapshot.NewPlatformNamespaces, []string{"empty-addon"}) || snapshot.BaselineReview.Status != "pending" {
		t.Fatalf("missing new platform namespace: %#v", snapshot)
	}
	if repository.baseline != baseline || !sameStrings(baseline.SystemNamespaces, []string{"kube-system"}) {
		t.Fatal("delivery baseline changed automatically")
	}
	repository.baseline = nil
	snapshot, err = service.GetCapacity(context.Background(), "cluster-1")
	if err != nil || len(snapshot.NewPlatformNamespaces) != 0 {
		t.Fatalf("without baseline no post-delivery additions should be reported: %#v %v", snapshot, err)
	}
}

func TestCapacityDiscoveryMergesNamespacesAcrossTechnologyStacks(t *testing.T) {
	cipher, _ := NewAESGCMCipher(base64.StdEncoding.EncodeToString(make([]byte, 32)))
	encrypted, _ := cipher.Encrypt([]byte(`{"ca_data":"Y2E=","token":"token"}`))
	repo := &multiRecordRepository{records: []ClusterRecord{
		{ManagedCluster: ManagedCluster{ID: "a", TechnologyStack: "TCS"}, EncryptedCredentials: encrypted},
		{ManagedCluster: ManagedCluster{ID: "b", TechnologyStack: "OCP"}, EncryptedCredentials: encrypted},
	}}
	service := NewOnboardingService(fakeConnector{capacity: ClusterCapacity{Namespaces: []NamespaceCapacity{{Namespace: "myapp", Class: "business", Pods: 1, CapacityValues: CapacityValues{CPURequestsMilli: 10}}}}}, cipher, repo)
	overview, err := service.GetCapacityClassification(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(overview.Discovered) != 1 || overview.Discovered[0].ClusterCount != 2 || overview.Discovered[0].Pods != 2 || overview.Discovered[0].CPURequestsMilli != 20 {
		t.Fatalf("expected one global namespace: %#v", overview)
	}
}

type multiRecordRepository struct{ records []ClusterRecord }

func (repository *multiRecordRepository) Create(_ context.Context, record ClusterRecord) (ManagedCluster, error) {
	return record.ManagedCluster, nil
}
func (repository *multiRecordRepository) ListRecords(context.Context) ([]ClusterRecord, error) {
	return repository.records, nil
}
func (repository *multiRecordRepository) Delete(context.Context, string) error { return nil }
func (repository *multiRecordRepository) GetRecord(_ context.Context, id string) (ClusterRecord, error) {
	for _, record := range repository.records {
		if record.ID == id {
			return record, nil
		}
	}
	return ClusterRecord{}, newError(CodeClusterNotFound)
}

func TestDeleteRemovesManagedClusterRecord(t *testing.T) {
	repository := &capturingRepository{}
	service := NewOnboardingService(fakeConnector{}, nil, repository)

	if err := service.Delete(context.Background(), "cluster-1"); err != nil {
		t.Fatal(err)
	}
	if repository.deletedID != "cluster-1" {
		t.Fatalf("expected cluster-1 to be deleted, got %q", repository.deletedID)
	}
}

func TestOnboardingValidatesTechnologyStack(t *testing.T) {
	service := NewOnboardingService(fakeConnector{}, nil, &capturingRepository{})

	if _, err := service.Onboard(context.Background(), "cluster-1", "PRD", "", "TCS", nil); ErrorCode(err) != CodeClusterTypeRequired {
		t.Fatalf("expected cluster type required, got %v", err)
	}
	if _, err := service.Onboard(context.Background(), "cluster-1", "PRD", "G01", "", nil); ErrorCode(err) != CodeTechnologyStackRequired {
		t.Fatalf("expected technology stack required, got %v", err)
	}
	if _, err := service.Onboard(context.Background(), "cluster-1", "PRD", "G01", "RANCHER", nil); ErrorCode(err) != CodeTechnologyStackInvalid {
		t.Fatalf("expected invalid technology stack, got %v", err)
	}
	if _, err := service.Onboard(context.Background(), "cluster-1", "PRD", "OCP", "TCS", nil); ErrorCode(err) != CodeClusterTypeMismatch {
		t.Fatalf("expected cluster type mismatch, got %v", err)
	}
}

func TestListReturnsStoredClusterHealthWithoutLiveCollection(t *testing.T) {
	cipher, err := NewAESGCMCipher(base64.StdEncoding.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := cipher.Encrypt([]byte(`{"ca_data":"Y2E=","token":"token"}`))
	if err != nil {
		t.Fatal(err)
	}
	nodesReady, latency := 2, int64(12)
	repository := &capturingRepository{record: ClusterRecord{
		ManagedCluster:       ManagedCluster{ID: "cluster-1", APIServer: "https://cluster.test", ConnectionStatus: "connected", NodesReady: &nodesReady, APILatencyMS: &latency},
		EncryptedCredentials: encrypted,
	}}
	service := NewOnboardingService(fakeConnector{err: fmt.Errorf("live connection must not be used")}, cipher, repository)
	service.now = func() time.Time { return time.Date(2026, 10, 16, 4, 0, 0, 0, time.UTC) }

	managed, err := service.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(managed) != 1 || managed[0].NodesReady == nil || *managed[0].NodesReady != 2 {
		t.Fatalf("unexpected managed clusters: %#v", managed)
	}
	if managed[0].APILatencyMS == nil || *managed[0].APILatencyMS != 12 {
		t.Fatalf("unexpected API latency: %#v", managed[0].APILatencyMS)
	}
}

func TestGetNodesUsesStoredClusterCredentials(t *testing.T) {
	cipher, err := NewAESGCMCipher(base64.StdEncoding.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := cipher.Encrypt([]byte(`{"ca_data":"Y2E=","token":"token"}`))
	if err != nil {
		t.Fatal(err)
	}
	repository := &capturingRepository{record: ClusterRecord{
		ManagedCluster:       ManagedCluster{ID: "cluster-1", APIServer: "https://cluster.test"},
		EncryptedCredentials: encrypted,
	}}
	want := []ClusterNode{{Name: "worker-1", Status: "Ready", Roles: []string{"worker"}}}
	service := NewOnboardingService(fakeConnector{nodes: want}, cipher, repository)

	got, err := service.GetNodes(context.Background(), "cluster-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != want[0].Name {
		t.Fatalf("unexpected nodes: %#v", got)
	}
}

func TestGetWorkloadsUsesStoredClusterCredentials(t *testing.T) {
	cipher, err := NewAESGCMCipher(base64.StdEncoding.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := cipher.Encrypt([]byte(`{"ca_data":"Y2E=","token":"token"}`))
	if err != nil {
		t.Fatal(err)
	}
	repository := &capturingRepository{record: ClusterRecord{
		ManagedCluster:       ManagedCluster{ID: "cluster-1", APIServer: "https://cluster.test"},
		EncryptedCredentials: encrypted,
	}}
	want := []ClusterWorkload{{Kind: "Deployment", Namespace: "app", Name: "web"}}
	service := NewOnboardingService(fakeConnector{workloads: want}, cipher, repository)

	got, err := service.GetWorkloads(context.Background(), "cluster-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "web" {
		t.Fatalf("unexpected workloads: %#v", got)
	}
}

func TestOnboardingEncryptsAndPersistsCredentials(t *testing.T) {
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
	cipher, err := NewAESGCMCipher(base64.StdEncoding.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	repository := &capturingRepository{}
	service := NewOnboardingService(fakeConnector{version: "v1.32.4"}, cipher, repository)
	service.now = func() time.Time { return time.Date(2026, 10, 16, 1, 2, 3, 0, time.UTC) }

	managed, err := service.Onboard(context.Background(), " prd-01 ", " PRD ", " ocp ", " ocp ", kubeconfig)
	if err != nil {
		t.Fatalf("Onboard returned error: %v", err)
	}
	if managed.Name != "prd-01" || managed.Environment != "PRD" || managed.ClusterType != "OCP" || managed.TechnologyStack != "OCP" || managed.ConnectionStatus != "connected" {
		t.Fatalf("unexpected managed cluster: %#v", managed)
	}
	if strings.Contains(repository.record.EncryptedCredentials, "super-secret-token") {
		t.Fatal("stored credentials contain plaintext token")
	}
	decrypted, err := cipher.Decrypt(repository.record.EncryptedCredentials)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(decrypted), "super-secret-token") {
		t.Fatal("encrypted credential envelope is missing token")
	}
}
