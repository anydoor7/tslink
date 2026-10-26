package server

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"syscall"
	"testing"
)

type failingDialer struct{ err error }

func (d failingDialer) DialContext(context.Context, string, string) (net.Conn, error) {
	return nil, d.err
}

// A backend failure of an authorized request must produce the same visitor
// response on a direct app node and through the gateway, for every kind of
// client: the access log records the bytes actually written, so any difference
// in the generated response is a difference in recorded bytes_out. A timeout is
// 504 on both; every other failure is 502; the visitor code stays upstream_down.
func TestBackendFailureResponseIsTheSameOnBothEntryPaths(t *testing.T) {
	const target = "http://192.0.2.77:6543"
	type request struct {
		name, method string
		header       map[string]string
		body         string
	}
	requests := []request{
		{name: "api_post", method: http.MethodPost, body: "payload"},
		{name: "api_json", method: http.MethodGet, header: map[string]string{"Accept": "application/json"}},
		{name: "browser_accept", method: http.MethodGet, header: map[string]string{"Accept": "text/html,application/xhtml+xml"}},
		{name: "browser_navigate", method: http.MethodGet, header: map[string]string{"Sec-Fetch-Mode": "navigate"}},
		{name: "browser_head", method: http.MethodHead, header: map[string]string{"Sec-Fetch-Mode": "navigate"}},
	}
	for _, failure := range []struct {
		name       string
		err        error
		status     int
		class      string
		wantSubstr string
	}{
		{"refused", &net.OpError{Op: "dial", Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)}, http.StatusBadGateway, "connection_refused", "is not reachable right now"},
		{"timeout", &net.OpError{Op: "dial", Err: &timeoutError{timeout: true}}, http.StatusGatewayTimeout, "timeout", "did not respond in time"},
		{"other", &net.OpError{Op: "dial", Err: net.UnknownNetworkError("x")}, http.StatusBadGateway, "other", "is not reachable right now"},
	} {
		for _, req := range requests {
			t.Run(failure.name+"/"+req.name, func(t *testing.T) {
				type result struct {
					status        int
					header        http.Header
					body, class   string
					decision      string
					routeObserved []bool
				}
				serve := func(t *testing.T, options ProxyOptions, gateway bool) result {
					t.Helper()
					var observed []bool
					if gateway {
						options.Gateway = &GatewayProxyOptions{Authority: func() string { return "home.tailnet.ts.net:8443" }, App: "photos", HostLabel: "this host", Observe: func(up bool) { observed = append(observed, up) }}
					} else {
						options.App = "photos"
					}
					options.Dialer = failingDialer{failure.err}
					h, err := NewProxyHandlerWithOptions(target, nil, options)
					if err != nil {
						t.Fatal(err)
					}
					obs := &accessObservation{}
					r := httptest.NewRequest(req.method, "http://app.example/path", strings.NewReader(req.body))
					r = r.WithContext(context.WithValue(r.Context(), accessObservationKey{}, obs))
					for k, v := range req.header {
						r.Header.Set(k, v)
					}
					w := httptest.NewRecorder()
					h.ServeHTTP(w, r)
					obs.mu.Lock()
					defer obs.mu.Unlock()
					return result{status: w.Code, header: w.Header(), body: w.Body.String(), class: obs.errorClass, decision: obs.decision, routeObserved: observed}
				}
				direct, gateway := serve(t, ProxyOptions{}, false), serve(t, ProxyOptions{}, true)
				if direct.status != failure.status || gateway.status != failure.status {
					t.Fatalf("status direct=%d gateway=%d, want %d", direct.status, gateway.status, failure.status)
				}
				if direct.class != failure.class || gateway.class != failure.class {
					t.Fatalf("error class direct=%q gateway=%q, want %q", direct.class, gateway.class, failure.class)
				}
				if direct.decision != "" || gateway.decision != "" {
					t.Fatalf("an authorized request failing downstream is never denied: direct=%q gateway=%q", direct.decision, gateway.decision)
				}
				if direct.body != gateway.body {
					t.Fatalf("response bodies differ:\ndirect:  %q\ngateway: %q", direct.body, gateway.body)
				}
				for _, name := range []string{"Content-Type", "Cache-Control", "Content-Security-Policy"} {
					if direct.header.Get(name) != gateway.header.Get(name) {
						t.Fatalf("%s differs: direct=%q gateway=%q", name, direct.header.Get(name), gateway.header.Get(name))
					}
				}
				if req.method != http.MethodHead {
					if !strings.Contains(gateway.body, "photos on this host "+failure.wantSubstr) {
						t.Fatalf("body lacks the message: %q", gateway.body)
					}
					if strings.Contains(gateway.body, "192.0.2.77") || strings.Contains(gateway.body, "6543") {
						t.Fatalf("body leaks the backend address: %q", gateway.body)
					}
					json := !strings.HasPrefix(gateway.header.Get("Content-Type"), "text/html")
					if wantJSON := req.name == "api_post" || req.name == "api_json"; json != wantJSON {
						t.Fatalf("content negotiation: json=%v for %s (%s)", json, req.name, gateway.header.Get("Content-Type"))
					}
					if json && !strings.Contains(gateway.body, `"code":"upstream_down"`) {
						t.Fatalf("JSON envelope lacks the upstream_down code: %q", gateway.body)
					}
				}
				// Route health is a gateway-only observation: every failure is a
				// down observation, never a healthy one.
				if len(gateway.routeObserved) != 1 || gateway.routeObserved[0] {
					t.Fatalf("gateway route observations %v, want one down", gateway.routeObserved)
				}
			})
		}
	}
}
