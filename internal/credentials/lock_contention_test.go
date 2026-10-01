package credentials

import (
	"bytes"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/registry"
)

func captureCredentialLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })
	return &buf
}

// holdCredentialFileLock locks path on its own descriptor, the way another
// tslink process holds it.
func holdCredentialFileLock(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	locked, err := tryLockCredentialFile(f)
	if err != nil || !locked {
		t.Fatalf("hold credential lock %s: locked=%v err=%v", path, locked, err)
	}
}

// holdCredentialTransaction keeps an in-process credential transaction open
// until the test ends.
func holdCredentialTransaction(t *testing.T) {
	t.Helper()
	held := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- WithMutationTransaction(func(*MutationTransaction) error {
			close(held)
			<-release
			return nil
		})
	}()
	select {
	case <-held:
	case <-time.After(3 * time.Second):
		t.Fatal("holder transaction never acquired the credential lock")
	}
	t.Cleanup(func() {
		close(release)
		<-done
	})
}

func assertCredentialLockContention(t *testing.T, err error, lockPath string) {
	t.Helper()
	if err == nil {
		t.Fatal("credential lock acquired while another holder had it")
	}
	if !errors.Is(err, ErrMutationLockBusy) {
		t.Fatalf("contention error %q does not wrap ErrMutationLockBusy", err)
	}
	var coded *registry.StableCodeError
	if !errors.As(err, &coded) || coded.StableCode() != "conflict" || len(coded.NextCommands()) == 0 {
		t.Fatalf("contention error %q is not a stable conflict code with next steps: %+v", err, coded)
	}
	for _, want := range []string{lockPath, "another tslink login, logout, or serve is running", "retry"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("contention error %q does not mention %q", err, want)
		}
	}
}

func TestCredentialLockContentionIsRetryableConflict(t *testing.T) {
	t.Run("other process holds the file", func(t *testing.T) {
		setup(t)
		t.Cleanup(SetMutationLockTimeoutForTesting(200 * time.Millisecond))
		lockPath, err := credentialMutationLockPathFunc()
		if err != nil {
			t.Fatal(err)
		}
		holdCredentialFileLock(t, lockPath)
		unlock, err := acquireCredentialMutationLock()
		if err == nil {
			unlock()
		}
		assertCredentialLockContention(t, err, lockPath)
	})
	t.Run("transaction in this process", func(t *testing.T) {
		setup(t)
		t.Cleanup(SetMutationLockTimeoutForTesting(200 * time.Millisecond))
		lockPath, err := credentialMutationLockPathFunc()
		if err != nil {
			t.Fatal(err)
		}
		holdCredentialTransaction(t)
		unlock, err := acquireCredentialMutationLock()
		if err == nil {
			unlock()
		}
		assertCredentialLockContention(t, err, lockPath)
	})
}

func TestMigrateFromLegacyWarnsWhenSkippedForLockContention(t *testing.T) {
	setup(t)
	t.Cleanup(SetMutationLockTimeoutForTesting(200 * time.Millisecond))
	if err := os.WriteFile(apiKeyPath(t), []byte("tskey-api-FAKE-legacy"), 0o600); err != nil {
		t.Fatal(err)
	}
	lockPath, err := credentialMutationLockPathFunc()
	if err != nil {
		t.Fatal(err)
	}
	holdCredentialFileLock(t, lockPath)
	logs := captureCredentialLogs(t)
	if MigrateFromLegacy() {
		t.Fatal("migration ran while another process held the credential lock")
	}
	out := logs.String()
	// The text handler quotes the error value and escapes it as strconv.Quote
	// does, so on Windows every separator of the lock path appears doubled.
	loggedLockPath := strings.Trim(strconv.Quote(lockPath), `"`)
	if !strings.Contains(out, "level=WARN") || !strings.Contains(out, "legacy credential migration skipped") || !strings.Contains(out, loggedLockPath) {
		t.Fatalf("migration skipped for lock contention without a warning naming the lock:\n%s", out)
	}
	if _, err := os.Stat(apiKeyPath(t)); err != nil {
		t.Fatalf("legacy file changed by a skipped migration: %v", err)
	}
}
