//go:build !windows

package filelock

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func tempLockFile(t *testing.T) *os.File {
	t.Helper()
	p := filepath.Join(t.TempDir(), "test.lock")
	f, err := os.OpenFile(p, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

func TestLockUnlock(t *testing.T) {
	f := tempLockFile(t)

	if err := Lock(f); err != nil {
		t.Fatalf("Lock: %v", err)
	}
	if err := Unlock(f); err != nil {
		t.Fatalf("Unlock: %v", err)
	}
}

func TestConcurrentLock(t *testing.T) {
	p := filepath.Join(t.TempDir(), "test.lock")

	f1, err := os.OpenFile(p, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f1.Close()

	if err := Lock(f1); err != nil {
		t.Fatalf("Lock f1: %v", err)
	}

	// Try non-blocking lock from another fd — should fail with EWOULDBLOCK.
	f2, err := os.OpenFile(p, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f2.Close()

	err = syscall.Flock(int(f2.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err == nil {
		t.Fatal("expected non-blocking lock to fail while f1 holds the lock")
	}

	if err := Unlock(f1); err != nil {
		t.Fatalf("Unlock f1: %v", err)
	}
}

func TestUnlockAllowsRelock(t *testing.T) {
	f := tempLockFile(t)

	if err := Lock(f); err != nil {
		t.Fatalf("first Lock: %v", err)
	}
	if err := Unlock(f); err != nil {
		t.Fatalf("first Unlock: %v", err)
	}

	// Another goroutine should be able to acquire the lock now.
	done := make(chan error, 1)
	go func() {
		p := f.Name()
		f2, err := os.OpenFile(p, os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			done <- err
			return
		}
		defer f2.Close()
		if err := Lock(f2); err != nil {
			done <- err
			return
		}
		done <- Unlock(f2)
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("re-lock failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("re-lock timed out — lock was not properly released")
	}
}
