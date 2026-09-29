package credentials

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"time"

	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/filelock"
)

var credentialMutationLockPathFunc = defaultCredentialMutationLockPath

// credentialLockPathCheck vets every lock path before it is created or
// opened. Production leaves it nil; IsolateForTesting installs a check that
// refuses the real account home.
var credentialLockPathCheck func(path string) error

// The keyring is shared by all TSLink config directories for this OS user.
// Keep its transaction lock outside TSLINK_CONFIG_DIR so a CLI and daemon
// using different config directories still serialize their credential writes.
func defaultCredentialMutationLockPath() (string, error) {
	if !keyringEnabledFunc() {
		// With keyring deliberately disabled, credential files are scoped to
		// this config directory. This also keeps isolated CLI/E2E fixtures
		// from creating a lock in the operator's real account home.
		dir, err := config.Dir()
		if err != nil {
			return "", err
		}
		return filepath.Join(dir, "credentials.lock"), nil
	}
	account, err := user.Current()
	if err != nil {
		return "", err
	}
	if account.HomeDir == "" {
		return "", fmt.Errorf("OS account has no home directory")
	}
	return filepath.Join(account.HomeDir, ".tslink", "credentials.lock"), nil
}

// Used only by fake-keyring tests; production always resolves the OS account.
func SetMutationLockPathForTesting(path string) (restore func()) {
	old := credentialMutationLockPathFunc
	credentialMutationLockPathFunc = func() (string, error) { return path, nil }
	return func() { credentialMutationLockPathFunc = old }
}

const credentialMutationLockTimeout = 5 * time.Second

var credentialMutationGate = func() chan struct{} {
	gate := make(chan struct{}, 1)
	gate <- struct{}{}
	return gate
}()

func acquireCredentialMutationLock() (func(), error) {
	deadline := time.Now().Add(credentialMutationLockTimeout)
	select {
	case <-credentialMutationGate:
	case <-time.After(time.Until(deadline)):
		return nil, fmt.Errorf("credential transaction lock timed out")
	}
	releaseGate := func() { credentialMutationGate <- struct{}{} }
	primaryPath, err := credentialMutationLockPathFunc()
	if err != nil {
		releaseGate()
		return nil, fmt.Errorf("resolve credential transaction lock: %w", err)
	}
	paths := []string{primaryPath}
	if keyringEnabledFunc() {
		// Enabled writers may fall back to the same file as a keyring-disabled
		// process. Always take that config lock after the account lock.
		dir, err := config.Dir()
		if err != nil {
			releaseGate()
			return nil, fmt.Errorf("resolve credential file transaction lock: %w", err)
		}
		filePath := filepath.Join(dir, "credentials.lock")
		if filePath != primaryPath {
			paths = append(paths, filePath)
		}
	}
	var lockedFiles []*os.File
	releaseAll := func() {
		for i := len(lockedFiles) - 1; i >= 0; i-- {
			_ = filelock.Unlock(lockedFiles[i])
			_ = lockedFiles[i].Close()
		}
		releaseGate()
	}
	for _, path := range paths {
		f, err := lockCredentialPath(path, deadline)
		if err != nil {
			releaseAll()
			return nil, err
		}
		lockedFiles = append(lockedFiles, f)
	}
	return releaseAll, nil
}

func lockCredentialPath(path string, deadline time.Time) (*os.File, error) {
	if credentialLockPathCheck != nil {
		if err := credentialLockPathCheck(path); err != nil {
			return nil, err
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create credential transaction lock directory: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open credential transaction lock: %w", err)
	}
	for {
		locked, lockErr := tryLockCredentialFile(f)
		if lockErr != nil {
			_ = f.Close()
			return nil, fmt.Errorf("lock credential transaction: %w", lockErr)
		}
		if locked {
			break
		}
		if !time.Now().Before(deadline) {
			_ = f.Close()
			return nil, fmt.Errorf("credential transaction lock timed out")
		}
		time.Sleep(min(25*time.Millisecond, time.Until(deadline)))
	}
	return f, nil
}

// MutationTransaction keeps the shared keyring lock across a multi-step
// credential commit, including its readback and rollback. Its methods skip
// re-locking; callers must use it only inside WithMutationTransaction.
type MutationTransaction struct{}

func WithMutationTransaction(fn func(*MutationTransaction) error) error {
	unlock, err := acquireCredentialMutationLock()
	if err != nil {
		return err
	}
	defer unlock()
	return fn(&MutationTransaction{})
}

func (*MutationTransaction) SetAPIKeyWithBackend(key string) (CredentialBackend, error) {
	return storeCredentialWithBackendLocked("API key", keychainAPIKey, key, apiKeyPathFunc)
}

func (*MutationTransaction) SaveClientSecretWithBackend(secret string) (CredentialBackend, error) {
	if !strings.HasPrefix(secret, "tskey-client-") {
		return "", fmt.Errorf("invalid client secret: must start with 'tskey-client-'")
	}
	return storeCredentialWithBackendLocked("OAuth client secret", keychainClientSecret, secret, clientSecretPathFunc)
}

func (*MutationTransaction) DeleteAPIKeyChecked() error {
	return deleteAPIKeyCheckedLocked()
}

func (*MutationTransaction) DeleteClientSecretChecked() error {
	return deleteClientSecretCheckedLocked()
}
