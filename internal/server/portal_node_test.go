package server

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/registry"
	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/tailcfg"
)

func TestPortalNodeFailurePaths(t *testing.T) {
	for _, kind := range []string{"funnel", "mcp-collision", "directory-file", "default-tag", "credential-control-url", "auth", "up", "up-stall", "local-client", "listener", "cancelled", "interactive"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv(config.ConfigDirEnv, dir)
			s, err := New("fixture", "")
			if err != nil {
				t.Fatal(err)
			}
			p := registry.PortalConfig{Enabled: true, Hostname: "home", Owner: "owner"}
			fake := &fakeTSNetServer{dnsName: "home.tailnet.ts.net", localClient: fakeWhoIsClient(t, &apitype.WhoIsResponse{UserProfile: &tailcfg.UserProfile{LoginName: "owner"}}, nil)}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch kind {
			case "funnel":
				p.Funnel = true
			case "mcp-collision":
				s.mcpControlPlane = &MCPControlPlane{NodeName: "home"}
			case "directory-file":
				if err := os.WriteFile(filepath.Join(dir, "portal-nodes"), []byte("fixture"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "auth":
				s.authKeyProvider = func(context.Context, registry.Service) (string, error) { return "", errors.New("fixture auth failed") }
			case "default-tag":
				s.credentialed = true
				if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"unknown":true}`), 0o600); err != nil {
					t.Fatal(err)
				}
			case "credential-control-url":
				s.credentialed, s.authKeyUserSupplied = true, false
				s.controlURL = "https://control.example.invalid"
			case "interactive":
				s.authKeyProvider = func(context.Context, registry.Service) (string, error) { return "", nil }
			case "up":
				fake.upErr = errors.New("fixture up failed")
			case "up-stall":
				fake.upWait = true
			case "local-client":
				fake.localClient = nil
			case "listener":
				fake.listenTLSErr = errors.New("fixture TLS listener failed")
			case "cancelled":
				cancel()
			}
			run := &portalRun{config: p}
			_, err = s.startPortalNode(ctx, run, func(registry.Service, string, string, string) tsnetServer { return fake }, time.Now, 20*time.Millisecond)
			if err == nil {
				if run.node != nil {
					run.node.close()
				}
				t.Fatal("failed path succeeded")
			}
			if fake.listenFunnelCalled != 0 {
				t.Fatal("Funnel called on failure path")
			}
			if run.node != nil && fake.closeCount.Load() != 1 {
				t.Fatalf("failed node closed %d times", fake.closeCount.Load())
			}
		})
	}
}

func TestPortalClockCapturedAtConstruction(t *testing.T) {
	f := newPortalFixture(t)
	serverNowFn = func() time.Time { panic("handler read mutable clock seam") }
	if len(f.apps(t)) != 1 {
		t.Fatal("initial injected clock not used")
	}
	f.now.Add(int64(time.Hour))
	if len(f.apps(t)) != 0 {
		t.Fatal("captured clock deadline not used")
	}
}

func TestPortalInteractiveEnrollmentAndCancellation(t *testing.T) {
	for _, cancelEnrollment := range []bool{false, true} {
		t.Run(map[bool]string{false: "running", true: "cancelled"}[cancelEnrollment], func(t *testing.T) {
			t.Setenv(config.ConfigDirEnv, t.TempDir())
			s, err := New("", "")
			if err != nil {
				t.Fatal(err)
			}
			fake := &fakeInteractiveTSNetServer{fakeTSNetServer: fakeTSNetServer{
				localClient: fakeWhoIsClient(t, &apitype.WhoIsResponse{UserProfile: &tailcfg.UserProfile{LoginName: "owner"}}, nil),
				certDomains: []string{"home.tailnet.ts.net"},
			}}
			oldFactory, oldStatus := newTSNetServerFn, tsnetStatusClientFn
			newTSNetServerFn = func(svc registry.Service, dir, key, control string) tsnetServer {
				if svc.Ephemeral || key != "" || filepath.Base(dir) != "home" {
					t.Error("interactive portal did not keep its own persistent identity")
				}
				return fake
			}
			entered := make(chan struct{})
			s.authKeyProvider = func(ctx context.Context, _ registry.Service) (string, error) {
				if cancelEnrollment {
					close(entered)
					<-ctx.Done()
					return "", ctx.Err()
				}
				return "", nil
			}
			tsnetStatusClientFn = func(tsnetServer) (tsnetStatusClient, error) {
				return &sequenceTSNetStatusClient{statuses: []*ipnstate.Status{{
					BackendState: "Running", TailscaleIPs: []netip.Addr{netip.MustParseAddr("100.64.0.2")},
					Self: &ipnstate.PeerStatus{DNSName: "home.tailnet.ts.net."},
				}}}, nil
			}
			t.Cleanup(func() { s.closePortal(); newTSNetServerFn = oldFactory; tsnetStatusClientFn = oldStatus })
			s.syncPortal(context.Background(), &registry.PortalConfig{Enabled: true, Hostname: "home", Owner: "owner"})
			run := s.portalRun
			if cancelEnrollment {
				select {
				case <-entered:
				case <-time.After(time.Second):
					t.Fatal("portal enrollment never started")
				}
				s.closePortal()
				if s.portalState.State != "disabled" || s.portalRetryPending.Load() {
					t.Fatal("cancelled enrollment published a stale failure or retry")
				}
			} else {
				select {
				case <-run.done:
				case <-time.After(time.Second):
					t.Fatal("interactive enrollment did not finish")
				}
				if !fake.startCalled || fake.upCalled || s.portalState.URL != "https://home.tailnet.ts.net" {
					t.Fatalf("interactive portal=%+v", s.portalState)
				}
			}
		})
	}
}

func TestPortalCancelledDuringHTTPSetup(t *testing.T) {
	t.Setenv(config.ConfigDirEnv, t.TempDir())
	s, err := New("fixture", "")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fake := &fakeTSNetServer{localClient: fakeWhoIsClient(t, nil, nil)}
	oldHTTP := newHTTPServerFn
	newHTTPServerFn = func(h http.Handler) *http.Server { cancel(); return &http.Server{Handler: h} }
	defer func() { newHTTPServerFn = oldHTTP }()
	run := &portalRun{config: registry.PortalConfig{Enabled: true, Hostname: "home", Owner: "owner"}}
	_, err = s.startPortalNode(ctx, run, func(registry.Service, string, string, string) tsnetServer { return fake }, time.Now, time.Second)
	if !errors.Is(err, context.Canceled) || fake.closeCount.Load() != 1 {
		t.Fatalf("cancelled setup=%v closes=%d", err, fake.closeCount.Load())
	}
}

func TestPortalRequestLimitsListener(t *testing.T) {
	f := newPortalFixture(t)
	node := f.s.portalRun.node
	if node.httpSrv.ReadHeaderTimeout != 10*time.Second || node.httpSrv.IdleTimeout != 60*time.Second || node.httpSrv.ReadTimeout != 0 {
		t.Fatalf("F8 defaults drifted: %+v", node.httpSrv)
	}
	conn, err := net.Dial("tcp", f.addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := conn.Write([]byte("POST / HTTP/1.1\r\nHost: home.tailnet.ts.net\r\nContent-Length: 33554433\r\n\r\n")); err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.Header.Get("Cache-Control") != "private, no-store" || resp.Header.Get("Content-Security-Policy") == "" {
		t.Fatal("limit refusal omitted security headers")
	}
	if resp.StatusCode != 413 {
		t.Fatalf("F8 body cap status=%d", resp.StatusCode)
	}
}

func TestPortalWhoIsStallListener(t *testing.T) {
	f := newPortalFixture(t)
	f.fake.localClient.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) { <-r.Context().Done(); return nil, r.Context().Err() })
	r, _ := http.NewRequest("GET", "http://"+f.addr+"/api/apps", nil)
	r.Host = "home.tailnet.ts.net"
	start := time.Now()
	resp, err := (&http.Client{Timeout: 7 * time.Second}).Do(r)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 403 || time.Since(start) > 6*time.Second {
		t.Fatalf("WhoIs stall status=%d elapsed=%s", resp.StatusCode, time.Since(start))
	}
}

func TestPortalReconcileKeepsAppNodes(t *testing.T) {
	f := newPortalFixture(t)
	photos, payroll := f.s.nodes["photos"], f.s.nodes["secret-payroll"]
	if err := f.s.syncNodes(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.s.nodes["photos"] != photos || f.s.nodes["secret-payroll"] != payroll {
		t.Fatal("enabling portal restarted app nodes")
	}
	if err := registry.DisablePortal(f.path); err != nil {
		t.Fatal(err)
	}
	if err := f.s.syncNodes(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.s.nodes["photos"] != photos || f.s.nodes["secret-payroll"] != payroll {
		t.Fatal("disabling portal restarted app nodes")
	}
	if f.s.portalState.State != "disabled" || f.fake.closeCount.Load() != 1 {
		t.Fatalf("portal not closed: %+v", f.s.portalState)
	}
}

func TestPortalFailureRetriesIndependently(t *testing.T) {
	f := newPortalFixture(t)
	f.s.closePortal()
	f.fake.upErr = errors.New("temporary fixture coordination failure")
	p := &registry.PortalConfig{Enabled: true, Hostname: "home", Owner: "owner"}
	f.s.syncPortal(context.Background(), p)
	<-f.s.portalRun.done
	if f.s.portalState.State != "failed" || !f.s.portalRetryPending.Load() {
		t.Fatalf("failure not retained: %+v", f.s.portalState)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	recovered := &preserveHostTSNetServer{ln: ln, fakeTSNetServer: fakeTSNetServer{localClient: f.fake.localClient, dnsName: "home.tailnet.ts.net", certDomains: []string{"home.tailnet.ts.net"}}}
	newTSNetServerFn = func(registry.Service, string, string, string) tsnetServer { return recovered }
	f.s.syncPortal(context.Background(), p)
	<-f.s.portalRun.done
	if f.s.portalState.State != "running" || f.s.portalRetryPending.Load() {
		t.Fatalf("retry did not recover: %+v", f.s.portalState)
	}
}

func TestPortalAndAppUseSameDecision(t *testing.T) {
	f := newPortalFixture(t)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "APP-FIXTURE") }))
	defer backend.Close()
	svc, err := registry.MutateService(f.path, "photos", func(s registry.Service) (registry.Service, error) { s.Target = backend.URL; return s, nil })
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	appNode := &preserveHostTSNetServer{ln: ln, fakeTSNetServer: fakeTSNetServer{localClient: f.fake.localClient, dnsName: "photos.tailnet.ts.net", certDomains: []string{"photos.tailnet.ts.net"}}}
	newTSNetServerFn = func(registry.Service, string, string, string) tsnetServer { return appNode }
	if err := f.s.startNodeLocked(context.Background(), svc); err != nil {
		t.Fatal(err)
	}
	defer f.s.stopNodeLocked("photos")
	for _, tc := range []struct {
		login string
		tags  []string
		want  int
	}{
		{"owner", nil, 200}, {"admin", nil, 200}, {"alice", nil, 200}, {"stranger", nil, 403}, {"owner", []string{"tag:other"}, 403},
	} {
		f.who.Store(&apitype.WhoIsResponse{UserProfile: &tailcfg.UserProfile{LoginName: tc.login}, Node: &tailcfg.Node{Tags: tc.tags}})
		r, err := http.Get("http://" + ln.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(r.Body)
		r.Body.Close()
		if err != nil || r.StatusCode != tc.want {
			t.Fatalf("app %s: %d %s %v", tc.login, r.StatusCode, body, err)
		}
		visible := false
		for _, a := range f.apps(t) {
			visible = visible || a.Name == "photos"
		}
		if visible != (tc.want == 200) {
			t.Fatalf("portal/app decision differs for %s", tc.login)
		}
	}
	f.who.Store(&apitype.WhoIsResponse{UserProfile: &tailcfg.UserProfile{LoginName: "alice"}})
	f.now.Add(int64(time.Hour))
	if len(f.apps(t)) != 0 {
		t.Fatal("portal retained deadline grant")
	}
	r, err := http.Get("http://" + ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != 403 {
		t.Fatal("app retained deadline grant")
	}
}

func TestPortalTLSListener(t *testing.T) {
	f := newPortalFixture(t)
	f.s.closePortal()
	cert, pool := authorityTLS(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	tlsLn := tls.NewListener(ln, &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})
	fake := &preserveHostTSNetServer{ln: tlsLn, fakeTSNetServer: fakeTSNetServer{localClient: f.fake.localClient, dnsName: "home.tailnet.ts.net", certDomains: []string{"home.tailnet.ts.net"}}}
	newTSNetServerFn = func(registry.Service, string, string, string) tsnetServer { return fake }
	f.s.syncPortal(context.Background(), &registry.PortalConfig{Enabled: true, Hostname: "home", Owner: "owner"})
	<-f.s.portalRun.done
	client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, ServerName: "public.review.example", MinVersion: tls.VersionTLS12}}}
	defer client.CloseIdleConnections()
	r, _ := http.NewRequest("GET", "https://"+ln.Addr().String()+"/api/apps", nil)
	r.Host = "home.tailnet.ts.net"
	resp, err := client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil || resp.StatusCode != 200 || !strings.Contains(string(body), `"name":"photos"`) || strings.Contains(string(body), "secret-payroll") {
		t.Fatalf("TLS response=%d %s %v", resp.StatusCode, body, err)
	}
}
