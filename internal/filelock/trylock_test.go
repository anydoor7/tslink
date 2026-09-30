package filelock

import (
	"os"
	"path/filepath"
	"testing"
)

// Use two real file descriptors on a scratch path so the platform's actual
// advisory lock implementation, including its contention error, is exercised.
func TestTryLockContentionAndRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "proof.lock")
	holder, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	contender, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer contender.Close()
	if err := Lock(holder); err != nil {
		t.Fatal(err)
	}
	acquired, err := TryLock(contender)
	if err != nil || acquired {
		t.Fatalf("held lock: acquired=%v err=%v", acquired, err)
	}
	if err := Unlock(holder); err != nil {
		t.Fatal(err)
	}
	acquired, err = TryLock(contender)
	if err != nil || !acquired {
		t.Fatalf("released lock: acquired=%v err=%v", acquired, err)
	}
	if err := Unlock(contender); err != nil {
		t.Fatal(err)
	}
}
