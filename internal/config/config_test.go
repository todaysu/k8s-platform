package config

import (
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
)

func TestLoadRejectsMissingSecrets(t *testing.T) {
	t.Setenv("ADMIN_PASSWORD_HASH", "")
	t.Setenv("CREDENTIAL_ENCRYPTION_KEY", "")

	if _, err := Load(); err == nil {
		t.Fatal("expected missing secret error")
	}
}

func TestLoadParsesSystemNamespaces(t *testing.T) {
	t.Setenv("ADMIN_PASSWORD_HASH", "hash")
	t.Setenv("CREDENTIAL_ENCRYPTION_KEY", "key")
	t.Setenv("SYSTEM_NAMESPACES", "kube-system, cmb-monitoring,kube-system")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.SystemNamespaces) != 2 || cfg.SystemNamespaces[1] != "cmb-monitoring" {
		t.Fatalf("unexpected namespaces: %#v", cfg.SystemNamespaces)
	}
}

func TestLoadParsesHealthCollectionLimits(t *testing.T) {
	t.Setenv("ADMIN_PASSWORD_HASH", "hash")
	t.Setenv("CREDENTIAL_ENCRYPTION_KEY", "key")
	t.Setenv("HEALTH_COLLECTION_INTERVAL", "90s")
	t.Setenv("HEALTH_COLLECTION_TIMEOUT", "15s")
	t.Setenv("HEALTH_COLLECTION_WORKERS", "4")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HealthCollectionInterval.String() != "1m30s" || cfg.HealthCollectionTimeout.String() != "15s" || cfg.HealthCollectionWorkers != 4 {
		t.Fatalf("unexpected health collection config: %#v", cfg)
	}
}

func TestLoadRejectsInvalidHealthCollectionWorkers(t *testing.T) {
	t.Setenv("ADMIN_PASSWORD_HASH", "hash")
	t.Setenv("CREDENTIAL_ENCRYPTION_KEY", "key")
	t.Setenv("HEALTH_COLLECTION_WORKERS", "0")
	if _, err := Load(); err == nil {
		t.Fatal("expected invalid health collection workers error")
	}
}

func TestLoadParsesSecurityPodStartupTimeout(t *testing.T) {
	t.Setenv("ADMIN_PASSWORD_HASH", "hash")
	t.Setenv("CREDENTIAL_ENCRYPTION_KEY", "key")
	t.Setenv("SECURITY_POD_STARTUP_TIMEOUT", "2m")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SecurityPodStartupTimeout != 2*time.Minute {
		t.Fatalf("unexpected security pod startup timeout: %s", cfg.SecurityPodStartupTimeout)
	}
}

func TestLoadBuildsMySQLDSNFromConnectionFields(t *testing.T) {
	t.Setenv("ADMIN_PASSWORD_HASH", "hash")
	t.Setenv("CREDENTIAL_ENCRYPTION_KEY", "key")
	t.Setenv("PLATFORM_DATABASE_DSN", "")
	t.Setenv("PLATFORM_DB_HOST", "mysql.example.com")
	t.Setenv("PLATFORM_DB_PORT", "3307")
	t.Setenv("PLATFORM_DB_USER", "ftc")
	t.Setenv("PLATFORM_DB_PASSWORD", "p@ss:w/rd")
	t.Setenv("PLATFORM_DB_NAME", "ftc_platform")
	t.Setenv("PLATFORM_DB_TLS", "skip-verify")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := mysql.ParseDSN(cfg.PlatformDatabaseDSN)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.User != "ftc" || parsed.Passwd != "p@ss:w/rd" || parsed.Addr != "mysql.example.com:3307" || parsed.DBName != "ftc_platform" || parsed.TLSConfig != "skip-verify" || !parsed.ParseTime {
		t.Fatalf("unexpected DSN fields: %#v", parsed)
	}
}

func TestLoadRejectsIncompleteMySQLConnectionFields(t *testing.T) {
	t.Setenv("ADMIN_PASSWORD_HASH", "hash")
	t.Setenv("CREDENTIAL_ENCRYPTION_KEY", "key")
	t.Setenv("PLATFORM_DATABASE_DSN", "")
	t.Setenv("PLATFORM_DB_HOST", "mysql.example.com")
	t.Setenv("PLATFORM_DB_USER", "")
	if _, err := Load(); err == nil {
		t.Fatal("expected missing database user error")
	}
}

func TestLoadAllowsSetupModeWithoutAdminHash(t *testing.T) {
	t.Setenv("ADMIN_PASSWORD_HASH", "")
	t.Setenv("CREDENTIAL_ENCRYPTION_KEY", "key")
	t.Setenv("SETUP_MODE", "true")
	t.Setenv("SETUP_TOKEN", "one-time-token")
	t.Setenv("PLATFORM_DATABASE_DSN", "")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.SetupMode || cfg.SetupToken != "one-time-token" || cfg.AdminPasswordHash != "" {
		t.Fatalf("unexpected setup config: %#v", cfg)
	}
}

func TestLoadRequiresSetupTokenInSetupMode(t *testing.T) {
	t.Setenv("ADMIN_PASSWORD_HASH", "")
	t.Setenv("CREDENTIAL_ENCRYPTION_KEY", "key")
	t.Setenv("SETUP_MODE", "true")
	t.Setenv("SETUP_TOKEN", "")
	t.Setenv("PLATFORM_DATABASE_DSN", "")
	if _, err := Load(); err == nil {
		t.Fatal("expected setup token error")
	}
}

func TestRuntimeRoleValidation(t *testing.T) {
	t.Setenv("ADMIN_PASSWORD_HASH", "hash")
	t.Setenv("CREDENTIAL_ENCRYPTION_KEY", "key")
	t.Setenv("PLATFORM_DATABASE_DSN", "test:test@tcp(localhost:3306)/test?parseTime=true")
	for _, role := range []string{"all", "api", "collector", "worker"} {
		t.Setenv("PLATFORM_ROLE", role)
		cfg, err := Load()
		if err != nil {
			t.Fatal(err)
		}
		if cfg.ServesAPI() != (role == "all" || role == "api") || cfg.Collects() != (role == "all" || role == "collector") || cfg.RunsWorker() != (role == "all" || role == "worker") {
			t.Fatalf("wrong runtime flags for %s", role)
		}
	}
	t.Setenv("PLATFORM_ROLE", "unsupported")
	if _, err := Load(); err == nil {
		t.Fatal("unimplemented role must be rejected")
	}
	t.Setenv("PLATFORM_ROLE", "collector")
	t.Setenv("PLATFORM_DATABASE_DSN", "")
	t.Setenv("PLATFORM_DB_HOST", "")
	if _, err := Load(); err == nil {
		t.Fatal("split roles must not silently use independent sqlite files")
	}
}

func TestShardedCollectionRequiresSharedDatabaseAndWorkloadMode(t *testing.T) {
	t.Setenv("ADMIN_PASSWORD_HASH", "hash")
	t.Setenv("CREDENTIAL_ENCRYPTION_KEY", "key")
	t.Setenv("PLATFORM_ROLE", "collector")
	t.Setenv("COLLECTION_SHARDS", "16")
	t.Setenv("PLATFORM_DATABASE_DSN", "test:test@tcp(localhost:3306)/test?parseTime=true")
	t.Setenv("HEALTH_INSTANCE_SOURCE", "workloads")
	cfg, err := Load()
	if err != nil || cfg.CollectionShards != 16 {
		t.Fatalf("shard config: %+v %v", cfg, err)
	}
	t.Setenv("HEALTH_INSTANCE_SOURCE", "pods")
	if _, err = Load(); err == nil {
		t.Fatal("sharded health silently listed full Pods")
	}
	t.Setenv("HEALTH_INSTANCE_SOURCE", "workloads")
	t.Setenv("COLLECTION_SHARDS", "65")
	if _, err = Load(); err == nil {
		t.Fatal("unbounded shard count")
	}
}
