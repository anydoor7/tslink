package cmd

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anydoor7/tslink/internal/inspect"
	"github.com/anydoor7/tslink/internal/output"
	"github.com/anydoor7/tslink/internal/registry"
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
	if !strings.Contains(next, "finite") || !strings.Contains(next, "RFC 3339") {
		t.Fatalf("next = %q, want finite deadline recovery", next)
	}
}

// New public never is refused on both surfaces without writing a registry.
func TestAddFunnelTTLRefusesNever(t *testing.T) {
	regPath := stubAddWritePaths(t)
	if _, err := runAddCmdOutput(t, []string{"cli-forever"}, map[string]string{"proxy": "localhost:3000", "funnel": "true", "public": "true", "funnel-ttl": "never"}); err == nil || !strings.Contains(err.Error(), "never is allowed only") {
		t.Fatalf("CLI never: %v", err)
	}
	paths := mcpSharePaths(t)
	paths.Registry = regPath
	if _, err := defaultMCPActions(paths, io.Discard).add(context.Background(), AddParams{Name: "mcp-forever", Proxy: "localhost:3001", Funnel: true, Public: true, FunnelTTL: "never", FunnelTTLSet: true, NoDaemonInstall: true}, false); err == nil || !strings.Contains(err.Error(), "never is allowed only") {
		t.Fatalf("MCP never: %v", err)
	}
	if _, err := os.Stat(regPath); !os.IsNotExist(err) {
		t.Fatalf("refused lifetime created registry: %v", err)
	}
}

// TestShareResultTellsTheThreeExposuresApart pins the share result against
// the three exposures an agent has to tell apart: tailnet-only, public until a
// deadline, and public with no deadline.
func TestShareResultTellsTheThreeExposuresApart(t *testing.T) {
	actions, path := shareMCPWireActions(t)
	tailnet := callMCPShare(t, actions, `{"target":"4000"}`)
	until := callMCPShare(t, actions, `{"target":"3000","funnel":true,"public_ack":true,"funnel_ttl":"1h"}`)
	// Preserve and project a legacy explicit permanent entry losslessly.
	legacy := registry.Service{Name: "legacy-public", Type: registry.TypeProxy, Target: "http://localhost:3001", Funnel: true, PublicAck: true}
	if _, err := registry.Add(path, legacy); err != nil {
		t.Fatal(err)
	}
	stored, err := registry.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	var forever map[string]any
	for _, svc := range stored.Services {
		if svc.Name == legacy.Name {
			result := withShareFunnelState(ShareResult{}, shareRegistration{Service: svc})
			wire, _ := json.Marshal(result)
			if err := json.Unmarshal(wire, &forever); err != nil {
				t.Fatal(err)
			}
		}
	}

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
