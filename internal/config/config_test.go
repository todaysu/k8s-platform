package config

import "testing"

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
