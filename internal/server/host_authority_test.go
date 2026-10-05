package server

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/registry"
	"github.com/anydoor7/tslink/internal/testwait"
	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/tailcfg"
)

func authorityTLS(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "public.review.example"}, DNSNames: []string{"public.review.example", "admin.review.example"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, pool
}

// Uses real TLS and upstream listeners, production node assembly and ACL middleware.
// Only tsnet coordination and WhoIs are faked; no real nodes or credentials exist.
func TestProxyHostCannotCrossNodePolicy(t *testing.T) {
	for _, preserve := range []bool{false, true} {
		t.Run(fmt.Sprint(preserve), func(t *testing.T) {
			t.Setenv(config.ConfigDirEnv, t.TempDir())
			if err := config.EnsureDir(); err != nil {
				t.Fatal(err)
			}
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Seen-Host", r.Host)
				w.Header().Set("Seen-XFH", r.Header.Get("X-Forwarded-Host"))
				if r.Host == "admin.review.example" {
					fmt.Fprint(w, "ADMIN-ONLY-FIXTURE")
				} else {
					fmt.Fprint(w, "PUBLIC-FIXTURE")
				}
			}))
			defer backend.Close()
			cert, roots := authorityTLS(t)
			listeners := map[string]net.Listener{}
			for _, name := range []string{"public", "admin"} {
				ln, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				listeners[name] = tls.NewListener(ln, &tls.Config{Certificates: []tls.Certificate{cert}})
				defer listeners[name].Close()
			}
			lc := fakeWhoIsClient(t, &apitype.WhoIsResponse{UserProfile: &tailcfg.UserProfile{LoginName: "alice@example.com"}}, nil)
			old := newTSNetServerFn
			t.Cleanup(func() { newTSNetServerFn = old })
			newTSNetServerFn = func(svc registry.Service, _, _, _ string) tsnetServer {
				return &preserveHostTSNetServer{ln: listeners[svc.Name], fakeTSNetServer: fakeTSNetServer{localClient: lc, dnsName: svc.Name + ".review.example", certDomains: []string{svc.Name + ".review.example"}}}
			}
			path, err := registryPathFn()
			if err != nil {
				t.Fatal(err)
			}
			s, err := New("isolated-fixture", "")
			if err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"public", "admin"} {
				allow := "alice@example.com"
				if name == "admin" {
					allow = "bob@example.com"
				}
				svc := registry.Service{Name: name, Type: registry.TypeProxy, Target: backend.URL, PreserveHost: preserve, AllowedUsers: []string{allow}}
				if _, err := registry.Add(path, svc); err != nil {
					t.Fatal(err)
				}
				if err := s.startNodeLocked(context.Background(), svc); err != nil {
					t.Fatal(err)
				}
				defer s.stopNodeLocked(name)
			}
			request := func(node, host, uri string) (int, string, string, string) {
				t.Helper()
				c, err := tls.Dial("tcp", listeners[node].Addr().String(), &tls.Config{RootCAs: roots, ServerName: node + ".review.example"})
				if err != nil {
					t.Fatal(err)
				}
				defer c.Close()
				_ = c.SetDeadline(time.Now().Add(testwait.Budget(t)))
				if len(c.ConnectionState().VerifiedChains) == 0 {
					t.Fatal("TLS certificate not verified")
				}
				fmt.Fprintf(c, "GET %s HTTP/1.1\r\nHost: %s\r\nX-Forwarded-Host: forged.invalid\r\nConnection: close\r\n\r\n", uri, host)
				resp, err := http.ReadResponse(bufio.NewReader(c), nil)
				if err != nil {
					t.Fatal(err)
				}
				defer resp.Body.Close()
				b, err := io.ReadAll(resp.Body)
				if err != nil {
					t.Fatal(err)
				}
				t.Logf("node=%s TLS SNI=%s requestHost=%s requestURI=%s status=%d backendHost=%s XFH=%s body=%s", node, c.ConnectionState().ServerName, host, uri, resp.StatusCode, resp.Header.Get("Seen-Host"), resp.Header.Get("Seen-XFH"), strings.TrimSpace(string(b)))
				return resp.StatusCode, string(b), resp.Header.Get("Seen-Host"), resp.Header.Get("Seen-XFH")
			}
			t.Run("own_host_control", func(t *testing.T) {
				code, body, _, xfh := request("public", "public.review.example", "/")
				if code != 200 || body != "PUBLIC-FIXTURE" || xfh != "public.review.example" {
					t.Fatal("broken own-host/forged-XFH control")
				}
			})
			t.Run("other_node_acl_control", func(t *testing.T) {
				code, body, _, _ := request("admin", "admin.review.example", "/")
				if code != 403 || strings.Contains(body, "ADMIN-ONLY") {
					t.Fatal("broken other-node ACL control")
				}
			})
			for _, tc := range []struct{ name, host, uri string }{{"unexpected_host", "admin.review.example", "/"}, {"absolute_form", "public.review.example", "https://admin.review.example/"}} {
				t.Run(tc.name, func(t *testing.T) {
					code, body, seenHost, xfh := request("public", tc.host, tc.uri)
					if preserve && (seenHost != "public.review.example" || xfh != "public.review.example") {
						t.Errorf("canonical headers: Host=%s XFH=%s", seenHost, xfh)
					}
					if code != 200 || body != "PUBLIC-FIXTURE" {
						t.Error("client reached admin virtual host through the public node despite admin node ACL denying this identity")
					}
				})
			}
		})
	}
}

func TestProxyHTTP2Authority(t *testing.T) {
	for _, preserve := range []bool{false, true} {
		t.Run(fmt.Sprint(preserve), func(t *testing.T) {
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Seen-Host", r.Host)
				w.Header().Set("Seen-XFH", r.Header.Get("X-Forwarded-Host"))
				if r.Host == "admin.review.example" {
					fmt.Fprint(w, "ADMIN-ONLY-FIXTURE")
				} else {
					fmt.Fprint(w, "PUBLIC-FIXTURE")
				}
			}))
			defer backend.Close()
			proxy, err := NewProxyHandlerWithOptions(backend.URL, nil, ProxyOptions{PreserveHost: preserve, CanonicalHost: func() string { return "public.review.example" }})
			if err != nil {
				t.Fatal(err)
			}
			front := httptest.NewUnstartedServer(proxy)
			front.EnableHTTP2 = true
			front.StartTLS()
			defer front.Close()
			for _, host := range []string{"public.review.example", "admin.review.example"} {
				t.Run(host, func(t *testing.T) {
					req, _ := http.NewRequest("GET", front.URL, nil)
					req.Host = host
					req.Header.Set("X-Forwarded-Host", "forged.invalid")
					resp, err := front.Client().Do(req)
					if err != nil {
						t.Fatal(err)
					}
					defer resp.Body.Close()
					b, _ := io.ReadAll(resp.Body)
					if resp.ProtoMajor != 2 {
						t.Fatal("HTTP/2 not negotiated")
					}
					t.Logf("proto=%s authority=%s backendHost=%s XFH=%s body=%s", resp.Proto, host, resp.Header.Get("Seen-Host"), resp.Header.Get("Seen-XFH"), b)
					wantXFH := host
					if preserve {
						wantXFH = "public.review.example"
					}
					if preserve && resp.Header.Get("Seen-Host") != "public.review.example" {
						t.Error("backend Host is not canonical")
					}
					if resp.Header.Get("Seen-XFH") != wantXFH {
						t.Fatal("forged forwarded host was not replaced")
					}
					if resp.StatusCode != 200 || string(b) != "PUBLIC-FIXTURE" {
						t.Error("HTTP/2 authority selects another backend virtual host")
					}
				})
			}
		})
	}
}
