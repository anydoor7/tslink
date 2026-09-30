//go:build windows

package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setWindowsDefaultRoots(t *testing.T) (current, legacy string) {
	t.Helper()
	t.Setenv(ConfigDirEnv, "")
	appData := filepath.Join(t.TempDir(), "AppData", "Roaming")
	profile := filepath.Join(t.TempDir(), "profile")
	t.Setenv("APPDATA", appData)
	t.Setenv("USERPROFILE", profile)
	return filepath.Join(appData, "tslink"), filepath.Join(profile, ".config", "tslink")
}

func TestWindowsDefaultConfigDirUsesAppData(t *testing.T) {
	current, _ := setWindowsDefaultRoots(t)

	got, err := Dir()
	if err != nil {
		t.Fatalf("Dir() error = %v", err)
	}
	if got != current {
		t.Fatalf("Dir() = %q, want AppData path %q", got, current)
	}
}

// TestWindowsDefaultConfigDirRefusesLegacyOnlyWithoutMoving replaces the two
// tests of the removed migration (it moved the legacy directory, and silently
// kept using it when the move failed): Dir() now only reads, and refuses.
func TestWindowsDefaultConfigDirRefusesLegacyOnlyWithoutMoving(t *testing.T) {
	current, legacy := setWindowsDefaultRoots(t)
	if err := os.MkdirAll(legacy, 0o700); err != nil {
		t.Fatalf("MkdirAll(legacy) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "registry.json"), []byte("legacy fixture"), 0o600); err != nil {
		t.Fatalf("WriteFile(legacy) error = %v", err)
	}

	got, err := Dir()
	var legacyErr *LegacyConfigDirError
	if !errors.As(err, &legacyErr) {
		t.Fatalf("Dir() = %q, %v; want LegacyConfigDirError", got, err)
	}
	if legacyErr.Legacy != legacy || legacyErr.Current != current {
		t.Fatalf("LegacyConfigDirError = %+v, want legacy %q and current %q", legacyErr, legacy, current)
	}
	if data, err := os.ReadFile(filepath.Join(legacy, "registry.json")); err != nil || string(data) != "legacy fixture" {
		t.Fatalf("legacy registry was moved or changed: %q, %v", data, err)
	}
	if _, err := os.Stat(current); !os.IsNotExist(err) {
		t.Fatalf("current config unexpectedly exists: %v", err)
	}
}

func TestWindowsDefaultConfigDirRejectsConflictingDirectories(t *testing.T) {
	current, legacy := setWindowsDefaultRoots(t)
	for _, dir := range []string{current, legacy} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("MkdirAll(%q) error = %v", dir, err)
		}
	}

	_, err := Dir()
	if err == nil {
		t.Fatal("Dir() error = nil, want conflict error")
	}
	if !strings.Contains(err.Error(), fmt.Sprintf("%q", current)) || !strings.Contains(err.Error(), fmt.Sprintf("%q", legacy)) {
		t.Fatalf("Dir() error = %v, want both conflicting paths", err)
	}
	for _, dir := range []string{current, legacy} {
		if _, statErr := os.Stat(dir); statErr != nil {
			t.Fatalf("conflict handling modified %q: %v", dir, statErr)
		}
	}
}
