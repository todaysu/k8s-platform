package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRegistryExposesCountersAndGauges(t *testing.T) {
	registry := NewRegistry()
	counter := registry.Counter("ftc_test_total", "Test counter.")
	gauge := registry.Gauge("ftc_test_value", "Test gauge.")
	counter.Add(3)
	gauge.Set(7)

	recorder := httptest.NewRecorder()
	registry.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := recorder.Body.String()
	for _, expected := range []string{
		"# TYPE ftc_test_total counter",
		"ftc_test_total 3",
		"# TYPE ftc_test_value gauge",
		"ftc_test_value 7",
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("missing %q in:\n%s", expected, body)
		}
	}
}

func TestMiddlewareCountsRequestsAndServerErrors(t *testing.T) {
	beforeRequests := RequestsTotal.Value()
	beforeErrors := RequestErrorsTotal.Value()
	handler := Middleware(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusInternalServerError)
	}))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/clusters", nil))
	if RequestsTotal.Value() != beforeRequests+1 {
		t.Fatalf("request counter did not advance: %d -> %d", beforeRequests, RequestsTotal.Value())
	}
	if RequestErrorsTotal.Value() != beforeErrors+1 {
		t.Fatalf("error counter did not advance: %d -> %d", beforeErrors, RequestErrorsTotal.Value())
	}
}
