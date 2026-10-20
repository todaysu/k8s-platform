package cluster

import (
	"context"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestEventResourceValidatesUIDAndResolvesControllerWithoutNameGuessing(t *testing.T) {
	for _, test := range []struct {
		name, uid, owner string
		code             int
		want             string
	}{{"normal", "pod-uid", "", 200, "normal"}, {"controller", "pod-uid", `,"ownerReferences":[{"apiVersion":"apps/v1","kind":"ReplicaSet","name":"api-rs","uid":"rs-uid","controller":true}]`, 200, "normal"}, {"replaced", "other-uid", "", 200, "replaced"}, {"deleted", "", "", 404, "deleted"}, {"forbidden", "", "", 403, "error"}} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if strings.Contains(r.URL.Path, "replicasets/api-rs") {
					w.Write([]byte(`{"metadata":{"uid":"rs-uid","ownerReferences":[{"kind":"Deployment","name":"api","uid":"deployment-uid","controller":true}]}}`))
					return
				}
				if test.code != 200 {
					w.WriteHeader(test.code)
					reason := "NotFound"
					if test.code == 403 {
						reason = "Forbidden"
					}
					w.Write([]byte(`{"kind":"Status","apiVersion":"v1","reason":"` + reason + `","code":` + httpCode(test.code) + `}`))
					return
				}
				w.Write([]byte(`{"metadata":{"name":"pod","namespace":"myapp","uid":"` + test.uid + `"` + test.owner + `},"spec":{"nodeName":"node-1"},"status":{"phase":"Running","conditions":[{"type":"Ready","status":"True"}],"containerStatuses":[{"name":"web","ready":true,"restartCount":1,"state":{"running":{"startedAt":"2026-10-19T00:00:00Z"}},"lastState":{"terminated":{"exitCode":1,"reason":"Error"}}}]}}`))
			}))
			defer server.Close()
			ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
			snapshot, err := NewKubernetesConnector(time.Second).EventResource(context.Background(), ParsedKubeconfig{APIServer: server.URL, CAData: ca}, EventResourceQuery{Kind: "Pod", Namespace: "myapp", Name: "pod", UID: "pod-uid"})
			if test.want == "error" {
				if ErrorCode(err) != CodeAuthorizationFailed {
					t.Fatal(err)
				}
				return
			}
			if err != nil || snapshot.Status != test.want {
				t.Fatalf("unexpected result: %#v %v", snapshot, err)
			}
			if test.name == "controller" && (len(snapshot.Owners) != 2 || snapshot.Owners[1].Kind != "Deployment") {
				t.Fatalf("missing real owner chain: %#v", snapshot)
			}
			if test.name == "normal" && (len(snapshot.Owners) != 0 || !snapshot.Containers[0].HasPreviousLogs) {
				t.Fatalf("independent pod/log evidence incorrect: %#v", snapshot)
			}
			if (test.want == "deleted" || test.want == "replaced") && len(snapshot.Containers) > 0 {
				t.Fatal("must not expose replacement container as original")
			}
		})
	}
}
func httpCode(code int) string {
	if code == 403 {
		return "403"
	}
	return "404"
}
