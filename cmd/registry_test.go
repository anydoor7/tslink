package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
