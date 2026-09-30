package cmd

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/inspect"
)

// seedLostRegistryState leaves the config directory holding regPath the way a
// lost registry.json leaves it: node state for a, an identity record for b,
// an unretired ownership row for d, and a retired row for a removed service.
func seedLostRegistryState(t *testing.T, regPath string) {
	t.Helper()
	configDir := filepath.Dir(regPath)
	if err := os.MkdirAll(filepath.Join(config.NodesDirIn(configDir), "a"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(config.NodeIdentitiesDirIn(configDir), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config.NodeIdentitiesDirIn(configDir), "b.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	ledger := `{"schema_version":2,"nodes":[` +
		`{"service_name":"d","node_id":"n-d","recorded_at":"2026-09-01T00:00:00Z"},` +
		`{"service_name":"old","node_id":"n-old","recorded_at":"2026-09-01T00:00:00Z","retired_at":"2026-09-02T00:00:00Z"}]}`
	if err := os.WriteFile(config.NodeOwnershipPathIn(configDir), []byte(ledger), 0o600); err != nil {
		t.Fatal(err)
	}
}

func findWarning(warnings []inspect.WarningView, code string) (inspect.WarningView, bool) {
	for _, warning := range warnings {
		if warning.Code == code {
			return warning, true
		}
	}
	return inspect.WarningView{}, false
}

// TestAddThatRecreatesRegistryWarnsAboutOrphanedNodeState is A1's run2: a lost
// registry.json followed by add c used to succeed with no word about the node
// state of the services the old registry listed.
func TestAddThatRecreatesRegistryWarnsAboutOrphanedNodeState(t *testing.T) {
	regPath := stubAddWritePaths(t)
	seedLostRegistryState(t, regPath)
	paths := mcpSharePaths(t)
	paths.Registry = regPath
	actions := defaultMCPActions(paths, io.Discard)

	result, err := actions.add(context.Background(), AddParams{Name: "c", Proxy: "127.0.0.1:3000", NoDaemonInstall: true}, true)
	if err != nil {
		t.Fatal(err)
	}
	validateAgainstToolOutputSchema(t, "add", result)
	warning, ok := findWarning(result.(AddResult).Warnings, inspect.WarningCodeRegistryRecreated)
	if !ok {
		t.Fatalf("add result warnings = %+v, want %s", result.(AddResult).Warnings, inspect.WarningCodeRegistryRecreated)
	}
	// "3 services (a, b, d)": the retired row of a removed service and the
	// service just added are not counted.
	for _, want := range []string{"3 services", "(a, b, d)", "restore the old registry.json"} {
		if !strings.Contains(warning.Message, want) {
			t.Fatalf("warning %q does not say %q", warning.Message, want)
		}
	}

	// The registry exists now; the next add creates nothing and says nothing.
	result, err = actions.add(context.Background(), AddParams{Name: "e", Proxy: "127.0.0.1:3001", NoDaemonInstall: true}, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := findWarning(result.(AddResult).Warnings, inspect.WarningCodeRegistryRecreated); ok {
		t.Fatalf("add to an existing registry warned: %+v", result.(AddResult).Warnings)
	}
}

func TestFirstAddWithoutNodeStateDoesNotWarn(t *testing.T) {
	regPath := stubAddWritePaths(t)
	paths := mcpSharePaths(t)
	paths.Registry = regPath
	result, err := defaultMCPActions(paths, io.Discard).add(context.Background(), AddParams{Name: "c", Proxy: "127.0.0.1:3000", NoDaemonInstall: true}, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := findWarning(result.(AddResult).Warnings, inspect.WarningCodeRegistryRecreated); ok {
		t.Fatalf("first add on a clean config directory warned: %+v", result.(AddResult).Warnings)
	}
}

func TestShareThatRecreatesRegistryWarnsAboutOrphanedNodeState(t *testing.T) {
	actions, regPath := shareMCPWireActions(t)
	seedLostRegistryState(t, regPath)
	shared := callMCPShare(t, actions, `{"target":"3000"}`)
	if !resultHasWarning(shared, inspect.WarningCodeRegistryRecreated) {
		t.Fatalf("share result = %v, want %s", shared, inspect.WarningCodeRegistryRecreated)
	}
	again := callMCPShare(t, actions, `{"target":"3001"}`)
	if resultHasWarning(again, inspect.WarningCodeRegistryRecreated) {
		t.Fatalf("share into an existing registry warned: %v", again)
	}
}
