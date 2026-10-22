package setup

import (
	"strings"
	"testing"

	"github.com/go-sql-driver/mysql"
)

func TestExternalDatabaseDSN(t *testing.T) {
	dsn, err := externalDatabaseDSN(ExternalDatabase{
		Host: "mysql.internal", Port: "3307", Database: "ftc_platform",
		Username: "ftc", Password: "p@ss word", TLS: "preferred",
	})
	if err != nil {
		t.Fatal(err)
	}
	config, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if config.Addr != "mysql.internal:3307" || config.DBName != "ftc_platform" {
		t.Fatalf("unexpected dsn config: %#v", config)
	}
	if config.User != "ftc" || config.Passwd != "p@ss word" {
		t.Fatalf("unexpected credentials: %#v", config)
	}
	if config.TLSConfig != "preferred" || !config.ParseTime {
		t.Fatalf("unexpected tls/time settings: %#v", config)
	}
}

func TestExternalDatabaseDSNValidation(t *testing.T) {
	base := ExternalDatabase{Host: "mysql", Port: "3306", Database: "ftc_platform", Username: "ftc", Password: "secret"}
	cases := []struct {
		name   string
		mutate func(*ExternalDatabase)
		want   string
	}{
		{"missing host", func(value *ExternalDatabase) { value.Host = "" }, "地址"},
		{"bad port", func(value *ExternalDatabase) { value.Port = "70000" }, "端口"},
		{"bad database", func(value *ExternalDatabase) { value.Database = "bad-name" }, "数据库名"},
		{"bad user", func(value *ExternalDatabase) { value.Username = "bad-user" }, "用户名"},
		{"missing password", func(value *ExternalDatabase) { value.Password = "" }, "密码"},
		{"bad tls", func(value *ExternalDatabase) { value.TLS = "insecure" }, "TLS"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			value := base
			test.mutate(&value)
			_, err := externalDatabaseDSN(value)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected error containing %q, got %v", test.want, err)
			}
		})
	}
}
