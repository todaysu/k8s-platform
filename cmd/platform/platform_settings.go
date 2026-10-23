package main

import (
	"time"

	"example.com/k8s-platform/internal/config"
	"example.com/k8s-platform/internal/platformsettings"
)

// applySavedPlatformSettings overlays only sections that an administrator has
// explicitly saved. Environment variables therefore remain authoritative on a
// fresh installation, while the settings page becomes authoritative after the
// first save of a section.
func applySavedPlatformSettings(cfg config.Config, settings platformsettings.Settings) config.Config {
	if settings.UpdatedAt[platformsettings.SectionRuntime] != "" {
		cfg.HTTPQueryConcurrency = settings.Runtime.QueryConcurrency
		cfg.HTTPRealtimeConcurrency = settings.Runtime.RealtimeConcurrency
		cfg.HTTPMonitoringConcurrency = settings.Runtime.MonitoringConcurrency
		cfg.HTTPSessionConcurrency = settings.Runtime.SessionConcurrency
		cfg.HTTPClusterConcurrency = settings.Runtime.PerClusterConcurrency
	}
	if settings.UpdatedAt[platformsettings.SectionCollection] != "" {
		cfg.HealthCollectionInterval = time.Duration(settings.Collection.HealthIntervalSeconds) * time.Second
		cfg.HealthCollectionTimeout = time.Duration(settings.Collection.HealthTimeoutSeconds) * time.Second
		cfg.HealthCollectionWorkers = settings.Collection.HealthWorkers
		cfg.CapacityCollectionInterval = time.Duration(settings.Collection.CapacityIntervalSeconds) * time.Second
		cfg.CapacityCollectionTimeout = time.Duration(settings.Collection.CapacityTimeoutSeconds) * time.Second
		cfg.CapacityCollectionWorkers = settings.Collection.CapacityWorkers
		cfg.ResourceIndexInterval = time.Duration(settings.Collection.ResourceIntervalSeconds) * time.Second
		cfg.ResourceIndexTimeout = time.Duration(settings.Collection.ResourceTimeoutSeconds) * time.Second
		cfg.ResourceIndexWorkers = settings.Collection.ResourceWorkers
	}
	if settings.UpdatedAt[platformsettings.SectionSecurity] != "" {
		cfg.KubeBenchImage = settings.Security.KubeBenchImage
		cfg.KubeBenchImageAMD64 = settings.Security.KubeBenchImageAMD64
		cfg.KubeBenchImageARM64 = settings.Security.KubeBenchImageARM64
		cfg.SecurityScannerPullSecret = settings.Security.ImagePullSecret
		cfg.SecurityScannerNamespace = settings.Security.Namespace
		cfg.SecurityScanWorkers = settings.Security.ScanWorkers
		cfg.SecurityHostScanWorkers = settings.Security.HostWorkers
		cfg.SecurityScanTimeout = time.Duration(settings.Security.ScanTimeoutMinutes) * time.Minute
		cfg.SecurityPodStartupTimeout = time.Duration(settings.Security.PodStartupMinutes) * time.Minute
	}
	return cfg
}
