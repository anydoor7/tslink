package server

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/registry"
	"tailscale.com/derp/derpserver"
	"tailscale.com/net/netns"
	"tailscale.com/net/stun/stuntest"
	"tailscale.com/tailcfg"
	"tailscale.com/tsnet"
	"tailscale.com/tstest/integration/testcontrol"
	"tailscale.com/types/key"
	"tailscale.com/types/logger"
	"tailscale.com/types/nettype"
)

// This portable integration uses an in-process control plane and DERP/STUN,
// with real encrypted tsnet links and real LocalAPI WhoIs. Plain incoming
// listeners isolate admission from certificate issuance (the test control
// plane does not implement public ACME). Remote upstream TLS is real and uses
// a test CA. Production ListenTLS assembly is covered by the gateway fixture.
func TestGatewayRealTSNetRoundTrip(t *testing.T) {
	t.Setenv(config.ConfigDirEnv, t.TempDir())
	t.Setenv("TSLINK_DISABLE_KEYRING", "1")
	for _, name := range []string{"TS_AUTHKEY", "TS_CLIENT_SECRET", "TS_CLIENT_ID", "TS_ID_TOKEN", "TS_AUDIENCE"} {
		t.Setenv(name, "")
	}
	t.Setenv("TS_NO_LOGS_NO_SUPPORT", "true")
	netns.SetEnabled(false)
	t.Cleanup(func() { netns.SetEnabled(true) })
	derpMap := gatewayTestDERP(t)
	control := &testcontrol.Server{DERPMap: derpMap, DNSConfig: &tailcfg.DNSConfig{Proxied: true}, MagicDNSDomain: "tailnet.ts.net", TagOwners: map[string][]string{"tag:gateway": nil}, AllOnline: true, Logf: logger.Discard}
	control.HTTPTestServer = httptest.NewServer(control)
	t.Cleanup(control.HTTPTestServer.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	type node struct {
		s     *tsnet.Server
		ip    netip.Addr
		login string
	}
	up := func(name string, tags []string) node {
		t.Helper()
		s := &tsnet.Server{Dir: filepath.Join(t.TempDir(), name), Hostname: name, ControlURL: control.HTTPTestServer.URL, AuthKey: "hermetic-fixture", AdvertiseTags: tags, Logf: logger.Discard, UserLogf: logger.Discard}
		t.Cleanup(func() { s.Close() })
		st, err := s.Up(ctx)
		if err != nil {
			t.Fatal(name, err)
		}
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			report := s.Sys().MagicSock.Get().GetLastNetcheckReport(ctx)
			if report != nil && report.PreferredDERP != 0 && !s.Sys().HealthTracker.Get().GetDERPRegionReceivedTime(report.PreferredDERP).IsZero() {
				break
			}
			select {
			case <-ctx.Done():
				t.Fatal("hermetic DERP not ready", name, ctx.Err())
			case <-ticker.C:
			}
		}
		return node{s: s, ip: st.TailscaleIPs[0], login: st.User[st.Self.UserID].LoginName}
	}
	gateway := up("gateway", []string{"tag:gateway"})
	allowed := up("allowed", nil)
	other := up("other", nil)
	stranger := up("stranger", nil)
	remote := up("remote", nil)
	lc, err := gateway.s.LocalClient()
	if err != nil {
		t.Fatal(err)
	}
	// Ensure gateway has received peer identity maps before measuring refusals.
	for _, peer := range []node{allowed, other, stranger, remote} {
		for {
			who, err := lc.WhoIs(ctx, net.JoinHostPort(peer.ip.String(), "1234"))
			if err == nil && who.UserProfile != nil {
				if peer.login != "" && peer.login != who.UserProfile.LoginName {
					t.Fatal("identity mismatch")
				}
				break
			}
			select {
			case <-ctx.Done():
				t.Fatal("real WhoIs unavailable", err)
			case <-time.After(10 * time.Millisecond):
			}
		}
	}
	path := filepath.Join(t.TempDir(), "registry.json")
	if err := registry.SetPortal(path, &registry.PortalConfig{Enabled: true, Hostname: "gateway", Owner: "owner", Gateway: &registry.GatewayConfig{Enabled: true, Ports: registry.DefaultGatewayPorts()}}); err != nil {
		t.Fatal(err)
	}
	var hits [3]atomic.Int64
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/payroll") {
			hits[1].Add(1)
		} else {
			hits[0].Add(1)
		}
		io.WriteString(w, "local-ok")
	}))
	defer local.Close()
	cert, roots := gatewayTLSCertificateForNames(t, "remote.tailnet.ts.net")
	remoteLn, err := remote.s.Listen("tcp", ":9443")
	if err != nil {
		t.Fatal(err)
	}
	remoteLC, err := remote.s.LocalClient()
	if err != nil {
		t.Fatal(err)
	}
	remoteHTTP := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		who, err := remoteLC.WhoIs(r.Context(), r.RemoteAddr)
		if err != nil || who.Node == nil || len(who.Node.Tags) != 1 || who.Node.Tags[0] != "tag:gateway" {
			t.Errorf("remote did not authenticate gateway tag: %+v %v", who, err)
			w.WriteHeader(403)
			return
		}
		for k := range r.Header {
			if strings.Contains(strings.ToLower(k), "tailscale") {
				t.Errorf("visitor identity reached remote: %s", k)
			}
		}
		hits[2].Add(1)
		io.WriteString(w, "remote-ok")
	})}
	go remoteHTTP.Serve(tls.NewListener(remoteLn, &tls.Config{Certificates: []tls.Certificate{cert}}))
	t.Cleanup(func() { remoteHTTP.Close() })
	for i, name := range []string{"photos", "payroll", "remote"} {
		target := local.URL + "/" + name
		upstream := ""
		if name == "remote" {
			target = "https://remote.tailnet.ts.net:9443"
			upstream = "tailnet"
		}
		if _, err := registry.Add(path, registry.Service{Name: name, Type: registry.TypeProxy, Target: target, PortalOnly: true, Upstream: upstream, PortalRoute: &registry.PortalRoute{Port: 8443 + i}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := registry.ChangePerson(path, allowed.login, []string{"photos", "remote"}, nil, true, false); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.ChangePerson(path, other.login, []string{"payroll"}, nil, true, false); err != nil {
		t.Fatal(err)
	}
	reg, err := registry.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, svc := range reg.Services {
		authority := net.JoinHostPort("gateway.tailnet.ts.net", strconv.Itoa(svc.PortalRoute.Port))
		gate := &gatewayGate{path: path, svc: svc, lc: lc, now: time.Now, authority: authority, expire: registry.TryExpirePeople}
		var dialer UpstreamDialer = &net.Dialer{}
		if svc.Upstream == "tailnet" {
			d := &gatewayRemoteUpstream{node: gateway.s, lc: lc, target: svc.Target}
			if code := d.check(ctx); code != "" {
				t.Fatal("tagged real gateway refused", code)
			}
			dialer = d
			gate.upstreamCheck = d.check
		}
		handler, err := NewProxyHandlerWithOptions(svc.Target, nil, ProxyOptions{Dialer: dialer, Gateway: &GatewayProxyOptions{Authority: func() string { return authority }, App: svc.Name, Remote: svc.Upstream == "tailnet"}})
		if err != nil {
			t.Fatal(err)
		}
		proxy := handler.(*gatewayProxy)
		proxy.transport.TLSClientConfig = &tls.Config{RootCAs: roots}
		t.Cleanup(func() { proxy.Close() })
		ln, err := gateway.s.Listen("tcp", ":"+strconv.Itoa(svc.PortalRoute.Port))
		if err != nil {
			t.Fatal(err)
		}
		srv := &http.Server{Handler: gate.handler(handler)}
		go srv.Serve(newGatewayListener(ctx, ln, gate))
		t.Cleanup(func() { srv.Close() })
	}
	client := func(peer node) *http.Client {
		tr := &http.Transport{DialContext: peer.s.Dial}
		t.Cleanup(tr.CloseIdleConnections)
		return &http.Client{Transport: tr, Timeout: 10 * time.Second}
	}
	request := func(peer node, port int, want int, text string) {
		t.Helper()
		res, err := client(peer).Get("http://" + net.JoinHostPort(gateway.ip.String(), strconv.Itoa(port)) + "/")
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		if res.StatusCode != want || !strings.Contains(string(b), text) {
			t.Fatalf("real route port=%d status=%d body=%s", port, res.StatusCode, b)
		}
	}
	request(allowed, 8443, 200, "local-ok")
	request(other, 8444, 200, "local-ok")
	request(other, 8443, 403, "no_grant")
	request(allowed, 8444, 403, "no_grant")
	for _, port := range []int{8443, 8444} {
		conn, err := stranger.s.Dial(ctx, "tcp", net.JoinHostPort(gateway.ip.String(), strconv.Itoa(port)))
		if err != nil {
			t.Fatal(err)
		}
		conn.SetDeadline(time.Now().Add(10 * time.Second))
		var b [1]byte
		n, err := conn.Read(b[:])
		conn.Close()
		if n != 0 || !errors.Is(err, io.EOF) {
			t.Fatalf("stranger before accept: bytes=%d err=%v", n, err)
		}
	}
	if hits[0].Load() != 1 || hits[1].Load() != 1 || hits[2].Load() != 0 {
		t.Fatal("denied clients reached backend", hits[0].Load(), hits[1].Load(), hits[2].Load())
	}
	request(allowed, 8445, 200, "remote-ok")
	if hits[2].Load() != 1 {
		t.Fatal("real tsnet remote round trip missing")
	}
	gatewayAssertNoHostFallback(t, gateway.s)
	t.Log("5 real tsnet nodes, 3 route ports; real WhoIs gates; stranger EOF with zero upstream traffic; remote HTTPS through real tsnet Dial authenticates gateway tag. Incoming gateway TLS/certificate issuance is not covered by this plain-listener integration.")
}

// Keep the relay fixture small: importing the upstream integration harness
// would also import its VM/build tooling into every TSLink test binary.
func gatewayTestDERP(t *testing.T) *tailcfg.DERPMap {
	t.Helper()
	relay := derpserver.New(key.NewNode(), logger.Discard)
	api := httptest.NewUnstartedServer(derpserver.Handler(relay))
	api.Config.TLSNextProto = map[string]func(*http.Server, *tls.Conn, http.Handler){}
	api.StartTLS()
	stun, stop := stuntest.ServeWithPacketListener(t, nettype.Std{})
	t.Cleanup(func() { api.CloseClientConnections(); api.Close(); relay.Close(); stop() })
	port := api.Listener.Addr().(*net.TCPAddr).Port
	return &tailcfg.DERPMap{Regions: map[tailcfg.DERPRegionID]*tailcfg.DERPRegion{1: {RegionID: 1, RegionCode: "fixture", Nodes: []*tailcfg.DERPNode{{Name: "fixture", RegionID: 1, HostName: "127.0.0.1", IPv4: "127.0.0.1", IPv6: "none", STUNPort: stun.Port, DERPPort: port, InsecureForTests: true, STUNTestIP: "127.0.0.1"}}}}}
}
