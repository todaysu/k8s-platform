package cluster

import (
	"context"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestGroupEventWorkloadsByControllerUID(t *testing.T) {
	now := time.Now()
	objects := map[string]FleetEventObject{}
	for _, value := range []FleetEventObject{
		{ClusterID: "a", Namespace: "app", Kind: "Pod", Name: "web-rs-a", UID: "p1", EventCount: 3, OccurrenceCount: 5, LastObservedAt: now},
		{ClusterID: "a", Namespace: "app", Kind: "Pod", Name: "web-rs-b", UID: "p2", EventCount: 4, OccurrenceCount: 8, LastObservedAt: now.Add(time.Minute), Message: "latest"},
		{ClusterID: "a", Namespace: "app", Kind: "Pod", Name: "web-rs-c", UID: "p3", EventCount: 1},
		{ClusterID: "a", Namespace: "app", Kind: "Pod", Name: "standalone", UID: "solo", EventCount: 2},
		{ClusterID: "b", Namespace: "app", Kind: "Pod", Name: "web-rs-d", UID: "p4", EventCount: 1},
	} {
		objects[value.UID] = value
	}
	owners := map[string]EventResourceReference{"p1": {Kind: "Deployment", Name: "web", UID: "deploy-v1"}, "p2": {Kind: "Deployment", Name: "web", UID: "deploy-v1"}, "p3": {Kind: "Deployment", Name: "web", UID: "deploy-v2"}, "p4": {Kind: "Deployment", Name: "web", UID: "deploy-v1"}}
	result := groupEventWorkloads(objects, owners)
	if len(result) != 4 {
		t.Fatalf("must separate replacement, standalone and clusters: %#v", result)
	}
	merged := result["a/Deployment/app/deploy-v1"]
	if merged.EventCount != 7 || merged.OccurrenceCount != 13 || len(merged.Members) != 2 || merged.Message != "latest" {
		t.Fatalf("incorrect workload aggregation: %#v", merged)
	}
	if len(groupEventWorkloads(objects, nil)) != 5 {
		t.Fatal("unknown controllers must not be guessed from names")
	}
}

func TestEventWorkloadOwnersUsesReplicaSetControllerReferences(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/pods":
			w.Write([]byte(`{"kind":"PodList","apiVersion":"v1","items":[{"metadata":{"name":"unrelated-name","namespace":"app","uid":"p1","ownerReferences":[{"kind":"ReplicaSet","name":"rs","uid":"rs1","controller":true}]}},{"metadata":{"name":"web-looks-controlled","namespace":"app","uid":"solo"}}]}`))
		case "/apis/apps/v1/replicasets":
			w.Write([]byte(`{"kind":"ReplicaSetList","apiVersion":"apps/v1","items":[{"metadata":{"name":"rs","namespace":"app","uid":"rs1","ownerReferences":[{"kind":"Deployment","name":"web","uid":"d1","controller":true}]}}]}`))
		default:
			t.Errorf("unexpected lookup: %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	owners, err := NewKubernetesConnector(time.Second).EventWorkloadOwners(context.Background(), ParsedKubeconfig{APIServer: server.URL, CAData: ca})
	if err != nil {
		t.Fatal(err)
	}
	if owners["p1"].Kind != "Deployment" || owners["p1"].UID != "d1" || owners["solo"].Kind != "" {
		t.Fatalf("wrong controller ownership: %#v", owners)
	}
	if _, known := owners["solo"]; !known {
		t.Fatal("known independent Pod must be distinguishable from missing history")
	}
}
