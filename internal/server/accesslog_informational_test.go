package server

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAccessLogProxyInformationalThenFinalStatus(t *testing.T) {
	oldLogger := slog.Default()
	logs := installCaptureLogger()
	t.Cleanup(func() { slog.SetDefault(oldLogger) })

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Link", "</style.css>; rel=preload")
		w.WriteHeader(http.StatusEarlyHints)
		switch r.URL.Path {
		case "/no-body":
			w.WriteHeader(http.StatusNoContent)
		case "/ok":
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, "ok")
		default:
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, "backend unavailable")
		}
	}))
	t.Cleanup(backend.Close)
	proxy, err := NewProxyHandler(backend.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	completed := make(chan struct{}, 1)
	chain := AccessLogMiddleware("probe", nil, proxy)
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chain.ServeHTTP(w, r)
		completed <- struct{}{}
	}))
	t.Cleanup(front.Close)

	for i, tc := range []struct {
		name, method, path string
		wantStatus         int
		wantBody           string
	}{
		{name: "GET error", method: http.MethodGet, path: "/error", wantStatus: http.StatusServiceUnavailable, wantBody: "backend unavailable"},
		{name: "HEAD error", method: http.MethodHead, path: "/error", wantStatus: http.StatusServiceUnavailable},
		{name: "GET no body", method: http.MethodGet, path: "/no-body", wantStatus: http.StatusNoContent},
		{name: "GET success", method: http.MethodGet, path: "/ok", wantStatus: http.StatusOK, wantBody: "ok"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(tc.method, front.URL+tc.path, nil)
			if err != nil {
				t.Fatal(err)
			}
			resp, err := front.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}
			// Receiving the response can precede the access-log write, especially
			// for HEAD and 204. Wait for this request's handler to finish.
			select {
			case <-completed:
			case <-time.After(5 * time.Second):
				t.Fatal("request handler did not finish")
			}
			if resp.StatusCode != tc.wantStatus || string(body) != tc.wantBody {
				t.Fatalf("response = %d %q, want %d %q", resp.StatusCode, body, tc.wantStatus, tc.wantBody)
			}
			attrs := logs.attrMap(t, i)
			if attrs["service"] != "probe" || attrs["method"] != tc.method || attrs["path"] != tc.path {
				t.Fatalf("log attributed to a different request: %v", attrs)
			}
			if got := attrs["status"]; got != int64(tc.wantStatus) {
				t.Fatalf("logged final status = %v, want %d", got, tc.wantStatus)
			}
		})
	}
}
