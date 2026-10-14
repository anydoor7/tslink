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
	sys.Dialer.Set(&tsdial.Dialer{UseNetstackForIP: func(ip netip.Addr) bool { return ip == netip.MustParseAddr("100.64.0.9") }, NetstackDialTCP: func(ctx context.Context, a netip.AddrPort) (net.Conn, error) { return f(ctx, "tcp", a.String()) }})
	return sys
}

func remoteLocalClient(st *atomic.Pointer[ipnstate.Status], fail *atomic.Bool) *LocalClient {
	return &LocalClient{OmitAuth: true, Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		if fail != nil && fail.Load() {
			return nil, errors.New("status unavailable")
		}
		body, _ := json.Marshal(st.Load())
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
	for _, name := range []string{"remote", "home", "wrong"} {
		peers[key.NewNode().Public()] = &ipnstate.PeerStatus{DNSName: name + ".tailnet.ts.net.", TailscaleIPs: []netip.Addr{netip.MustParseAddr("100.64.0.9")}}
	}
	return &ipnstate.Status{Peer: peers, Self: &ipnstate.PeerStatus{Tags: &v}, CurrentTailnet: &ipnstate.TailnetStatus{MagicDNSSuffix: "tailnet.ts.net"}}
}

func TestGatewayRemoteDialBoundary(t *testing.T) {
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
	var dials atomic.Int64
	var offline atomic.Bool
	f := newGatewayFixture(t, func(f *gatewayFixture) {
		original := f.node.localClient.Transport
		f.node.localClient.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			if strings.HasSuffix(r.URL.Path, "/status") {
				return remoteLocalClient(&st, nil).Transport.RoundTrip(r)
			}
			return original.RoundTrip(r)
		})
		f.node.dial = func(ctx context.Context, n, a string) (net.Conn, error) {
			dials.Add(1)
			if a != "100.64.0.9:443" {
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
