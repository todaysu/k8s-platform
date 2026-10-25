package setup

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-sql-driver/mysql"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestConnectionDSNUsesExternalHostAndEncodesPassword(t *testing.T) {
	server := &Server{config: Config{DefaultDBName: "ftc_platform"}}
	recorder := httptest.NewRecorder()
	dsn, ok := server.connectionDSN(recorder, connectionRequest{Host: "mysql.external.example", Password: "p@ss:w/rd", User: "ftc"})
	if !ok {
		t.Fatalf("connectionDSN failed: %s", recorder.Body.String())
	}
	parsed, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.User != "ftc" || parsed.Passwd != "p@ss:w/rd" || parsed.Addr != "mysql.external.example:3306" || parsed.DBName != "ftc_platform" {
		t.Fatalf("unexpected DSN: %#v", parsed)
	}
}

func TestConnectionDSNRejectsMissingPasswordAndInvalidPort(t *testing.T) {
	server := &Server{config: Config{DefaultDBHost: "mysql", DefaultDBName: "ftc_platform"}}
	for _, payload := range []connectionRequest{
		{Host: "mysql", User: "ftc"},
		{Host: "mysql", User: "ftc", Password: "secret", Port: "not-a-port"},
	} {
		recorder := httptest.NewRecorder()
		if _, ok := server.connectionDSN(recorder, payload); ok {
			t.Fatalf("invalid connection accepted: %#v", payload)
		}
	}
}

func TestSetupTokenComparison(t *testing.T) {
	server := &Server{config: Config{Token: "one-time-token"}}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("POST", "http://localhost/api/v1/setup/test", nil)
	if !server.validToken(recorder, request, "one-time-token") {
		t.Fatal("valid token rejected")
	}
	recorder = httptest.NewRecorder()
	if server.validToken(recorder, request, "wrong-token") {
		t.Fatal("invalid token accepted")
	}
	if recorder.Code != 403 {
		t.Fatalf("expected 403, got %d", recorder.Code)
	}
	// Repeated wrong guesses must eventually be throttled.
	for i := 0; i < setupFailuresPerSource+2; i++ {
		guess := httptest.NewRecorder()
		server.validToken(guess, request, "wrong-token")
		if guess.Code == http.StatusTooManyRequests {
			return
		}
	}
	t.Fatal("setup token guessing was never throttled")
}

func TestTokenlessSetupAllowsRequests(t *testing.T) {
	server := &Server{config: Config{}}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("POST", "http://localhost/api/v1/setup/preflight", nil)
	if !server.validToken(recorder, request, "") {
		t.Fatalf("tokenless setup request rejected: %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestWriteRuntimeSecretCreatesAndUpdates(t *testing.T) {
	client := fake.NewSimpleClientset()
	server := &Server{config: Config{Namespace: "ftc-platform", RuntimeSecret: "ftc-platform-runtime"}, client: client}
	ctx := context.Background()
	if err := server.writeRuntimeSecret(ctx, "dsn-one", "admin", "hash-one"); err != nil {
		t.Fatal(err)
	}
	secret, err := client.CoreV1().Secrets("ftc-platform").Get(ctx, "ftc-platform-runtime", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if string(secret.Data["dsn"]) != "dsn-one" || string(secret.Data["admin-username"]) != "admin" {
		t.Fatalf("unexpected secret: %#v", secret.Data)
	}
	if err = server.writeRuntimeSecret(ctx, "dsn-two", "root", "hash-two"); err != nil {
		t.Fatal(err)
	}
	secret, _ = client.CoreV1().Secrets("ftc-platform").Get(ctx, "ftc-platform-runtime", metav1.GetOptions{})
	if string(secret.Data["dsn"]) != "dsn-two" || string(secret.Data["admin-username"]) != "root" {
		t.Fatalf("update failed: %#v", secret.Data)
	}
}
