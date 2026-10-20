package cluster_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"example.com/k8s-platform/internal/cluster"
	"example.com/k8s-platform/internal/store"
)

type tenantCipher struct{}

func (tenantCipher) Encrypt(value []byte) (string, error) { return string(value), nil }
func (tenantCipher) Decrypt(value string) ([]byte, error) { return []byte(value), nil }

type ownerConnector struct {
	cluster.Connector
	calls atomic.Int32
}

func (connector *ownerConnector) EventWorkloadOwners(context.Context, cluster.ParsedKubeconfig) (map[string]cluster.EventResourceReference, error) {
	connector.calls.Add(1)
	return map[string]cluster.EventResourceReference{
		"pod-1": {Kind: "Deployment", Name: "web", UID: "deploy-1"},
	}, nil
}

func TestFleetResourcesDoNotFanOutToEveryCluster(t *testing.T) {
	ctx := context.Background()
	database, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	now := time.Now().UTC().Truncate(time.Second)
	for _, id := range []string{"c1", "c2"} {
		if _, err = database.Create(ctx, cluster.ClusterRecord{ManagedCluster: cluster.ManagedCluster{ID: id, Name: id, APIServer: "https://" + id, CreatedAt: now, LastCheckedAt: now}, EncryptedCredentials: `{"token":"t"}`, Fingerprint: id}); err != nil {
			t.Fatal(err)
		}
	}
	if err = database.UpsertEvents(ctx, []cluster.ClusterEvent{{
		ClusterID: "c1", EventUID: "e1", Type: "Warning", Reason: "FailedScheduling", RegardingKind: "Pod",
		RegardingNamespace: "app", RegardingName: "pod-1", RegardingUID: "pod-1", Message: "no nodes",
		FirstObservedAt: now, LastObservedAt: now,
	}}); err != nil {
		t.Fatal(err)
	}
	connector := &ownerConnector{}
	service := cluster.NewOnboardingService(connector, tenantCipher{}, database)
	service.SetEventRepository(database)

	fleet := cluster.FleetEventQuery{View: "resources", IncludeNormal: true, EventQuery: cluster.EventQuery{Since: now.Add(-time.Hour), Until: now.Add(time.Hour), Limit: 25}}
	result, err := service.GetFleetEvents(ctx, fleet)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Objects) != 1 || result.Objects[0].Kind != "Pod" {
		t.Fatalf("expected ungrouped fleet object, got %#v", result.Objects)
	}
	if connector.calls.Load() != 0 {
		t.Fatalf("fleet-wide resources must not call cluster APIs, got %d calls", connector.calls.Load())
	}

	fleet.ClusterID = "c1"
	result, err = service.GetFleetEvents(ctx, fleet)
	if err != nil {
		t.Fatal(err)
	}
	if connector.calls.Load() != 1 {
		t.Fatalf("expected one single-cluster owner lookup, got %d", connector.calls.Load())
	}
	if len(result.Objects) != 1 || result.Objects[0].Kind != "Deployment" || result.Objects[0].Name != "web" {
		t.Fatalf("expected workload grouping for the selected cluster, got %#v", result.Objects)
	}
	if _, err = service.GetFleetEvents(ctx, fleet); err != nil {
		t.Fatal(err)
	}
	if connector.calls.Load() != 1 {
		t.Fatalf("owner cache miss: %d", connector.calls.Load())
	}
}
