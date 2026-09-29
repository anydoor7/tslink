package runtime

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/monody0007/tslink/internal/config"
)

func writeNodeState(t *testing.T, configDir, name string) string {
	t.Helper()
	dir := filepath.Join(config.NodesDirIn(configDir), name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tailscaled.state"), []byte("machine key"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return dir
}

// TestRemoveServiceNodeStateStaysInTheGivenConfigDir is the property that keeps
// this function from being able to reach an installation it was not asked
// about: the config directory is an argument, and the process-wide one is not
// consulted even when it is set and points somewhere else.
//
// The two-directory shape is the whole test. A single-directory version would
// pass identically against an implementation that ignored the argument and read
// config.Dir(), because in that version the two are the same directory.
func TestRemoveServiceNodeStateStaysInTheGivenConfigDir(t *testing.T) {
	asked := t.TempDir()
	elsewhere := t.TempDir()
	t.Setenv(config.ConfigDirEnv, elsewhere)

	askedState := writeNodeState(t, asked, "report")
	elsewhereState := writeNodeState(t, elsewhere, "report")

	if err := RemoveServiceNodeState(asked, "report"); err != nil {
		t.Fatalf("RemoveServiceNodeState() error = %v", err)
	}
	if _, err := os.Stat(askedState); !os.IsNotExist(err) {
		t.Fatalf("Stat(%q) err = %v, want the state in the given config dir removed", askedState, err)
	}
	if _, err := os.Stat(elsewhereState); err != nil {
		t.Fatalf("Stat(%q) err = %v, want the process default config dir untouched", elsewhereState, err)
	}
}

// TestRemoveServiceNodeStateRefusesNamesThatAreNotServiceNames pins the guard
// in front of a RemoveAll. Each rejected name is one that would make the joined
// path leave the per-service directory.
func TestRemoveServiceNodeStateRefusesNamesThatAreNotServiceNames(t *testing.T) {
	configDir := t.TempDir()
	sibling := writeNodeState(t, configDir, "keepme")
	nodesDir := config.NodesDirIn(configDir)

	for _, name := range []string{"", ".", "..", "../..", "keepme/..", "/", "Report", "has space", "under_score"} {
		t.Run("name="+name, func(t *testing.T) {
			if err := RemoveServiceNodeState(configDir, name); err == nil {
				t.Fatalf("RemoveServiceNodeState(%q) error = nil, want rejection", name)
			}
		})
	}
	if _, err := os.Stat(sibling); err != nil {
		t.Fatalf("a rejected name removed a real service's state: %v", err)
	}
	if _, err := os.Stat(nodesDir); err != nil {
		t.Fatalf("a rejected name removed the nodes directory itself: %v", err)
	}

	// Control: a real service name is accepted, so the table above is rejecting
	// names rather than rejecting everything.
	if err := RemoveServiceNodeState(configDir, "keepme"); err != nil {
		t.Fatalf("RemoveServiceNodeState(valid name) error = %v", err)
	}
	if _, err := os.Stat(sibling); !os.IsNotExist(err) {
		t.Fatalf("Stat(%q) err = %v, want removed", sibling, err)
	}
}

// TestRemoveServiceNodeStateRefusesAnEmptyConfigDir covers the derivation
// failing upstream. Without this, an empty config dir would join to "nodes/<name>"
// and delete relative to the process working directory.
func TestRemoveServiceNodeStateRefusesAnEmptyConfigDir(t *testing.T) {
	if err := RemoveServiceNodeState("", "report"); err == nil {
		t.Fatal("RemoveServiceNodeState(\"\", ...) error = nil, want rejection")
	}
	if err := RemoveServiceNodeState("   ", "report"); err == nil {
		t.Fatal("RemoveServiceNodeState(whitespace, ...) error = nil, want rejection")
	}
}

func TestServiceNodeStateConfigDirIsTheRegistrySibling(t *testing.T) {
	// FromSlash keeps the fixture a native path: on Windows the result is
	// built with `\`, and a `/` literal would never compare equal to it.
	if got := ServiceNodeStateConfigDir(filepath.FromSlash("/home/u/.config/tslink/registry.json")); got != filepath.FromSlash("/home/u/.config/tslink") {
		t.Fatalf("ServiceNodeStateConfigDir() = %q, want the registry's own directory", got)
	}
	if got := ServiceNodeStateConfigDir(""); got != "" {
		t.Fatalf("ServiceNodeStateConfigDir(\"\") = %q, want empty so the removal refuses", got)
	}
}
