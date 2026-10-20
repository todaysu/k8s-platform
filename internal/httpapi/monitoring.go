package httpapi

import (
	"context"
	"errors"
	"example.com/k8s-platform/internal/cluster"
	"net/http"
	"time"
)

type monitoringManager interface {
	GetMonitoring(context.Context, string, cluster.MonitoringQuery) (cluster.MonitoringSnapshot, error)
}

func monitoringHandler(manager any) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		runner, ok := manager.(monitoringManager)
		if !ok {
			writeJSON(w, 503, response{Error: errorResponse{Code: "MONITORING_UNAVAILABLE", Message: "监控查询不可用"}})
			return
		}
		start, _ := time.Parse(time.RFC3339, r.URL.Query().Get("start"))
		end, _ := time.Parse(time.RFC3339, r.URL.Query().Get("end"))
		q := cluster.MonitoringQuery{View: r.URL.Query().Get("view"), Namespace: r.URL.Query().Get("namespace"), Pod: r.URL.Query().Get("pod"), Start: start, End: end}
		if cluster.ValidateMonitoringQuery(q) != nil {
			writeJSON(w, 400, response{Error: errorResponse{Code: "INVALID_MONITORING_QUERY", Message: "请选择有效视图、命名空间和最多 7 天的时间范围。指定 Pod 时必须选择命名空间。"}})
			return
		}
		data, err := runner.GetMonitoring(r.Context(), r.PathValue("id"), q)
		if err != nil {
			var detail *cluster.MonitoringError
			if errors.As(err, &detail) {
				status := 503
				if detail.Code == "MONITORING_FORBIDDEN" {
					status = 403
				}
				if detail.Code == "MONITORING_TIMEOUT" {
					status = 504
				}
				writeJSON(w, status, response{Error: errorResponse{Code: detail.Code, Message: detail.Message}})
			} else {
				writeJSON(w, 503, response{Error: errorResponse{Code: "MONITORING_QUERY_FAILED", Message: "监控查询失败，请检查集群记录与纳管凭据。"}})
			}
			return
		}
		writeJSON(w, 200, response{Data: data})
	}
}
