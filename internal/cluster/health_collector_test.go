package cluster

import (
	"context"
	"encoding/base64"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type healthCollectorRepository struct {
	mu      sync.Mutex
	records []ClusterRecord
	updates map[string]ManagedCluster
}

func (repository *healthCollectorRepository) Create(_ context.Context, record ClusterRecord) (ManagedCluster, error) {
	return record.ManagedCluster, nil
}
func (repository *healthCollectorRepository) Delete(context.Context, string) error { return nil }
func (repository *healthCollectorRepository) GetRecord(context.Context, string) (ClusterRecord, error) {
	return ClusterRecord{}, newError(CodeClusterNotFound)
}
func (repository *healthCollectorRepository) ListRecords(context.Context) ([]ClusterRecord, error) {
	return repository.records, nil
}
func (repository *healthCollectorRepository) UpdateClusterHealth(_ context.Context, value ManagedCluster) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if repository.updates == nil {
		repository.updates = make(map[string]ManagedCluster)
	}
	repository.updates[value.ID] = value
	return nil
}

func TestHealthCollectorPersistsSuccessAndIsolatesFailure(t *testing.T) {
	cipher, _ := NewAESGCMCipher(base64.StdEncoding.EncodeToString(make([]byte, 32)))
	encrypted, _ := cipher.Encrypt([]byte(`{"ca_data":"Y2E=","token":"token"}`))
	previousCheck := time.Date(2026, 10, 20, 1, 0, 0, 0, time.UTC)
	previousNodes := 7
	repository := &healthCollectorRepository{records: []ClusterRecord{
		{ManagedCluster: ManagedCluster{ID: "healthy", APIServer: "https://healthy.test"}, EncryptedCredentials: encrypted},
		{ManagedCluster: ManagedCluster{ID: "failed", APIServer: "https://failed.test", LastCheckedAt: previousCheck, NodesReady: &previousNodes}, EncryptedCredentials: encrypted},
	}}
	connector := fakeConnector{healthFn: func(_ context.Context, parsed ParsedKubeconfig) (HealthSnapshot, error) {
		if parsed.APIServer == "https://failed.test" {
			return HealthSnapshot{}, errors.New("unreachable")
		}
		return HealthSnapshot{NodesReady: 10, NodesTotal: 10, APILatencyMS: 9}, nil
	}}
	service := NewOnboardingService(connector, cipher, repository)
	service.now = func() time.Time { return time.Date(2026, 10, 20, 2, 0, 0, 0, time.UTC) }
	NewHealthCollector(service, time.Minute, 2, time.Second).CollectOnce(context.Background())

	healthy := repository.updates["healthy"]
	if healthy.ConnectionStatus != "connected" || healthy.NodesReady == nil || *healthy.NodesReady != 10 || !healthy.LastCheckedAt.Equal(service.now()) {
		t.Fatalf("unexpected healthy snapshot: %#v", healthy)
	}
	failed := repository.updates["failed"]
	if failed.ConnectionStatus != "unreachable" || failed.NodesReady == nil || *failed.NodesReady != previousNodes || !failed.LastCheckedAt.Equal(previousCheck) {
		t.Fatalf("failed collection must preserve last successful metrics: %#v", failed)
	}
}

func TestHealthCollectorLimitsConcurrentClusterRequests(t *testing.T) {
	cipher, _ := NewAESGCMCipher(base64.StdEncoding.EncodeToString(make([]byte, 32)))
	encrypted, _ := cipher.Encrypt([]byte(`{"ca_data":"Y2E=","token":"token"}`))
	repository := &healthCollectorRepository{}
	for index := 0; index < 8; index++ {
		repository.records = append(repository.records, ClusterRecord{ManagedCluster: ManagedCluster{ID: string(rune('a' + index)), APIServer: "https://cluster.test"}, EncryptedCredentials: encrypted})
	}
	var active, maximum atomic.Int32
	connector := fakeConnector{healthFn: func(ctx context.Context, _ ParsedKubeconfig) (HealthSnapshot, error) {
		current := active.Add(1)
		defer active.Add(-1)
		for {
			observed := maximum.Load()
			if current <= observed || maximum.CompareAndSwap(observed, current) {
				break
			}
		}
		select {
		case <-ctx.Done():
			return HealthSnapshot{}, ctx.Err()
		case <-time.After(10 * time.Millisecond):
			return HealthSnapshot{}, nil
		}
	}}
	service := NewOnboardingService(connector, cipher, repository)
	NewHealthCollector(service, time.Minute, 3, time.Second).CollectOnce(context.Background())
	if maximum.Load() > 3 {
		t.Fatalf("expected at most 3 concurrent requests, got %d", maximum.Load())
	}
}
