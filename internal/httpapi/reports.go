package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	reportsvc "example.com/k8s-platform/internal/reports"
)

const reportBodyLimit = 16 << 10

func listReports(service *reportsvc.Service) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		if service == nil {
			writeReportError(writer, http.StatusServiceUnavailable, "REPORTS_UNAVAILABLE", "运行报告服务尚未就绪。")
			return
		}
		ctx, cancel := requestTimeout(request, 5*time.Second)
		defer cancel()
		items, err := service.List(ctx)
		if err != nil {
			writeReportError(writer, http.StatusServiceUnavailable, "REPORTS_READ_FAILED", "读取报告记录失败。")
			return
		}
		writeJSON(writer, http.StatusOK, response{Data: map[string]any{"items": items}})
	}
}

func generateReport(service *reportsvc.Service) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		if service == nil {
			writeReportError(writer, http.StatusServiceUnavailable, "REPORTS_UNAVAILABLE", "运行报告服务尚未就绪。")
			return
		}
		var payload reportsvc.Request
		if err := decodeReportJSON(writer, request, &payload); err != nil {
			writeReportError(writer, http.StatusBadRequest, "REPORT_REQUEST_INVALID", "请检查报告周期、日期和筛选范围。")
			return
		}
		if _, err := reportsvc.PeriodWindow(payload.Period, payload.Date, time.Now()); err != nil {
			writeReportError(writer, http.StatusUnprocessableEntity, "REPORT_PERIOD_INVALID", err.Error())
			return
		}
		ctx, cancel := requestTimeout(request, 60*time.Second)
		defer cancel()
		report, err := service.Generate(ctx, payload, time.Now())
		if err != nil {
			switch {
			case errors.Is(err, reportsvc.ErrBusy):
				writeReportError(writer, http.StatusConflict, "REPORT_BUSY", reportsvc.ErrBusy.Error())
			case strings.Contains(err.Error(), "当前筛选范围") || strings.Contains(err.Error(), "资源池类型") || strings.Contains(err.Error(), "筛选范围"):
				writeReportError(writer, http.StatusUnprocessableEntity, "REPORT_SCOPE_INVALID", err.Error())
			default:
				writeReportError(writer, http.StatusServiceUnavailable, "REPORT_GENERATION_FAILED", "生成报告失败，请检查已保存的采集数据后重试。")
			}
			return
		}
		writeJSON(writer, http.StatusCreated, response{Data: report})
	}
}

func getReport(service *reportsvc.Service) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		report, ok := readReport(writer, request, service)
		if !ok {
			return
		}
		writeJSON(writer, http.StatusOK, response{Data: report})
	}
}

func getReportHTML(service *reportsvc.Service) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		report, ok := readReport(writer, request, service)
		if !ok {
			return
		}
		page, err := reportsvc.RenderHTML(report)
		if err != nil {
			writeReportError(writer, http.StatusInternalServerError, "REPORT_RENDER_FAILED", "渲染报告失败。")
			return
		}
		writer.Header().Set("Content-Type", "text/html; charset=utf-8")
		writer.Header().Set("Cache-Control", "private, no-store")
		writer.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; img-src data:; frame-ancestors 'self'; base-uri 'none'; form-action 'none'")
		writer.Header().Set("X-Frame-Options", "SAMEORIGIN")
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write(page)
	}
}

func getReportModelConfig(service *reportsvc.Service) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		if service == nil {
			writeReportError(writer, http.StatusServiceUnavailable, "REPORTS_UNAVAILABLE", "运行报告服务尚未就绪。")
			return
		}
		ctx, cancel := requestTimeout(request, 5*time.Second)
		defer cancel()
		config, err := service.ModelConfig(ctx)
		if err != nil {
			writeReportError(writer, http.StatusServiceUnavailable, "REPORT_MODEL_READ_FAILED", "读取模型配置失败。")
			return
		}
		writeJSON(writer, http.StatusOK, response{Data: config})
	}
}

func saveReportModelConfig(service *reportsvc.Service) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		if service == nil {
			writeReportError(writer, http.StatusServiceUnavailable, "REPORTS_UNAVAILABLE", "运行报告服务尚未就绪。")
			return
		}
		var payload reportsvc.ModelConfig
		if err := decodeReportJSON(writer, request, &payload); err != nil {
			writeReportError(writer, http.StatusBadRequest, "REPORT_MODEL_INVALID", "请检查模型接口、模型名称和 API Key。")
			return
		}
		ctx, cancel := requestTimeout(request, 10*time.Second)
		defer cancel()
		config, err := service.SaveModelConfig(ctx, payload)
		if err != nil {
			writeReportError(writer, http.StatusUnprocessableEntity, "REPORT_MODEL_INVALID", err.Error())
			return
		}
		writeJSON(writer, http.StatusOK, response{Data: config})
	}
}

func readReport(writer http.ResponseWriter, request *http.Request, service *reportsvc.Service) (reportsvc.Report, bool) {
	if service == nil {
		writeReportError(writer, http.StatusServiceUnavailable, "REPORTS_UNAVAILABLE", "运行报告服务尚未就绪。")
		return reportsvc.Report{}, false
	}
	ctx, cancel := requestTimeout(request, 5*time.Second)
	defer cancel()
	report, err := service.Get(ctx, request.PathValue("id"))
	if errors.Is(err, reportsvc.ErrNotFound) {
		writeReportError(writer, http.StatusNotFound, "REPORT_NOT_FOUND", reportsvc.ErrNotFound.Error())
		return reportsvc.Report{}, false
	}
	if err != nil {
		writeReportError(writer, http.StatusServiceUnavailable, "REPORT_READ_FAILED", "读取报告失败。")
		return reportsvc.Report{}, false
	}
	return report, true
}

func decodeReportJSON(writer http.ResponseWriter, request *http.Request, destination any) error {
	request.Body = http.MaxBytesReader(writer, request.Body, reportBodyLimit)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request must contain one JSON object")
	}
	return nil
}

func writeReportError(writer http.ResponseWriter, status int, code, message string) {
	writeJSON(writer, status, response{Error: errorResponse{Code: code, Message: message}})
}
