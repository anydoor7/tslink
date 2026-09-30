package server

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

// A backend 1xx followed by an error must reach the client through the
// production chain as that error, and the access log must record it, rather
// than the wire carrying an implicit 200 that the record does not show.
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
	front := httptest.NewServer(instrumentServiceHandler("probe", nil, proxy))
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
}
