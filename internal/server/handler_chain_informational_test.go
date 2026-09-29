package server

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/monody0007/tslink/internal/metrics"
)

// requestsTotalLines returns the tslink_requests_total series from the real
// Prometheus exposition, so the assertion reads what a scrape would read.
func requestsTotalLines(t *testing.T, m *metrics.Metrics) []string {
	t.Helper()
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	var lines []string
	for _, line := range strings.Split(rec.Body.String(), "\n") {
		if strings.HasPrefix(line, "tslink_requests_total{") {
			lines = append(lines, line)
		}
	}
	return lines
}

// The production chain puts metrics outside the access log. A backend 1xx
// followed by an error must reach the client as that error, and the access
// log and metrics must both record it, rather than the wire carrying an
// implicit 200 that neither record shows.
func TestServiceHandlerChainInformationalThenFinalStatus(t *testing.T) {
	oldLogger := slog.Default()
	logs := installCaptureLogger()
	t.Cleanup(func() { slog.SetDefault(oldLogger) })

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/continue-503":
			w.WriteHeader(http.StatusContinue)
		default:
			w.Header().Set("Link", "</style.css>; rel=preload")
			w.WriteHeader(http.StatusEarlyHints)
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, "backend unavailable")
	}))
	t.Cleanup(backend.Close)
	proxy, err := NewProxyHandler(backend.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	m := metrics.New()
	front := httptest.NewServer(instrumentServiceHandler(m, "probe", nil, proxy))
	t.Cleanup(front.Close)

	for i, path := range []string{"/hints-503", "/continue-503"} {
		resp, err := front.Client().Get(front.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusServiceUnavailable || string(body) != "backend unavailable" {
			t.Fatalf("%s: client response = %d %q, want 503 %q", path, resp.StatusCode, body, "backend unavailable")
		}
		if got := logs.attrMap(t, i)["status"]; got != int64(http.StatusServiceUnavailable) {
			t.Fatalf("%s: logged status = %v, want 503", path, got)
		}
	}
	want := `tslink_requests_total{method="GET",service="probe",status="503"} 2`
	if got := requestsTotalLines(t, m); len(got) != 1 || got[0] != want {
		t.Fatalf("request metrics = %q, want only %q", got, want)
	}
}
