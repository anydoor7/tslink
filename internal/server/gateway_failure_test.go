package server

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/filelock"
	"github.com/anydoor7/tslink/internal/registry"
	"github.com/anydoor7/tslink/internal/testwait"
	"tailscale.com/tailcfg"
)

func TestGatewayHostCannotSelectUpstream(t *testing.T) {
	f := newGatewayFixture(t, nil)
	f.waitRoute(t, "photos", "running")
	var diverted atomic.Int64
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { diverted.Add(1); _, _ = io.WriteString(w, "diverted") }))
	defer evil.Close()
	r, _ := http.NewRequest("GET", "https://home.tailnet.ts.net:8443/", nil)
	r.Host = strings.TrimPrefix(evil.URL, "http://")
	resp, err := f.client(t).Do(r)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(body) != "photos" || f.backend[0].Load() != 1 || diverted.Load() != 0 {
		t.Fatalf("Host selected upstream: status=%d body=%s registered=%d diverted=%d", resp.StatusCode, body, f.backend[0].Load(), diverted.Load())
	}
}

func TestGatewayIdentityChangeAndConfigHotReload(t *testing.T) {
	f := newGatewayFixture(t, nil)
	f.waitRoute(t, "photos", "running")
	p := *f.config
	p.Admins = []string{"replacement-admin"}
	g := *p.Gateway
	g.Ports.To = 8500
	p.Gateway = &g
	if err := registry.SetPortal(f.path, &p); err != nil {
		t.Fatal(err)
	}
	f.s.syncPortal(context.Background(), &p)
	if f.factories.Load() != 1 || f.node.closeCount.Load() != 0 {
		t.Fatal("grant metadata restarted gateway")
	}
	f.s.closePortal()
	prior := f.node
	nextStatus := *prior.status
	self := *nextStatus.Self
	self.ID = "gateway-node-2"
	nextStatus.Self = &self
	f.node = &gatewayFakeNode{fakeTSNetServer: fakeTSNetServer{localClient: prior.localClient, certDomains: prior.certDomains, status: &nextStatus}, ports: map[string]net.Listener{}, fail: map[string]bool{}, stall: map[string]chan struct{}{}, cert: prior.cert, stop: make(chan struct{})}
	f.s.syncPortal(context.Background(), &p)
	testwait.Recv(t, f.s.portalRun.done, "changed identity startup")
	f.s.mu.Lock()
	state := f.s.portalState.Gateway
	f.s.mu.Unlock()
	if state == nil || !state.IdentityChanged || !state.OwnershipProven || state.NodeID != "gateway-node-2" {
		t.Fatalf("identity change lost: %+v", state)
	}
}

func TestGatewayConcurrentReloadAndPartialInput(t *testing.T) {
	f := newGatewayFixture(t, nil)
	f.waitRoute(t, "photos", "running")
	f.waitRoute(t, "payroll", "running")
	client := f.client(t)
	var wg sync.WaitGroup
	failures := make(chan error, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 10; i++ {
			resp, err := client.Get("https://home.tailnet.ts.net:8443/")
			if err != nil {
				failures <- err
				return
			}
			b, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			if err != nil || resp.StatusCode != 200 || string(b) != "photos" {
				failures <- fmt.Errorf("concurrent response %d %s %v", resp.StatusCode, b, err)
				return
			}
		}
	}()
	for i := 0; i < 5; i++ {
		if _, err := registry.Remove(f.path, "payroll"); err != nil {
			t.Fatal(err)
		}
		f.s.syncPortal(context.Background(), f.config)
		if _, err := registry.Add(f.path, registry.Service{Name: "payroll", Type: registry.TypeProxy, Target: "http://127.0.0.1:1", PortalOnly: true}); err != nil {
			t.Fatal(err)
		}
		f.s.syncPortal(context.Background(), f.config)
	}
	wg.Wait()
	select {
	case err := <-failures:
		t.Fatal(err)
	default:
	}
	if f.node.closeCount.Load() != 0 || f.factories.Load() != 1 {
		t.Fatal("reload closed node")
	}
	if _, err := registry.MutateService(f.path, "photos", func(s registry.Service) (registry.Service, error) {
		s.RequestLimits = &registry.RequestLimits{HeaderTimeout: "100ms"}
		return s, nil
	}); err != nil {
		t.Fatal(err)
	}
	f.s.syncPortal(context.Background(), f.config)
	f.waitRoute(t, "photos", "running")
	conn, err := tls.Dial("tcp", f.node.addr("8443"), &tls.Config{RootCAs: f.pool, ServerName: "home.tailnet.ts.net", MinVersion: tls.VersionTLS12})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	_, _ = io.WriteString(conn, "GET / HTTP/1.1\r\nHost: home.tailnet.ts.net:8443\r\nX-Stalled:")
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 408 {
		t.Fatalf("partial header not bounded: %d", resp.StatusCode)
	}
	if f.backend[0].Load() != 10 {
		t.Fatalf("partial input reached backend: %d", f.backend[0].Load())
	}
}

func TestGatewaySameOriginMatrix(t *testing.T) {
	for _, tc := range []struct {
		method, mode, site, origin, upgrade string
		want                                bool
	}{
		{"GET", "navigate", "cross-site", "https://evil.invalid", "", true},
		{"HEAD", "navigate", "same-site", "null", "", true},
		{"POST", "navigate", "same-site", "", "", false},
		{"GET", "", "", "", "", true},
		{"POST", "", "", "", "", true},
		{"GET", "cors", "same-site", "", "", false},
		{"POST", "cors", "same-origin", "https://home:8443", "", true},
		{"POST", "", "none", "", "", true},
		{"GET", "", "", "https://home:8443", "", true},
		{"GET", "", "", "https://home:8444", "", false},
		{"POST", "", "", "http://home:8443", "", false},
		{"POST", "", "", "null", "", false},
		{"GET", "navigate", "cross-site", "", "websocket", false},
		{"GET", "", "same-origin", "", "websocket", true},
		{"GET", "", "", "https://home:8443", "websocket", true},
		{"GET", "", "", "https://evil.invalid", "websocket", false},
	} {
		t.Run(fmt.Sprintf("%s_%s_%s_%s_%s", tc.method, tc.mode, tc.site, tc.origin, tc.upgrade), func(t *testing.T) {
			r := httptest.NewRequest(tc.method, "https://home:8443/", nil)
			if tc.mode != "" {
				r.Header.Set("Sec-Fetch-Mode", tc.mode)
			}
			if tc.site != "" {
				r.Header.Set("Sec-Fetch-Site", tc.site)
			}
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			if tc.upgrade != "" {
				r.Header.Set("Upgrade", tc.upgrade)
			}
			if gatewaySameOrigin(r, "https://home:8443") != tc.want {
				t.Fatalf("same origin = %v, want %v", !tc.want, tc.want)
			}
		})
	}
}

func TestGatewayConnectionIdentityTable(t *testing.T) {
	f := newGatewayFixture(t, nil)
	f.waitRoute(t, "photos", "running")
	f.waitRoute(t, "payroll", "running")
	if _, err := registry.MutateService(f.path, "photos", func(s registry.Service) (registry.Service, error) {
		s.AllowedUsers = []string{"legacy@example.com", "tag:allowed"}
		return s, nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		login string
		tags  []string
		want  bool
	}{
		{"owner", nil, true}, {"admin", nil, true}, {"alice", nil, true}, {"legacy@example.com", nil, true}, {"stranger", nil, false},
		{"owner", []string{"tag:other"}, false}, {"tagged", []string{"tag:allowed"}, true}, {"tagged", []string{"tag:other"}, false},
	} {
		t.Run(tc.login+strings.Join(tc.tags, ","), func(t *testing.T) {
			who := whoIsUser(tc.login, "fixture")
			who.Node = &tailcfg.Node{Tags: tc.tags}
			f.who.Store(who)
			for _, port := range []string{"443", "8443", "8444"} {
				c, err := tls.DialWithDialer(&net.Dialer{Timeout: testwait.Budget(t)}, "tcp", f.node.addr(port), &tls.Config{RootCAs: f.pool, ServerName: "home.tailnet.ts.net", MinVersion: tls.VersionTLS12})
				if c != nil {
					c.Close()
				}
				if (err == nil) != tc.want {
					t.Fatalf("%s TLS admitted=%v want=%v", port, err == nil, tc.want)
				}
			}
		})
	}
	if f.backend[0].Load() != 0 || f.backend[1].Load() != 0 {
		t.Fatal("TLS-only test reached backend")
	}
}

func TestGatewayExpiryLatchClockAndNoPassThrough(t *testing.T) {
	f := newGatewayFixture(t, nil)
	f.waitRoute(t, "photos", "running")
	c := f.client(t)
	deadline := time.Unix(0, f.now.Load()).Add(time.Hour)
	if _, err := registry.ChangePerson(f.path, "alice", []string{"photos"}, &deadline, true, true); err != nil {
		t.Fatal(err)
	}
	code, _, _ := gatewayGet(t, c, "8443", "/", nil)
	if code != 200 {
		t.Fatal(code)
	}
	f.now.Add(int64(time.Hour))
	code, body, _ := gatewayGet(t, c, "8443", "/", nil)
	if code != 403 || !strings.Contains(body, "expired") {
		t.Fatalf("expiry: %d %s", code, body)
	}
	reg, err := registry.Load(f.path)
	if err != nil || !reg.People[0].Grants[0].Expired {
		t.Fatal("expiry was not latched", err)
	}
	f.now.Add(-int64(2 * time.Hour))
	code, body, _ = gatewayGet(t, c, "8443", "/", nil)
	if code != 403 || !strings.Contains(body, "expired") {
		t.Fatal("clock rollback reopened access", code, body)
	}
	// A fresh malformed registry must never select the legacy pass-through.
	raw := `{"schema_version":3,"services":[{"name":"photos","type":"proxy","target":"http://localhost:1","portal_route":{"port":8443}}]}`
	if err := os.WriteFile(f.path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	code, body, _ = gatewayGet(t, c, "8443", "/", nil)
	if code != 403 || !strings.Contains(body, "registry_unavailable") || f.backend[0].Load() != 1 {
		t.Fatalf("pass-through: %d %s count=%d", code, body, f.backend[0].Load())
	}
}

func TestGatewayWhoIsTimeouts(t *testing.T) {
	f := newGatewayFixture(t, nil)
	f.waitRoute(t, "photos", "running")
	c := f.client(t)
	code, _, _ := gatewayGet(t, c, "8443", "/", nil)
	if code != 200 {
		t.Fatal(code)
	}
	f.stallWho.Store(true)
	start := time.Now()
	code, body, _ := gatewayGet(t, c, "8443", "/", nil)
	if code != 403 || !strings.Contains(body, "whois_failed") || time.Since(start) < 4900*time.Millisecond || time.Since(start) > 8*time.Second {
		t.Fatalf("request timeout: %d %s %s", code, body, time.Since(start))
	}
	conn, err := net.Dial("tcp", f.node.addr("8443"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(8 * time.Second))
	start = time.Now()
	var b [1]byte
	n, err := conn.Read(b[:])
	if n != 0 || !errors.Is(err, io.EOF) || time.Since(start) > 7*time.Second {
		t.Fatalf("connection timeout sent data or stalled: %d %v %s", n, err, time.Since(start))
	}
	if f.backend[0].Load() != 1 {
		t.Fatal("timeout reached upstream")
	}
}

func TestGatewayStalledListenerDoesNotCloseNode(t *testing.T) {
	f := newGatewayFixture(t, func(f *gatewayFixture) {
		// Enroll and persist the shared node before measuring route timeout.
		// A 100ms node deadline also times filesystem work under -race.
		_, err := registry.MutateService(f.path, "payroll", func(s registry.Service) (registry.Service, error) {
			s.PortalOnly, s.PortalRoute = false, nil
			return s, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		f.node.stall[":8444"] = make(chan struct{})
	})
	f.waitRoute(t, "photos", "running")
	// The running-state barrier proves the only existing activation has read
	// its timeout. Keep the original 100ms deadline for the new stalled route.
	f.s.portalRun.timeout = 100 * time.Millisecond
	if err := registry.SetPortalRoute(context.Background(), f.path, "payroll", &registry.PortalRoute{Port: 8444}); err != nil {
		t.Fatal(err)
	}
	f.s.syncGatewayRoutes(f.s.portalRun)
	r := f.waitRoute(t, "payroll", "failed")
	if r.Reason != "route_listener_failed" {
		t.Fatal(r)
	}
	code, _, _ := gatewayGet(t, f.client(t), "8443", "/", nil)
	if code != 200 || f.node.closeCount.Load() != 0 {
		t.Fatal("stalled route killed node")
	}
	// Disable closes the shared owner exactly once and unblocks the stuck API.
	f.s.closePortal()
	if f.node.closeCount.Load() != 1 {
		t.Fatal("shared node did not close once")
	}
}

func TestGatewayOfflineAndPreserveHost(t *testing.T) {
	f := newGatewayFixture(t, nil)
	f.waitRoute(t, "photos", "running")
	_, err := registry.MutateService(f.path, "photos", func(s registry.Service) (registry.Service, error) { s.PreserveHost = true; return s, nil })
	if err != nil {
		t.Fatal(err)
	}
	f.s.syncPortal(context.Background(), f.config)
	f.waitRoute(t, "photos", "running")
	c := f.client(t)
	code, _, _ := gatewayGet(t, c, "8443", "/", nil)
	if code != 200 {
		t.Fatal(code)
	}
	r := <-f.seen
	if r.Host != "home.tailnet.ts.net:8443" {
		t.Fatalf("preserve_host lost port: %s", r.Host)
	}
	_, err = registry.MutateService(f.path, "photos", func(s registry.Service) (registry.Service, error) { s.Target = "http://127.0.0.1:1"; return s, nil })
	if err != nil {
		t.Fatal(err)
	}
	f.s.syncPortal(context.Background(), f.config)
	f.waitRoute(t, "photos", "running")
	c.CloseIdleConnections()
	code, body, _ := gatewayGet(t, c, "8443", "/", map[string]string{"Accept": "text/html"})
	if code != 502 || !strings.Contains(body, "photos on this host is not reachable right now") || strings.Contains(body, "127.0.0.1") {
		t.Fatalf("offline: %d %s", code, body)
	}
	state := f.waitRoute(t, "photos", "running")
	if state.Health != "down" || state.OfflineSince == nil || state.OfflineSince.Year() != 2030 {
		t.Fatalf("offline observation: %+v", state)
	}
	code, body, _ = gatewayGet(t, c, "8443", "/", nil)
	if code != 502 || !strings.Contains(body, "upstream_down") {
		t.Fatal(code, body)
	}
	for _, e := range f.events.all() {
		if e.Reason == "upstream_down" && e.Via == "portal" {
			return
		}
	}
	t.Fatal("offline event missing")
}

func TestGatewayPortalOnlyFileAndDashboard(t *testing.T) {
	f := newGatewayFixture(t, nil)
	f.waitRoute(t, "photos", "running")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("file fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Add(f.path, registry.Service{Name: "files", Type: registry.TypeFile, Path: dir, PortalOnly: true}); err != nil {
		t.Fatal(err)
	}
	f.s.syncPortal(context.Background(), f.config)
	f.waitRoute(t, "files", "running")
	f.who.Store(whoIsUser("owner", "fixture"))
	c := f.client(t)
	code, body, _ := gatewayGet(t, c, "8445", "/hello.txt", nil)
	if code != 200 || body != "file fixture" {
		t.Fatal(code, body)
	}
	code, body, _ = gatewayGet(t, c, "443", "/", nil)
	if code != 200 || !strings.Contains(body, "this host") || !strings.Contains(body, "https://home.tailnet.ts.net:8445/") {
		t.Fatalf("dashboard: %d %s", code, body)
	}
	f.who.Store(whoIsUser("alice", "fixture"))
	code, body, _ = gatewayGet(t, c, "443", "/api/apps", nil)
	if code != 200 || strings.Contains(body, "payroll") || strings.Contains(body, "files") || !strings.Contains(body, "https://home.tailnet.ts.net:8443/") {
		t.Fatalf("directory disclosure: %d %s", code, body)
	}
	// Drive actual daemon reconciliation: portal-only apps must not create nodes.
	if err := f.s.syncNodes(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.factories.Load() != 1 || len(f.s.nodes) != 0 {
		t.Fatalf("portal-only created app nodes: factories=%d nodes=%d", f.factories.Load(), len(f.s.nodes))
	}
}

func TestGatewayOwnershipFailureAndRecovery(t *testing.T) {
	for _, failure := range []string{"missing-id", "corrupt-ledger", "writer-busy", "lock-file-unwritable"} {
		t.Run(failure, func(t *testing.T) {
			f := newGatewayFixture(t, nil)
			f.waitRoute(t, "photos", "running")
			f.s.closePortal()
			prior := f.node
			resetNode := func() {
				status := *prior.status
				self := *status.Self
				status.Self = &self
				f.node = &gatewayFakeNode{fakeTSNetServer: fakeTSNetServer{localClient: prior.localClient, certDomains: prior.certDomains, status: &status}, ports: map[string]net.Listener{}, fail: map[string]bool{}, stall: map[string]chan struct{}{}, cert: prior.cert, stop: make(chan struct{})}
			}
			resetNode()
			path := filepath.Join(filepath.Dir(f.path), "portal-nodes", "home", "ownership.json")
			repair := func() {}
			switch failure {
			case "missing-id":
				f.node.status.Self.ID = ""
			case "corrupt-ledger":
				before, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(`{"nodes":`), 0600); err != nil {
					t.Fatal(err)
				}
				repair = func() {
					if err := os.WriteFile(path, before, 0600); err != nil {
						t.Error(err)
					}
				}
			case "writer-busy":
				lock, err := os.OpenFile(path+".lock", os.O_RDWR, 0600)
				if err != nil {
					t.Fatal(err)
				}
				if ok, err := filelock.TryLock(lock); !ok || err != nil {
					t.Fatal("lock control", ok, err)
				}
				var once sync.Once
				repair = func() { once.Do(func() { _ = filelock.Unlock(lock); _ = lock.Close() }) }
			case "lock-file-unwritable":
				if err := os.Remove(path + ".lock"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(path+".lock", 0700); err != nil {
					t.Fatal(err)
				}
				repair = func() { _ = os.Remove(path + ".lock") }
			}
			t.Cleanup(repair)
			f.s.syncPortal(context.Background(), f.config)
			testwait.Recv(t, f.s.portalRun.done, "ownership refusal must not wait for a writer")
			f.s.mu.Lock()
			state := f.s.portalState
			f.s.mu.Unlock()
			if state.State != "failed" || len(f.node.ports) != 0 || f.node.closeCount.Load() != 1 || f.backend[0].Load() != 0 {
				t.Fatalf("unproven gateway served: state=%+v ports=%d closes=%d requests=%d", state, len(f.node.ports), f.node.closeCount.Load(), f.backend[0].Load())
			}
			repair()
			resetNode()
			f.s.syncPortal(context.Background(), f.config)
			testwait.Recv(t, f.s.portalRun.done, "repaired gateway startup")
			f.waitRoute(t, "photos", "running")
			code, _, _ := gatewayGet(t, f.client(t), "8443", "/", nil)
			if code != 200 {
				t.Fatal("repair control", code)
			}
		})
	}
}

func TestGatewayUnreadableRegistryAndExpiryWriterContention(t *testing.T) {
	f := newGatewayFixture(t, nil)
	f.waitRoute(t, "photos", "running")
	c := f.client(t)
	deadline := time.Unix(0, f.now.Load()).Add(time.Hour)
	if _, err := registry.ChangePerson(f.path, "alice", []string{"photos"}, &deadline, true, true); err != nil {
		t.Fatal(err)
	}
	code, _, _ := gatewayGet(t, c, "8443", "/", nil)
	if code != 200 {
		t.Fatal(code)
	}
	if err := os.Rename(f.path, f.path+".saved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(f.path, 0700); err != nil {
		t.Fatal(err)
	}
	code, body, _ := gatewayGet(t, c, "8443", "/", nil)
	if code != 403 || !strings.Contains(body, "registry_unavailable") || f.backend[0].Load() != 1 {
		t.Fatalf("unreadable registry: %d %s", code, body)
	}
	if err := os.Remove(f.path); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(f.path+".saved", f.path); err != nil {
		t.Fatal(err)
	}
	lock, err := os.OpenFile(f.path+".lock", os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := filelock.TryLock(lock); !ok || err != nil {
		t.Fatal("lock control", ok, err)
	}
	var once sync.Once
	unlock := func() { once.Do(func() { _ = filelock.Unlock(lock); _ = lock.Close() }) }
	t.Cleanup(unlock)
	f.now.Add(int64(time.Hour))
	code, body, _ = gatewayGet(t, c, "8443", "/", nil)
	if code != 403 || !strings.Contains(body, "registry_unavailable") || f.backend[0].Load() != 1 {
		t.Fatalf("busy expiry persistence: %d %s", code, body)
	}
	unlock()
	code, body, _ = gatewayGet(t, c, "8443", "/", nil)
	if code != 403 || !strings.Contains(body, "expired") || f.backend[0].Load() != 1 {
		t.Fatalf("expiry recovery: %d %s", code, body)
	}
	reg, err := registry.Load(f.path)
	if err != nil || !reg.People[0].Grants[0].Expired {
		t.Fatal("expiry not persisted after unlock", err)
	}
}
