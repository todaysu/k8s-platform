package httpapi

import (
	"context"
	"example.com/k8s-platform/internal/cluster"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeKubectlRunner struct {
	fakeValidator
	id    string
	calls int
}

func (f *fakeKubectlRunner) RunKubectl(_ context.Context, id string, args []string, stdin string) (cluster.KubectlResult, error) {
	f.id = id
	f.calls++
	return cluster.KubectlResult{Output: "nodes ready", ExitCode: 0}, nil
}
func TestClusterKubectlRouteBindsTargetAndRejectsCredentialOverrides(t *testing.T) {
	f := &fakeKubectlRunner{}
	handler := NewRouter("test", f)
	for _, tc := range []struct {
		body   string
		status int
	}{{`{"args":["get","nodes"]}`, 200}, {`{"args":["get","nodes","--context=other"]}`, 400}, {`{"args":["apply","-f","/etc/passwd"]}`, 400}} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/test-target/kubectl", strings.NewReader(tc.body))
		handler.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("%d %s", w.Code, w.Body.String())
		}
	}
	if f.calls != 1 || f.id != "test-target" {
		t.Fatalf("wrong target or invalid command executed: %#v", f)
	}
}
