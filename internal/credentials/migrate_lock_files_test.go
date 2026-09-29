package credentials

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/monody0007/tslink/internal/config"
	"github.com/zalando/go-keyring"
)

// serve calls MigrateFromLegacy on every start. Installs that never had a
// legacy API key file must not get lock files from it.
func TestMigrateFromLegacyWithoutLegacyFileCreatesNoLockFiles(t *testing.T) {
	setup(t)
	t.Setenv("TSLINK_DISABLE_KEYRING", "0")
	accountLock, err := credentialMutationLockPathFunc()
	if err != nil {
		t.Fatal(err)
	}
	configDir, err := config.Dir()
	if err != nil {
		t.Fatal(err)
	}
	locks := []string{accountLock, filepath.Join(configDir, "credentials.lock")}
	if MigrateFromLegacy() {
		t.Fatal("migration reported success without a legacy file")
	}
	for _, path := range locks {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("migration without a legacy file created lock file %s: %v", path, err)
		}
	}
	// Control: with a legacy file the migration does take the lock.
	if err := os.WriteFile(apiKeyPath(t), []byte("tskey-api-FAKE-legacy"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !MigrateFromLegacy() {
		t.Fatal("migration of a legacy file failed")
	}
	if _, err := os.Stat(accountLock); err != nil {
		t.Fatalf("migration of a legacy file did not take the credential lock: %v", err)
	}
}

// The existence check before the lock is only a shortcut: the file is read
// again under the lock, so one removed meanwhile is not migrated.
func TestMigrateFromLegacyRechecksLegacyFileUnderLock(t *testing.T) {
	setup(t)
	if err := os.WriteFile(apiKeyPath(t), []byte("tskey-api-FAKE-legacy"), 0o600); err != nil {
		t.Fatal(err)
	}
	held := make(chan struct{})
	release := make(chan struct{})
	txDone := make(chan error, 1)
	go func() {
		txDone <- WithMutationTransaction(func(*MutationTransaction) error {
			close(held)
			<-release
			// Another process migrated or removed the file meanwhile.
			return os.Remove(apiKeyPath(t))
		})
	}()
	<-held
	migrated := make(chan bool, 1)
	go func() { migrated <- MigrateFromLegacy() }()
	time.Sleep(100 * time.Millisecond)
	close(release)
	if err := <-txDone; err != nil {
		t.Fatal(err)
	}
	select {
	case ok := <-migrated:
		if ok {
			t.Fatal("migration reported success for a legacy file removed before it held the lock")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("migration did not finish")
	}
	if _, err := keyring.Get(keychainService, keychainAPIKey); !errors.Is(err, keyring.ErrNotFound) {
		t.Fatalf("migration wrote a removed legacy file into the keyring: %v", err)
	}
}
