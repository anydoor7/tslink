package metrics

import (
	"bufio"
	"errors"
	"net"
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

func TestMetricsMiddleware_DelegatesFlush(t *testing.T) {
	m := New()
	const svc = "flushsvc"

	handler := m.Middleware(svc, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("wrapped ResponseWriter should implement http.Flusher")
		}
		flusher.Flush()
	}))

	req := httptest.NewRequest(http.MethodGet, "/events", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if !rec.Flushed {
		t.Fatal("expected Flush to delegate to the underlying ResponseWriter")
	}
}

type hijackRecorder struct {
	*httptest.ResponseRecorder
	conn   net.Conn
	called bool
}

func (r *hijackRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	r.called = true
	if r.conn == nil {
		return nil, nil, errors.New("no conn")
	}
	return r.conn, bufio.NewReadWriter(bufio.NewReader(r.conn), bufio.NewWriter(r.conn)), nil
}

func TestResponseWriterHijackDelegates(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	rec := &hijackRecorder{
		ResponseRecorder: httptest.NewRecorder(),
		conn:             server,
	}
	rw := &responseWriter{ResponseWriter: rec}

	conn, _, err := rw.Hijack()
	if err != nil {
		t.Fatalf("Hijack() error = %v", err)
	}
	if conn != server {
		t.Fatalf("Hijack() conn = %v, want delegated server conn", conn)
	}
	if !rec.called {
		t.Fatal("underlying Hijack was not called")
	}
}

func TestResponseWriterHijackRequiresUnderlyingHijacker(t *testing.T) {
	rw := &responseWriter{ResponseWriter: httptest.NewRecorder()}

	conn, buf, err := rw.Hijack()
	if err == nil {
		t.Fatal("Hijack() error = nil, want unsupported error")
	}
	if conn != nil || buf != nil {
		t.Fatalf("Hijack() = conn %v buf %v, want nils on error", conn, buf)
	}
	if !strings.Contains(err.Error(), "does not implement http.Hijacker") {
		t.Fatalf("Hijack() error = %v, want unsupported hijacker message", err)
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
