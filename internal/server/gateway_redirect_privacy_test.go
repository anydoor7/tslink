package server

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/anydoor7/tslink/internal/registry"
	"tailscale.com/ipn/ipnstate"
)

func TestGatewayOriginReferenceRewrites(t *testing.T) {
	for _, target := range []string{"https://remote.tailnet.ts.net", "http://localhost:3000"} {
		u, _ := url.Parse(target)
		for _, tt := range []struct {
			value   string
			rewrite bool
		}{
			{target + "/exact?q=1#f", true},
			{u.Scheme + "://" + net.JoinHostPort(u.Hostname()+".", urlPort(u)) + "/dot", true},
			{"//" + u.Host + "/relative?q=2", true},
			{"//" + net.JoinHostPort(strings.ToUpper(u.Hostname())+".", urlPort(u)) + "/both", true},
			{"/relative-path", false}, {"//foreign.tailnet.ts.net/path", false},
			{"//" + net.JoinHostPort(u.Hostname(), "12345") + "/foreign-port", false},
		} {
			t.Run(target+tt.value, func(t *testing.T) {
				resp := &http.Response{Header: make(http.Header)}
				resp.Header.Set("Location", tt.value)
				resp.Header.Set("Content-Location", tt.value)
				resp.Header.Set("Refresh", "0; url=\""+tt.value+"\"")
				rewriteGatewayResponse(resp, u, "home.tailnet.ts.net:8443")
				want := tt.value
				if tt.rewrite {
					parsed, _ := url.Parse(tt.value)
					parsed.Scheme, parsed.Host = "https", "home.tailnet.ts.net:8443"
					want = parsed.String()
				}
				if got := resp.Header.Get("Location"); got != want {
					t.Errorf("Location=%q want %q", got, want)
				}
				if got := resp.Header.Get("Content-Location"); got != want {
					t.Errorf("Content-Location=%q want %q", got, want)
				}
				if got := resp.Header.Get("Refresh"); got != "0; url=\""+want+"\"" {
					t.Errorf("Refresh=%q want %q", got, want)
				}
			})
		}
	}
}

func TestGatewayRemoteFailurePagePrivacy(t *testing.T) {
	for _, failure := range []error{errors.New("offline"), context.DeadlineExceeded} {
		t.Run(failure.Error(), func(t *testing.T) {
			var calls atomic.Int64
			var st atomic.Pointer[ipnstate.Status]
			st.Store(taggedGatewayStatus(true))
			f := newGatewayFixture(t, func(f *gatewayFixture) {
				original := f.node.localClient.Transport
				f.node.localClient.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
					if strings.HasSuffix(r.URL.Path, "/status") {
						return remoteLocalClient(&st, nil).Transport.RoundTrip(r)
					}
					return original.RoundTrip(r)
				})
				f.node.dial = func(context.Context, string, string) (net.Conn, error) { calls.Add(1); return nil, failure }
				reg, err := registry.Load(f.path)
				if err != nil {
					t.Fatal(err)
				}
				svc := reg.Services[0]
				svc.Upstream, svc.Target = "tailnet", "https://remote.tailnet.ts.net:9443"
				if _, err := registry.Add(f.path, svc); err != nil {
					t.Fatal(err)
				}
			})
			f.waitRoute(t, "photos", "running")
			for _, accept := range []string{"text/html", "application/json"} {
				code, body, _ := gatewayGet(t, f.client(t), "8443", "/", map[string]string{"Accept": accept})
				if code != 502 || (accept == "application/json" && !strings.Contains(body, "upstream_down")) || !strings.Contains(body, "photos on remote") || strings.Contains(body, "remote.tailnet.ts.net") || strings.Contains(body, "9443") || strings.Contains(body, "100.64.0.9") {
					t.Fatalf("remote error leaked target or hid failure: %d %s", code, body)
				}
			}
			if calls.Load() != 2 {
				t.Fatal("refusal did not execute remote dials", calls.Load())
			}
		})
	}
}
