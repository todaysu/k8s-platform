package main

import (
	"testing"
	"time"

	"example.com/k8s-platform/internal/config"
	"example.com/k8s-platform/internal/platformsettings"
)

func TestApplySavedPlatformSettingsOnlyOverlaysPersistedSections(t *testing.T) {
	cfg := config.Config{HTTPQueryConcurrency: 15, HealthCollectionWorkers: 9, KubeBenchImage: "env/image:1"}
	settings := platformsettings.Defaults()
	settings.Runtime.QueryConcurrency = 80
	settings.Collection.HealthWorkers = 3
	settings.Security.KubeBenchImage = "saved/image:2"
	settings.Security.ScanTimeoutMinutes = 40
	settings.UpdatedAt[platformsettings.SectionRuntime] = time.Now().Format(time.RFC3339)
	settings.UpdatedAt[platformsettings.SectionSecurity] = time.Now().Format(time.RFC3339)

	result := applySavedPlatformSettings(cfg, settings)
	if result.HTTPQueryConcurrency != 80 || result.KubeBenchImage != "saved/image:2" || result.SecurityScanTimeout != 40*time.Minute {
		t.Fatalf("persisted sections were not applied: %#v", result)
	}
	if result.HealthCollectionWorkers != 9 {
		t.Fatal("unsaved collection settings must not override environment configuration")
	}
}
