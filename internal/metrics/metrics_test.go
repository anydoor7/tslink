package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// getCounterValue reads the current value from a CounterVec for the given label values.
func getCounterValue(t *testing.T, cv *prometheus.CounterVec, lvs ...string) float64 {
	t.Helper()
	c, err := cv.GetMetricWithLabelValues(lvs...)
	if err != nil {
		t.Fatalf("GetMetricWithLabelValues: %v", err)
	}
	var m dto.Metric
	if err := c.Write(&m); err != nil {
		t.Fatalf("Write metric: %v", err)
	}
	return m.Counter.GetValue()
}

// getHistogramCount returns the sample count from a HistogramVec for the given label values.
func getHistogramCount(t *testing.T, hv *prometheus.HistogramVec, lvs ...string) uint64 {
	t.Helper()
	o, err := hv.GetMetricWithLabelValues(lvs...)
	if err != nil {
		t.Fatalf("GetMetricWithLabelValues: %v", err)
	}
	var m dto.Metric
	if err := o.(prometheus.Metric).Write(&m); err != nil {
		t.Fatalf("Write metric: %v", err)
	}
	return m.Histogram.GetSampleCount()
}

func TestMetricsMiddleware_IncrementsCounter(t *testing.T) {
	m := New()
	const svc = "testsvc"

	handler := m.Middleware(svc, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	val := getCounterValue(t, m.RequestsTotal, svc, http.MethodGet, "200")
	if val != 1 {
		t.Errorf("expected RequestsTotal=1, got %v", val)
	}
}

func TestMetricsMiddleware_RecordsDuration(t *testing.T) {
	m := New()
	const svc = "durationsvc"

	handler := m.Middleware(svc, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/ping", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	count := getHistogramCount(t, m.RequestDuration, svc, http.MethodPost)
	if count != 1 {
		t.Errorf("expected RequestDuration sample count=1, got %d", count)
	}
}

func TestMetricsMiddleware_ResponseBytes(t *testing.T) {
	m := New()
	const svc = "bytessvc"
	body := "hello world"

	handler := m.Middleware(svc, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(body)) //nolint:errcheck
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	val := getCounterValue(t, m.ResponseBytes, svc)
	if val != float64(len(body)) {
		t.Errorf("expected ResponseBytes=%d, got %v", len(body), val)
	}
}

func TestMetricsHandler(t *testing.T) {
	m := New()
	const svc = "handlersvc"

	// Trigger a request so metrics have data
	mw := m.Middleware(svc, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	// Now hit the /metrics endpoint
	metricsReq := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	metricsRec := httptest.NewRecorder()
	m.Handler().ServeHTTP(metricsRec, metricsReq)

	if metricsRec.Code != http.StatusOK {
		t.Fatalf("expected 200 from /metrics, got %d", metricsRec.Code)
	}

	body := metricsRec.Body.String()
	if !strings.Contains(body, "tslink_requests_total") {
		t.Errorf("/metrics response missing tslink_requests_total")
	}
	if !strings.Contains(body, "tslink_request_duration_seconds") {
		t.Errorf("/metrics response missing tslink_request_duration_seconds")
	}
	if !strings.Contains(body, "tslink_response_bytes_total") {
		t.Errorf("/metrics response missing tslink_response_bytes_total")
	}
}
