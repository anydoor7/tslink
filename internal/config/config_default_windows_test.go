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

func TestWindowsDefaultConfigDirMigratesLegacyDirectory(t *testing.T) {
	current, legacy := setWindowsDefaultRoots(t)
	if err := os.MkdirAll(legacy, 0o700); err != nil {
		t.Fatalf("MkdirAll(legacy) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "registry.json"), []byte("legacy fixture"), 0o600); err != nil {
		t.Fatalf("WriteFile(legacy) error = %v", err)
	}

	got, err := Dir()
	if err != nil {
		t.Fatalf("Dir() error = %v", err)
	}
	if got != current {
		t.Fatalf("Dir() = %q, want migrated path %q", got, current)
	}
	data, err := os.ReadFile(filepath.Join(current, "registry.json"))
	if err != nil {
		t.Fatalf("ReadFile(migrated registry) error = %v", err)
	}
	if string(data) != "legacy fixture" {
		t.Fatalf("migrated registry = %q, want legacy fixture", data)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatalf("legacy directory still exists after migration: %v", err)
	}
}

func TestWindowsDefaultConfigDirKeepsLegacyOnMigrationFailure(t *testing.T) {
	current, legacy := setWindowsDefaultRoots(t)
	if err := os.MkdirAll(legacy, 0o700); err != nil {
		t.Fatalf("MkdirAll(legacy) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "config.json"), []byte("legacy fixture"), 0o600); err != nil {
		t.Fatalf("WriteFile(legacy) error = %v", err)
	}
	orig := renameConfigDir
	renameConfigDir = func(string, string) error { return errors.New("synthetic cross-volume move failure") }
	t.Cleanup(func() { renameConfigDir = orig })

	got, err := Dir()
	if err != nil {
		t.Fatalf("Dir() error = %v", err)
	}
	if got != legacy {
		t.Fatalf("Dir() = %q, want preserved legacy path %q", got, legacy)
	}
	if _, err := os.Stat(filepath.Join(legacy, "config.json")); err != nil {
		t.Fatalf("legacy config was not preserved: %v", err)
	}
	if _, err := os.Stat(current); !os.IsNotExist(err) {
		t.Fatalf("current config unexpectedly exists after failed migration: %v", err)
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
