package server

// Regression cases derived from the independent gateway review.

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/registry"
	"tailscale.com/tailcfg"
)

func gatewayDropPeople(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	delete(doc, "people")
	out, _ := json.MarshalIndent(doc, "", "  ")
	if err := os.WriteFile(path, append(out, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
}

// no people + empty allow must still deny a known visitor.
func TestGatewayNoPeopleKnownVisitorDenied(t *testing.T) {
	f := newGatewayFixture(t, nil)
	f.waitRoute(t, "photos", "running")
	f.waitRoute(t, "payroll", "running")
	if _, err := registry.MutateService(f.path, "payroll", func(s registry.Service) (registry.Service, error) {
		s.AllowedUsers = []string{"tag:ci"}
		return s, nil
	}); err != nil {
		t.Fatal(err)
	}
	gatewayDropPeople(t, f.path)
	reg, err := registry.Load(f.path)
	if err != nil || len(reg.People) != 0 {
		t.Fatalf("fixture people=%d err=%v", len(reg.People), err)
	}
	f.s.syncPortal(context.Background(), f.config)
	f.waitRoute(t, "payroll", "running")
	f.waitRoute(t, "photos", "running")
	who := whoIsUser("tagged-devices", "ci")
	who.Node = &tailcfg.Node{ComputedName: "ci", Tags: []string{"tag:ci"}}
	f.who.Store(who)
	c := f.client(t)
	code, body, _ := gatewayGet(t, c, "8444", "/", nil)
	if code != 200 || body != "payroll" {
		t.Fatalf("control: allowed tag on payroll: %d %s", code, body)
	}
	code, body, _ = gatewayGet(t, c, "8443", "/", nil)
	if code != 403 || f.backend[0].Load() != 0 {
		t.Fatalf("known visitor reached photos with no people records: %d %s backend=%d", code, body, f.backend[0].Load())
	}
}

// X-Forwarded-Host must come from the route authority, not the client Host.
func TestGatewayForwardedHostIgnoresClientHost(t *testing.T) {
	f := newGatewayFixture(t, nil)
	f.waitRoute(t, "photos", "running")
	r, _ := http.NewRequest("GET", "https://home.tailnet.ts.net:8443/", nil)
	r.Host = "evil.example:1"
	resp, err := f.client(t).Do(r)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	seen := <-f.seen
	if resp.StatusCode != 200 || seen.Header.Get("X-Forwarded-Host") != "home.tailnet.ts.net:8443" {
		t.Fatalf("X-Forwarded-Host from client: status=%d xfh=%q", resp.StatusCode, seen.Header.Get("X-Forwarded-Host"))
	}
}

// absolute URLs that are not the upstream origin stay unchanged.
func TestGatewayForeignLocationUntouched(t *testing.T) {
	f := newGatewayFixture(t, nil)
	f.waitRoute(t, "photos", "running")
	reg, _ := registry.Load(f.path)
	var target string
	for _, s := range reg.Services {
		if s.Name == "photos" {
			target = s.Target
		}
	}
	for _, foreign := range []string{"https://accounts.example/login?x=1", strings.Replace(target, "127.0.0.1:", "127.0.0.1:1", 1) + "/other", "http://localhost.example" + target[len("http://127.0.0.1"):] + "/x"} {
		resp := &http.Response{Header: http.Header{"Location": []string{foreign}}}
		u, _ := url.Parse(target)
		rewriteGatewayResponse(resp, u, "home.tailnet.ts.net:8443")
		if got := resp.Header.Get("Location"); got != foreign {
			t.Fatalf("foreign Location rewritten: %q -> %q", foreign, got)
		}
	}
}

// the per-route opt-out must let cross-origin requests through.
func TestGatewayCrossOriginOptOut(t *testing.T) {
	f := newGatewayFixture(t, nil)
	f.waitRoute(t, "photos", "running")
	if err := registry.SetPortalRoute(context.Background(), f.path, "photos", &registry.PortalRoute{Port: 8443, AllowCrossOrigin: true}); err != nil {
		t.Fatal(err)
	}
	f.s.syncPortal(context.Background(), f.config)
	f.waitRoute(t, "photos", "running")
	c := f.client(t)
	c.CloseIdleConnections()
	code, body, _ := gatewayGet(t, c, "8443", "/", map[string]string{"Sec-Fetch-Site": "same-site", "Sec-Fetch-Mode": "cors", "Origin": "https://home.tailnet.ts.net:8444"})
	if code != 200 || body != "photos" {
		t.Fatalf("opt-out ignored: %d %s", code, body)
	}
}

// dropped connections are logged once per second with conn_gate_drop.
func TestGatewayConnGateDropLoggedAndRateLimited(t *testing.T) {
	f := newGatewayFixture(t, nil)
	f.waitRoute(t, "photos", "running")
	f.who.Store(whoIsUser("stranger", "scanner"))
	for i := 0; i < 5; i++ {
		_ = gatewayTLSDialQuick(f, "8443")
	}
	drops := 0
	for _, e := range f.events.all() {
		if e.Reason == "conn_gate_drop" && e.App == "photos" {
			drops++
		}
	}
	if drops != 1 {
		t.Fatalf("conn_gate_drop events in one second = %d, want 1", drops)
	}
	f.now.Add(int64(2 * time.Second))
	_ = gatewayTLSDialQuick(f, "8443")
	drops = 0
	for _, e := range f.events.all() {
		if e.Reason == "conn_gate_drop" && e.App == "photos" {
			drops++
		}
	}
	if drops != 2 {
		t.Fatalf("conn_gate_drop after the window = %d, want 2", drops)
	}
}

func gatewayTLSDialQuick(f *gatewayFixture, port string) error {
	c, err := gatewayTLSDial(f, port)
	if c != nil {
		c.Close()
	}
	return err
}

func gatewayTLSDial(f *gatewayFixture, port string) (*tls.Conn, error) {
	return tls.DialWithDialer(&net.Dialer{Timeout: 3 * time.Second}, "tcp", f.node.addr(port), &tls.Config{RootCAs: f.pool, ServerName: "home.tailnet.ts.net", MinVersion: tls.VersionTLS12})
}
