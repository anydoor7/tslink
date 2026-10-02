package atomicfile

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestWriteFileWithReplaceReusesPreparedTemp(t *testing.T) {
	restoreAtomicFileHooks(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := os.WriteFile(path, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	creates, renames := 0, 0
	openFileFn = func(name string, flags int, mode os.FileMode) (*os.File, error) {
		if flags&os.O_CREATE != 0 {
			creates++
		}
		return os.OpenFile(name, flags, mode)
	}
	var source string
	renameFn = func(old, new string) error {
		renames++
		if source == "" {
			source = old
		}
		if old != source || new != path {
			t.Fatalf("replace changed source/target: %q -> %q", old, new)
		}
		// The temp must already be closed, synced and complete on every attempt.
		data, err := os.ReadFile(old)
		if err != nil || string(data) != "new" {
			t.Fatalf("unprepared temp: %q, %v", data, err)
		}
		if renames < 3 {
			return &os.LinkError{Op: "rename", Old: old, New: new, Err: syscall.Errno(32)}
		}
		return os.Rename(old, new)
	}
	retries := 0
	err := WriteFileWithReplace(path, []byte("new"), func(old, new string) error {
		retries++
		for i := 0; i < 3; i++ {
			if err := renameFn(old, new); !errors.Is(err, syscall.Errno(32)) {
				return err
			}
		}
		t.Fatal("replace did not recover")
		return nil
	})
	if err != nil || creates != 1 || renames != 3 || retries != 1 {
		t.Fatalf("err=%v creates=%d renames=%d policy_calls=%d", err, creates, renames, retries)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "new" {
		t.Fatalf("durable target: %q, %v", data, err)
	}
	assertNoOwnedTemps(t, dir, filepath.Base(path))
}

func TestWriteFileWithReplaceReportsPreparationFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "parent")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	called := false
	err := WriteFileWithReplace(filepath.Join(path, "state.json"), nil, func(old, new string) error { called = true; return renameFn(old, new) })
	if err == nil || called {
		t.Fatalf("preparation failure: error=%v retry called=%v", err, called)
	}
}
