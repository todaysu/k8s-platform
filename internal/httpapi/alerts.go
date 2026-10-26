package httpapi

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	alertsvc "example.com/k8s-platform/internal/alerts"
)

func listAlerts(service *alertsvc.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if service == nil {
			writeJSON(w, 503, response{Error: errorResponse{Code: "ALERTS_UNAVAILABLE", Message: "告警服务尚未就绪。"}})
			return
		}
		values := r.URL.Query()
		view := values.Get("view")
		if view != "" && view != "current" && view != "history" {
			writeJSON(w, 400, response{Error: errorResponse{Code: "INVALID_ALERT_QUERY", Message: "告警视图无效。"}})
			return
		}
		for _, key := range []string{"cluster", "severity", "namespace", "q", "state", "source", "sort", "duration", "resource"} {
			if len(values.Get(key)) > 200 {
				writeJSON(w, 400, response{Error: errorResponse{Code: "INVALID_ALERT_QUERY", Message: "筛选条件过长。"}})
				return
			}
		}
		valid := func(value string, allowed ...string) bool {
			if value == "" {
				return true
			}
			for _, item := range allowed {
				if value == item {
					return true
				}
			}
			return false
		}
		if !valid(values.Get("state"), "firing", "suppressed", "resolved") || !valid(values.Get("source"), "platform", "business") || !valid(values.Get("sort"), "severity", "duration", "recent", "recurrence") || !valid(values.Get("duration"), "lt1h", "1h-6h", "6h-24h", "gt24h") || !valid(values.Get("resource"), "linked") {
			writeJSON(w, 400, response{Error: errorResponse{Code: "INVALID_ALERT_QUERY", Message: "告警筛选条件无效。"}})
			return
		}
		page, err := strconv.Atoi(values.Get("page"))
		if values.Get("page") != "" && (err != nil || page < 1 || page > 10000) {
			writeJSON(w, 400, response{Error: errorResponse{Code: "INVALID_ALERT_QUERY", Message: "页码无效。"}})
			return
		}
		rangeValue := values.Get("range")
		ranges := map[string]time.Duration{"": 0, "all": 0, "1h": time.Hour, "6h": 6 * time.Hour, "24h": 24 * time.Hour, "7d": 7 * 24 * time.Hour, "30d": 30 * 24 * time.Hour}
		duration, ok := ranges[rangeValue]
		if !ok {
			writeJSON(w, 400, response{Error: errorResponse{Code: "INVALID_ALERT_QUERY", Message: "告警时间范围无效。"}})
			return
		}
		q := alertsvc.Query{View: view, ClusterID: values.Get("cluster"), Severity: values.Get("severity"), Namespace: values.Get("namespace"), Search: strings.TrimSpace(values.Get("q")), State: values.Get("state"), SourceType: values.Get("source"), Sort: values.Get("sort"), Duration: values.Get("duration"), ResourceOnly: values.Get("resource") == "linked", Page: page, PageSize: 30}
		if view == "history" && duration > 0 {
			q.Since = time.Now().UTC().Add(-duration)
		}
		ctx, cancel := requestTimeout(r, 5*time.Second)
		defer cancel()
		result, err := service.List(ctx, q)
		if err != nil {
			writeJSON(w, 503, response{Error: errorResponse{Code: "ALERTS_READ_FAILED", Message: "读取告警记录失败。"}})
			return
		}
		writeJSON(w, 200, response{Data: result})
	}
}
