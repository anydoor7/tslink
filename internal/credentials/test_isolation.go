package credentials

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/zalando/go-keyring"
)

// IsolateForTesting switches this process to go-keyring's in-memory mock, so
// a test can never read, write, or delete the operator's real OS keyring
// items. go-keyring offers no way back to the real provider once the mock is
// installed. It also moves the account credential lock into the config
// directory, which tests point at temporary directories, and refuses any lock
// path inside the real account home.
//
// Every test binary that links go-keyring calls this from an init function in
// a _test.go file; that runs before TestMain and before any test, and
// self-exec children re-run the same binary and install it again.
// TestEveryKeyringLinkingTestBinaryInstallsIsolation enforces the rule.
// Production code must never call it.
func IsolateForTesting() {
	keyring.MockInit()
	credentialMutationLockPathFunc = isolatedCredentialMutationLockPath
	credentialLockPathCheck = refuseRealAccountHomeLockPath
}

// In a test binary the account lock is the config-directory lock, so it is
// created and removed with the test's temporary config directory.
func isolatedCredentialMutationLockPath() (string, error) {
	dir, err := config.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "credentials.lock"), nil
}

// refuseRealAccountHomeLockPath fails closed: a lock path inside the real
// account home is refused unless it is also inside the temporary directory
// (Windows keeps TEMP under the profile).
func refuseRealAccountHomeLockPath(path string) error {
	account, err := user.Current()
	if err != nil || account.HomeDir == "" {
		return fmt.Errorf("test isolation cannot resolve the OS account home to vet credential lock %s: %v", path, err)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("test isolation cannot vet credential lock %s: %w", path, err)
	}
	if isPathWithin(abs, os.TempDir()) || !isPathWithin(abs, account.HomeDir) {
		return nil
	}
	return fmt.Errorf("test isolation refused credential lock %s inside the real account home; point TSLINK_CONFIG_DIR or SetMutationLockPathForTesting at a temporary directory", path)
}

func isPathWithin(path, dir string) bool {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
