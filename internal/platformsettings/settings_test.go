package platformsettings

import (
	"context"
	"testing"
	"time"
)

type memoryRepository struct{ records map[string]Record }

func (repository *memoryRepository) ListPlatformSettings(context.Context) ([]Record, error) {
	result := make([]Record, 0, len(repository.records))
	for _, record := range repository.records {
		result = append(result, record)
	}
	return result, nil
}

func (repository *memoryRepository) SavePlatformSetting(_ context.Context, record Record) error {
	if repository.records == nil {
		repository.records = map[string]Record{}
	}
	repository.records[record.Section] = record
	return nil
}

func TestLoadUsesDefaultsUntilASectionIsSaved(t *testing.T) {
	value, err := Load(context.Background(), &memoryRepository{})
	if err != nil {
		t.Fatal(err)
	}
	if value.Runtime.QueryConcurrency != 64 || value.Security.ScanWorkers != 2 {
		t.Fatalf("unexpected defaults: %#v", value)
	}
	if len(value.UpdatedAt) != 0 {
		t.Fatalf("defaults must not look persisted: %#v", value.UpdatedAt)
	}
}

func TestLoadPreservesSettingsAfterFileServiceRemoval(t *testing.T) {
	repository := &memoryRepository{records: map[string]Record{
		SectionRuntime: {Section: SectionRuntime, Payload: []byte(`{"query_concurrency":80,"artifact_max_upload_mib":64}`)},
	}}
	value, err := Load(context.Background(), repository)
	if err != nil {
		t.Fatal(err)
	}
	if value.Runtime.QueryConcurrency != 80 || value.Runtime.RealtimeConcurrency != Defaults().Runtime.RealtimeConcurrency {
		t.Fatalf("existing runtime configuration was lost: %#v", value.Runtime)
	}
}

func TestSaveValidatesAndReturnsMergedSettings(t *testing.T) {
	repository := &memoryRepository{}
	now := time.Date(2026, time.October, 22, 2, 0, 0, 0, time.UTC)
	payload := []byte(`{"query_concurrency":80,"realtime_concurrency":12,"monitoring_concurrency":6,"session_concurrency":8,"per_cluster_concurrency":2,"auto_refresh_seconds":30}`)
	value, err := Save(context.Background(), repository, SectionRuntime, payload, now)
	if err != nil {
		t.Fatal(err)
	}
	if value.Runtime.QueryConcurrency != 80 || value.UpdatedAt[SectionRuntime] != now.Format(time.RFC3339) {
		t.Fatalf("saved settings were not returned: %#v", value)
	}
	if value.Collection.HealthIntervalSeconds != 60 {
		t.Fatal("unsaved sections must retain recommended defaults")
	}
}

func TestSaveRejectsInvalidEventRetention(t *testing.T) {
	payload := []byte(`{"normal_retention_days":30,"warning_retention_days":7,"rollup_retention_days":365,"daily_retention_days":31,"cleanup_batch_size":5000}`)
	if _, err := Save(context.Background(), &memoryRepository{}, SectionEvents, payload, time.Now()); err == nil {
		t.Fatal("expected warning retention validation error")
	}
}

func TestJavaDefaultsToBuiltinAndValidatesOfflineOverride(t *testing.T) {
	value := Defaults()
	if value.Java.ArthasURL != "" || value.Java.ArthasVersion != "4.3.5" {
		t.Fatal("Arthas should default to its embedded distribution")
	}
	if err := validate(value, SectionJava); err != nil {
		t.Fatal(err)
	}
	value.Java.ArthasURL = "https://repo.internal/arthas.zip"
	value.Java.ArthasVersion = "4.4.0"
	if err := validate(value, SectionJava); err != nil {
		t.Fatal(err)
	}
	value.Java.ArthasVersion = "4.4.0; unsafe"
	if err := validate(value, SectionJava); err == nil {
		t.Fatal("invalid override version accepted")
	}
	value.Java.ArthasVersion = "4.4.0"
	value.Java.ArthasURL = "https://user:password@repo.internal/arthas.zip"
	if err := validate(value, SectionJava); err == nil {
		t.Fatal("URL credentials accepted")
	}
}
