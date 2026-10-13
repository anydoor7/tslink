package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anydoor7/tslink/internal/inspect"
	"github.com/anydoor7/tslink/internal/registry"
	tsRuntime "github.com/anydoor7/tslink/internal/runtime"
)

func TestGatewayCompiledCLIContracts(t *testing.T) {
	binary := compiledTSLinkBinary(t)
	dir := t.TempDir()
	env := e2eEnv(dir)
	run := func(args ...string) map[string]any {
		t.Helper()
		args = append(args, "--json")
		r := e2eRunBinary(t, binary, dir, "", env, args...)
		e, data := e2eDecodeEnvelope(t, r, strings.Join(args, " "))
		if !e.OK || r.ExitCode != 0 {
			t.Fatalf("command failed: %s %s", r.Stdout, r.Stderr)
		}
		return data
	}
	run("portal", "enable", "--owner", "owner@example.com", "--gateway", "--gateway-ports", "8443-8445", "--gateway-tag", "tag:door")
	run("add", "photos", "--proxy", "localhost:3000", "--portal-only", "--portal-port", "8444")
	run("add", "files", "--proxy", "localhost:3001")
	run("portal", "route", "add", "files", "--allow-cross-origin")
	data := run("portal", "route", "list")
	routes := data["routes"].([]any)
	if len(routes) != 2 {
		t.Fatalf("routes: %+v", data)
	}
	reg, err := registry.Load(filepath.Join(dir, "registry.json"))
	if err != nil {
		t.Fatal(err)
	}
	if reg.SchemaVersion != 3 || !reg.Services[0].PortalOnly || reg.Services[0].PortalRoute.Port != 8444 || reg.Services[1].PortalRoute.Port != 8443 || !reg.Services[1].PeopleScoped || !reg.Services[1].PortalRoute.AllowCrossOrigin {
		t.Fatalf("registry: %+v", reg.Services)
	}
	for _, args := range [][]string{{"status"}, {"status", "--urls"}, {"list", "--verbose"}} {
		data = run(args...)
		portal, ok := data["portal"].(map[string]any)
		if !ok || portal["gateway"] == nil {
			t.Fatalf("gateway missing from %v: %+v", args, data)
		}
	}
	before, _ := os.ReadFile(filepath.Join(dir, "registry.json"))
	for _, tc := range []struct {
		args []string
		code string
	}{
		{[]string{"portal", "route", "remove", "photos"}, "portal_only_requires_route"},
		{[]string{"portal", "route", "add", "files", "--port", "8444"}, "portal_route_port_conflict"},
		{[]string{"portal", "route", "add", "files", "--port", "6000"}, "portal_route_bad_port"},
		{[]string{"portal", "enable", "--owner", "owner@example.com", "--gateway-ports", "8000-9000"}, "gateway_required"},
	} {
		r := e2eRunBinary(t, binary, dir, "", env, append(tc.args, "--json")...)
		e, _ := e2eDecodeEnvelope(t, r, tc.code)
		if e.OK || e.Error == nil || e.Error.Code != tc.code {
			t.Fatalf("wrong refusal: %s", r.Stdout)
		}
		after, _ := os.ReadFile(filepath.Join(dir, "registry.json"))
		if !bytes.Equal(before, after) {
			t.Fatal("failed command changed registry")
		}
	}
	run("portal", "route", "remove", "files")
	run("portal", "disable")
	reg, err = registry.Load(filepath.Join(dir, "registry.json"))
	if err != nil || reg.Portal.Enabled || reg.Services[0].PortalRoute == nil || !reg.Services[1].PeopleScoped {
		t.Fatal("disable/remove discarded route or sticky scope", err)
	}
}

func TestGatewayStatusURLAndDoctorProjection(t *testing.T) {
	paths := peopleTestPaths(t)
	p, err := changePortal(paths, portalArguments{Owner: "owner", Gateway: true}, true)
	if err != nil {
		t.Fatal(err)
	}
	if p.Gateway == nil || p.Gateway.Persistent || p.Gateway.NodeID != "" || p.URL != "" {
		t.Fatal("configuration invented runtime evidence")
	}
	if _, err := registry.Add(paths.Registry, registry.Service{Name: "app", Type: registry.TypeProxy, Target: "http://localhost:3000", PortalOnly: true}); err != nil {
		t.Fatal(err)
	}
	reg, err := registry.Load(paths.Registry)
	if err != nil {
		t.Fatal(err)
	}
	p = portalView(reg, nil, false)
	if p.Gateway.Routes[0].URL != "" || p.Gateway.Routes[0].State != "pending" {
		t.Fatal("pending URL invented")
	}
	observed := p
	observed.State = "running"
	observed.URL = "https://home.tailnet.ts.net"
	observed.Gateway = &tsRuntime.GatewayState{Enabled: true, Persistent: true, NodeID: "proven-node", OwnershipProven: true, Ports: reg.Portal.Gateway.Ports, Routes: []tsRuntime.GatewayRouteState{{App: "app", Port: 8443, State: "running", URL: "https://home.tailnet.ts.net:8443/", HostLabel: "this host"}}}
	for _, exact := range []bool{true, false} {
		view := portalView(reg, &tsRuntime.Snapshot{Portal: observed}, exact)
		svc := StatusServiceView{Name: "app", PortalOnly: true}
		applyGatewayServiceView(&svc, view)
		got, ready, found := exactServiceURL(StatusURLsResult{Services: []StatusServiceView{svc}}, "app")
		if !found || ready != exact || (exact && got.Result.URL != "https://home.tailnet.ts.net:8443/") || (!exact && svc.Endpoint.Display != "") {
			t.Fatalf("URL evidence: %+v %t", got, ready)
		}
	}
	svc := StatusServiceView{Name: "app", Endpoint: inspect.EndpointView{State: "exact", Display: "https://app.tailnet.ts.net"}}
	applyGatewayServiceView(&svc, observed)
	got, ready, _ := exactServiceURL(StatusURLsResult{Services: []StatusServiceView{svc}}, "app")
	if !ready || got.Result.URL != "https://app.tailnet.ts.net" || got.Result.PortalURL != "https://home.tailnet.ts.net:8443/" {
		t.Fatalf("direct URL changed: %+v", got)
	}
	observed.Gateway.IdentityChanged = true
	observed.Gateway.Persistent = false
	observed.Gateway.Routes[0].Reason = "route_listener_failed"
	d := DoctorResult{Portal: observed}
	diagnoseGateway(&d)
	if len(d.Findings) != 3 {
		t.Fatalf("doctor findings: %+v", d.Findings)
	}
	data, _ := json.Marshal(d)
	for _, code := range []string{"gateway_node_identity_changed", "gateway_ephemeral_refused", "route_listener_failed"} {
		if !strings.Contains(string(data), code) {
			t.Fatal("finding missing", code)
		}
	}
	if err := registry.SetPortalRoute(context.Background(), paths.Registry, "app", nil); err == nil {
		t.Fatal("removed only route")
	}
}

func TestGatewayArgumentValidation(t *testing.T) {
	for _, args := range []portalArguments{{GatewayPorts: "8443-8499"}, {GatewayTag: "tag:door"}, {Gateway: true, GatewayPorts: "bad"}, {Gateway: true, GatewayPorts: "0-9"}, {Gateway: true, GatewayPorts: "8499-8443"}, {Gateway: true, GatewayTag: "invalid"}} {
		if _, err := parseGatewayArguments(args); err == nil {
			t.Fatalf("accepted %+v", args)
		}
	}
	g, err := parseGatewayArguments(portalArguments{Gateway: true})
	if err != nil || g.Ports.From != 8443 || g.Ports.To != 8499 {
		t.Fatal(g, err)
	}
}
