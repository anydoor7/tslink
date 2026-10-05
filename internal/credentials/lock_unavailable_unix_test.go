//go:build !windows

package credentials

import (
	"errors"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
)

func stubCredentialFileLock(t *testing.T, try func(*os.File) (bool, error)) {
	t.Helper()
	old := tryLockCredentialFileFunc
	tryLockCredentialFileFunc = try
	t.Cleanup(func() { tryLockCredentialFileFunc = old })
	credentialLockUnavailableWarned.Store(false)
	t.Cleanup(func() { credentialLockUnavailableWarned.Store(false) })
}

// NFS without lockd (ENOLCK) and some SMB/FUSE mounts (ENOTSUP, EOPNOTSUPP,
// EINVAL) cannot flock. Login and logout must still work there, serialized
// by the in-process gate, with one warning per process.
func TestUnsupportedFileLockFallsBackToInProcessGate(t *testing.T) {
	for _, errno := range []syscall.Errno{syscall.ENOLCK, syscall.ENOTSUP, syscall.EOPNOTSUPP, syscall.EINVAL} {
		t.Run(errno.Error(), func(t *testing.T) {
			setup(t)
			stubCredentialFileLock(t, func(*os.File) (bool, error) { return false, errno })
			logs := captureCredentialLogs(t)

			if err := WithMutationTransaction(func(tx *MutationTransaction) error {
				_, err := tx.SetAPIKeyWithBackend("tskey-api-<test-only-FAKE-nolock>")
				return err
			}); err != nil {
				t.Fatalf("login transaction with %v: %v", errno, err)
			}
			if err := SaveClientSecret("tskey-client-FAKE-nolock"); err != nil {
				t.Fatalf("credential write with %v: %v", errno, err)
			}
			if err := DeleteStoredCredentialsStrict(); err != nil {
				t.Fatalf("logout delete with %v: %v", errno, err)
			}
			if got := strings.Count(logs.String(), "credential transaction file lock unavailable"); got != 1 {
				t.Fatalf("warnings = %d, want exactly one per process:\n%s", got, logs.String())
			}
			if !strings.Contains(logs.String(), "level=WARN") {
				t.Fatalf("fallback was not logged as a warning:\n%s", logs.String())
			}

			// The in-process gate still serializes transactions.
			held := make(chan struct{})
			release := make(chan struct{})
			txDone := make(chan error, 1)
			go func() {
				txDone <- WithMutationTransaction(func(*MutationTransaction) error {
					close(held)
					<-release
					return nil
				})
			}()
			<-held
			second := make(chan error, 1)
			go func() { second <- WithMutationTransaction(func(*MutationTransaction) error { return nil }) }()
			select {
			case err := <-second:
				close(release)
				t.Fatalf("second transaction ran inside the first without a file lock: %v", err)
			case <-time.After(100 * time.Millisecond):
			}
			close(release)
			if err := <-txDone; err != nil {
				t.Fatal(err)
			}
			if err := <-second; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestOtherFileLockErrorsStayFatal(t *testing.T) {
	setup(t)
	stubCredentialFileLock(t, func(*os.File) (bool, error) { return false, syscall.EIO })
	unlock, err := acquireCredentialMutationLock()
	if err == nil {
		unlock()
		t.Fatal("credential lock acquired although flock failed with EIO")
	}
	if !errors.Is(err, syscall.EIO) || !strings.Contains(err.Error(), "lock credential transaction") {
		t.Fatalf("acquire error = %v, want the EIO lock failure", err)
	}
	// The gate is released after the failure.
	stubCredentialFileLock(t, tryLockCredentialFile)
	unlock, err = acquireCredentialMutationLock()
	if err != nil {
		t.Fatalf("lock unavailable after a failed acquisition: %v", err)
	}
	unlock()
}
