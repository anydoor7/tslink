package cmd

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monody0007/tslink/internal/inspect"
	"github.com/monody0007/tslink/internal/output"
	"github.com/monody0007/tslink/internal/registry"
)

// stubAddWritePaths points `tslink add` at a scratch registry and keeps it
// from touching any background service.
func stubAddWritePaths(t *testing.T) string {
	t.Helper()
	regPath := filepath.Join(t.TempDir(), "registry.json")
	oldRegPath, oldEnsureDir, oldEnsureDaemon, oldRunning := registryPathFn, ensureDirFn, ensureDaemonFn, isRunningFn
	t.Cleanup(func() {
		registryPathFn, ensureDirFn, ensureDaemonFn, isRunningFn = oldRegPath, oldEnsureDir, oldEnsureDaemon, oldRunning
	})
	registryPathFn = func() (string, error) { return regPath, nil }
	ensureDirFn = func() error { return nil }
	ensureDaemonFn = func(context.Context, io.Writer, bool) error { return nil }
	isRunningFn = func(string) bool { return false }
	return regPath
}

// TestRegistryCheckReportsHandWrittenFunnelWithoutExpiry is the audit's
// probe: a hand-written Funnel with public_ack and no funnel_expires_at used to
// pass `registry check` with no issues and list as public forever.
func TestRegistryCheckReportsHandWrittenFunnelWithoutExpiry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.json")
	if err := os.WriteFile(path, []byte(`{"schema_version":1,"services":[{"name":"pub","type":"proxy","target":"http://localhost:3000","funnel":true,"public_ack":true}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := registryCheck(path)
	if err == nil {
		t.Fatalf("registryCheck = %+v, nil; want the undecided Funnel reported", result)
	}
	if len(result.Issues) != 1 || result.Issues[0].Code != registry.CodeFunnelExpiryRequired {
		t.Fatalf("issues = %+v, want one %s issue", result.Issues, registry.CodeFunnelExpiryRequired)
	}
	failure := output.NewFailureForError("registry check", err)
	if failure.Error == nil || failure.Error.Code != registry.CodeFunnelExpiryRequired || failure.Code != output.ExitUsage {
		t.Fatalf("failure envelope = %+v, want %s with exit %d", failure, registry.CodeFunnelExpiryRequired, output.ExitUsage)
	}
	next := strings.Join(failure.Error.Next, "\n")
	if !strings.Contains(next, `"funnel_expires_at": "never"`) || !strings.Contains(next, "RFC 3339") {
		t.Fatalf("next = %q, want both ways to decide the lifetime", next)
	}
}

// TestAddFunnelTTLNeverStoresExplicitNever pins the CLI and MCP writers of
// "never": the stored form is explicit and registry check accepts it.
func TestAddFunnelTTLNeverStoresExplicitNever(t *testing.T) {
	regPath := stubAddWritePaths(t)
	if _, err := runAddCmdOutput(t, []string{"cli-forever"}, map[string]string{
		"proxy": "localhost:3000", "funnel": "true", "public": "true", "funnel-ttl": "never",
	}); err != nil {
		t.Fatalf("add --funnel-ttl never: %v", err)
	}
	paths := mcpSharePaths(t)
	paths.Registry = regPath
	if _, err := defaultMCPActions(paths, io.Discard).add(context.Background(), AddParams{
		Name: "mcp-forever", Proxy: "localhost:3001", Funnel: true, Public: true,
		FunnelTTL: "never", FunnelTTLSet: true, NoDaemonInstall: true,
	}, false); err != nil {
		t.Fatalf("MCP add funnel_ttl never: %v", err)
	}

	data, err := os.ReadFile(regPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(data), `"funnel_expires_at": "never"`); got != 2 {
		t.Fatalf("registry.json = %s, want both never entries stored explicitly", data)
	}
	result, err := registryCheck(regPath)
	if err != nil || len(result.Issues) != 0 || result.ValidServices != 2 {
		t.Fatalf("registryCheck = %+v, %v; want two valid services and no issues", result, err)
	}
}

// TestShareResultTellsTheThreeExposuresApart pins the share result against
// the three exposures an agent has to tell apart: tailnet-only, public until a
// deadline, and public with no deadline.
func TestShareResultTellsTheThreeExposuresApart(t *testing.T) {
	actions, _ := shareMCPWireActions(t)
	tailnet := callMCPShare(t, actions, `{"target":"4000"}`)
	until := callMCPShare(t, actions, `{"target":"3000","funnel":true,"public_ack":true,"funnel_ttl":"1h"}`)
	forever := callMCPShare(t, actions, `{"target":"3001","funnel":true,"public_ack":true,"funnel_ttl":"never"}`)

	exposureKind := func(name string, result map[string]any) string {
		t.Helper()
		exposure, ok := result["exposure"].(map[string]any)
		if !ok {
			t.Fatalf("%s share result has no exposure: %v", name, result)
		}
		kind, _ := exposure["kind"].(string)
		return kind
	}
	if kind := exposureKind("tailnet", tailnet); kind != inspect.ExposureTailnet {
		t.Fatalf("tailnet share exposure = %q, want %q", kind, inspect.ExposureTailnet)
	}
	if _, ok := tailnet["funnel_expires_at"]; ok {
		t.Fatalf("tailnet share carries funnel_expires_at: %v", tailnet)
	}
	if kind := exposureKind("until", until); kind != inspect.ExposurePublicFunnel {
		t.Fatalf("1h share exposure = %q, want %q", kind, inspect.ExposurePublicFunnel)
	}
	if _, ok := until["funnel_expires_at"].(string); !ok {
		t.Fatalf("1h share has no deadline: %v", until)
	}
	if kind := exposureKind("forever", forever); kind != inspect.ExposurePublicFunnel {
		t.Fatalf("never share exposure = %q, want %q", kind, inspect.ExposurePublicFunnel)
	}
	if _, ok := forever["funnel_expires_at"]; ok {
		t.Fatalf("never share carries a deadline: %v", forever)
	}
}
