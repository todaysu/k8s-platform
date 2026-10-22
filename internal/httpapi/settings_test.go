package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"example.com/k8s-platform/internal/platformsettings"
)

type settingsMemoryRepository struct {
	records map[string]platformsettings.Record
}

func (repository *settingsMemoryRepository) ListPlatformSettings(context.Context) ([]platformsettings.Record, error) {
	result := make([]platformsettings.Record, 0, len(repository.records))
	for _, record := range repository.records {
		result = append(result, record)
	}
	return result, nil
}

func (repository *settingsMemoryRepository) SavePlatformSetting(_ context.Context, record platformsettings.Record) error {
	if repository.records == nil {
		repository.records = map[string]platformsettings.Record{}
	}
	repository.records[record.Section] = record
	return nil
}

type settingsRestartStub struct{ section string }

func (stub *settingsRestartStub) Restart(_ context.Context, section string) (platformsettings.ApplyStatus, error) {
	stub.section = section
	return platformsettings.ApplyStatus{Status: "restarting", Targets: []string{"ftc-platform"}, Message: "配置已保存，正在滚动重启 ftc-platform。"}, nil
}

func TestSavePlatformSettingsReturnsApplyStatus(t *testing.T) {
	repository := &settingsMemoryRepository{}
	restarter := &settingsRestartStub{}
	request := httptest.NewRequest(http.MethodPut, "/api/v1/settings/runtime", strings.NewReader(`{"query_concurrency":80,"realtime_concurrency":12,"monitoring_concurrency":6,"session_concurrency":8,"per_cluster_concurrency":2,"auto_refresh_seconds":30}`))
	request.SetPathValue("section", platformsettings.SectionRuntime)
	recorder := httptest.NewRecorder()

	savePlatformSettings(repository, restarter).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", recorder.Code, recorder.Body.String())
	}
	if restarter.section != platformsettings.SectionRuntime {
		t.Fatalf("unexpected restart section %q", restarter.section)
	}
	var envelope struct {
		Data platformsettings.SaveResult `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if envelope.Data.Settings.Runtime.QueryConcurrency != 80 || envelope.Data.Apply.Status != "restarting" {
		t.Fatalf("unexpected save result: %#v", envelope.Data)
	}
}
