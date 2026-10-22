package cluster

import (
	"context"
	"encoding/base64"
	"sync"
	"testing"
	"time"
)

type capacityCollectorRepository struct {
	mu       sync.Mutex
	records  []ClusterRecord
	items    []FleetCapacityItem
	failures []FleetCapacityFailure
}

func (repository *capacityCollectorRepository) Create(_ context.Context, record ClusterRecord) (ManagedCluster, error) {
	return record.ManagedCluster, nil
}
func (repository *capacityCollectorRepository) Delete(context.Context, string) error { return nil }
func (repository *capacityCollectorRepository) GetRecord(context.Context, string) (ClusterRecord, error) {
	return ClusterRecord{}, newError(CodeClusterNotFound)
}
func (repository *capacityCollectorRepository) ListRecords(context.Context) ([]ClusterRecord, error) {
	return repository.records, nil
}
func (repository *capacityCollectorRepository) SaveFleetCapacitySuccess(_ context.Context, item FleetCapacityItem, _ time.Time) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.items = append(repository.items, item)
	return nil
}
func (repository *capacityCollectorRepository) SaveFleetCapacityFailure(_ context.Context, item FleetCapacityFailure, _ time.Time) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.failures = append(repository.failures, item)
	return nil
}
func (repository *capacityCollectorRepository) ListFleetCapacitySnapshots(context.Context) (FleetCapacity, error) {
	return FleetCapacity{Items: repository.items, Failures: repository.failures}, nil
}

func TestCapacityCollectorPersistsSnapshotAndFleetReadsCache(t *testing.T) {
	cipher, _ := NewAESGCMCipher(base64.StdEncoding.EncodeToString(make([]byte, 32)))
	encrypted, _ := cipher.Encrypt([]byte(`{"ca_data":"Y2E=","token":"token"}`), "cluster-1")
	repository := &capacityCollectorRepository{records: []ClusterRecord{{ManagedCluster: ManagedCluster{ID: "cluster-1", Name: "stp-1", Environment: "stp", ClusterType: "G01", TechnologyStack: "TCS"}, EncryptedCredentials: encrypted}}}
	service := NewOnboardingService(fakeConnector{capacity: ClusterCapacity{WorkerNodes: 200, CPUAllocatableMilli: 640000}}, cipher, repository)
	NewCapacityCollector(service, time.Minute, 1, time.Second).CollectOnce(context.Background())
	got, err := service.GetFleetCapacity(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Items) != 1 || got.Items[0].Capacity.WorkerNodes != 200 || got.Items[0].ClusterName != "stp-1" {
		t.Fatalf("unexpected cached fleet capacity: %#v", got)
	}
}
