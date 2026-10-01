package cmd

import (
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/registry"
	tsruntime "github.com/anydoor7/tslink/internal/runtime"
)

// TestAuthenticatedMeansANodeIsAuthorizedOnEverySurface is A3-5's probe: with
// a fake, never-verified stored token, CLI status said authenticated:true and
// auth_status:"authenticated" while MCP status said authenticated:false. A
// string on disk proves nothing; authenticated and auth_status now mean one
// thing on every surface: a service node is authorized on the tailnet.
// credential_stored still reports the string on disk.
func TestAuthenticatedMeansANodeIsAuthorizedOnEverySurface(t *testing.T) {
	dir := t.TempDir()
	paths := sharePaths{
		Registry:    filepath.Join(dir, "registry.json"),
		PID:         filepath.Join(dir, "tslink.pid"),
		Snapshot:    filepath.Join(dir, "runtime.json"),
		AuthHandoff: filepath.Join(dir, "auth-handoff.json"),
		Ownership:   filepath.Join(dir, "node-ownership.json"),
	}
	startedAt := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	svc := addStatusTestService(t, paths.Registry, registry.Service{Name: "web", Type: registry.TypeProxy, Target: "http://localhost:3000", Tags: []string{"tag:tsmain"}})
	withStatusURLSeams(t, true, 4242, startedAt)
	getAPIKeyFn = func() (string, error) { return "tskey-api-FAKEFAKEFAKE-notreal", nil }
	restoreShareSeams(t)
	sharePollableStatusFn = getPollableStatus
	mcpStatus := func() mcpStatusSummary {
		t.Helper()
		value, err := defaultMCPActions(paths, io.Discard).status()
		if err != nil {
			t.Fatal(err)
		}
		return value.(mcpStatusSummary)
	}

	cli, err := getPollableStatus(paths.PID, paths.Registry, paths.Snapshot, paths.AuthHandoff)
	if err != nil {
		t.Fatal(err)
	}
	if !cli.CredentialStored {
		t.Fatal("control: the fake token is not seen as stored; the probe is blind")
	}
	if cli.Authenticated || cli.AuthStatus == authStatusAuthenticated {
		t.Fatalf("CLI status with an unverified stored token: authenticated=%v auth_status=%q, want false and not authenticated", cli.Authenticated, cli.AuthStatus)
	}
	if mcp := mcpStatus(); mcp.Authenticated != cli.Authenticated || mcp.NodeAuthorized != cli.NodeAuthorized {
		t.Fatalf("MCP status authenticated=%v node_authorized=%v, CLI %v/%v; want one meaning", mcp.Authenticated, mcp.NodeAuthorized, cli.Authenticated, cli.NodeAuthorized)
	}

	// Control: once the daemon reports the node running, both surfaces say
	// authenticated.
	snapshot := tsruntime.NewSnapshot(4242, startedAt, statusRegistryFingerprint(t, paths.Registry), startedAt.Add(time.Second), []tsruntime.ServiceState{
		{Service: svc, RuntimeHost: "web.tailnet.ts.net"},
	})
	if err := tsruntime.Save(paths.Snapshot, snapshot); err != nil {
		t.Fatal(err)
	}
	cli, err = getPollableStatus(paths.PID, paths.Registry, paths.Snapshot, paths.AuthHandoff)
	if err != nil {
		t.Fatal(err)
	}
	if !cli.Authenticated || !cli.NodeAuthorized || cli.AuthStatus != authStatusAuthenticated {
		t.Fatalf("CLI status with a running node: authenticated=%v node_authorized=%v auth_status=%q", cli.Authenticated, cli.NodeAuthorized, cli.AuthStatus)
	}
	if mcp := mcpStatus(); !mcp.Authenticated || !mcp.NodeAuthorized {
		t.Fatalf("MCP status with a running node = %+v", mcp)
	}
}
