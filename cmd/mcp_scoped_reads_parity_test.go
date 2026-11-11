package cmd

import (
	"io"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/mcpscope"
	"github.com/anydoor7/tslink/internal/registry"
	tsRuntime "github.com/anydoor7/tslink/internal/runtime"
)

// reducedStatusFixture is one host state read by the real status reader, so
// owner and reduced results come from the same owner code path.
type reducedStatusFixture struct {
	name  string
	app   string
	setup func(t *testing.T, paths sharePaths)
}

func reducedStatusFixtures() []reducedStatusFixture {
	started := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	return []reducedStatusFixture{
		{
			// A portal-only app is authorized through its running gateway
			// route. The earlier Status == "up" recount happened to agree here
			// (applyGatewayStatus also marks the route up); this case pins
			// that the reduced count and flag stay the owner's one decision.
			name: "portal-only route", app: "app",
			setup: func(t *testing.T, paths sharePaths) {
				cfg := &registry.PortalConfig{Enabled: true, Hostname: "home", Owner: "owner", Gateway: &registry.GatewayConfig{Enabled: true, Ports: registry.DefaultGatewayPorts()}}
				if err := registry.SetPortal(paths.Registry, cfg); err != nil {
					t.Fatal(err)
				}
				addStatusTestService(t, paths.Registry, registry.Service{Name: "app", Type: registry.TypeProxy, Target: "http://localhost:9", PortalOnly: true})
				snapshot := tsRuntime.NewSnapshot(4242, started, statusRegistryFingerprint(t, paths.Registry), started.Add(time.Second), nil)
				snapshot.Portal = tsRuntime.PortalState{Enabled: true, Hostname: "home", State: "running", URL: "https://home.tailnet.ts.net", Gateway: &tsRuntime.GatewayState{Enabled: true, Persistent: true, Ports: cfg.Gateway.Ports, Routes: []tsRuntime.GatewayRouteState{{App: "app", Port: 8443, State: "running", URL: "https://home.tailnet.ts.net:8443/", HostLabel: "this host"}}}}
				if err := tsRuntime.Save(paths.Snapshot, snapshot); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			// An unreadable registry: the owner counts services the last
			// snapshot reports "running". The earlier Status == "up" recount
			// read 0 here; this is the case that was wrong.
			name: "registry global failure", app: "private-app",
			setup: func(t *testing.T, paths sharePaths) {
				if err := os.WriteFile(paths.Registry, []byte("{invalid"), 0o600); err != nil {
					t.Fatal(err)
				}
				snapshot := tsRuntime.NewPartialSnapshot(4242, started, "sha256:last-good", started.Add(time.Second), []tsRuntime.ServiceState{{
					Service:      registry.Service{Name: "private-app", Type: registry.TypeProxy, Target: "http://localhost:3000"},
					RuntimeState: tsRuntime.ServiceRuntimeRunning,
				}})
				snapshot.GlobalError = &tsRuntime.ServiceError{Code: registry.CodeRegistryReloadInvalid, Message: "invalid", Next: []string{"tslink registry check --json"}}
				if err := tsRuntime.Save(paths.Snapshot, snapshot); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
}

func newReducedStatusPaths(t *testing.T) sharePaths {
	t.Helper()
	dir := t.TempDir()
	t.Setenv(config.ConfigDirEnv, dir)
	return sharePaths{Registry: filepath.Join(dir, "registry.json"), PID: filepath.Join(dir, "tslink.pid"), Snapshot: filepath.Join(dir, "runtime.json"), AuthHandoff: filepath.Join(dir, "auth-handoff.json")}
}

// Reduced authorization is the owner's own per-service decision filtered to
// the role's apps, never a second rule. For an inventory viewer, which sees
// every app, the count must equal the owner's; for an app-scoped viewer whose
// one app is authorized it must be 1. Host-level booleans either equal the
// owner's or are absent and named in withheld.
func TestMCPReducedStatusAuthorizationIsTheOwnersDecision(t *testing.T) {
	for _, fx := range reducedStatusFixtures() {
		t.Run(fx.name, func(t *testing.T) {
			paths := newReducedStatusPaths(t)
			fx.setup(t, paths)
			withStatusURLSeams(t, true, 4242, time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC))
			actions := defaultMCPActions(paths, io.Discard)
			owner := reducedToolPayload(t, actions, testRoleScope("owner"), "status")
			if owner["authorized_service_count"] != float64(1) {
				t.Fatalf("fixture control: owner authorized_service_count = %v", owner["authorized_service_count"])
			}
			for _, scope := range []mcpscope.Scope{{Role: "viewer", Inventory: true}, {Role: "viewer", Apps: []string{fx.app}}, {Role: "app-operator", Apps: []string{fx.app}, MaxDuration: "2h"}} {
				for _, tool := range []string{"status", "health"} {
					got := reducedToolPayload(t, actions, scope, tool)
					if tool == "status" && got["authorized_service_count"] != owner["authorized_service_count"] {
						t.Fatalf("%s %v: reduced authorized_service_count = %v, owner %v", tool, scope, got["authorized_service_count"], owner["authorized_service_count"])
					}
					withheld := reducedWithheld(t, got)
					for _, field := range []string{"authenticated", "node_authorized"} {
						value, present := got[field]
						if present && value != owner[field] {
							t.Fatalf("%s %v: reduced %s = %v, owner %v", tool, scope, field, value, owner[field])
						}
						if tool == "status" && !present && !slices.Contains(withheld, field) {
							t.Fatalf("%s absent but not withheld: %v", field, withheld)
						}
					}
					services, _ := got["services"].([]any)
					if len(services) != 1 || services[0].(map[string]any)["authorized"] != true {
						t.Fatalf("%s %v: services = %v, want the one app authorized", tool, scope, services)
					}
				}
			}
		})
	}
}
