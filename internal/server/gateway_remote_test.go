package server

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"github.com/anydoor7/tslink/internal/health"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/registry"
	tsRuntime "github.com/anydoor7/tslink/internal/runtime"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/net/tsdial"
	"tailscale.com/tsd"
	"tailscale.com/types/key"
	"tailscale.com/types/views"
)

type remoteDialFunc func(context.Context, string, string) (net.Conn, error)

func (f remoteDialFunc) Dial(ctx context.Context, n, a string) (net.Conn, error) { return f(ctx, n, a) }
func (f remoteDialFunc) Sys() *tsd.System {
	sys := &tsd.System{}
	sys.Dialer.Set(&tsdial.Dialer{UseNetstackForIP: func(ip netip.Addr) bool {
		return ip == netip.MustParseAddr("100.64.0.9") || ip == netip.MustParseAddr("100.64.0.10") || ip == netip.MustParseAddr("100.64.0.11")
	}, NetstackDialTCP: func(ctx context.Context, a netip.AddrPort) (net.Conn, error) { return f(ctx, "tcp", a.String()) }})
	return sys
}

func remoteLocalClient(st *atomic.Pointer[ipnstate.Status], fail *atomic.Bool) *LocalClient {
	return &LocalClient{OmitAuth: true, Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		if fail != nil && fail.Load() {
			return nil, errors.New("status unavailable")
		}
		status := *st.Load()
		if r.URL.Query().Get("peers") == "false" {
			status.Peer = nil
		}
		body, _ := json.Marshal(&status)
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(body)))}, nil
	})}
}
func taggedGatewayStatus(tagged bool) *ipnstate.Status {
	tags := []string{}
	if tagged {
		tags = append(tags, "tag:gateway")
	}
	v := views.SliceOf(tags)
	peers := map[key.NodePublic]*ipnstate.PeerStatus{}
	for i, name := range []string{"remote", "home", "wrong"} {
		peers[key.NewNode().Public()] = &ipnstate.PeerStatus{DNSName: name + ".tailnet.ts.net.", TailscaleIPs: []netip.Addr{netip.AddrFrom4([4]byte{100, 64, 0, byte(9 + i)})}}
	}
	return &ipnstate.Status{Peer: peers, Self: &ipnstate.PeerStatus{Tags: &v}, CurrentTailnet: &ipnstate.TailnetStatus{MagicDNSSuffix: "tailnet.ts.net"}}
}

func TestGatewayRemoteUnchangedTagDoesNotLockServer(t *testing.T) {
	t.Setenv("TSLINK_CONFIG_DIR", t.TempDir())
	run := &portalRun{node: &mcpControlPlaneNode{}}
	s := &Server{portalRun: run, portalState: tsRuntime.PortalState{Gateway: &tsRuntime.GatewayState{}}}
	d := s.remoteGatewayUpstream(run, registry.Service{})
	for _, tagged := range []bool{false, true, false} {
		// A changed observation must reach the published state too.
		d.observe(tagged)
		if s.portalState.Gateway.Tagged != tagged {
			t.Fatal("changed tag was not published")
		}
		s.mu.Lock()
		done := make(chan struct{})
		go func() { d.observe(tagged); close(done) }()
		select {
		case <-done:
			s.mu.Unlock()
		case <-time.After(2 * time.Second):
			s.mu.Unlock()
			<-done
			t.Fatal("unchanged tag waited for Server.mu")
		}
	}
}

func TestGatewayRemoteDialBoundary(t *testing.T) {
	// A host-dial mutation must not issue real DNS queries from this fixture.
	resolver := net.DefaultResolver
	net.DefaultResolver = &net.Resolver{PreferGo: true, Dial: func(context.Context, string, string) (net.Conn, error) {
		return nil, errors.New("host resolver forbidden in gateway fixture")
	}}
	t.Cleanup(func() { net.DefaultResolver = resolver })
	var calls atomic.Int64
	var status atomic.Pointer[ipnstate.Status]
	var failed atomic.Bool
	status.Store(taggedGatewayStatus(true))
	d := &gatewayRemoteUpstream{lc: remoteLocalClient(&status, &failed), target: "https://remote.tailnet.ts.net", node: remoteDialFunc(func(ctx context.Context, n, a string) (net.Conn, error) {
		calls.Add(1)
		if n != "tcp" || a != "100.64.0.9:443" {
			t.Errorf("destination=%s/%s", n, a)
		}
		aConn, bConn := net.Pipe()
		_ = bConn.Close()
		return aConn, nil
	})}
	conn, err := d.DialContext(context.Background(), "tcp", "remote.tailnet.ts.net:443")
	if err != nil || calls.Load() != 1 {
		t.Fatalf("spy control: calls=%d err=%v", calls.Load(), err)
	}
	conn.Close()
	for _, tt := range []struct {
		name, target, network, address, code string
		tagged, fail, noDial                 bool
	}{
		{name: "untagged", target: d.target, network: "tcp", address: "remote.tailnet.ts.net:443", code: "remote_upstream_requires_tag"},
		{name: "foreign", target: "https://remote.foreign.ts.net", network: "tcp", address: "remote.foreign.ts.net:443", tagged: true, code: "remote_upstream_outside_tailnet"},
		{name: "injected-address", target: d.target, network: "tcp", address: "attacker.tailnet.ts.net:443", tagged: true, code: "portal_upstream_invalid"},
		{name: "udp", target: d.target, network: "udp", address: "remote.tailnet.ts.net:443", tagged: true, code: "portal_upstream_invalid"},
		{name: "status-failed", target: d.target, network: "tcp", address: "remote.tailnet.ts.net:443", tagged: true, fail: true, code: "remote_upstream_unavailable"},
		{name: "missing-dial", target: d.target, network: "tcp", address: "remote.tailnet.ts.net:443", tagged: true, noDial: true, code: "remote_upstream_unavailable"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			status.Store(taggedGatewayStatus(tt.tagged))
			failed.Store(tt.fail)
			copy := *d
			copy.target = tt.target
			if tt.noDial {
				copy.node = nil
			}
			before := calls.Load()
			c, err := copy.DialContext(context.Background(), tt.network, tt.address)
			code, _ := registry.ErrorCode(err)
			if c != nil || code != tt.code || calls.Load() != before {
				t.Fatalf("refusal: conn=%v code=%s calls=%d", c, code, calls.Load())
			}
		})
	}
	status.Store(taggedGatewayStatus(true))
	failed.Store(false)
	d.node = remoteDialFunc(func(ctx context.Context, _, _ string) (net.Conn, error) { <-ctx.Done(); return nil, ctx.Err() })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := d.DialContext(ctx, "tcp", "remote.tailnet.ts.net:443"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
}

// A subprocess supplies a hermetic root CA without changing the parent's
// process-wide system root cache. The child runs the production transports.
func TestGatewayRemoteTrafficAndHealth(t *testing.T) {
	if os.Getenv("TSLINK_TEST_REMOTE_ROOTS") != "1" {
		exe, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(exe, "-test.run=^TestGatewayRemoteTrafficAndHealth$", "-test.v", "-test.timeout=90s")
		cmd.Env = append(os.Environ(), "TSLINK_TEST_REMOTE_ROOTS=1", "GODEBUG=x509usefallbackroots=1")
		out, err := cmd.CombinedOutput()
		t.Log(string(out))
		if err != nil {
			t.Fatal(err)
		}
		return
	}
	cert, roots := gatewayTLSCertificate(t)
	x509.SetFallbackRoots(roots)
	var seen atomic.Int64
	upstream := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.Add(1)
		if r.Header.Get("X-Forwarded-For") != "" {
			t.Error("remote received visitor IP claim")
		}
		for key := range r.Header {
			k := strings.ToLower(strings.ReplaceAll(key, "_", "-"))
			if strings.HasPrefix(k, "x-tailscale-") || strings.HasPrefix(k, "tailscale-") {
				t.Errorf("remote received identity claim: %s", key)
			}
		}
		io.WriteString(w, "remote-ok")
	}))
	upstream.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
	upstream.StartTLS()
	defer upstream.Close()
	var st atomic.Pointer[ipnstate.Status]
	st.Store(taggedGatewayStatus(true))
	var dials, fullStatus, leanStatus atomic.Int64
	var expectedIP atomic.Value
	expectedIP.Store("100.64.0.10:443")
	var offline atomic.Bool
	f := newGatewayFixture(t, func(f *gatewayFixture) {
		original := f.node.localClient.Transport
		f.node.localClient.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			if strings.HasSuffix(r.URL.Path, "/status") {
				if r.URL.Query().Get("peers") == "false" {
					leanStatus.Add(1)
				} else {
					fullStatus.Add(1)
				}
				return remoteLocalClient(&st, nil).Transport.RoundTrip(r)
			}
			return original.RoundTrip(r)
		})
		f.node.dial = func(ctx context.Context, n, a string) (net.Conn, error) {
			dials.Add(1)
			if a != expectedIP.Load().(string) {
				t.Errorf("unexpected remote destination %q", a)
			}
			if offline.Load() {
				return nil, errors.New("offline")
			}
			return (&net.Dialer{}).DialContext(ctx, n, upstream.Listener.Addr().String())
		}
		reg, err := registry.Load(f.path)
		if err != nil {
			t.Fatal(err)
		}
		svc := reg.Services[0]
		svc.Upstream = "tailnet"
		svc.Target = "https://home.tailnet.ts.net"
		svc.Health = &registry.HealthConfig{Interval: "10s", Timeout: "1s"}
		if _, err := registry.Add(f.path, svc); err != nil {
			t.Fatal(err)
		}
	})
	f.waitRoute(t, "photos", "running")
	recorder := health.NewRecorder(filepath.Join(filepath.Dir(f.path), "health-fixture.json"), health.NotifierConfig{})
	now := time.Unix(0, f.now.Load())
	cycle := func() {
		f.now.Store(now.UnixNano())
		f.s.healthCycle(context.Background(), recorder, now, health.Probe)
		now = now.Add(2 * time.Minute)
	}
	cycle()
	var state string
	for _, r := range f.s.portalRun.routeStates() {
		if r.App == "photos" {
			state = r.Health
			if r.HealthChecked == nil {
				t.Fatal("remote probe not checked")
			}
		}
	}
	if state != health.Healthy || dials.Load() != 1 || seen.Load() != 1 {
		t.Fatalf("periodic remote control: state=%s dials=%d seen=%d", state, dials.Load(), seen.Load())
	}
	c := f.client(t)
	code, body, _ := gatewayGet(t, c, "8443", "/", map[string]string{"X-Tailscale-User-Login": "forged", "Tailscale_User_Login": "forged"})
	if code != 200 || body != "remote-ok" {
		t.Fatal(code, body)
	}
	// A second request uses the existing upstream connection, but still checks
	// the gateway tag and suffix without fetching the peer list.
	beforeFull, beforeLean, beforeDials := fullStatus.Load(), leanStatus.Load(), dials.Load()
	if beforeFull == 0 || beforeDials == 0 {
		t.Fatal("positive control did not fetch peers and dial")
	}
	code, body, _ = gatewayGet(t, c, "8443", "/", nil)
	if code != 200 || body != "remote-ok" || dials.Load() != beforeDials || fullStatus.Load() != beforeFull || leanStatus.Load() != beforeLean+1 {
		t.Fatalf("pooled request: status=%d body=%s dials=%d->%d full=%d->%d lean=%d->%d", code, body, beforeDials, dials.Load(), beforeFull, fullStatus.Load(), beforeLean, leanStatus.Load())
	}
	before := seen.Load()
	st.Store(taggedGatewayStatus(false))
	code, body, _ = gatewayGet(t, c, "8443", "/", nil)
	if code != 503 || !strings.Contains(body, "remote_upstream_requires_tag") || seen.Load() != before {
		t.Fatalf("tag removal reused pooled upstream: %d %s hits=%d", code, body, seen.Load())
	}
	cycle()
	f.s.mu.Lock()
	tagged := f.s.portalState.Gateway.Tagged
	f.s.mu.Unlock()
	if tagged {
		t.Fatal("runtime retained tagged identity")
	}
	for _, r := range f.s.portalRun.routeStates() {
		if r.App == "photos" && r.Reason != "remote_upstream_requires_tag" {
			t.Fatalf("missing reason: %+v", r)
		}
	}
	st.Store(taggedGatewayStatus(true))
	cycle()
	offline.Store(true)
	for i := 0; i < 3; i++ {
		cycle()
	}
	for _, r := range f.s.portalRun.routeStates() {
		if r.App == "photos" && (r.Health != health.Down || r.OfflineSince == nil) {
			t.Fatalf("remote offline not observed: %+v", r)
		}
	}
	offline.Store(false)
	cycle()
	// Name mismatch reaches the same TLS peer but must fail normal verification.
	reg, err := registry.Load(f.path)
	if err != nil {
		t.Fatal(err)
	}
	svc := reg.Services[0]
	svc.Target = "https://wrong.tailnet.ts.net"
	expectedIP.Store("100.64.0.11:443")
	if _, err := registry.Add(f.path, svc); err != nil {
		t.Fatal(err)
	}
	f.s.syncGatewayRoutes(f.s.portalRun)
	f.waitRoute(t, "photos", "running")
	before = seen.Load()
	code, body, _ = gatewayGet(t, f.client(t), "8443", "/", nil)
	if code != 502 || !strings.Contains(body, "upstream_down") || seen.Load() != before {
		t.Fatalf("TLS name mismatch accepted: %d %s", code, body)
	}
}
