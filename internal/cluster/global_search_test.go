package cluster

import "testing"

func TestMatchingResourcesIncludesClusterLocationAndPods(t *testing.T) {
	managed := ManagedCluster{ID: "cluster-1", Name: "shanghai-01", Environment: "production"}
	snapshot := cachedResources{
		workloads: []ClusterWorkload{{Kind: "Deployment", Namespace: "payments", Name: "checkout-api", Status: "healthy", Images: []string{"registry/checkout:v3"}}},
		pods:      []ClusterPod{{Namespace: "payments", Name: "checkout-api-abc", Phase: "Running", OwnerKind: "Deployment", OwnerName: "checkout-api", NodeName: "node-1"}},
	}

	items := matchingResources(managed, snapshot, "checkout")
	if len(items) != 2 {
		t.Fatalf("expected workload and pod, got %d", len(items))
	}
	for _, item := range items {
		if item.ClusterID != "cluster-1" || item.ClusterName != "shanghai-01" || item.Environment != "production" {
			t.Fatalf("result lost cluster location: %#v", item)
		}
	}
}

func TestMatchingResourcesSearchesNamespaceImageOwnerAndNode(t *testing.T) {
	managed := ManagedCluster{ID: "cluster-1", Name: "cluster"}
	snapshot := cachedResources{
		workloads: []ClusterWorkload{{Kind: "StatefulSet", Namespace: "observability", Name: "metrics", Images: []string{"quay.io/prometheus/prometheus:v3"}}},
		pods:      []ClusterPod{{Namespace: "apps", Name: "api-x7", OwnerKind: "Deployment", OwnerName: "order-service", NodeName: "worker-07"}},
	}

	for _, query := range []string{"observability", "prometheus", "order-service", "worker-07"} {
		if len(matchingResources(managed, snapshot, query)) != 1 {
			t.Fatalf("expected one match for %q", query)
		}
	}
}
