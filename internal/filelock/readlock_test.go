package filelock

import (
	"path/filepath"
	"testing"
)

func TestReadLockSharesAndExcludesWriter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.lock")
	first := openLockFile(t, path)
	second := openLockFile(t, path)
	writer := openLockFile(t, path)
	if ok, err := TryReadLock(first); !ok || err != nil {
		t.Fatal("first reader", ok, err)
	}
	if ok, err := TryReadLock(second); !ok || err != nil {
		t.Fatal("second reader", ok, err)
	}
	if ok, err := TryLock(writer); ok || err != nil {
		t.Fatal("writer during shared read", ok, err)
	}
	if err := Unlock(first); err != nil {
		t.Fatal(err)
	}
	if err := Unlock(second); err != nil {
		t.Fatal(err)
	}
	if ok, err := TryLock(writer); !ok || err != nil {
		t.Fatal("writer after reads", ok, err)
	}
	if ok, err := TryReadLock(first); ok || err != nil {
		t.Fatal("read during writer", ok, err)
	}
	if err := Unlock(writer); err != nil {
		t.Fatal(err)
	}
	if ok, err := TryReadLock(first); !ok || err != nil {
		t.Fatal("reader recovery", ok, err)
	}
	if err := Unlock(first); err != nil {
		t.Fatal(err)
	}
}
