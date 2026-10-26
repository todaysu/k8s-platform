package platformsettings

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strings"
	"time"

	"example.com/k8s-platform/internal/arthasdist"
)

const (
	SectionRuntime    = "runtime"
	SectionCollection = "collection"
	SectionEvents     = "events"
	SectionAlerts     = "alerts"
	SectionSecurity   = "security"
	SectionJava       = "java"
	SectionDatabase   = "database"
)

var sections = []string{SectionRuntime, SectionCollection, SectionEvents, SectionAlerts, SectionSecurity, SectionJava, SectionDatabase}

type Record struct {
	Section   string
	Payload   []byte
	UpdatedAt time.Time
}

type Repository interface {
	ListPlatformSettings(context.Context) ([]Record, error)
	SavePlatformSetting(context.Context, Record) error
}

type Runtime struct {
	QueryConcurrency          int  `json:"query_concurrency"`
	RealtimeConcurrency       int  `json:"realtime_concurrency"`
	MonitoringConcurrency     int  `json:"monitoring_concurrency"`
	SessionConcurrency        int  `json:"session_concurrency"`
	PerClusterConcurrency     int  `json:"per_cluster_concurrency"`
	AutoRefreshSeconds        int  `json:"auto_refresh_seconds"`
	OperationalReportsEnabled bool `json:"operational_reports_enabled"`
}

type Collection struct {
	HealthIntervalSeconds   int `json:"health_interval_seconds"`
	HealthTimeoutSeconds    int `json:"health_timeout_seconds"`
	HealthWorkers           int `json:"health_workers"`
	CapacityIntervalSeconds int `json:"capacity_interval_seconds"`
	CapacityTimeoutSeconds  int `json:"capacity_timeout_seconds"`
	CapacityWorkers         int `json:"capacity_workers"`
	ResourceIntervalSeconds int `json:"resource_interval_seconds"`
	ResourceTimeoutSeconds  int `json:"resource_timeout_seconds"`
	ResourceWorkers         int `json:"resource_workers"`
}

type Events struct {
	NormalRetentionDays  int `json:"normal_retention_days"`
	WarningRetentionDays int `json:"warning_retention_days"`
	RollupRetentionDays  int `json:"rollup_retention_days"`
	DailyRetentionDays   int `json:"daily_retention_days"`
	CleanupBatchSize     int `json:"cleanup_batch_size"`
}

type Alerts struct {
	ResolvedRetentionDays   int `json:"resolved_retention_days"`
	TransitionRetentionDays int `json:"transition_retention_days"`
	HourlyRetentionDays     int `json:"hourly_retention_days"`
	DailyRetentionDays      int `json:"daily_retention_days"`
	CoverageRetentionDays   int `json:"coverage_retention_days"`
	CleanupBatchSize        int `json:"cleanup_batch_size"`
}

type Security struct {
	KubeBenchImage      string `json:"kube_bench_image"`
	KubeBenchImageAMD64 string `json:"kube_bench_image_amd64"`
	KubeBenchImageARM64 string `json:"kube_bench_image_arm64"`
	ImagePullSecret     string `json:"image_pull_secret"`
	Namespace           string `json:"namespace"`
	ScanWorkers         int    `json:"scan_workers"`
	HostWorkers         int    `json:"host_workers"`
	ScanTimeoutMinutes  int    `json:"scan_timeout_minutes"`
	PodStartupMinutes   int    `json:"pod_startup_timeout_minutes"`
}

type Java struct {
	ArthasURL                string `json:"arthas_url"`
	ArthasVersion            string `json:"arthas_version"`
	ArthasSHA256             string `json:"arthas_sha256"`
	DownloadTimeoutSeconds   int    `json:"download_timeout_seconds"`
	DiagnosticTimeoutMinutes int    `json:"diagnostic_timeout_minutes"`
	MaxConcurrency           int    `json:"max_concurrency"`
	HeapDumpEnabled          bool   `json:"heap_dump_enabled"`
	HeapDumpMaxGiB           int    `json:"heap_dump_max_gib"`
	TemporaryRetentionHours  int    `json:"temporary_retention_hours"`
}

type Database struct {
	BackupEnabled          bool   `json:"backup_enabled"`
	BackupTime             string `json:"backup_time"`
	BackupRetentionDays    int    `json:"backup_retention_days"`
	BackupTargetNode       string `json:"backup_target_node"`
	BackupTargetPath       string `json:"backup_target_path"`
	SpaceWarningPercent    int    `json:"space_warning_percent"`
	SpaceCriticalPercent   int    `json:"space_critical_percent"`
	ConnectionCheckSeconds int    `json:"connection_check_seconds"`
}

type Settings struct {
	Runtime    Runtime           `json:"runtime"`
	Collection Collection        `json:"collection"`
	Events     Events            `json:"events"`
	Alerts     Alerts            `json:"alerts"`
	Security   Security          `json:"security"`
	Java       Java              `json:"java"`
	Database   Database          `json:"database"`
	UpdatedAt  map[string]string `json:"updated_at"`
}

type ApplyStatus struct {
	Status  string   `json:"status"`
	Targets []string `json:"targets,omitempty"`
	Message string   `json:"message"`
}

type SaveResult struct {
	Settings Settings    `json:"settings"`
	Apply    ApplyStatus `json:"apply"`
}

func Defaults() Settings {
	return Settings{
		Runtime:    Runtime{QueryConcurrency: 64, RealtimeConcurrency: 20, MonitoringConcurrency: 8, SessionConcurrency: 10, PerClusterConcurrency: 2, AutoRefreshSeconds: 30, OperationalReportsEnabled: false},
		Collection: Collection{HealthIntervalSeconds: 60, HealthTimeoutSeconds: 20, HealthWorkers: 5, CapacityIntervalSeconds: 300, CapacityTimeoutSeconds: 45, CapacityWorkers: 3, ResourceIntervalSeconds: 600, ResourceTimeoutSeconds: 30, ResourceWorkers: 2},
		Events:     Events{NormalRetentionDays: 7, WarningRetentionDays: 30, RollupRetentionDays: 365, DailyRetentionDays: 31, CleanupBatchSize: 5000},
		Alerts:     Alerts{ResolvedRetentionDays: 180, TransitionRetentionDays: 180, HourlyRetentionDays: 400, DailyRetentionDays: 1095, CoverageRetentionDays: 400, CleanupBatchSize: 5000},
		Security:   Security{KubeBenchImage: "docker.io/aquasec/kube-bench:v0.16.0", Namespace: "kube-system", ScanWorkers: 2, HostWorkers: 5, ScanTimeoutMinutes: 30, PodStartupMinutes: 5},
		Java:       Java{ArthasVersion: "4.3.5", DownloadTimeoutSeconds: 60, DiagnosticTimeoutMinutes: 30, MaxConcurrency: 2, HeapDumpMaxGiB: 4, TemporaryRetentionHours: 24},
		Database:   Database{BackupEnabled: false, BackupTime: "02:00", BackupRetentionDays: 30, SpaceWarningPercent: 80, SpaceCriticalPercent: 90, ConnectionCheckSeconds: 30},
		UpdatedAt:  map[string]string{},
	}
}

func Load(ctx context.Context, repository Repository) (Settings, error) {
	value := Defaults()
	if repository == nil {
		return value, nil
	}
	records, err := repository.ListPlatformSettings(ctx)
	if err != nil {
		return Settings{}, err
	}
	for _, record := range records {
		if record.Section == SectionRuntime {
			// Older releases stored the removed file service's upload limit.
			// Ignore only that retired key when reading existing settings.
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(record.Payload, &fields); err == nil {
				delete(fields, "artifact_max_upload_mib")
				record.Payload, err = json.Marshal(fields)
				if err != nil {
					return Settings{}, err
				}
			}
		}
		if err := decodeInto(&value, record.Section, record.Payload); err != nil {
			return Settings{}, fmt.Errorf("decode %s platform settings: %w", record.Section, err)
		}
		value.UpdatedAt[record.Section] = record.UpdatedAt.UTC().Format(time.RFC3339)
	}
	return value, nil
}

func Save(ctx context.Context, repository Repository, section string, payload []byte, now time.Time) (Settings, error) {
	if repository == nil {
		return Settings{}, errors.New("platform settings storage is unavailable")
	}
	section = strings.ToLower(strings.TrimSpace(section))
	value := Defaults()
	if err := decodeInto(&value, section, payload); err != nil {
		return Settings{}, err
	}
	if err := validate(value, section); err != nil {
		return Settings{}, err
	}
	canonical, err := sectionJSON(value, section)
	if err != nil {
		return Settings{}, err
	}
	if err := repository.SavePlatformSetting(ctx, Record{Section: section, Payload: canonical, UpdatedAt: now.UTC()}); err != nil {
		return Settings{}, err
	}
	return Load(ctx, repository)
}

func decodeInto(value *Settings, section string, payload []byte) error {
	var target any
	switch section {
	case SectionRuntime:
		target = &value.Runtime
	case SectionCollection:
		target = &value.Collection
	case SectionEvents:
		target = &value.Events
	case SectionAlerts:
		target = &value.Alerts
	case SectionSecurity:
		target = &value.Security
	case SectionJava:
		target = &value.Java
	case SectionDatabase:
		target = &value.Database
	default:
		return fmt.Errorf("unknown settings section %q", section)
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("invalid settings payload: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("invalid settings payload: multiple JSON values")
	}
	return nil
}

func sectionJSON(value Settings, section string) ([]byte, error) {
	switch section {
	case SectionRuntime:
		return json.Marshal(value.Runtime)
	case SectionCollection:
		return json.Marshal(value.Collection)
	case SectionEvents:
		return json.Marshal(value.Events)
	case SectionAlerts:
		return json.Marshal(value.Alerts)
	case SectionSecurity:
		return json.Marshal(value.Security)
	case SectionJava:
		return json.Marshal(value.Java)
	case SectionDatabase:
		return json.Marshal(value.Database)
	default:
		return nil, fmt.Errorf("unknown settings section %q", section)
	}
}

func validate(value Settings, section string) error {
	rangeCheck := func(name string, number, minimum, maximum int) error {
		if number < minimum || number > maximum {
			return fmt.Errorf("%s must be between %d and %d", name, minimum, maximum)
		}
		return nil
	}
	switch section {
	case SectionRuntime:
		checks := []struct {
			name            string
			value, min, max int
		}{{"query_concurrency", value.Runtime.QueryConcurrency, 1, 1000}, {"realtime_concurrency", value.Runtime.RealtimeConcurrency, 1, 200}, {"monitoring_concurrency", value.Runtime.MonitoringConcurrency, 1, 100}, {"session_concurrency", value.Runtime.SessionConcurrency, 1, 100}, {"per_cluster_concurrency", value.Runtime.PerClusterConcurrency, 1, 20}, {"auto_refresh_seconds", value.Runtime.AutoRefreshSeconds, 10, 600}}
		for _, item := range checks {
			if err := rangeCheck(item.name, item.value, item.min, item.max); err != nil {
				return err
			}
		}
	case SectionCollection:
		checks := []struct {
			name            string
			value, min, max int
		}{{"health_interval_seconds", value.Collection.HealthIntervalSeconds, 15, 3600}, {"health_timeout_seconds", value.Collection.HealthTimeoutSeconds, 5, 300}, {"health_workers", value.Collection.HealthWorkers, 1, 64}, {"capacity_interval_seconds", value.Collection.CapacityIntervalSeconds, 60, 86400}, {"capacity_timeout_seconds", value.Collection.CapacityTimeoutSeconds, 5, 600}, {"capacity_workers", value.Collection.CapacityWorkers, 1, 32}, {"resource_interval_seconds", value.Collection.ResourceIntervalSeconds, 60, 86400}, {"resource_timeout_seconds", value.Collection.ResourceTimeoutSeconds, 5, 600}, {"resource_workers", value.Collection.ResourceWorkers, 1, 32}}
		for _, item := range checks {
			if err := rangeCheck(item.name, item.value, item.min, item.max); err != nil {
				return err
			}
		}
	case SectionEvents:
		checks := []struct {
			name            string
			value, min, max int
		}{{"normal_retention_days", value.Events.NormalRetentionDays, 1, 365}, {"warning_retention_days", value.Events.WarningRetentionDays, 1, 730}, {"rollup_retention_days", value.Events.RollupRetentionDays, 30, 3650}, {"daily_retention_days", value.Events.DailyRetentionDays, 7, 730}, {"cleanup_batch_size", value.Events.CleanupBatchSize, 100, 50000}}
		for _, item := range checks {
			if err := rangeCheck(item.name, item.value, item.min, item.max); err != nil {
				return err
			}
		}
		if value.Events.WarningRetentionDays < value.Events.NormalRetentionDays {
			return errors.New("warning_retention_days cannot be shorter than normal_retention_days")
		}
	case SectionAlerts:
		checks := []struct {
			name            string
			value, min, max int
		}{{"resolved_retention_days", value.Alerts.ResolvedRetentionDays, 1, 3650}, {"transition_retention_days", value.Alerts.TransitionRetentionDays, 1, 3650}, {"hourly_retention_days", value.Alerts.HourlyRetentionDays, 30, 3650}, {"daily_retention_days", value.Alerts.DailyRetentionDays, 90, 3650}, {"coverage_retention_days", value.Alerts.CoverageRetentionDays, 30, 3650}, {"cleanup_batch_size", value.Alerts.CleanupBatchSize, 100, 50000}}
		for _, item := range checks {
			if err := rangeCheck(item.name, item.value, item.min, item.max); err != nil {
				return err
			}
		}
	case SectionSecurity:
		if strings.TrimSpace(value.Security.KubeBenchImage) == "" && strings.TrimSpace(value.Security.KubeBenchImageAMD64) == "" {
			return errors.New("a kube-bench image is required")
		}
		if !dnsLabel.MatchString(value.Security.Namespace) {
			return errors.New("namespace is invalid")
		}
		checks := []struct {
			name            string
			value, min, max int
		}{{"scan_workers", value.Security.ScanWorkers, 1, 20}, {"host_workers", value.Security.HostWorkers, 1, 50}, {"scan_timeout_minutes", value.Security.ScanTimeoutMinutes, 5, 240}, {"pod_startup_timeout_minutes", value.Security.PodStartupMinutes, 1, 30}}
		for _, item := range checks {
			if err := rangeCheck(item.name, item.value, item.min, item.max); err != nil {
				return err
			}
		}
	case SectionJava:
		if value.Java.ArthasURL != "" {
			parsed, err := url.Parse(value.Java.ArthasURL)
			if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
				return errors.New("arthas_url must be an HTTP or HTTPS URL")
			}
			if !arthasdist.ValidVersion(value.Java.ArthasVersion) {
				return errors.New("arthas_version must use a version such as 4.3.5")
			}
		}
		if value.Java.ArthasSHA256 != "" && !sha256Pattern.MatchString(value.Java.ArthasSHA256) {
			return errors.New("arthas_sha256 must contain 64 hexadecimal characters")
		}
		checks := []struct {
			name            string
			value, min, max int
		}{{"download_timeout_seconds", value.Java.DownloadTimeoutSeconds, 5, 600}, {"diagnostic_timeout_minutes", value.Java.DiagnosticTimeoutMinutes, 5, 240}, {"max_concurrency", value.Java.MaxConcurrency, 1, 20}, {"heap_dump_max_gib", value.Java.HeapDumpMaxGiB, 1, 100}, {"temporary_retention_hours", value.Java.TemporaryRetentionHours, 1, 168}}
		for _, item := range checks {
			if err := rangeCheck(item.name, item.value, item.min, item.max); err != nil {
				return err
			}
		}
	case SectionDatabase:
		if _, err := time.Parse("15:04", value.Database.BackupTime); err != nil {
			return errors.New("backup_time must use HH:MM")
		}
		if value.Database.BackupEnabled && (strings.TrimSpace(value.Database.BackupTargetNode) == "" || !strings.HasPrefix(strings.TrimSpace(value.Database.BackupTargetPath), "/")) {
			return errors.New("backup target node and absolute path are required when backup is enabled")
		}
		checks := []struct {
			name            string
			value, min, max int
		}{{"backup_retention_days", value.Database.BackupRetentionDays, 1, 365}, {"space_warning_percent", value.Database.SpaceWarningPercent, 50, 95}, {"space_critical_percent", value.Database.SpaceCriticalPercent, 60, 99}, {"connection_check_seconds", value.Database.ConnectionCheckSeconds, 10, 600}}
		for _, item := range checks {
			if err := rangeCheck(item.name, item.value, item.min, item.max); err != nil {
				return err
			}
		}
		if value.Database.SpaceCriticalPercent <= value.Database.SpaceWarningPercent {
			return errors.New("space_critical_percent must be greater than space_warning_percent")
		}
	default:
		return fmt.Errorf("unknown settings section %q", section)
	}
	return nil
}

func Sections() []string { return append([]string(nil), sections...) }

var dnsLabel = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
var sha256Pattern = regexp.MustCompile(`^[a-fA-F0-9]{64}$`)
