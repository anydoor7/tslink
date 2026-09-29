package credentials

import (
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/filelock"
	"github.com/monody0007/tslink/internal/registry"
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

const defaultCredentialMutationLockTimeout = 5 * time.Second

// credentialMutationLockTimeoutOverride is zero outside contention tests.
var credentialMutationLockTimeoutOverride atomic.Int64

func credentialMutationLockTimeout() time.Duration {
	if timeout := credentialMutationLockTimeoutOverride.Load(); timeout > 0 {
		return time.Duration(timeout)
	}
	return defaultCredentialMutationLockTimeout
}

// Used only by lock-contention tests; production always waits 5 s.
func SetMutationLockTimeoutForTesting(timeout time.Duration) (restore func()) {
	old := credentialMutationLockTimeoutOverride.Swap(int64(timeout))
	return func() { credentialMutationLockTimeoutOverride.Store(old) }
}

// ErrMutationLockBusy reports that another credential transaction held the
// lock for the whole wait. It is wrapped in a registry.StableCodeError with
// the CLI's retryable "conflict" code.
var ErrMutationLockBusy = errors.New("credential transaction lock busy")

// credentialLockConflictCode is the stable code the CLI maps to its
// retryable conflict exit (output.StableErrorCode(output.ExitConflict)).
const credentialLockConflictCode = "conflict"

func credentialLockBusyError(path string, waited time.Duration) error {
	target := "the credential transaction lock"
	if path != "" {
		target = path
	}
	return &registry.StableCodeError{
		Code: credentialLockConflictCode,
		Next: []string{"Wait for the other tslink login, logout, or serve to finish, then retry the command"},
		Err:  fmt.Errorf("%w: waited %s for %s; another tslink login, logout, or serve is running; retry when it finishes", ErrMutationLockBusy, waited, target),
	}
}

var credentialMutationGate = func() chan struct{} {
	gate := make(chan struct{}, 1)
	gate <- struct{}{}
	return gate
}()

func acquireCredentialMutationLock() (func(), error) {
	timeout := credentialMutationLockTimeout()
	deadline := time.Now().Add(timeout)
	select {
	case <-credentialMutationGate:
	case <-time.After(time.Until(deadline)):
		path, _ := credentialMutationLockPathFunc()
		return nil, credentialLockBusyError(path, timeout)
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
	var lockedInfos []os.FileInfo
	releaseAll := func() {
		for i := len(lockedFiles) - 1; i >= 0; i-- {
			_ = filelock.Unlock(lockedFiles[i])
			_ = lockedFiles[i].Close()
		}
		releaseGate()
	}
	for _, path := range paths {
		f, info, err := openCredentialLockPath(path)
		if err != nil {
			releaseAll()
			return nil, err
		}
		if sameCredentialLockFile(lockedInfos, info) {
			// Another spelling of a lock this process already holds, e.g. a
			// symlink or case variant of ~/.tslink as TSLINK_CONFIG_DIR. A
			// second descriptor would wait on this process's own lock.
			_ = f.Close()
			continue
		}
		if err := lockCredentialFile(f, path, timeout, deadline); err != nil {
			releaseAll()
			return nil, err
		}
		lockedFiles = append(lockedFiles, f)
		lockedInfos = append(lockedInfos, info)
	}
	return releaseAll, nil
}

func sameCredentialLockFile(locked []os.FileInfo, info os.FileInfo) bool {
	for _, held := range locked {
		if os.SameFile(held, info) {
			return true
		}
	}
	return false
}

func openCredentialLockPath(path string) (*os.File, os.FileInfo, error) {
	if credentialLockPathCheck != nil {
		if err := credentialLockPathCheck(path); err != nil {
			return nil, nil, err
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, nil, fmt.Errorf("create credential transaction lock directory: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, nil, fmt.Errorf("open credential transaction lock: %w", err)
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, nil, fmt.Errorf("inspect credential transaction lock: %w", err)
	}
	return f, info, nil
}

// lockCredentialFile waits for an exclusive lock on f until deadline. It
// closes f on failure.
func lockCredentialFile(f *os.File, path string, timeout time.Duration, deadline time.Time) error {
	for {
		locked, lockErr := tryLockCredentialFile(f)
		if lockErr != nil {
			_ = f.Close()
			return fmt.Errorf("lock credential transaction: %w", lockErr)
		}
		if locked {
			return nil
		}
		if !time.Now().Before(deadline) {
			_ = f.Close()
			return credentialLockBusyError(path, timeout)
		}
		time.Sleep(min(25*time.Millisecond, time.Until(deadline)))
	}
}

// MutationTransaction keeps the shared keyring lock across a multi-step
// credential commit, including its readback and rollback. Its methods skip
// re-locking; callers must use it only inside WithMutationTransaction.
type MutationTransaction struct{}

// withCredentialMutationLock runs fn under the credential mutation lock. The
// lock is not reentrant: fn must use only ...Locked helpers.
func withCredentialMutationLock(fn func() error) error {
	unlock, err := acquireCredentialMutationLock()
	if err != nil {
		return err
	}
	defer unlock()
	return fn()
}

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

// CredentialFileSnapshot holds the credential files that existed when a
// transaction began. It carries credential material: never log or persist it.
type CredentialFileSnapshot struct {
	files map[string][]byte
}

// SnapshotCredentialFiles records the API key and client secret files. A
// keyring write removes the file copy, and a login snapshot records only the
// effective keyring-first value, so a file that disagreed with the keyring
// would otherwise be lost when the login rolls back.
func (*MutationTransaction) SnapshotCredentialFiles() CredentialFileSnapshot {
	snapshot := CredentialFileSnapshot{files: map[string][]byte{}}
	for _, pathFunc := range []func() (string, error){apiKeyPathFunc, clientSecretPathFunc} {
		path, err := pathFunc()
		if err != nil {
			continue
		}
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		if data, err := os.ReadFile(path); err == nil {
			snapshot.files[path] = data
		}
	}
	return snapshot
}

// RestoreRemovedCredentialFiles writes back each snapshotted file that is now
// missing. It never overwrites or deletes a file: after a rollback, an
// existing file is the one the value restore wrote. Windows never writes
// credential files, so it restores nothing there.
func (*MutationTransaction) RestoreRemovedCredentialFiles(snapshot CredentialFileSnapshot) error {
	if !fileCredentialFallbackEnabledFunc() {
		return nil
	}
	var errs []error
	for path, data := range snapshot.files {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			continue
		}
		if err := credentialFileWriteFunc(path, data); err != nil {
			errs = append(errs, fmt.Errorf("restore credential file %s: %w", path, err))
		}
	}
	return errors.Join(errs...)
}
