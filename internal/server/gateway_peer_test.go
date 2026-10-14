package server

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/health"
	"github.com/anydoor7/tslink/internal/registry"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/net/tsdial"
	"tailscale.com/tsd"
	"tailscale.com/tsnet"
)

// Called after the positive peer round trip. The node's resolver seam supplies
// a host-only localhost address for a valid registered name absent from its
// LocalAPI peer list. This models a stale/split DNS result without changing the
// machine's resolver or calling any public DNS server. Real UserDial and its
// SystemDial fallback remain intact, so the old implementation hits the host.
func gatewayAssertNoHostFallback(t *testing.T, node *tsnet.Server) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	hits := make(chan struct{}, 16)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			c, e := ln.Accept()
			if e != nil {
				return
			}
			hits <- struct{}{}
			c.Close()
		}
	}()
	defer func() { ln.Close(); <-done }()
	port := strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
	// Discrimination control: unmodified tsnet UserDial reaches host localhost.
	c, err := node.Dial(ctx, "tcp", net.JoinHostPort("localhost", port))
	if err != nil {
		t.Fatal("host listener control", err)
	}
	c.Close()
	select {
	case <-hits:
	case <-ctx.Done():
		t.Fatal("host listener control not observed")
	}
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip4", "localhost")
	if err != nil || len(ips) == 0 || !ips[0].IsLoopback() {
		t.Fatalf("host DNS control: %v %v", ips, err)
	}
	var resolutions atomic.Int64
	node.Sys().Dialer.Get().SetResolveMagicDNS(func(host, network string) (netip.Addr, bool) {
		resolutions.Add(1)
		if host == "ghost.tailnet.ts.net" {
			return ips[0], true
		}
		return netip.Addr{}, false
	})
	lc, err := node.LocalClient()
	if err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"ghost.tailnet.ts.net", "localhost"} {
		t.Run(host, func(t *testing.T) {
			d := &gatewayRemoteUpstream{node: node, lc: lc, target: "https://" + net.JoinHostPort(host, port)}
			c, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host, port))
			if c != nil {
				c.Close()
			}
			code, _ := registry.ErrorCode(err)
			want := "remote_upstream_not_peer"
			if host == "localhost" {
				want = "portal_upstream_invalid"
			}
			if code != want {
				t.Errorf("non-peer refusal=%q want %q (err=%v)", code, want, err)
			}
			svc := registry.Service{Name: "remote", Type: registry.TypeProxy, Target: d.target, Upstream: "tailnet", PortalOnly: true}
			if result := health.ProbeWithDialer(ctx, svc, d); result == health.Healthy {
				t.Error("host-only target reported healthy")
			}
		})
	}
	if resolutions.Load() != 0 {
		t.Errorf("remote traffic entered UserDial name resolution: %d", resolutions.Load())
	}
	ln.Close()
	<-done
	select {
	case <-hits:
		t.Fatal("remote traffic/probe reached host listener")
	default:
	}
}

type peerTestNode struct{ sys *tsd.System }

func (n peerTestNode) Dial(context.Context, string, string) (net.Conn, error) {
	return nil, errors.New("unexpected UserDial")
}

func (n peerTestNode) Sys() *tsd.System { return n.sys }

func TestGatewayRemotePeerResolution(t *testing.T) {
	var status atomic.Pointer[ipnstate.Status]
	st := taggedGatewayStatus(true)
	for _, p := range st.Peer {
		p.DNSName = "REMOTE.TAILNET.TS.NET."
	}
	status.Store(st)
	var calls atomic.Int64
	var peer atomic.Bool
	peer.Store(true)
	sys := &tsd.System{}
	sys.Dialer.Set(&tsdial.Dialer{UseNetstackForIP: func(ip netip.Addr) bool { return peer.Load() && ip == netip.MustParseAddr("100.64.0.9") }, NetstackDialTCP: func(_ context.Context, ip netip.AddrPort) (net.Conn, error) {
		calls.Add(1)
		if ip.String() != "100.64.0.9:443" {
			t.Error(ip)
		}
		return nil, errors.New("netstack offline")
	}})
	d := &gatewayRemoteUpstream{node: peerTestNode{sys}, lc: remoteLocalClient(&status, nil), target: "https://remote.tailnet.ts.net."}
	_, err := d.DialContext(context.Background(), "tcp", "remote.tailnet.ts.net.:443")
	if err == nil || err.Error() != "netstack offline" || calls.Load() != 1 {
		t.Fatalf("positive peer resolution: %v calls=%d", err, calls.Load())
	}
	peer.Store(false)
	_, err = d.DialContext(context.Background(), "tcp", "remote.tailnet.ts.net.:443")
	code, _ := registry.ErrorCode(err)
	if code != "remote_upstream_not_peer" || calls.Load() != 1 {
		t.Fatalf("disappeared peer: %v calls=%d", err, calls.Load())
	}
	st = taggedGatewayStatus(true)
	st.Peer = nil
	status.Store(st)
	if code := d.check(context.Background()); code != "remote_upstream_not_peer" {
		t.Fatal(code)
	}
}
