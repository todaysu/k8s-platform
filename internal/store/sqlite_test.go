package store

import (
	"context"
	"testing"
	"time"

	"example.com/k8s-platform/internal/cluster"
)

func TestSQLiteCreatesClusterAndRejectsDuplicates(t *testing.T) {
	store, err := OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	record := cluster.ClusterRecord{
		ManagedCluster: cluster.ManagedCluster{
			ID: "cluster-1", Name: "prd-01", APIServer: "https://cluster.test",
			Environment: "PRD", ClusterType: "G01", TechnologyStack: "TCS",
			ContextName: "ctx", KubernetesVersion: "v1.32.4",
			AuthenticationType: "客户端证书", ConnectionStatus: "connected",
			CreatedAt: now, LastCheckedAt: now,
		},
		EncryptedCredentials: "encrypted",
		Fingerprint:          "fingerprint-1",
	}
	created, err := store.Create(context.Background(), record)
	if err != nil {
		t.Fatal(err)
	}
	if created.ID != "cluster-1" {
		t.Fatalf("unexpected cluster: %#v", created)
	}
	listed, err := store.ListRecords(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].Environment != "PRD" || listed[0].ClusterType != "G01" || listed[0].TechnologyStack != "TCS" {
		t.Fatalf("unexpected cluster list: %#v", listed)
	}
	if err := store.UpdateClusterClassification(context.Background(), "cluster-1", "OCP", "OCP"); err != nil {
		t.Fatal(err)
	}
	updated, err := store.GetRecord(context.Background(), "cluster-1")
	if err != nil || updated.ClusterType != "OCP" || updated.TechnologyStack != "OCP" {
		t.Fatalf("unexpected updated classification: %#v, %v", updated.ManagedCluster, err)
	}

	record.ID = "cluster-2"
	record.Fingerprint = "fingerprint-2"
	if _, err := store.Create(context.Background(), record); cluster.ErrorCode(err) != cluster.CodeNameDuplicate {
		t.Fatalf("expected name duplicate, got %v", err)
	}
	record.Name = "prd-02"
	record.Fingerprint = "fingerprint-1"
	if _, err := store.Create(context.Background(), record); cluster.ErrorCode(err) != cluster.CodeClusterDuplicate {
		t.Fatalf("expected cluster duplicate, got %v", err)
	}
}

func TestSQLiteDeletesClusterAndEncryptedCredentials(t *testing.T) {
	store, err := OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	record := cluster.ClusterRecord{
		ManagedCluster: cluster.ManagedCluster{
			ID: "cluster-1", Name: "prd-01", Environment: "PRD", TechnologyStack: "CMBK8S", APIServer: "https://cluster.test",
			ContextName: "ctx", KubernetesVersion: "v1.32.4", AuthenticationType: "客户端证书",
			ConnectionStatus: "connected", CreatedAt: now, LastCheckedAt: now,
		},
		EncryptedCredentials: "encrypted-secret",
		Fingerprint:          "fingerprint-1",
	}
	if _, err := store.Create(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(context.Background(), "cluster-1"); err != nil {
		t.Fatal(err)
	}
	listed, err := store.ListRecords(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 0 {
		t.Fatalf("expected cluster record to be deleted, got %#v", listed)
	}
	if err := store.Delete(context.Background(), "cluster-1"); cluster.ErrorCode(err) != cluster.CodeClusterNotFound {
		t.Fatalf("expected cluster not found, got %v", err)
	}
}

func TestSQLiteGetsClusterRecordByID(t *testing.T) {
	store, err := OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	record := cluster.ClusterRecord{
		ManagedCluster: cluster.ManagedCluster{
			ID: "cluster-1", Name: "prd-01", Environment: "PRD", TechnologyStack: "CMBK8S", APIServer: "https://cluster.test",
			ContextName: "ctx", KubernetesVersion: "v1.32.4", AuthenticationType: "客户端证书",
			ConnectionStatus: "connected", CreatedAt: now, LastCheckedAt: now,
		},
		EncryptedCredentials: "encrypted-secret",
		Fingerprint:          "fingerprint-1",
	}
	if _, err := store.Create(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetRecord(context.Background(), "cluster-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "prd-01" || got.TechnologyStack != "CMBK8S" || got.EncryptedCredentials != "encrypted-secret" {
		t.Fatalf("unexpected record: %#v", got)
	}
	if _, err := store.GetRecord(context.Background(), "missing"); cluster.ErrorCode(err) != cluster.CodeClusterNotFound {
		t.Fatalf("expected cluster not found, got %v", err)
	}
}

func TestSQLiteStoresImmutableCapacityBaselineVersions(t *testing.T) {
	store, err := OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	_, err = store.Create(context.Background(), cluster.ClusterRecord{ManagedCluster: cluster.ManagedCluster{ID: "cluster-1", Name: "prd-01", Environment: "PRD", TechnologyStack: "TCS", APIServer: "https://cluster.test", ContextName: "ctx", KubernetesVersion: "v1.32.4", AuthenticationType: "token", ConnectionStatus: "connected", CreatedAt: now, LastCheckedAt: now}, EncryptedCredentials: "encrypted", Fingerprint: "fingerprint"})
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.CreateCapacityBaseline(context.Background(), cluster.CapacityBaseline{ClusterID: "cluster-1", CapturedAt: now.Add(-time.Hour), SystemNamespaces: []string{"kube-system", "cmb-monitoring"}, WorkerNodes: 3, CPUAllocatableMilli: 24000, MemoryAllocatable: 64 << 30, PlatformCPURequests: 2000})
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.CreateCapacityBaseline(context.Background(), cluster.CapacityBaseline{ClusterID: "cluster-1", CapturedAt: now, SystemNamespaces: []string{"kube-system", "cmb-monitoring"}, WorkerNodes: 4, CPUAllocatableMilli: 32000, MemoryAllocatable: 96 << 30, PlatformCPURequests: 2500})
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == second.ID {
		t.Fatalf("expected immutable versions, got identical ID %d", first.ID)
	}
	latest, err := store.LatestCapacityBaseline(context.Background(), "cluster-1")
	if err != nil {
		t.Fatal(err)
	}
	if latest == nil || latest.ID != second.ID || latest.WorkerNodes != 4 || len(latest.SystemNamespaces) != 2 {
		t.Fatalf("unexpected latest baseline: %#v", latest)
	}
}

func TestSQLiteStoresCapacityClassificationObservationsAndDecisions(t *testing.T) {
	store, err := OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Date(2026, 10, 18, 1, 0, 0, 0, time.UTC)

	first, err := store.CreateCapacityClassification(context.Background(), cluster.CapacityClassification{
		CreatedAt: now, BusinessNamespaces: []string{"business"}, CommonPlatformNamespaces: []string{"kube-system"},
		StackPlatformNamespaces: map[string][]string{"OCP": {"openshift-monitoring"}}, Note: "initial",
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.CreateCapacityClassification(context.Background(), cluster.CapacityClassification{
		CreatedAt: now.Add(time.Minute), BusinessNamespaces: []string{"business", "orders"}, CommonPlatformNamespaces: []string{"kube-system"},
		StackPlatformNamespaces: map[string][]string{"OCP": {"openshift-monitoring"}}, Note: "add orders",
	})
	if err != nil {
		t.Fatal(err)
	}
	latest, err := store.LatestCapacityClassification(context.Background())
	if err != nil || latest == nil || latest.ID != second.ID || first.ID == second.ID || len(latest.BusinessNamespaces) != 2 {
		t.Fatalf("unexpected latest classification: %#v, err=%v", latest, err)
	}

	if err := store.ObserveNamespaces(context.Background(), []cluster.NamespaceObservation{{TechnologyStack: "OCP", Namespace: "orders", FirstSeenAt: now, LastSeenAt: now}}); err != nil {
		t.Fatal(err)
	}
	if err := store.ObserveNamespaces(context.Background(), []cluster.NamespaceObservation{{TechnologyStack: "OCP", Namespace: "orders", FirstSeenAt: now.Add(time.Hour), LastSeenAt: now.Add(time.Hour)}}); err != nil {
		t.Fatal(err)
	}
	observations, err := store.ListNamespaceObservations(context.Background())
	if err != nil || len(observations) != 1 || !observations[0].FirstSeenAt.Equal(now) || !observations[0].LastSeenAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("unexpected observations: %#v, err=%v", observations, err)
	}

	decision, err := store.CreateCapacityBaselineDecision(context.Background(), cluster.CapacityBaselineDecision{ClusterID: "cluster-1", ClassificationVersionID: second.ID, Decision: "keep", Reason: "temporary component", CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	gotDecision, err := store.LatestCapacityBaselineDecision(context.Background(), "cluster-1", second.ID)
	if err != nil || gotDecision == nil || gotDecision.ID != decision.ID || gotDecision.Reason != "temporary component" {
		t.Fatalf("unexpected decision: %#v, err=%v", gotDecision, err)
	}
}

func TestSQLitePersistsLatestClusterHealthSnapshot(t *testing.T) {
	store, err := OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Date(2026, 10, 20, 2, 0, 0, 0, time.UTC)
	_, err = store.Create(context.Background(), cluster.ClusterRecord{
		ManagedCluster:       cluster.ManagedCluster{ID: "health-1", Name: "stp-g01-01", Environment: "stp", ClusterType: "G01", TechnologyStack: "TCS", APIServer: "https://cluster.test", ContextName: "ctx", KubernetesVersion: "v1.32.4", AuthenticationType: "token", ConnectionStatus: "connected", CreatedAt: now, LastCheckedAt: now},
		EncryptedCredentials: "encrypted", Fingerprint: "health-fingerprint",
	})
	if err != nil {
		t.Fatal(err)
	}
	nodesReady, nodesTotal, instancesReady, instancesTotal := 199, 200, 3990, 4000
	workerCores, latency := int64(6400), int64(18)
	cpu, memory := 41.2, 63.8
	checkedAt := now.Add(time.Minute)
	if err := store.UpdateClusterHealth(context.Background(), cluster.ManagedCluster{
		ID: "health-1", ConnectionStatus: "connected", LastCheckedAt: checkedAt,
		NodesReady: &nodesReady, NodesTotal: &nodesTotal, InstancesReady: &instancesReady, InstancesTotal: &instancesTotal,
		WorkerCores: &workerCores, APILatencyMS: &latency, CPUUtilization: &cpu, MemoryUtilization: &memory, MetricsAvailable: true,
	}); err != nil {
		t.Fatal(err)
	}
	record, err := store.GetRecord(context.Background(), "health-1")
	if err != nil {
		t.Fatal(err)
	}
	if record.NodesReady == nil || *record.NodesReady != nodesReady || record.WorkerCores == nil || *record.WorkerCores != workerCores || !record.MetricsAvailable || !record.LastCheckedAt.Equal(checkedAt) {
		t.Fatalf("unexpected stored health snapshot: %#v", record.ManagedCluster)
	}
	items, err := store.ListRecords(context.Background())
	if err != nil || len(items) != 1 || items[0].CPUUtilization == nil || *items[0].CPUUtilization != cpu {
		t.Fatalf("unexpected listed health snapshot: %#v, err=%v", items, err)
	}
}

func TestSQLitePersistsFleetCapacitySnapshotAndFailureState(t *testing.T) {
	store, err := OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Date(2026, 10, 20, 3, 0, 0, 0, time.UTC)
	_, err = store.Create(context.Background(), cluster.ClusterRecord{ManagedCluster: cluster.ManagedCluster{ID: "capacity-1", Name: "capacity-1", Environment: "stp", ClusterType: "G01", TechnologyStack: "TCS", APIServer: "https://cluster.test", ContextName: "ctx", KubernetesVersion: "v1.32", AuthenticationType: "token", ConnectionStatus: "connected", CreatedAt: now, LastCheckedAt: now}, EncryptedCredentials: "encrypted", Fingerprint: "capacity-fingerprint"})
	if err != nil {
		t.Fatal(err)
	}
	item := cluster.FleetCapacityItem{ClusterID: "capacity-1", ClusterName: "capacity-1", Capacity: cluster.ClusterCapacity{WorkerNodes: 200, CPUAllocatableMilli: 640000}}
	if err := store.SaveFleetCapacitySuccess(context.Background(), item, now); err != nil {
		t.Fatal(err)
	}
	failedAt := now.Add(time.Minute)
	if err := store.SaveFleetCapacityFailure(context.Background(), cluster.FleetCapacityFailure{ClusterID: "capacity-1", Code: cluster.CodeConnectionTimeout}, failedAt); err != nil {
		t.Fatal(err)
	}
	fleet, err := store.ListFleetCapacitySnapshots(context.Background())
	if err != nil || len(fleet.Items) != 1 || fleet.Items[0].Capacity.WorkerNodes != 200 || len(fleet.Failures) != 1 || fleet.Failures[0].Code != cluster.CodeConnectionTimeout || !fleet.CollectedAt.Equal(failedAt) {
		t.Fatalf("unexpected fleet capacity cache: %#v err=%v", fleet, err)
	}
}
