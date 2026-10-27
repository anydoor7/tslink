package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/registry"
	"github.com/anydoor7/tslink/internal/testwait"
	"tailscale.com/tailcfg"
)

func gatewayEditRegistry(t *testing.T, path string, edit func(map[string]json.RawMessage)) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	edit(doc)
	data, err = json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func gatewayAssertClosed(t *testing.T, f *gatewayFixture, port string) {
	t.Helper()
	c, err := net.DialTimeout("tcp", f.node.addr(port), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	var b [1]byte
	if n, err := c.Read(b[:]); n != 0 || !errors.Is(err, io.EOF) {
		t.Fatalf("connection must close before TLS: bytes=%d err=%v", n, err)
	}
}

func TestGatewayDashboardAudienceAndRouteScope(t *testing.T) {
	f := newGatewayFixture(t, nil)
	f.waitRoute(t, "photos", "running")
	for _, svc := range []registry.Service{
		{Name: "notes", Type: registry.TypeProxy, Target: "http://localhost:9", PeopleScoped: true},
		{Name: "wiki", Type: registry.TypeProxy, Target: "http://localhost:9", PeopleScoped: true, Requestable: true},
	} {
		if _, err := registry.Add(f.path, svc); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := registry.ChangePerson(f.path, "bob", []string{"notes"}, nil, true, false); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ login, app string }{{"bob", "notes"}, {"carol", "wiki"}} {
		t.Run(tc.login, func(t *testing.T) {
			who := whoIsUser(tc.login, "laptop")
			who.Node.Hostinfo = (&tailcfg.Hostinfo{}).View()
			f.who.Store(who)
			code, body, _ := gatewayGet(t, f.client(t), "443", "/", nil)
			if code != 200 || !strings.Contains(body, tc.app) {
				t.Fatalf("dashboard audience: %d %s", code, body)
			}
			gatewayAssertClosed(t, f, "8443") // non-routed grants and requestability do not admit a route
		})
	}
	f.who.Store(whoIsUser("stranger", "shared-in"))
	gatewayAssertClosed(t, f, "443")
	gatewayAssertClosed(t, f, "8443")
	if f.backend[0].Load() != 0 {
		t.Fatal("dashboard-only visitor reached upstream")
	}
}

func TestGatewayUnrelatedServiceIssueIsolation(t *testing.T) {
	f := newGatewayFixture(t, nil)
	f.waitRoute(t, "photos", "running")
	f.waitRoute(t, "payroll", "running")
	c := f.client(t)
	if code, _, _ := gatewayGet(t, c, "8443", "/", nil); code != 200 {
		t.Fatal(code)
	}
	gatewayEditRegistry(t, f.path, func(doc map[string]json.RawMessage) {
		var services []json.RawMessage
		if err := json.Unmarshal(doc["services"], &services); err != nil {
			t.Fatal(err)
		}
		services = append(services, json.RawMessage(`{"name":"legacy-public","type":"proxy","target":"http://localhost:9","funnel":true}`))
		doc["services"], _ = json.Marshal(services)
	})
	_, issues, err := registry.PortalPreflight(f.path)
	if err != nil || len(issues) != 1 || issues[0].Name != "legacy-public" {
		t.Fatalf("fixture: %v %v", issues, err)
	}
	if code, body, _ := gatewayGet(t, c, "8443", "/", nil); code != 200 {
		t.Fatalf("unrelated issue denied route: %d %s", code, body)
	}
	f.s.syncPortal(context.Background(), f.config)
	if got := len(f.s.portalRun.routeStates()); got != 2 {
		t.Fatalf("unrelated issue closed routes: %d", got)
	}
	f.who.Store(whoIsUser("owner", "laptop"))
	if code, _, _ := gatewayGet(t, f.client(t), "443", "/", nil); code != 200 {
		t.Fatal(code)
	}
	// The same issue on the route itself must deny requests and close its listener.
	gatewayEditRegistry(t, f.path, func(doc map[string]json.RawMessage) {
		var services []map[string]any
		if err := json.Unmarshal(doc["services"], &services); err != nil {
			t.Fatal(err)
		}
		for _, svc := range services {
			if svc["name"] == "photos" {
				svc["target"] = "not a valid target"
			}
		}
		doc["services"], _ = json.Marshal(services)
	})
	if code, body, _ := gatewayGet(t, c, "8443", "/", nil); code != 403 || !strings.Contains(body, "registry_unavailable") {
		t.Fatalf("own issue: %d %s", code, body)
	}
	f.s.syncPortal(context.Background(), f.config)
	if f.node.addr("8443") != "" || f.node.addr("8444") == "" {
		t.Fatal("own issue was not isolated to its listener")
	}
}

func TestGatewaySlowWhoIsDoesNotBlockOtherConnections(t *testing.T) {
	f := newGatewayFixture(t, nil)
	f.waitRoute(t, "photos", "running")
	c, err := gatewayTLSDial(f, "8443")
	if err != nil {
		t.Fatal("control", err)
	}
	c.Close()
	f.stallWho.Store(true)
	before := f.calls.Load()
	slow, err := net.Dial("tcp", f.node.addr("8443"))
	if err != nil {
		t.Fatal(err)
	}
	defer slow.Close()
	deadline := time.Now().Add(testwait.Budget(t))
	for f.calls.Load() == before && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if f.calls.Load() == before {
		t.Fatal("stalled WhoIs never started")
	}
	f.stallWho.Store(false)
	c, err = gatewayTLSDial(f, "8443")
	if err != nil {
		t.Fatal("normal handshake blocked behind stalled WhoIs", err)
	}
	c.Close()
	// The pending peer has sent and received no TLS; closing the listener must join it.
	f.s.closePortal()
	_ = slow.SetReadDeadline(time.Now().Add(time.Second))
	var b [1]byte
	if n, err := slow.Read(b[:]); n != 0 || !errors.Is(err, io.EOF) {
		t.Fatalf("pending admission on shutdown: %d %v", n, err)
	}
}

func TestGatewayDenyPrivacyReasonsAndMatchedGrant(t *testing.T) {
	f := newGatewayFixture(t, nil)
	f.waitRoute(t, "photos", "running")
	f.waitRoute(t, "payroll", "running")
	c := f.client(t)
	for _, accept := range []string{"application/json", "text/html"} {
		code, body, _ := gatewayGet(t, c, "8444", "/", map[string]string{"Accept": accept})
		if code != 403 || strings.Contains(body, "payroll") || !strings.Contains(body, "You do not have access to this app") {
			t.Fatalf("hidden app disclosed: %d %s", code, body)
		}
	}
	if _, err := registry.MutateService(f.path, "payroll", func(s registry.Service) (registry.Service, error) { s.Requestable = true; return s, nil }); err != nil {
		t.Fatal(err)
	}
	// A caller whose node WhoIs does not describe cannot ask for the app, so a
	// requestable app is no more named to them than a hidden one.
	code, body, _ := gatewayGet(t, c, "8444", "/", map[string]string{"Accept": "text/html"})
	if code != 403 || strings.Contains(body, "payroll") || !strings.Contains(body, "You do not have access to this app") {
		t.Fatalf("requestable app named to a caller who cannot request it: %d %s", code, body)
	}
	eligible := whoIsUser("alice", "laptop")
	eligible.Node.Hostinfo = (&tailcfg.Hostinfo{}).View()
	f.who.Store(eligible)
	code, body, _ = gatewayGet(t, c, "8444", "/", map[string]string{"Accept": "text/html"})
	if code != 403 || !strings.Contains(body, "payroll") || strings.Contains(body, "href") {
		t.Fatalf("requestable app: %d %s", code, body)
	}
	code, _, _ = gatewayGet(t, c, "8443", "/", nil)
	if code != 200 {
		t.Fatal(code)
	}
	found := false
	for _, e := range f.events.all() {
		if e.App == "photos" && e.Reason == "granted" && e.Grant != nil && e.Grant.Kind == "person" && e.Grant.Entry == "alice" {
			found = true
		}
	}
	if !found {
		t.Fatal("matched grant missing from gateway access event")
	}
	code, body, _ = gatewayGet(t, c, "8443", "/", map[string]string{"Sec-Fetch-Site": "same-site", "Sec-Fetch-Mode": "cors"})
	if code != 403 || !strings.Contains(body, "Cross-origin request blocked") || strings.Contains(body, "photos") {
		t.Fatalf("cross-origin reason: %d %s", code, body)
	}
	f.who.Store(nil)
	code, body, _ = gatewayGet(t, c, "8443", "/", nil)
	if code != 403 || !strings.Contains(body, "Unable to identify this connection") {
		t.Fatalf("identity reason: %d %s", code, body)
	}
	f.who.Store(whoIsUser("alice", "laptop"))
	if err := os.WriteFile(f.path, []byte(`{"services":`), 0600); err != nil {
		t.Fatal(err)
	}
	code, body, _ = gatewayGet(t, c, "8443", "/", nil)
	if code != 403 || !strings.Contains(body, "Access policy is temporarily unavailable") {
		t.Fatalf("registry reason: %d %s", code, body)
	}
}

func TestGatewayStaleTargetDeniedBeforeSync(t *testing.T) {
	f := newGatewayFixture(t, nil)
	f.waitRoute(t, "photos", "running")
	c := f.client(t)
	if code, _, _ := gatewayGet(t, c, "8443", "/", nil); code != 200 {
		t.Fatal(code)
	}
	if _, err := registry.MutateService(f.path, "photos", func(s registry.Service) (registry.Service, error) { s.Target = "http://localhost:9"; return s, nil }); err != nil {
		t.Fatal(err)
	}
	code, body, _ := gatewayGet(t, c, "8443", "/", nil)
	if code != 403 || !strings.Contains(body, "route_changed") || f.backend[0].Load() != 1 {
		t.Fatalf("stale target served: %d %s backend=%d", code, body, f.backend[0].Load())
	}
}

func TestGatewayDropCounterAndDashboardMarker(t *testing.T) {
	f := newGatewayFixture(t, nil)
	f.waitRoute(t, "photos", "running")
	f.who.Store(whoIsUser("stranger", "scanner"))
	for i := 0; i < 5; i++ {
		gatewayAssertClosed(t, f, "8443")
	}
	gatewayAssertClosed(t, f, "443")
	f.s.mu.Lock()
	f.s.refreshPortalURLLocked()
	dropped := f.s.portalState.Gateway.Dropped
	f.s.mu.Unlock()
	if dropped != 6 {
		t.Fatalf("dropped=%d want 6", dropped)
	}
	count := 0
	for _, e := range f.events.all() {
		if e.Reason == "conn_gate_drop" {
			count++
			if e.App != "photos" && e.App != "@portal" {
				t.Fatalf("phantom app: %s", e.App)
			}
		}
	}
	if count != 2 {
		t.Fatalf("rate limited drops=%d want 2", count)
	}
}

// The fake models simultaneous clients by their actual source address.
func TestGatewayPeerIdentityByRemoteAddress(t *testing.T) {
	f := newGatewayFixture(t, nil)
	f.waitRoute(t, "photos", "running")
	var peers []net.Conn
	for _, login := range []string{"alice", "stranger"} {
		c, err := net.Dial("tcp", f.node.addr("8443"))
		if err != nil {
			t.Fatal(err)
		}
		peers = append(peers, c)
		defer c.Close()
		f.whoByRemote.Store(c.LocalAddr().String(), whoIsUser(login, "peer"))
	}
	// Check the LocalAPI lookup itself with two addresses; admission integration is
	// covered by the allowed/stranger tests, without depending on scheduling here.
	for i, login := range []string{"alice", "stranger"} {
		got, err := f.node.localClient.WhoIs(context.Background(), peers[i].LocalAddr().String())
		if err != nil || got.UserProfile.LoginName != login {
			t.Fatalf("peer %d: %+v %v", i, got, err)
		}
	}
}

func TestGatewayDashboardOfflineTime(t *testing.T) {
	f := newGatewayFixture(t, nil)
	f.waitRoute(t, "photos", "running")
	f.s.portalRun.routesMu.Lock()
	route := f.s.portalRun.routes["photos"]
	f.s.portalRun.routesMu.Unlock()
	f.s.observeGatewayUpstream(f.s.portalRun, route, false)
	code, body, _ := gatewayGet(t, f.client(t), "443", "/", nil)
	if code != 200 || !strings.Contains(body, "Offline since 2 Jan 2030, 03:04 UTC") {
		t.Fatalf("offline time not formatted: %d %s", code, body)
	}
}
