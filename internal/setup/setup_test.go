package setup

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/go-sql-driver/mysql"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestConnectionDSNUsesDefaultsAndEncodesPassword(t *testing.T) {
	server := &Server{config: Config{DefaultDBHost: "platform-mysql.ftc-platform.svc", DefaultDBName: "ftc_platform"}}
	recorder := httptest.NewRecorder()
	dsn, ok := server.connectionDSN(recorder, connectionRequest{Password: "p@ss:w/rd", User: "ftc"})
	if !ok {
		t.Fatalf("connectionDSN failed: %s", recorder.Body.String())
	}
	parsed, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.User != "ftc" || parsed.Passwd != "p@ss:w/rd" || parsed.Addr != "platform-mysql.ftc-platform.svc:3306" || parsed.DBName != "ftc_platform" {
		t.Fatalf("unexpected DSN: %#v", parsed)
	}
}

func TestConnectionDSNRejectsMissingPasswordAndInvalidPort(t *testing.T) {
	server := &Server{config: Config{DefaultDBHost: "mysql", DefaultDBName: "ftc_platform"}}
	for _, payload := range []connectionRequest{
		{User: "ftc"},
		{User: "ftc", Password: "secret", Port: "not-a-port"},
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
	if !server.validToken(recorder, "one-time-token") {
		t.Fatal("valid token rejected")
	}
	recorder = httptest.NewRecorder()
	if server.validToken(recorder, "wrong-token") {
		t.Fatal("invalid token accepted")
	}
	if recorder.Code != 403 {
		t.Fatalf("expected 403, got %d", recorder.Code)
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
