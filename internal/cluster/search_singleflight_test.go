package cluster

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type plainCipher struct{}

func (plainCipher) Encrypt(value []byte) (string, error) { return string(value), nil }
func (plainCipher) Decrypt(value string) ([]byte, error) { return []byte(value), nil }

type searchConnector struct {
	Connector
	workloadCalls atomic.Int32
	podCalls      atomic.Int32
}

func (connector *searchConnector) Workloads(context.Context, ParsedKubeconfig) ([]ClusterWorkload, error) {
	connector.workloadCalls.Add(1)
	time.Sleep(20 * time.Millisecond)
	return []ClusterWorkload{}, nil
}

func (connector *searchConnector) Pods(context.Context, ParsedKubeconfig) ([]ClusterPod, error) {
	connector.podCalls.Add(1)
	time.Sleep(20 * time.Millisecond)
	return []ClusterPod{}, nil
}

func TestResourceSearchLoadsEachClusterOnce(t *testing.T) {
	connector := &searchConnector{}
	service := NewOnboardingService(connector, plainCipher{}, nil)
	record := ClusterRecord{ManagedCluster: ManagedCluster{ID: "c1", Name: "c1", APIServer: "https://c1"}, EncryptedCredentials: `{"token":"t"}`}

	var waitGroup sync.WaitGroup
	for index := 0; index < 8; index++ {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			if _, err := service.resourcesForSearch(context.Background(), record); err != nil {
				t.Errorf("search load failed: %v", err)
			}
		}()
	}
	waitGroup.Wait()
	if connector.workloadCalls.Load() != 1 || connector.podCalls.Load() != 1 {
		t.Fatalf("expected one load per cluster, got %d workloads and %d pods", connector.workloadCalls.Load(), connector.podCalls.Load())
	}
	if _, err := service.resourcesForSearch(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	if connector.workloadCalls.Load() != 1 || connector.podCalls.Load() != 1 {
		t.Fatalf("cache miss inside ttl: %d/%d", connector.workloadCalls.Load(), connector.podCalls.Load())
	}
}
