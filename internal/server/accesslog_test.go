package server

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// captureHandler is a slog.Handler that collects log records for assertions.
type captureHandler struct {
	records []slog.Record
	mu      sync.Mutex
}

func (h *captureHandler) Enabled(_ context.Context, _ slog.Level) bool { return true }

func (h *captureHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r)
	return nil
}

func (h *captureHandler) WithAttrs(attrs []slog.Attr) slog.Handler { return h }
func (h *captureHandler) WithGroup(name string) slog.Handler       { return h }

func (h *captureHandler) attrMap(t *testing.T, idx int) map[string]any {
	t.Helper()
	h.mu.Lock()
	defer h.mu.Unlock()
	if idx >= len(h.records) {
		t.Fatalf("no log record at index %d (have %d)", idx, len(h.records))
	}
	m := make(map[string]any)
	h.records[idx].Attrs(func(a slog.Attr) bool {
		m[a.Key] = a.Value.Any()
		return true
	})
	return m
}

func installCaptureLogger() *captureHandler {
	h := &captureHandler{}
	slog.SetDefault(slog.New(h))
	return h
}

func TestAccessLogMiddleware_BasicRequest(t *testing.T) {
	ch := installCaptureLogger()

	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	handler := AccessLogMiddleware("mysvc", inner)
	req := httptest.NewRequest(http.MethodGet, "/hello", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if !called {
		t.Fatal("inner handler was not called")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	attrs := ch.attrMap(t, 0)
	if attrs["service"] != "mysvc" {
		t.Errorf("service=%v, want mysvc", attrs["service"])
	}
	if attrs["method"] != http.MethodGet {
		t.Errorf("method=%v, want GET", attrs["method"])
	}
	if attrs["path"] != "/hello" {
		t.Errorf("path=%v, want /hello", attrs["path"])
	}
}

func TestAccessLogMiddleware_StatusCode(t *testing.T) {
	ch := installCaptureLogger()

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	handler := AccessLogMiddleware("svc", inner)
	req := httptest.NewRequest(http.MethodGet, "/missing", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	attrs := ch.attrMap(t, 0)
	// slog stores int64 for integer values; compare via int64
	status, ok := attrs["status"]
	if !ok {
		t.Fatal("status attribute missing from log")
	}
	// slog.Attr stores int as int64
	var statusInt int
	switch v := status.(type) {
	case int64:
		statusInt = int(v)
	case int:
		statusInt = v
	default:
		t.Fatalf("unexpected status type %T: %v", status, status)
	}
	if statusInt != http.StatusNotFound {
		t.Errorf("status=%d, want 404", statusInt)
	}
}

func TestAccessLogMiddleware_WritesBytes(t *testing.T) {
	ch := installCaptureLogger()

	body := "hello"
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(body)) //nolint:errcheck
	})

	handler := AccessLogMiddleware("svc", inner)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	attrs := ch.attrMap(t, 0)
	bytesVal, ok := attrs["bytes"]
	if !ok {
		t.Fatal("bytes attribute missing from log")
	}
	var bytesInt int64
	switch v := bytesVal.(type) {
	case int64:
		bytesInt = v
	default:
		t.Fatalf("unexpected bytes type %T: %v", bytesVal, bytesVal)
	}
	if bytesInt != int64(len(body)) {
		t.Errorf("bytes=%d, want %d", bytesInt, len(body))
	}
}

func TestResponseWriter_DefaultStatus(t *testing.T) {
	rw := &responseWriter{
		ResponseWriter: httptest.NewRecorder(),
		status:         http.StatusOK,
	}
	// No WriteHeader called; status should remain 200
	if rw.status != http.StatusOK {
		t.Errorf("default status=%d, want 200", rw.status)
	}
	if rw.wroteHeader {
		t.Error("wroteHeader should be false before any write")
	}
}

func TestResponseWriter_WriteBeforeWriteHeader(t *testing.T) {
	rec := httptest.NewRecorder()
	rw := &responseWriter{
		ResponseWriter: rec,
		status:         http.StatusOK,
	}

	// Calling Write without WriteHeader should implicitly set status 200
	rw.Write([]byte("data")) //nolint:errcheck

	if rw.status != http.StatusOK {
		t.Errorf("implicit status=%d, want 200", rw.status)
	}
	if !rw.wroteHeader {
		t.Error("wroteHeader should be true after Write")
	}
	if rw.bytes != 4 {
		t.Errorf("bytes=%d, want 4", rw.bytes)
	}
}
