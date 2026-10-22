package setup

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestNewInstallStatusStartsPending(t *testing.T) {
	status := newInstallStatus()
	if status.State != "running" || len(status.Steps) != len(installStepDefinitions) {
		t.Fatalf("unexpected status: %#v", status)
	}
	for _, step := range status.Steps {
		if step.Status != "pending" {
			t.Fatalf("step %s should be pending: %#v", step.Key, step)
		}
	}
}

func TestInstallStatusEndpointRequiresToken(t *testing.T) {
	server := &Server{config: Config{Token: "setup-token-value"}}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/setup/install", nil)
	request.Header.Set("X-Setup-Token", "setup-token-value")
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Data InstallStatus `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Data.State != "idle" {
		t.Fatalf("expected idle state, got %#v", payload.Data)
	}
	bad := httptest.NewRequest(http.MethodGet, "/api/v1/setup/install", nil)
	badRecorder := httptest.NewRecorder()
	server.ServeHTTP(badRecorder, bad)
	if badRecorder.Code != http.StatusForbidden {
		t.Fatalf("expected 403 without token, got %d", badRecorder.Code)
	}
}

func TestInstallRejectsInvalidAdmin(t *testing.T) {
	server := &Server{config: Config{Token: "setup-token-value"}}
	body := `{"token":"setup-token-value","domain":"ftc-platform.cmb.local","admin_username":"x","admin_password":"short"}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/setup/install", strings.NewReader(body))
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", recorder.Code, recorder.Body.String())
	}
}

func TestStartInstallRejectsConcurrentRuns(t *testing.T) {
	server := &Server{config: Config{Token: "setup-token-value"}, installRunning: true}
	if err := server.StartInstall(InstallRequest{}); err == nil {
		t.Fatal("concurrent install must be rejected")
	}
}

func TestResolvePlatformImage(t *testing.T) {
	client := fake.NewSimpleClientset(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "ftc-platform-install", Namespace: "ftc-platform"},
		Data:       map[string]string{"PLATFORM_IMAGE": "registry.local/ftc-platform:0.1.0"},
	})
	server := &Server{config: Config{Namespace: "ftc-platform"}, client: client}
	image, err := server.resolvePlatformImage(context.Background(), "")
	if err != nil || image != "registry.local/ftc-platform:0.1.0" {
		t.Fatalf("unexpected image: %q %v", image, err)
	}
	image, err = server.resolvePlatformImage(context.Background(), "registry.local/other:1")
	if err != nil || image != "registry.local/other:1" {
		t.Fatalf("explicit image was not used: %q %v", image, err)
	}
}

func TestHandleCADownload(t *testing.T) {
	client := fake.NewSimpleClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: platformCASecret, Namespace: "ftc-platform"},
		Data:       map[string][]byte{"ca.crt": []byte("CA-PEM")},
	})
	server := &Server{config: Config{Namespace: "ftc-platform"}, client: client}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/setup/ca", nil)
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || recorder.Body.String() != "CA-PEM" {
		t.Fatalf("unexpected CA response: %d %q", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Header().Get("Content-Disposition"), "ftc-platform-ca.crt") {
		t.Fatalf("missing attachment disposition: %#v", recorder.Header())
	}
}

func TestWaitForDeploymentReady(t *testing.T) {
	replicas := int32(2)
	client := fake.NewSimpleClientset(&appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "ftc-platform", Namespace: "ftc-platform", Generation: 1},
		Spec:       appsv1.DeploymentSpec{Replicas: &replicas},
		Status:     appsv1.DeploymentStatus{ReadyReplicas: 2, UpdatedReplicas: 2, ObservedGeneration: 1},
	})
	server := &Server{config: Config{Namespace: "ftc-platform"}, client: client}
	if err := server.WaitForDeployment(context.Background(), "ftc-platform", "ftc-platform", time.Second); err != nil {
		t.Fatal(err)
	}
}

func TestHandleInstallRejectsInvalidExternalDatabase(t *testing.T) {
	server := &Server{config: Config{Token: "setup-token-value"}}
	body := `{"token":"setup-token-value","domain":"ftc-platform.cmb.local","admin_username":"admin","admin_password":"long-enough-password","external_database":{"host":"","port":"3306","database":"ftc_platform","username":"ftc","password":"secret"}}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/setup/install", strings.NewReader(body))
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "INVALID_DATABASE") {
		t.Fatalf("unexpected response: %s", recorder.Body.String())
	}
}
