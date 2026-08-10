package testenv

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSetHomeSetsConfigOverrideBeforeDirectoryExists(t *testing.T) {
	home := filepath.Join(t.TempDir(), "not-created", "home")
	configDir := SetHome(t, home)

	if got := os.Getenv("HOME"); got != home {
		t.Fatalf("HOME = %q, want %q", got, home)
	}
	if got := os.Getenv(configDirEnv); got != configDir || got == "" {
		t.Fatalf("%s = %q, want non-empty %q", configDirEnv, got, configDir)
	}
	if _, err := os.Stat(configDir); !os.IsNotExist(err) {
		t.Fatalf("SetHome created config directory or returned unexpected stat error: %v", err)
	}
}

func TestConfigDirRejectsEmptyHome(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("ConfigDir(\"\") did not panic")
		}
	}()
	_ = ConfigDir("")
}

func TestSetHomeRestoresPriorConfigOverride(t *testing.T) {
	t.Setenv(configDirEnv, "preexisting-config")
	t.Setenv("HOME", "preexisting-home")
	t.Run("isolated", func(t *testing.T) {
		SetHome(t, filepath.Join(t.TempDir(), "isolated-home"))
	})
	if got := os.Getenv(configDirEnv); got != "preexisting-config" {
		t.Fatalf("%s after cleanup = %q, want prior value", configDirEnv, got)
	}
	if got := os.Getenv("HOME"); got != "preexisting-home" {
		t.Fatalf("HOME after cleanup = %q, want prior value", got)
	}
}
