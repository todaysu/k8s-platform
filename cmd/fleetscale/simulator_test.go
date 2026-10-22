package main

import (
	"context"
	"encoding/pem"
	"example.com/k8s-platform/internal/cluster"
	"testing"
	"time"
)

func TestSyntheticEndpointExercisesPagedRealConnector(t *testing.T) {
	source := &simulator{nodes: 501, pods: 12, workloads: 2}
	server := source.server()
	defer server.Close()
	parsed := cluster.ParsedKubeconfig{APIServer: server.URL, CAData: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), Token: "test"}
	connector := cluster.NewKubernetesConnectorWithInstanceSource(10*time.Second, "workloads")
	ctx := context.Background()
	health, err := connector.Health(ctx, parsed)
	if err != nil {
		t.Fatal(err)
	}
	if health.NodesReady != 501 || health.WorkerNodes != 501 || health.WorkerCores != 4008 || health.InstancesReady != 12 || health.CoreComponentsReady != 6 {
		t.Fatalf("paged health counters regressed: %+v", health)
	}
	capacity, err := connector.Capacity(ctx, parsed, cluster.CapacityRule{})
	if err != nil {
		t.Fatal(err)
	}
	if capacity.CPUAllocatableMilli != 4008000 || capacity.Platform.CPURequestsMilli != 600 || len(capacity.Namespaces) != 100 {
		t.Fatalf("paged capacity counters regressed: %+v", capacity)
	}
	workloads, pods, err := connector.IndexResources(ctx, parsed)
	if err != nil || len(workloads) != 2 || len(pods) != 12 {
		t.Fatalf("index summaries: %d %d %v", len(workloads), len(pods), err)
	}
	if pods[0].OwnerKind != "Deployment" {
		t.Fatalf("owner mapping lost: %+v", pods[0])
	}
}
