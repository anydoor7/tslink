package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monody0007/tslink/internal/output"
	"github.com/monody0007/tslink/internal/registry"
)

func TestRegistryCheckValidatesCopiesWithoutChangingThem(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.json")
	original := []byte(`{"schema_version":1,"services":[{"name":"web","type":"proxy","target":"http://localhost:3000"}]}`)
	if err := os.WriteFile(path, original, 0o640); err != nil {
		t.Fatal(err)
	}

	result, err := registryCheck(path)
	if err != nil {
		t.Fatalf("registryCheck() error = %v", err)
	}
	if result.Path != path || result.ValidServices != 1 || result.TotalServices != 1 || len(result.Issues) != 0 {
		t.Fatalf("registryCheck() result = %+v", result)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(original) {
		t.Fatalf("registry check changed file:\n got %s\nwant %s", after, original)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("registry check changed mode to %o, want 640", info.Mode().Perm())
	}
}

// TestRegistryCheckTreatsAbsentRegistryAsNormalFirstRun pins the first-run
// contract: a config directory that has never had a service registered is a
// normal empty state, not an internal failure.
func TestRegistryCheckTreatsAbsentRegistryAsNormalFirstRun(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "registry.json")

	result, err := registryCheck(path)
	if err != nil {
		t.Fatalf("registryCheck() on a first run error = %v, want nil", err)
	}
	if result.Path != path {
		t.Fatalf("result.Path = %q, want %q", result.Path, path)
	}
	if result.ValidServices != 0 || result.TotalServices != 0 || len(result.Issues) != 0 {
		t.Fatalf("registryCheck() first-run result = %+v, want an empty registry", result)
	}
	if result.SchemaVersion != registry.CurrentRegistrySchemaVersion {
		t.Fatalf("result.SchemaVersion = %d, want %d", result.SchemaVersion, registry.CurrentRegistrySchemaVersion)
	}

	// A read-only validator must not bring the file into existence.
	if _, statErr := os.Lstat(path); !os.IsNotExist(statErr) {
		t.Fatalf("registry check created or touched %s (stat err = %v); it must not write", path, statErr)
	}
}

// TestRegistryCheckStillRejectsAnExistingButEmptyRegistry pins the other side
// of the boundary drawn in registryCheck: only the absent state is normal.
// Registry writes are atomic, so a zero-byte registry.json is a truncation or a
// hand-made placeholder, and the strict validator must keep reporting it.
func TestRegistryCheckStillRejectsAnExistingButEmptyRegistry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.json")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := registryCheck(path); err == nil {
		t.Fatal("registryCheck() on an existing empty registry error = nil, want an error")
	}
}

// TestRegistryCheckLeavesAnInvalidRegistryUntouched extends the "validate
// without modifying" contract to the failure path.
//
// TestRegistryCheckValidatesCopiesWithoutChangingThem covers a valid file, but
// the interesting case is the broken one: that is when an operator is most
// likely to be inspecting a copy and comparing it byte for byte against the
// original. registryCheck only decides whether the path is absent after
// Preflight has failed, so this is the branch where reaching for a loader that
// repairs permission bits (registry.LoadWithFileState calls
// atomicfile.ConvergePrivateFile) would silently rewrite the mode of the very
// artifact under investigation. The failure path uses os.Lstat, which observes
// the path without opening, following or modifying it, and this test is what
// holds that in place.
func TestRegistryCheckLeavesAnInvalidRegistryUntouched(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.json")
	original := []byte(`{"schema_version":1,"services":[{"name":"web","type":"proxy"`)
	if err := os.WriteFile(path, original, 0o640); err != nil {
		t.Fatal(err)
	}

	if _, err := registryCheck(path); err == nil {
		t.Fatal("registryCheck() on a truncated registry error = nil, want an error")
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(original) {
		t.Fatalf("registry check rewrote an invalid registry:\n got %s\nwant %s", after, original)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("registry check changed the mode of an invalid registry to %o, want 640", info.Mode().Perm())
	}
}

// TestCompiledRegistryCheckAndListAgreeOnFirstRun is the cross-command half of
// the contract, run against the shipped binary: on a config directory with no
// registry yet, `registry check` and `list` must classify the state the same
// way. They disagreeing is the defect an earlier review surfaced.
func TestCompiledRegistryCheckAndListAgreeOnFirstRun(t *testing.T) {
	binary := compiledTSLinkBinary(t)
	configDir := t.TempDir()

	for _, command := range [][]string{
		{"registry", "check", "--json"},
		{"list", "--json"},
	} {
		t.Run(strings.Join(command, "_"), func(t *testing.T) {
			stdout, stderr, exitCode := runTSLinkBinaryWithConfigDir(t, binary, configDir, "", command...)
			if exitCode != output.ExitSuccess {
				t.Fatalf("tslink %v on a first run exit = %d, want %d\nstdout=%s\nstderr=%s",
					command, exitCode, output.ExitSuccess, stdout, stderr)
			}
			results := parseCompiledJSONLines(t, stdout)
			if len(results) != 1 {
				t.Fatalf("tslink %v envelope count = %d, want 1\nstdout=%s", command, len(results), stdout)
			}
			if !results[0].OK {
				t.Fatalf("tslink %v on a first run reported ok=false: %+v", command, results[0].Error)
			}
			if results[0].Code != output.ExitSuccess {
				t.Fatalf("tslink %v envelope code = %d, want %d", command, results[0].Code, output.ExitSuccess)
			}
		})
	}

	// Neither command may create the registry it was asked to inspect.
	if _, err := os.Lstat(filepath.Join(configDir, "registry.json")); !os.IsNotExist(err) {
		t.Fatalf("a read-only first run created registry.json (stat err = %v)", err)
	}
}

func TestUnknownRegistryKeyNextNamesExecutableRegistryCheck(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.json")
	if err := os.WriteFile(path, []byte(`{"schema_version":1,"services":[{"name":"web","type":"proxy","target":"http://localhost:3000","allow":["alice@example.com"]}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, issues, err := registry.Preflight(path)
	if err != nil || len(issues) != 1 {
		t.Fatalf("Preflight() issues=%+v err=%v", issues, err)
	}
	var recovery interface{ NextCommands() []string }
	if !errors.As(issues[0].Err, &recovery) {
		t.Fatalf("issue %T has no recovery commands", issues[0].Err)
	}
	next := recovery.NextCommands()
	if len(next) != 1 || next[0] != "tslink registry check --json" {
		t.Fatalf("next = %v, want executable registry check", next)
	}
	args := strings.Fields(strings.TrimPrefix(next[0], "tslink "))
	command, remaining, err := rootCmd.Find(args)
	if err != nil || command == nil || command.CommandPath() != "tslink registry check" || len(remaining) != 1 || remaining[0] != "--json" {
		t.Fatalf("rootCmd.Find(%v) = command:%v remaining:%v err:%v", args, command, remaining, err)
	}
}
