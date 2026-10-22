package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"example.com/k8s-platform/internal/platformsettings"
)

func getPlatformSettings(repository platformsettings.Repository) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		if repository == nil {
			writePlatformSettingsError(writer, http.StatusServiceUnavailable, "PLATFORM_SETTINGS_UNAVAILABLE", "平台配置存储暂不可用。")
			return
		}
		ctx, cancel := requestTimeout(request, 5*time.Second)
		defer cancel()
		value, err := platformsettings.Load(ctx, repository)
		if err != nil {
			writePlatformSettingsError(writer, http.StatusInternalServerError, "PLATFORM_SETTINGS_READ_FAILED", "读取平台配置失败。")
			return
		}
		writeJSON(writer, http.StatusOK, response{Data: value})
	}
}

func savePlatformSettings(repository platformsettings.Repository, restarter settingsRestarter) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		if repository == nil {
			writePlatformSettingsError(writer, http.StatusServiceUnavailable, "PLATFORM_SETTINGS_UNAVAILABLE", "平台配置存储暂不可用。")
			return
		}
		request.Body = http.MaxBytesReader(writer, request.Body, 32<<10)
		var payload json.RawMessage
		decoder := json.NewDecoder(request.Body)
		if err := decoder.Decode(&payload); err != nil {
			writePlatformSettingsError(writer, http.StatusBadRequest, "PLATFORM_SETTINGS_INVALID", "请提交有效的 JSON 配置。")
			return
		}
		var trailing any
		if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
			writePlatformSettingsError(writer, http.StatusBadRequest, "PLATFORM_SETTINGS_INVALID", "请只提交一个 JSON 对象。")
			return
		}
		ctx, cancel := requestTimeout(request, 10*time.Second)
		defer cancel()
		value, err := platformsettings.Save(ctx, repository, request.PathValue("section"), payload, time.Now())
		if err != nil {
			writePlatformSettingsError(writer, http.StatusBadRequest, "PLATFORM_SETTINGS_INVALID", err.Error())
			return
		}
		apply := platformsettings.ApplyStatus{
			Status:  "restart_unavailable",
			Message: "配置已保存；当前不是 Kubernetes 运行环境，请手动重启对应实例。",
		}
		if restarter != nil {
			restartContext, restartCancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer restartCancel()
			var restartErr error
			apply, restartErr = restarter.Restart(restartContext, request.PathValue("section"))
			if restartErr != nil && apply.Message == "" {
				apply = platformsettings.ApplyStatus{Status: "failed", Message: "配置已保存，但自动滚动重启提交失败。"}
			}
		}
		writeJSON(writer, http.StatusOK, response{Data: platformsettings.SaveResult{Settings: value, Apply: apply}})
	}
}

func writePlatformSettingsError(writer http.ResponseWriter, status int, code, message string) {
	writeJSON(writer, status, response{Error: errorResponse{Code: code, Message: message}})
}
