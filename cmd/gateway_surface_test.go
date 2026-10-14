package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/registry"
	tsRuntime "github.com/anydoor7/tslink/internal/runtime"
)

func TestGatewayEnablePreservesConfiguration(t *testing.T) {
	paths := gatewayCommandPaths(t)
	cfg := portalArguments{Owner: "owner", Gateway: true, GatewayPorts: "9000-9004", GatewayTag: "tag:door"}
	if _, err := changePortal(paths, cfg, true); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Add(paths.Registry, registry.Service{Name: "app", Type: registry.TypeProxy, Target: "http://localhost:9"}); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetPortalRoute(context.Background(), paths.Registry, "app", &registry.PortalRoute{Port: 9001}); err != nil {
		t.Fatal(err)
	}
	for _, args := range []portalArguments{{Owner: "owner", Admins: []string{"helper"}}, {Owner: "owner", Gateway: true}} {
		if _, err := changePortal(paths, args, true); err != nil {
			t.Fatal(err)
		}
		reg, err := registry.Load(paths.Registry)
		if err != nil || reg.Portal.Gateway == nil || reg.Portal.Gateway.Ports.From != 9000 || reg.Portal.Gateway.Tag != "tag:door" || reg.Services[0].PortalRoute.Port != 9001 {
			t.Fatalf("gateway settings replaced: %+v %v", reg, err)
		}
	}
	before, _ := os.ReadFile(paths.Registry)
	if _, err := changePortal(paths, portalArguments{Owner: "owner", GatewaySet: true}, true); err == nil {
		t.Fatal("disabled gateway with route")
	}
	after, _ := os.ReadFile(paths.Registry)
	if !bytes.Equal(before, after) {
		t.Fatal("refused disable wrote registry")
	}
	if err := registry.SetPortalRoute(context.Background(), paths.Registry, "app", nil); err != nil {
		t.Fatal(err)
	}
	out, stderr, code := runTSLinkBinaryWithConfigDir(t, compiledTSLinkBinary(t), filepath.Dir(paths.Registry), "", "portal", "enable", "--owner", "owner", "--gateway=false", "--json")
	if code != 0 {
		t.Fatal(out, stderr, code)
	}
	reg, err := registry.Load(paths.Registry)
	if err != nil || reg.Portal.Gateway != nil {
		t.Fatalf("explicit disable ignored: %+v %v", reg, err)
	}
}

func TestGatewayMCPToolsAndOwnerBoundary(t *testing.T) {
	paths := gatewayCommandPaths(t)
	if _, err := changePortal(paths, portalArguments{Owner: "owner", Gateway: true, GatewayPorts: "9000-9004", GatewayTag: "tag:door"}, true); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Add(paths.Registry, registry.Service{Name: "app", Type: registry.TypeProxy, Target: "http://localhost:9"}); err != nil {
		t.Fatal(err)
	}
	actions := defaultMCPActions(paths, io.Discard)
	owner := httptest.NewServer(mcpHTTPControlPlaneHandler(t, []string{"owner"}, "owner", actions))
	defer owner.Close()
	outsider := httptest.NewServer(mcpHTTPControlPlaneHandler(t, []string{"operator"}, "operator", actions))
	defer outsider.Close()
	for _, tool := range []string{"portal_route_add", "portal_route_remove", "portal_enable"} {
		args := map[string]any{"app": "app"}
		if tool == "portal_enable" {
			args = map[string]any{"owner": "owner", "gateway": true}
		}
		before, _ := os.ReadFile(paths.Registry)
		result := portalRemoteCall(t, outsider, tool, args)
		if !bytes.Contains(result, []byte("access_request_owner_required")) {
			t.Fatalf("%s owner check: %s", tool, result)
		}
		after, _ := os.ReadFile(paths.Registry)
		if !bytes.Equal(before, after) {
			t.Fatal("unauthorized mutation wrote registry")
		}
	}
	result := portalRemoteCall(t, owner, "portal_route_add", map[string]any{"app": "app", "port": 9002, "allow_cross_origin": true})
	if bytes.Contains(result, []byte(`"isError":true`)) {
		t.Fatal(string(result))
	}
	result = portalRemoteCall(t, owner, "portal_enable", map[string]any{"owner": "owner"})
	if bytes.Contains(result, []byte(`"isError":true`)) {
		t.Fatal(string(result))
	}
	reg, err := registry.Load(paths.Registry)
	if err != nil || reg.Portal.Gateway == nil || reg.Portal.Gateway.Ports.From != 9000 || reg.Portal.Gateway.Tag != "tag:door" || !reg.Services[0].PortalRoute.AllowCrossOrigin {
		t.Fatalf("MCP settings lost: %+v %v", reg, err)
	}
	before, _ := os.ReadFile(paths.Registry)
	listed, err := callMCPTool(context.Background(), actions, "portal_route_list", json.RawMessage(`{}`))
	if err != nil || listed.IsError {
		t.Fatal(listed, err)
	}
	validateAgainstToolOutputSchema(t, "portal_route_list", listed.StructuredContent)
	after, _ := os.ReadFile(paths.Registry)
	if !bytes.Equal(before, after) {
		t.Fatal("route list mutated registry")
	}
	result = portalRemoteCall(t, owner, "portal_enable", map[string]any{"owner": "owner", "gateway": false})
	if !bytes.Contains(result, []byte("gateway_required")) {
		t.Fatalf("gateway disabled with routes: %s", result)
	}
	result = portalRemoteCall(t, owner, "portal_route_remove", map[string]any{"app": "app"})
	if bytes.Contains(result, []byte(`"isError":true`)) {
		t.Fatal(string(result))
	}
	result = portalRemoteCall(t, owner, "portal_enable", map[string]any{"owner": "owner", "gateway": false})
	if bytes.Contains(result, []byte(`"isError":true`)) {
		t.Fatal(string(result))
	}
	reg, err = registry.Load(paths.Registry)
	if err != nil || reg.Portal.Gateway != nil {
		t.Fatalf("MCP explicit false ignored: %+v %v", reg, err)
	}
}

func TestGatewayRemoteCLIAndMCPRegistration(t *testing.T) {
	paths := gatewayCommandPaths(t)
	if _, err := changePortal(paths, portalArguments{Owner: "owner", Gateway: true}, true); err != nil {
		t.Fatal(err)
	}
	actions := defaultMCPActions(paths, io.Discard)
	for _, raw := range []string{`{"name":"local","type":"proxy","target":"localhost:9","portal_only":true,"portal_port":8443,"no_daemon_install":true}`, `{"name":"remote","type":"proxy","remote":"https://remote.tailnet.ts.net:9443/base","portal_port":8444,"no_daemon_install":true}`} {
		result, err := callMCPTool(context.Background(), actions, "add", json.RawMessage(raw))
		if err != nil || result.IsError {
			t.Fatal(result, err)
		}
		validateAgainstToolOutputSchema(t, "add", result.StructuredContent)
	}
	reg, err := registry.Load(paths.Registry)
	if err != nil || len(reg.Services) != 2 {
		t.Fatal(reg, err)
	}
	for _, svc := range reg.Services {
		if !svc.PortalOnly || !svc.PeopleScoped || svc.PortalRoute == nil {
			t.Fatalf("MCP add lost portal fields: %+v", svc)
		}
	}
	if reg.Services[1].Upstream != "tailnet" || reg.Services[1].Target != "https://remote.tailnet.ts.net:9443/base" {
		t.Fatalf("remote target lost: %+v", reg.Services[1])
	}
	out, stderr, code := runTSLinkBinaryWithConfigDir(t, compiledTSLinkBinary(t), filepath.Dir(paths.Registry), "", "add", "remote-cli", "--remote", "https://mini.tailnet.ts.net", "--portal-port", "8445", "--no-daemon-install", "--json")
	if code != 0 {
		t.Fatal(out, stderr, code)
	}
	for _, raw := range []string{`{"name":"bad","type":"proxy","remote":"http://remote.tailnet.ts.net","no_daemon_install":true}`, `{"name":"bad","type":"proxy","remote":"https://100.64.0.1","no_daemon_install":true}`, `{"name":"bad","type":"file","dir":"/tmp","remote":"https://remote.tailnet.ts.net","no_daemon_install":true}`, `{"name":"bad","type":"proxy","target":"localhost:9","remote":"https://remote.tailnet.ts.net","no_daemon_install":true}`} {
		before, _ := os.ReadFile(paths.Registry)
		result, err := callMCPTool(context.Background(), actions, "add", json.RawMessage(raw))
		if err != nil || !result.IsError {
			t.Fatalf("bad remote accepted: %s %+v %v", raw, result, err)
		}
		after, _ := os.ReadFile(paths.Registry)
		if !bytes.Equal(before, after) {
			t.Fatal("invalid remote changed registry")
		}
	}
}

func TestGatewayPortalOnlyAuthorizedCountAndAllowMode(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(config.ConfigDirEnv, dir)
	regPath, pidPath, snapshotPath := filepath.Join(dir, "registry.json"), filepath.Join(dir, "tslink.pid"), filepath.Join(dir, "runtime.json")
	cfg := &registry.PortalConfig{Enabled: true, Hostname: "home", Owner: "owner", Gateway: &registry.GatewayConfig{Enabled: true, Ports: registry.DefaultGatewayPorts()}}
	if err := registry.SetPortal(regPath, cfg); err != nil {
		t.Fatal(err)
	}
	addStatusTestService(t, regPath, registry.Service{Name: "app", Type: registry.TypeProxy, Target: "http://localhost:9", PortalOnly: true})
	started := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	snapshot := tsRuntime.NewSnapshot(4242, started, statusRegistryFingerprint(t, regPath), started.Add(time.Second), nil)
	snapshot.Portal = tsRuntime.PortalState{Enabled: true, Hostname: "home", State: "running", URL: "https://home.tailnet.ts.net", Gateway: &tsRuntime.GatewayState{Enabled: true, Persistent: true, Ports: cfg.Gateway.Ports, Routes: []tsRuntime.GatewayRouteState{{App: "app", Port: 8443, State: "running", URL: "https://home.tailnet.ts.net:8443/", HostLabel: "this host"}}}}
	if err := tsRuntime.Save(snapshotPath, snapshot); err != nil {
		t.Fatal(err)
	}
	withStatusURLSeams(t, true, 4242, started)
	result, err := getPollableStatus(context.Background(), pidPath, regPath, snapshotPath, filepath.Join(dir, "auth-handoff.json"))
	if err != nil || result.AuthorizedServiceCount != 1 || result.ServiceCount != 1 {
		t.Fatalf("portal-only count: %+v %v", result, err)
	}
	urls, err := getStatusURLs(context.Background(), pidPath, regPath, snapshotPath)
	if err != nil {
		t.Fatal(err)
	}
	svc := findStatusService(t, urls, "app")
	if svc.Allow.Mode != "people" || svc.Backend.Display == "" || svc.Endpoint.Display != "https://home.tailnet.ts.net:8443/" || svc.Type != "proxy" {
		t.Fatalf("consumer shape: %+v", svc)
	}
	snapshot.Portal.Gateway.Routes[0].State = "failed"
	snapshot.Portal.Gateway.Routes[0].URL = ""
	if err := tsRuntime.Save(snapshotPath, snapshot); err != nil {
		t.Fatal(err)
	}
	result, err = getPollableStatus(context.Background(), pidPath, regPath, snapshotPath, filepath.Join(dir, "auth-handoff.json"))
	if err != nil || result.AuthorizedServiceCount != 0 {
		t.Fatalf("failed route counted: %+v %v", result, err)
	}
}

func TestGatewayDoctorSeverity(t *testing.T) {
	d := DoctorResult{Portal: tsRuntime.PortalState{Enabled: true, State: "running", Gateway: &tsRuntime.GatewayState{Enabled: true, Persistent: false, IdentityChanged: true, Routes: []tsRuntime.GatewayRouteState{{App: "app", State: "failed", Reason: "route_listener_failed", Health: "down"}}}}}
	diagnoseGateway(&d)
	got := map[string]string{}
	for _, f := range d.Findings {
		got[f.Code] = f.Severity
	}
	want := map[string]string{"gateway_node_identity_changed": "warning", "gateway_ephemeral_refused": "error", "route_listener_failed": "warning", "upstream_unreachable": "warning"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("doctor severity=%v want %v", got, want)
	}
	for _, name := range []string{"portal_route_add", "portal_route_remove", "portal_route_list"} {
		h := mcpToolHints[name]
		if h == nil || h.ReadOnlyHint != (name == "portal_route_list") {
			t.Fatalf("tool annotations: %s %+v", name, h)
		}
	}
}

func gatewayCommandPaths(t *testing.T) sharePaths {
	t.Helper()
	dir := t.TempDir()
	t.Setenv(config.ConfigDirEnv, dir)
	return sharePaths{Registry: filepath.Join(dir, "registry.json"), PID: filepath.Join(dir, "tslink.pid"), Snapshot: filepath.Join(dir, "runtime.json")}
}

func TestGatewayDoctorNeverDialsRemoteFromHost(t *testing.T) {
	old := doctorProbeTargetFn
	defer func() { doctorProbeTargetFn = old }()
	calls := 0
	doctorProbeTargetFn = func(context.Context, string, time.Duration) error { calls++; return nil }
	svc := registry.Service{Name: "remote", Type: registry.TypeProxy, Target: "https://remote.tailnet.ts.net", Upstream: "tailnet"}
	result := DoctorResult{}
	diagnoseNetworkTarget(&result, svc, doctorOptions{ProbeExternal: true}, "")
	if calls != 0 || len(result.Findings) != 1 || result.Findings[0].Code != "target_probe_skipped_remote" {
		t.Fatalf("host remote probe: calls=%d result=%+v", calls, result)
	}
	// Same network spy has a reachable local control in the existing doctor path.
	svc.Upstream = ""
	svc.Type = registry.TypeTCP
	svc.Target = "127.0.0.1:1234"
	result.Daemon.Running = true
	diagnoseNetworkTarget(&result, svc, doctorOptions{ProbeExternal: true}, "")
	if calls != 1 {
		t.Fatal("host probe spy did not observe local control", calls)
	}
}
