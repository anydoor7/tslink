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
	"golang.org/x/net/dns/dnsmessage"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/net/tsdial"
	"tailscale.com/tsd"
	"tailscale.com/tsnet"
)

// Called from the real-tsnet fixture after the positive peer round trip.
// A private DNS responder makes a valid but absent MagicDNS name resolve on
// the host to a real listener; no public DNS or live tailnet is involved.
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
	// Discrimination control: UserDial really can reach the host-only target.
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
	dns, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var lookups atomic.Int64
	dnsDone := make(chan struct{})
	go func() {
		defer close(dnsDone)
		b := make([]byte, 2048)
		for {
			n, addr, err := dns.ReadFrom(b)
			if err != nil {
				return
			}
			lookups.Add(1)
			var q dnsmessage.Message
			if q.Unpack(b[:n]) != nil {
				continue
			}
			reply := dnsmessage.Message{Header: dnsmessage.Header{ID: q.ID, Response: true, RecursionAvailable: true}, Questions: q.Questions}
			for _, question := range q.Questions {
				if question.Type == dnsmessage.TypeA {
					reply.Answers = append(reply.Answers, dnsmessage.Resource{Header: dnsmessage.ResourceHeader{Name: question.Name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: 1}, Body: &dnsmessage.AResource{A: [4]byte{127, 0, 0, 1}}})
				}
			}
			data, _ := reply.Pack()
			dns.WriteTo(data, addr)
		}
	}()
	defer func() { dns.Close(); <-dnsDone }()
	old := net.DefaultResolver
	net.DefaultResolver = &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "udp", dns.LocalAddr().String())
	}}
	defer func() { net.DefaultResolver = old }()
	// Prove that this host resolver can resolve the absent name.
	ips, err := net.DefaultResolver.LookupHost(ctx, "ghost.tailnet.ts.net")
	if err != nil || len(ips) == 0 || ips[0] != "127.0.0.1" {
		t.Fatalf("DNS control: %v %v", ips, err)
	}
	before := lookups.Load()
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
			result := health.ProbeWithDialer(ctx, svc, d)
			if result == health.Healthy {
				t.Error("host-only target reported healthy")
			}
		})
	}
	if lookups.Load() != before {
		t.Errorf("remote traffic consulted host DNS: %d", lookups.Load()-before)
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
