package server

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/registry"
)

type preserveHostTSNetServer struct {
	fakeTSNetServer
	ln net.Listener
}

func (s *preserveHostTSNetServer) ListenTLS(string, string) (net.Listener, error) { return s.ln, nil }

func TestStartNodePreserveHost(t *testing.T) {
	for _, preserve := range []bool{false, true} {
		t.Run(fmt.Sprint(preserve), func(t *testing.T) {
			t.Setenv(config.ConfigDirEnv, t.TempDir())
			if err := config.EnsureDir(); err != nil {
				t.Fatal(err)
			}
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Header().Set("Seen-Host", r.Host) }))
			defer backend.Close()
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer ln.Close()
			fake := &preserveHostTSNetServer{ln: ln, fakeTSNetServer: fakeTSNetServer{localClient: fakeWhoIsClient(t, nil, fmt.Errorf("fixture no identity")), dnsName: "app.review.example"}}
			old := newTSNetServerFn
			t.Cleanup(func() { newTSNetServerFn = old })
			newTSNetServerFn = func(registry.Service, string, string, string) tsnetServer { return fake }
			s, err := New("fixture-key", "")
			if err != nil {
				t.Fatal(err)
			}
			if err = s.startNodeLocked(context.Background(), registry.Service{Name: "app", Type: registry.TypeProxy, Target: backend.URL, PreserveHost: preserve}); err != nil {
				t.Fatal(err)
			}
			defer s.stopNodeLocked("app")
			req, err := http.NewRequest(http.MethodGet, "http://"+ln.Addr().String(), nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Host = "app.review.example"
			res, err := (&http.Client{Timeout: httpReadHeaderTimeout}).Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			u, _ := url.Parse(backend.URL)
			want := u.Host
			if preserve {
				want = req.Host
			}
			if res.Header.Get("Seen-Host") != want {
				t.Fatalf("runtime Host=%q want %q", res.Header.Get("Seen-Host"), want)
			}
		})
	}
}

func TestProxyPreserveHost(t *testing.T) {
	for _, preserve := range []bool{false, true} {
		for _, secure := range []bool{false, true} {
			for _, host := range []string{"app.review.example", "app.review.example:8443"} {
				t.Run(fmt.Sprintf("preserve=%t/https=%t/host=%s", preserve, secure, host), func(t *testing.T) {
					backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.Header().Set("Seen-Host", r.Host)
						for _, key := range []string{"X-Forwarded-Host", "X-Forwarded-Proto", "X-Forwarded-For"} {
							w.Header().Set("Seen-"+key, r.Header.Get(key))
						}
					}))
					defer backend.Close()
					proxy, err := NewProxyHandlerWithOptions(backend.URL, nil, ProxyOptions{PreserveHost: preserve, CanonicalHost: func() string { return "app.review.example" }})
					if err != nil {
						t.Fatal(err)
					}
					// A real listener exercises the outgoing Host and headers on the wire.
					front := httptest.NewUnstartedServer(proxy)
					if secure {
						front.StartTLS()
					} else {
						front.Start()
					}
					defer front.Close()
					client := front.Client()
					req, err := http.NewRequest(http.MethodGet, front.URL, nil)
					if err != nil {
						t.Fatal(err)
					}
					req.Host = host
					req.Header.Set("X-Forwarded-Host", "attacker.invalid")
					req.Header.Set("X-Forwarded-Proto", "forged")
					req.Header.Set("X-Forwarded-For", "192.0.2.123")
					res, err := client.Do(req)
					if err != nil {
						t.Fatal(err)
					}
					defer res.Body.Close()
					u, _ := url.Parse(backend.URL)
					wantHost, wantProto := u.Host, "http"
					if preserve {
						wantHost = "app.review.example"
					}
					if secure {
						wantProto = "https"
					}
					if res.Header.Get("Seen-Host") != wantHost {
						t.Errorf("Host=%q want %q", res.Header.Get("Seen-Host"), wantHost)
					}
					wantXFH := host
					if preserve {
						wantXFH = "app.review.example"
					}
					if res.Header.Get("Seen-X-Forwarded-Host") != wantXFH {
						t.Errorf("forwarded host=%q want real %q", res.Header.Get("Seen-X-Forwarded-Host"), wantXFH)
					}
					if res.Header.Get("Seen-X-Forwarded-Proto") != wantProto {
						t.Errorf("forwarded proto=%q want %q", res.Header.Get("Seen-X-Forwarded-Proto"), wantProto)
					}
					if res.Header.Get("Seen-X-Forwarded-For") != "127.0.0.1" {
						t.Errorf("forwarded for=%q want real loopback IP", res.Header.Get("Seen-X-Forwarded-For"))
					}
				})
			}
		}
	}
}

func TestServiceChangedPreserveHost(t *testing.T) {
	a := registry.Service{Name: "app", Type: registry.TypeProxy, Target: "http://127.0.0.1:8080"}
	b := a
	b.PreserveHost = true
	if serviceChanged(a, a) || serviceChanged(b, b) {
		t.Fatal("unchanged service restarted")
	}
	if !serviceChanged(a, b) || !serviceChanged(b, a) {
		t.Fatal("Host policy change must restart the service in either direction")
	}
}
