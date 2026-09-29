package credentials

import (
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monody0007/tslink/internal/config"
)

func realAccountHome(t *testing.T) string {
	t.Helper()
	account, err := user.Current()
	if err != nil || account.HomeDir == "" {
		t.Fatalf("resolve OS account home: %v", err)
	}
	return account.HomeDir
}

func pathInside(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// Only resolves paths; nothing is opened, so a regression cannot touch the
// real account home while this test reports it.
func TestTestBinaryAccountLockStaysOutOfRealAccountHome(t *testing.T) {
	t.Setenv("TSLINK_DISABLE_KEYRING", "0")
	configDir := t.TempDir()
	t.Setenv(config.ConfigDirEnv, configDir)
	path, err := credentialMutationLockPathFunc()
	if err != nil {
		t.Fatal(err)
	}
	if pathInside(path, realAccountHome(t)) && !pathInside(path, os.TempDir()) {
		t.Fatalf("test-binary account lock resolves inside the real account home: %s", path)
	}
	if filepath.Dir(path) != configDir {
		t.Fatalf("test-binary account lock = %s, want it inside the test config directory %s", path, configDir)
	}
}

func TestIsolationRefusesLockPathsInRealAccountHome(t *testing.T) {
	home := realAccountHome(t)
	if credentialLockPathCheck == nil {
		t.Fatal("test isolation installed no credential lock path check")
	}
	for _, path := range []string{
		filepath.Join(home, ".tslink", "credentials.lock"),
		filepath.Join(home, ".config", "tslink", "credentials.lock"),
	} {
		if pathInside(path, os.TempDir()) {
			continue
		}
		if err := credentialLockPathCheck(path); err == nil || !strings.Contains(err.Error(), "real account home") {
			t.Fatalf("lock path check accepted %s: %v", path, err)
		}
	}
	// Control: an ordinary temporary lock must stay allowed.
	if err := credentialLockPathCheck(filepath.Join(t.TempDir(), "credentials.lock")); err != nil {
		t.Fatalf("lock path check refused a temporary path: %v", err)
	}
}

func TestLockPathCheckRunsBeforeAnyFileIsCreated(t *testing.T) {
	setup(t)
	lockDir := filepath.Join(t.TempDir(), "refused")
	t.Cleanup(SetMutationLockPathForTesting(filepath.Join(lockDir, "credentials.lock")))
	old := credentialLockPathCheck
	t.Cleanup(func() { credentialLockPathCheck = old })
	refused := errors.New("synthetic refusal")
	credentialLockPathCheck = func(string) error { return refused }
	unlock, err := acquireCredentialMutationLock()
	if err == nil {
		unlock()
		t.Fatal("credential lock acquired although the path check refused it")
	}
	if !errors.Is(err, refused) {
		t.Fatalf("acquire error = %v, want the path check refusal", err)
	}
	if _, statErr := os.Stat(lockDir); !os.IsNotExist(statErr) {
		t.Fatalf("refused lock path was created anyway: %v", statErr)
	}
	// The in-process gate must be released after the refusal.
	credentialLockPathCheck = old
	unlock, err = acquireCredentialMutationLock()
	if err != nil {
		t.Fatalf("lock unavailable after a refused acquisition: %v", err)
	}
	unlock()
}
