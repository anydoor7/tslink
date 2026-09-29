// Package testenv provides cross-platform process environment isolation for tests.
package testenv

import (
	"path/filepath"
	"testing"
)

const configDirEnv = "TSLINK_CONFIG_DIR"

// ConfigDir returns the non-empty TSLink config override associated with home.
// It deliberately does not require the directory to exist yet.
func ConfigDir(home string) string {
	if home == "" {
		panic("testenv.ConfigDir requires a non-empty home")
	}
	return filepath.Join(home, ".config", "tslink")
}

// SetHome points every home, config, data and cache lookup of the process at
// home (the same variable set Main uses, see HomeEnv) and returns TSLink's
// config override. The override is always non-empty and does not need to
// exist yet; testing.TB.Setenv restores the prior environment exactly during
// cleanup.
func SetHome(t testing.TB, home string) string {
	t.Helper()
	if home == "" {
		t.Fatal("testenv.SetHome requires a non-empty home")
	}

	for _, kv := range HomeEnv(home) {
		t.Setenv(kv[0], kv[1])
	}
	return ConfigDir(home)
}
