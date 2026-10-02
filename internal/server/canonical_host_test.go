package server

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/registry"
)

func TestProxyCanonicalHostAvailability(t *testing.T) {
	var calls atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Seen-Host", r.Host)
		w.Header().Set("Seen-XFH", r.Header.Get("X-Forwarded-Host"))
	}))
	defer backend.Close()
	var canonical atomic.Value
	canonical.Store("")
	proxy, err := NewProxyHandlerWithOptions(backend.URL, nil, ProxyOptions{
		PreserveHost: true, CanonicalHost: func() string { return canonical.Load().(string) },
	})
	if err != nil {
		t.Fatal(err)
	}
	front := httptest.NewServer(proxy)
	defer front.Close()
	for _, host := range []string{"", "App.Tailnet.TS.Net.", "", "new.tailnet.ts.net"} {
		canonical.Store(host)
		before := calls.Load()
		req, _ := http.NewRequest(http.MethodGet, front.URL, nil)
		req.Host = "admin.attacker.example:8443"
		res, err := front.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(res.Body)
		res.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if host == "" {
			if res.StatusCode != 503 || string(body) != "canonical_host_unavailable: node's canonical external name is unavailable\n" || calls.Load() != before {
				t.Fatalf("missing canonical host forwarded: status=%d body=%q calls=%d", res.StatusCode, body, calls.Load()-before)
			}
		} else {
			want := strings.ToLower(strings.TrimSuffix(host, "."))
			if res.StatusCode != 200 || res.Header.Get("Seen-Host") != want || res.Header.Get("Seen-XFH") != want || calls.Load() != before+1 {
				t.Fatalf("canonical forwarding: status=%d headers=%v calls=%d", res.StatusCode, res.Header, calls.Load()-before)
			}
		}
	}
	for _, options := range []ProxyOptions{{PreserveHost: true}, {PreserveHost: true, CanonicalHost: func() string { return "https://bad.example/" }}} {
		proxy, err := NewProxyHandlerWithOptions(backend.URL, nil, options)
		if err != nil {
			t.Fatal(err)
		}
		rec := httptest.NewRecorder()
		before := calls.Load()
		proxy.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "https://admin.attacker.example/", nil))
		if rec.Code != 503 || !strings.HasPrefix(rec.Body.String(), "canonical_host_unavailable:") || calls.Load() != before {
			t.Fatalf("invalid provider/name did not fail closed: %d %s", rec.Code, rec.Body.String())
		}
	}
}

func TestNormalizeCanonicalProxyHost(t *testing.T) {
	for _, host := range []string{"", "app", "app..example", ".example", "-app.example", "app-.example", "https://app.example", "app.example:443", "app_example.net", "app.example/path", "app.example\r\nInjected: value", "127.0.0.1", "::1", "\u00e9.example", strings.Repeat("a", 64) + ".example", strings.Repeat("abcd.", 51) + "example"} {
		if got := normalizeCanonicalProxyHost(host); got != "" {
			t.Errorf("invalid canonical DNS name %q accepted as %q", host, got)
		}
	}
	for _, host := range []string{" app.tailnet.ts.net. ", "APP.TAILNET.TS.NET", "app.tailnet.ts.net"} {
		if got := normalizeCanonicalProxyHost(host); got != "app.tailnet.ts.net" {
			t.Errorf("canonical name %q normalized to %q", host, got)
		}
	}
}

func TestStartNodeCanonicalAuthority(t *testing.T) {
	for _, tc := range []struct {
		name, dns, want string
		certs           []string
	}{
		{"DNS fallback", "app.tailnet.ts.net.", "app.tailnet.ts.net", nil},
		{"certificate wins", "magic.tailnet.ts.net", "public.tailnet.ts.net", []string{"public.tailnet.ts.net", "other.tailnet.ts.net"}},
		{"missing runtime name", "", "", nil},
		{"invalid certificate", "valid.tailnet.ts.net", "", []string{"bad:443"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(config.ConfigDirEnv, t.TempDir())
			if err := config.EnsureDir(); err != nil {
				t.Fatal(err)
			}
			var calls atomic.Int32
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Seen-Host", r.Host)
				w.Header().Set("Seen-XFH", r.Header.Get("X-Forwarded-Host"))
			}))
			defer backend.Close()
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer ln.Close()
			fake := &preserveHostTSNetServer{ln: ln, fakeTSNetServer: fakeTSNetServer{
				localClient: fakeWhoIsClient(t, nil, fmt.Errorf("no fixture identity")), dnsName: tc.dns, certDomains: tc.certs,
			}}
			old := newTSNetServerFn
			t.Cleanup(func() { newTSNetServerFn = old })
			newTSNetServerFn = func(registry.Service, string, string, string) tsnetServer { return fake }
			s, err := New("isolated-fixture", "")
			if err != nil {
				t.Fatal(err)
			}
			if err := s.startNodeLocked(context.Background(), registry.Service{Name: "registry-name", Type: registry.TypeProxy, Target: backend.URL, PreserveHost: true}); err != nil {
				t.Fatal(err)
			}
			defer s.stopNodeLocked("registry-name")
			for _, host := range []string{"app", "magic.tailnet.ts.net", "admin.attacker.example:8443"} {
				req, _ := http.NewRequest(http.MethodGet, "http://"+ln.Addr().String(), nil)
				req.Host = host
				req.Header.Set("X-Forwarded-Host", "forged.invalid")
				res, err := (&http.Client{Timeout: httpReadHeaderTimeout}).Do(req)
				if err != nil {
					t.Fatal(err)
				}
				body, err := io.ReadAll(res.Body)
				res.Body.Close()
				if err != nil {
					t.Fatal(err)
				}
				if tc.want == "" {
					if res.StatusCode != 503 || !strings.HasPrefix(string(body), "canonical_host_unavailable:") || calls.Load() != 0 {
						t.Fatalf("runtime name missing but forwarded: status=%d body=%s calls=%d", res.StatusCode, body, calls.Load())
					}
				} else if res.StatusCode != 200 || res.Header.Get("Seen-Host") != tc.want || res.Header.Get("Seen-XFH") != tc.want {
					t.Fatalf("runtime canonical headers: status=%d headers=%v want=%q", res.StatusCode, res.Header, tc.want)
				}
			}
		})
	}
}
