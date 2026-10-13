package server

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/accesslog"
	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/registry"
	tsRuntime "github.com/anydoor7/tslink/internal/runtime"
	"github.com/anydoor7/tslink/internal/testwait"
	"github.com/coder/websocket"
	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/ipn/ipnstate"
)

// One embedded node, with independent TLS listeners keyed by the requested
// tsnet port. Real clients reach those listeners through a loopback dial map.
type gatewayFakeNode struct {
	fakeTSNetServer
	mu    sync.Mutex
	ports map[string]net.Listener
	fail  map[string]bool
	stall map[string]chan struct{}
	cert  tls.Certificate
	stop  chan struct{}
	once  sync.Once
}

func (n *gatewayFakeNode) ListenTLS(_ string, addr string) (net.Listener, error) {
	n.mu.Lock()
	fail, stall := n.fail[addr], n.stall[addr]
	n.mu.Unlock()
	if fail {
		return nil, errors.New("fixture listener failure")
	}
	if stall != nil {
		select {
		case <-stall:
		case <-n.stop:
			return nil, net.ErrClosed
		}
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	ln = tls.NewListener(ln, &tls.Config{GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
		if hello.ServerName != "home.tailnet.ts.net" {
			return nil, errors.New("unknown SNI")
		}
		return &n.cert, nil
	}, MinVersion: tls.VersionTLS12})
	n.mu.Lock()
	defer n.mu.Unlock()
	select {
	case <-n.stop:
		_ = ln.Close()
		return nil, net.ErrClosed
	default:
	}
	if n.ports[addr] != nil {
		_ = ln.Close()
		return nil, errors.New("tsnet: listener already open")
	}
	ln = &gatewayFakeListener{Listener: ln, node: n, port: addr}
	n.ports[addr] = ln
	return ln, nil
}
func (n *gatewayFakeNode) Close() error {
	n.once.Do(func() {
		close(n.stop)
		n.closeCount.Add(1)
		n.mu.Lock()
		listeners := make([]net.Listener, 0, len(n.ports))
		for _, ln := range n.ports {
			listeners = append(listeners, ln)
		}
		n.mu.Unlock()
		for _, ln := range listeners {
			_ = ln.Close()
		}
	})
	return nil
}
func (n *gatewayFakeNode) addr(port string) string {
	n.mu.Lock()
	defer n.mu.Unlock()
	if ln := n.ports[":"+port]; ln != nil {
		return ln.Addr().String()
	}
	return ""
}

type gatewayEvents struct {
	mu     sync.Mutex
	events []accesslog.Event
}

func (w *gatewayEvents) Record(e accesslog.Event) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.events = append(w.events, e)
	return true
}
func (w *gatewayEvents) all() []accesslog.Event {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]accesslog.Event(nil), w.events...)
}

type gatewayFixture struct {
	s           *Server
	node        *gatewayFakeNode
	path        string
	config      *registry.PortalConfig
	pool        *x509.CertPool
	who         atomic.Pointer[apitype.WhoIsResponse]
	calls       atomic.Int64
	whoByRemote sync.Map
	now         atomic.Int64
	backend     [2]atomic.Int64
	seen        chan *http.Request
	events      gatewayEvents
	factories   atomic.Int64
	stallWho    atomic.Bool
	keys        []registry.Service
}

func newGatewayFixture(t *testing.T, setup func(*gatewayFixture)) *gatewayFixture {
	t.Helper()
	f := &gatewayFixture{path: filepath.Join(t.TempDir(), "registry.json"), seen: make(chan *http.Request, 100)}
	t.Setenv(config.ConfigDirEnv, filepath.Dir(f.path))
	t.Setenv("TSLINK_DISABLE_KEYRING", "1")
	f.now.Store(time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC).UnixNano())
	f.who.Store(whoIsUser("alice", "laptop"))
	f.config = &registry.PortalConfig{Enabled: true, Hostname: "home", Owner: "owner", Admins: []string{"admin"}, Gateway: &registry.GatewayConfig{Enabled: true, Ports: registry.DefaultGatewayPorts()}}
	if err := registry.SetPortal(f.path, f.config); err != nil {
		t.Fatal(err)
	}
	for i, name := range []string{"photos", "payroll"} {
		backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			f.backend[i].Add(1)
			f.seen <- r.Clone(r.Context())
			if r.URL.Path == "/ws" {
				c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
				if err != nil {
					return
				}
				defer c.CloseNow()
				for {
					typ, b, err := c.Read(r.Context())
					if err != nil {
						return
					}
					if c.Write(r.Context(), typ, b) != nil {
						return
					}
				}
			}
			if r.URL.Path == "/redirect" {
				w.Header().Set("Location", "http://"+r.Host+"/next?q=1")
				w.Header().Set("Content-Location", "http://"+r.Host+"/content")
				w.Header().Set("Refresh", "0; url=\"http://"+r.Host+"/refresh\"")
				w.Header().Add("Set-Cookie", gatewayCookiePrefix+"csrf=evil")
				w.Header().Add("Set-Cookie", "session=app")
				w.WriteHeader(302)
				return
			}
			_, _ = io.WriteString(w, name)
		}))
		t.Cleanup(backend.Close)
		if _, err := registry.Add(f.path, registry.Service{Name: name, Type: registry.TypeProxy, Target: backend.URL, PortalOnly: true}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := registry.ChangePerson(f.path, "alice", []string{"photos"}, nil, true, false); err != nil {
		t.Fatal(err)
	}
	cert, pool := gatewayTLSCertificate(t)
	f.pool = pool
	lc := &LocalClient{OmitAuth: true, Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		f.calls.Add(1)
		if f.stallWho.Load() {
			<-r.Context().Done()
			return nil, r.Context().Err()
		}
		who := f.who.Load()
		if peer, ok := f.whoByRemote.Load(r.URL.Query().Get("addr")); ok {
			who = peer.(*apitype.WhoIsResponse)
		}
		body, _ := json.Marshal(who)
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(body)))}, nil
	})}
	f.node = &gatewayFakeNode{fakeTSNetServer: fakeTSNetServer{localClient: lc, certDomains: []string{"home.tailnet.ts.net"}, status: &ipnstate.Status{Self: &ipnstate.PeerStatus{ID: "gateway-node-1", DNSName: "home.tailnet.ts.net."}}}, ports: map[string]net.Listener{}, fail: map[string]bool{}, stall: map[string]chan struct{}{}, cert: cert, stop: make(chan struct{})}
	s, err := New("fixture", "")
	if err != nil {
		t.Fatal(err)
	}
	f.s = s
	s.accessWriter = &f.events
	s.authKeyProvider = func(_ context.Context, svc registry.Service) (string, error) {
		f.keys = append(f.keys, svc)
		return "fixture", nil
	}
	oldFactory, oldNow := newTSNetServerFn, serverNowFn
	newTSNetServerFn = func(svc registry.Service, _, _, _ string) tsnetServer {
		f.factories.Add(1)
		if svc.Ephemeral {
			t.Error("gateway factory received ephemeral service")
		}
		return f.node
	}
	serverNowFn = func() time.Time { return time.Unix(0, f.now.Load()) }
	t.Cleanup(func() { s.closePortal(); newTSNetServerFn, serverNowFn = oldFactory, oldNow })
	if setup != nil {
		setup(f)
	}
	s.syncPortal(context.Background(), f.config)
	testwait.Recv(t, s.portalRun.done, "gateway startup")
	if s.portalState.State != "running" {
		t.Fatalf("portal failed: %+v", s.portalState)
	}
	return f
}

func (f *gatewayFixture) waitRoute(t *testing.T, app, state string) tsRuntime.GatewayRouteState {
	t.Helper()
	deadline := time.Now().Add(testwait.Budget(t))
	for time.Now().Before(deadline) {
		for _, r := range f.s.portalRun.routeStates() {
			if r.App == app && r.State == state {
				return r
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("route %s did not reach %s: %+v", app, state, f.s.portalRun.routeStates())
	return tsRuntime.GatewayRouteState{}
}
func (f *gatewayFixture) client(t *testing.T) *http.Client {
	t.Helper()
	tr := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: f.pool, ServerName: "home.tailnet.ts.net", MinVersion: tls.VersionTLS12}, DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
		_, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		return (&net.Dialer{}).DialContext(ctx, network, f.node.addr(port))
	}}
	c := &http.Client{Transport: tr, Timeout: testwait.Budget(t), CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	t.Cleanup(c.CloseIdleConnections)
	return c
}
func gatewayGet(t *testing.T, c *http.Client, port, path string, headers map[string]string) (int, string, http.Header) {
	t.Helper()
	r, _ := http.NewRequest("GET", "https://home.tailnet.ts.net:"+port+path, nil)
	if port == "443" {
		r.Host = "home.tailnet.ts.net"
	}
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	resp, err := c.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(b), resp.Header
}

func TestGatewayTLSGateAndPerRequestRevocation(t *testing.T) {
	f := newGatewayFixture(t, nil)
	f.waitRoute(t, "photos", "running")
	f.waitRoute(t, "payroll", "running")
	f.who.Store(whoIsUser("stranger", "laptop"))
	c, err := net.DialTimeout("tcp", f.node.addr("8443"), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_ = c.SetDeadline(time.Now().Add(7 * time.Second))
	// A TLS ClientHello makes bypassing the gate observable as server TLS bytes.
	tlsC := tls.Client(c, &tls.Config{RootCAs: f.pool, ServerName: "home.tailnet.ts.net", MinVersion: tls.VersionTLS12})
	if err := tlsC.Handshake(); !errors.Is(err, io.EOF) {
		t.Fatalf("stranger handshake must end with EOF, got %v", err)
	}
	_ = c.Close()
	if f.backend[0].Load() != 0 {
		t.Fatal("stranger reached upstream")
	}
	f.who.Store(whoIsUser("alice", "laptop"))
	client := f.client(t)
	code, body, _ := gatewayGet(t, client, "8444", "/", map[string]string{"Accept": "text/html"})
	if code != 403 || (!strings.Contains(body, "You do not have access to this app") || strings.Contains(body, "payroll")) || !strings.Contains(body, "https://home.tailnet.ts.net") {
		t.Fatalf("known visitor deny: %d %s", code, body)
	}
	if f.backend[1].Load() != 0 {
		t.Fatal("denied app reached upstream")
	}
	code, body, _ = gatewayGet(t, client, "8443", "/", nil)
	if code != 200 || body != "photos" {
		t.Fatalf("control: %d %s", code, body)
	}
	before := f.calls.Load()
	if _, err := registry.RemovePerson(f.path, "alice"); err != nil {
		t.Fatal(err)
	}
	code, body, _ = gatewayGet(t, client, "8443", "/", nil)
	if code != 403 || !strings.Contains(body, "revoked") || f.calls.Load() != before+1 || f.backend[0].Load() != 1 {
		t.Fatalf("fresh revocation: code=%d body=%s who=%d backend=%d", code, body, f.calls.Load()-before, f.backend[0].Load())
	}
	f.s.portalRun.routesMu.Lock()
	gateCount := len(f.s.portalRun.routes)
	f.s.portalRun.routesMu.Unlock()
	if gateCount != 2 {
		t.Fatal("routes missing")
	}
	for _, e := range f.events.all() {
		if e.App == "photos" && e.Reason == "revoked" && e.Via == "portal" {
			return
		}
	}
	t.Fatal("revocation event missing")
}

func TestGatewayRegistryFailureAndFreshWhoIs(t *testing.T) {
	f := newGatewayFixture(t, nil)
	f.waitRoute(t, "photos", "running")
	c := f.client(t)
	code, _, _ := gatewayGet(t, c, "8443", "/", nil)
	if code != 200 {
		t.Fatal(code)
	}
	f.who.Store(nil)
	code, body, _ := gatewayGet(t, c, "8443", "/", nil)
	if code != 403 || !strings.Contains(body, "whois_failed") {
		t.Fatalf("fresh whois: %d %s", code, body)
	}
	f.who.Store(whoIsUser("alice", "laptop"))
	before, _ := os.ReadFile(f.path)
	if err := os.WriteFile(f.path, []byte(`{"services":`), 0600); err != nil {
		t.Fatal(err)
	}
	code, body, _ = gatewayGet(t, c, "8443", "/", nil)
	if code != 403 || !strings.Contains(body, "registry_unavailable") {
		t.Fatalf("registry failure: %d %s", code, body)
	}
	if f.backend[0].Load() != 1 {
		t.Fatal("denied paths reached upstream")
	}
	if err := os.WriteFile(f.path, before, 0600); err != nil {
		t.Fatal(err)
	}
	code, _, _ = gatewayGet(t, c, "8443", "/", nil)
	if code != 200 {
		t.Fatal("recovery failed")
	}
}

func TestGatewayOriginHygieneAndFixedTarget(t *testing.T) {
	f := newGatewayFixture(t, nil)
	f.waitRoute(t, "photos", "running")
	c := f.client(t)
	for _, headers := range []map[string]string{{"Sec-Fetch-Site": "same-site", "Sec-Fetch-Mode": "cors"}, {"Origin": "https://home.tailnet.ts.net:8444"}, {"Sec-Fetch-Site": "cross-site", "Upgrade": "websocket", "Connection": "Upgrade"}} {
		code, body, _ := gatewayGet(t, c, "8443", "/", headers)
		if code != 403 || !strings.Contains(body, "cross_origin_blocked") {
			t.Fatalf("origin accepted: %d %s", code, body)
		}
	}
	if f.backend[0].Load() != 0 {
		t.Fatal("cross-origin reached upstream")
	}
	code, _, _ := gatewayGet(t, c, "8443", "/", map[string]string{"X-Tailscale-User-Login": "forged", "Tailscale_User_Login": "forged", "Cookie": gatewayCookiePrefix + "csrf=secret; session=app", "X-Forwarded-Host": "evil", "X-Forwarded-Proto": "http", "X-Forwarded-For": "fake"})
	if code != 200 {
		t.Fatal(code)
	}
	r := <-f.seen
	if r.Header.Get("X-Tailscale-User-Login") != "alice" || r.Header.Get("Tailscale_User_Login") != "" || strings.Contains(r.Header.Get("Cookie"), "secret") || r.Header.Get("Cookie") != "session=app" || r.Header.Get("X-Forwarded-Host") != "home.tailnet.ts.net:8443" || r.Header.Get("X-Forwarded-Proto") != "https" || strings.Contains(r.Header.Get("X-Forwarded-For"), "fake") {
		t.Fatalf("hygiene: %+v", r.Header)
	}
	code, _, h := gatewayGet(t, c, "8443", "/redirect", nil)
	if code != 302 || h.Get("Location") != "https://home.tailnet.ts.net:8443/next?q=1" || h.Get("Content-Location") != "https://home.tailnet.ts.net:8443/content" || h.Get("Refresh") != "0; url=\"https://home.tailnet.ts.net:8443/refresh\"" || len(h.Values("Set-Cookie")) != 1 {
		t.Fatalf("rewrite: %d %+v", code, h)
	}
	// Absolute-form requests and CONNECT are refused by the real HTTP parser/gate.
	for _, line := range []string{"GET http://127.0.0.1:1/ HTTP/1.1", "CONNECT 127.0.0.1:1 HTTP/1.1"} {
		conn, err := tls.Dial("tcp", f.node.addr("8443"), &tls.Config{RootCAs: f.pool, ServerName: "home.tailnet.ts.net", MinVersion: tls.VersionTLS12})
		if err != nil {
			t.Fatal(err)
		}
		_ = conn.SetDeadline(time.Now().Add(testwait.Budget(t)))
		_, _ = fmt.Fprintf(conn, "%s\r\nHost: evil.invalid\r\nConnection: close\r\n\r\n", line)
		resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		conn.Close()
		if resp.StatusCode != 403 {
			t.Fatal("request target accepted", resp.StatusCode)
		}
	}
	if f.backend[0].Load() != 2 {
		t.Fatal("request data reached upstream")
	}
}

func TestGatewayPersistentNodeHotReloadAndWebSocket(t *testing.T) {
	f := newGatewayFixture(t, nil)
	f.waitRoute(t, "photos", "running")
	f.waitRoute(t, "payroll", "running")
	if len(f.keys) != 1 || f.keys[0].Ephemeral {
		t.Fatal("ephemeral requested before key minting")
	}
	f.s.mu.Lock()
	g := f.s.portalState.Gateway
	f.s.mu.Unlock()
	if g == nil || !g.Persistent || g.NodeID != "gateway-node-1" || !g.OwnershipProven {
		t.Fatalf("node proof: %+v", g)
	}
	f.node.mu.Lock()
	var ports []string
	for p := range f.node.ports {
		ports = append(ports, p)
	}
	f.node.mu.Unlock()
	sort.Strings(ports)
	if strings.Join(ports, ",") != ":443,:8443,:8444" {
		t.Fatalf("unexpected listeners: %v", ports)
	}
	if f.node.listenCalled != 0 || f.node.listenFunnelCalled != 0 {
		t.Fatal("gateway opened a non-TLS route listener")
	}
	client := f.client(t)
	ctx, cancel := context.WithTimeout(context.Background(), testwait.Budget(t))
	defer cancel()
	ws, _, err := websocket.Dial(ctx, "wss://home.tailnet.ts.net:8443/ws", &websocket.DialOptions{HTTPClient: client, HTTPHeader: http.Header{"Origin": []string{"https://home.tailnet.ts.net:8443"}}})
	if err != nil {
		t.Fatal(err)
	}
	defer ws.CloseNow()
	old := f.node.addr("8443")
	if _, err := registry.Remove(f.path, "payroll"); err != nil {
		t.Fatal(err)
	}
	f.s.syncPortal(context.Background(), f.config)
	if f.factories.Load() != 1 || f.node.closeCount.Load() != 0 || f.node.addr("8443") != old {
		t.Fatal("route removal replaced gateway or sibling listener")
	}
	if err := ws.Write(ctx, websocket.MessageText, []byte("still open")); err != nil {
		t.Fatal(err)
	}
	_, body, err := ws.Read(ctx)
	if err != nil || string(body) != "still open" {
		t.Fatalf("sibling stream interrupted: %s %v", body, err)
	}
	_ = ws.CloseNow()
	f.s.closePortal()
	// Restart with a fresh fake object while retaining the tsnet state directory.
	prior := f.node
	f.node = &gatewayFakeNode{fakeTSNetServer: fakeTSNetServer{localClient: prior.localClient, certDomains: prior.certDomains, status: prior.status}, ports: map[string]net.Listener{}, fail: map[string]bool{}, stall: map[string]chan struct{}{}, cert: prior.cert, stop: make(chan struct{})}
	f.s.syncPortal(context.Background(), f.config)
	testwait.Recv(t, f.s.portalRun.done, "restart")
	f.s.mu.Lock()
	restarted := f.s.portalState.Gateway
	f.s.mu.Unlock()
	if restarted.NodeID != g.NodeID || restarted.IdentityChanged {
		t.Fatalf("identity changed: %+v", restarted)
	}
}

func TestGatewayListenerFailureIsolation(t *testing.T) {
	f := newGatewayFixture(t, func(f *gatewayFixture) { f.node.fail[":8444"] = true })
	f.waitRoute(t, "photos", "running")
	f.waitRoute(t, "payroll", "failed")
	code, body, _ := gatewayGet(t, f.client(t), "8443", "/", nil)
	if code != 200 || body != "photos" || f.node.closeCount.Load() != 0 {
		t.Fatal("one listener failure closed node")
	}
}

type gatewayFakeListener struct {
	net.Listener
	node *gatewayFakeNode
	port string
}

func (ln *gatewayFakeListener) Close() error {
	err := ln.Listener.Close()
	ln.node.mu.Lock()
	if ln.node.ports[ln.port] == ln {
		delete(ln.node.ports, ln.port)
	}
	ln.node.mu.Unlock()
	return err
}
