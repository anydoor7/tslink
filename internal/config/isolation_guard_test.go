package config

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// isolationRootEnv is testenv.RootEnv. It is spelled out so this guard does
// not depend on the mechanism it checks.
const isolationRootEnv = "TSLINK_TESTENV_ROOT"

// TestConfigAndHomeLookupsResolveUnderTheIsolationRoot (G3) checks, inside a
// running test binary, what the shared isolation (testenv.Main) promises:
// TSLink's config directory, with and without TSLINK_CONFIG_DIR, and the
// standard library's home, config and cache lookups all resolve inside the
// binary's temporary root, and the only TSLINK_ variables in the process are
// the ones the isolation itself sets.
func TestConfigAndHomeLookupsResolveUnderTheIsolationRoot(t *testing.T) {
	root := os.Getenv(isolationRootEnv)
	if root == "" || !filepath.IsAbs(root) {
		dir, _ := Dir()
		t.Errorf("%s = %q: this test binary is not running under testenv.Main, so config.Dir() resolves to %q, "+
			"which is the contributor's own config directory unless something else moved it", isolationRootEnv, root, dir)
	} else if info, err := os.Stat(root); err != nil || !info.IsDir() {
		t.Errorf("%s = %q is not an existing directory: %v", isolationRootEnv, root, err)
	}

	under := func(label, path string, err error) {
		t.Helper()
		if err != nil {
			t.Errorf("%s: %v", label, err)
			return
		}
		rel, relErr := filepath.Rel(root, path)
		if root == "" || relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			t.Errorf("%s = %q, want it inside the isolation root %q", label, path, root)
		}
	}
	dir, err := Dir()
	under("config.Dir()", dir, err)
	home, err := os.UserHomeDir()
	under("os.UserHomeDir()", home, err)
	userConfig, err := os.UserConfigDir()
	under("os.UserConfigDir()", userConfig, err)
	userCache, err := os.UserCacheDir()
	under("os.UserCacheDir()", userCache, err)

	allowed := map[string]bool{
		isolationRootEnv:                      true,
		"TSLINK_DOCTOR_SKIP_TAILSCALE_SSH":    true,
		ConfigDirEnv:                          true,
		"TSLINK_SERVICE_MANAGER_GUARD_REPORT": true,
		"TSLINK_NETWORK_GUARD_REPORT":         true,
	}
	var unexpected []string
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(name, "TSLINK_") && !allowed[name] {
			unexpected = append(unexpected, name)
		}
	}
	sort.Strings(unexpected)
	if len(unexpected) > 0 {
		t.Errorf("TSLINK_ variables the isolation did not set are visible to tests: %v", unexpected)
	}

	// The platform default, which is what a test that clears the override
	// reaches (on Windows it stats, and may migrate, the legacy directory).
	t.Setenv(ConfigDirEnv, "")
	defaultDir, err := Dir()
	under("config.Dir() with TSLINK_CONFIG_DIR cleared", defaultDir, err)
}
