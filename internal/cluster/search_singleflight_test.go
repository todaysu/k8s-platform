package cluster

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// plainCipher is a test double that keeps the cluster binding visible: it
// refuses to open a value sealed for a different cluster, so a test that mixes
// records up fails here instead of silently passing.
type plainCipher struct{}

func (plainCipher) Encrypt(value []byte, clusterID string) (string, error) {
	return aadPrefix + clusterID + ":" + string(value), nil
}

func (plainCipher) Decrypt(value, clusterID string) ([]byte, error) {
	// The prefix must be present before the cluster id is parsed: tests store
	// plaintext credentials that may themselves contain colons.
	rest, ok := strings.CutPrefix(value, aadPrefix)
	if !ok {
		return []byte(value), nil
	}
	bound, payload, ok := strings.Cut(rest, ":")
	if !ok || bound != clusterID {
		return nil, newError(CodeEncryptionFailed)
	}
	return []byte(payload), nil
}

func (plainCipher) NeedsRebind(value string) bool {
	return !strings.HasPrefix(value, aadPrefix)
}

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
