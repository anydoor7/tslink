package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// windowsDirCandidates returns an AppData and a legacy profile candidate in
// scratch space. Neither exists yet.
func windowsDirCandidates(t *testing.T) (current, legacy string) {
	t.Helper()
	return filepath.Join(t.TempDir(), "AppData", "Roaming", "tslink"), filepath.Join(t.TempDir(), ".config", "tslink")
}

// countingStat records every path the decision looks at, so a test can show
// the decision only reads.
func countingStat(seen *[]string) func(string) (os.FileInfo, error) {
	return func(path string) (os.FileInfo, error) {
		*seen = append(*seen, path)
		return os.Stat(path)
	}
}

func TestResolveWindowsConfigDirUsesAppDataWhenNoLegacyDirectory(t *testing.T) {
	current, legacy := windowsDirCandidates(t)
	var seen []string
	got, err := resolveWindowsConfigDir(current, legacy, countingStat(&seen))
	if err != nil || got != current {
		t.Fatalf("resolve(neither) = %q, %v; want %q", got, err, current)
	}
	if err := os.MkdirAll(current, 0o700); err != nil {
		t.Fatal(err)
	}
	got, err = resolveWindowsConfigDir(current, legacy, countingStat(&seen))
	if err != nil || got != current {
		t.Fatalf("resolve(current only) = %q, %v; want %q", got, err, current)
	}
	if _, err := os.Stat(legacy); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("resolution created the legacy directory: %v", err)
	}
}

// TestResolveWindowsConfigDirRefusesLegacyOnlyWithoutMoving is the finding:
// directory resolution used to rename the legacy directory into AppData, and
// silently kept using it when the rename failed. Now it moves nothing and
// says which one command moves it.
func TestResolveWindowsConfigDirRefusesLegacyOnlyWithoutMoving(t *testing.T) {
	current, legacy := windowsDirCandidates(t)
	state := filepath.Join(legacy, "nodes", "svc", "tailscaled.state")
	if err := os.MkdirAll(filepath.Dir(state), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(state, []byte("node key"), 0o600); err != nil {
		t.Fatal(err)
	}

	var seen []string
	for attempt := 1; attempt <= 2; attempt++ {
		got, err := resolveWindowsConfigDir(current, legacy, countingStat(&seen))
		if err == nil {
			t.Fatalf("attempt %d: resolve(legacy only) = %q, nil; want a refusal", attempt, got)
		}
		var legacyErr *LegacyConfigDirError
		if !errors.As(err, &legacyErr) || legacyErr.StableCode() != CodeLegacyConfigDirPresent {
			t.Fatalf("attempt %d: error = %#v, want %s", attempt, err, CodeLegacyConfigDirPresent)
		}
		for _, path := range []string{legacy, current} {
			if !strings.Contains(err.Error(), fmt.Sprintf("%q", path)) {
				t.Fatalf("attempt %d: message %q does not name %q", attempt, err, path)
			}
		}
		next := legacyErr.NextCommands()
		if want := `move "` + legacy + `" "` + current + `"`; len(next) != 1 || next[0] != want {
			t.Fatalf("attempt %d: next = %q, want the one command %q", attempt, next, want)
		}
		if data, err := os.ReadFile(state); err != nil || string(data) != "node key" {
			t.Fatalf("attempt %d: legacy node state was moved or changed: %q, %v", attempt, data, err)
		}
		if _, err := os.Stat(current); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("attempt %d: resolution created %q: %v", attempt, current, err)
		}
	}
	for _, path := range seen {
		if path != current && path != legacy {
			t.Fatalf("decision touched %q; it may only stat the two candidates", path)
		}
	}
}

func TestResolveWindowsConfigDirRefusesBothDirectories(t *testing.T) {
	current, legacy := windowsDirCandidates(t)
	for _, dir := range []string{current, legacy} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	_, err := resolveWindowsConfigDir(current, legacy, os.Stat)
	if err == nil || !strings.Contains(err.Error(), "both Windows config directories exist") ||
		!strings.Contains(err.Error(), fmt.Sprintf("%q", current)) || !strings.Contains(err.Error(), fmt.Sprintf("%q", legacy)) {
		t.Fatalf("resolve(both) error = %v, want the existing refusal naming both", err)
	}
}

func TestResolveWindowsConfigDirIgnoresALegacyFile(t *testing.T) {
	current, legacy := windowsDirCandidates(t)
	if err := os.MkdirAll(filepath.Dir(legacy), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, []byte("not a config dir"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := resolveWindowsConfigDir(current, legacy, os.Stat)
	if err != nil || got != current {
		t.Fatalf("resolve(legacy file) = %q, %v; want %q", got, err, current)
	}
}

func TestResolveWindowsConfigDirReportsUnreadableCandidates(t *testing.T) {
	current, legacy := windowsDirCandidates(t)
	denied := errors.New("synthetic access denied")
	for _, broken := range []string{current, legacy} {
		stat := func(path string) (os.FileInfo, error) {
			if path == broken {
				return nil, denied
			}
			return os.Stat(path)
		}
		if _, err := resolveWindowsConfigDir(current, legacy, stat); !errors.Is(err, denied) || !strings.Contains(err.Error(), strconv.Quote(broken)) {
			t.Fatalf("resolve with %q unreadable error = %v, want it named and wrapped", broken, err)
		}
	}
}
