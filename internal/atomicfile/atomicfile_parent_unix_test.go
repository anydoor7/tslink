//go:build !windows

package atomicfile

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestWriteFileInExistingDirRejectsWorldWritableParentWithoutMutation(t *testing.T) {
	restoreAtomicFileHooks(t)
	dir := filepath.Join(t.TempDir(), "shared")
	if err := os.Mkdir(dir, 0o777); err != nil {
		t.Fatalf("Mkdir(shared) error = %v", err)
	}
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatalf("Chmod(shared) error = %v", err)
	}
	target := filepath.Join(dir, "state.json")

	err := WriteFileInExistingDir(target, []byte("state\n"), PrivateFileMode)
	if err == nil || !strings.Contains(err.Error(), "world-writable") {
		t.Fatalf("WriteFileInExistingDir() error = %v, want world-writable rejection", err)
	}
	assertMode(t, dir, 0o777)
	if _, statErr := os.Lstat(target); !os.IsNotExist(statErr) {
		t.Fatalf("target exists after unsafe-parent rejection: %v", statErr)
	}
}

func TestWriteFileInExistingDirRejectsGroupWritableParentWithoutMutation(t *testing.T) {
	restoreAtomicFileHooks(t)
	dir := filepath.Join(t.TempDir(), "staff-shared")
	if err := os.Mkdir(dir, 0o775); err != nil {
		t.Fatalf("Mkdir(staff-shared) error = %v", err)
	}
	if err := os.Chmod(dir, 0o775); err != nil {
		t.Fatalf("Chmod(staff-shared) error = %v", err)
	}
	target := filepath.Join(dir, "state.json")

	err := WriteFileInExistingDir(target, []byte("state\n"), PrivateFileMode)
	if err == nil || !strings.Contains(err.Error(), "group- or world-writable") || !strings.Contains(err.Error(), "chmod g-w,o-w "+dir) {
		t.Fatalf("WriteFileInExistingDir() error = %v, want group-writable rejection with exact chmod remedy", err)
	}
	assertMode(t, dir, 0o775)
	if _, statErr := os.Lstat(target); !os.IsNotExist(statErr) {
		t.Fatalf("target exists after unsafe-parent rejection: %v", statErr)
	}
}

func TestWriteFileInExistingDirRejectsWorldWritableSymlinkReferentWithoutMutation(t *testing.T) {
	restoreAtomicFileHooks(t)
	root := t.TempDir()
	realDir := filepath.Join(root, "real")
	if err := os.Mkdir(realDir, 0o777); err != nil {
		t.Fatalf("Mkdir(real) error = %v", err)
	}
	if err := os.Chmod(realDir, 0o777); err != nil {
		t.Fatalf("Chmod(real) error = %v", err)
	}
	linkedDir := filepath.Join(root, "linked")
	if err := os.Symlink(realDir, linkedDir); err != nil {
		t.Fatalf("Symlink(parent) error = %v", err)
	}
	target := filepath.Join(linkedDir, "state.json")

	err := WriteFileInExistingDir(target, []byte("state\n"), PrivateFileMode)
	if err == nil || !strings.Contains(err.Error(), "world-writable") {
		t.Fatalf("WriteFileInExistingDir() error = %v, want referent rejection", err)
	}
	assertMode(t, realDir, 0o777)
	if _, statErr := os.Lstat(filepath.Join(realDir, "state.json")); !os.IsNotExist(statErr) {
		t.Fatalf("target exists after unsafe-referent rejection: %v", statErr)
	}
}

func TestWriteFileInExistingDirRejectsForeignOwnedParent(t *testing.T) {
	restoreAtomicFileHooks(t)
	dir := t.TempDir()
	info, err := os.Lstat(dir)
	if err != nil {
		t.Fatalf("Lstat(parent) error = %v", err)
	}
	target := filepath.Join(dir, "state.json")
	origLstat := lstatFn
	lstatFn = func(path string) (os.FileInfo, error) {
		if path == dir {
			return atomicfileForeignOwner{FileInfo: info}, nil
		}
		return origLstat(path)
	}

	err = WriteFileInExistingDir(target, []byte("state\n"), PrivateFileMode)
	if err == nil || !strings.Contains(err.Error(), "unsafe owner") {
		t.Fatalf("WriteFileInExistingDir() error = %v, want foreign-owner rejection", err)
	}
	if _, statErr := os.Lstat(target); !os.IsNotExist(statErr) {
		t.Fatalf("target exists after foreign-owner rejection: %v", statErr)
	}
}

type atomicfileForeignOwner struct {
	os.FileInfo
}

func (f atomicfileForeignOwner) Sys() any {
	stat, ok := f.FileInfo.Sys().(*syscall.Stat_t)
	if !ok {
		return f.FileInfo.Sys()
	}
	copy := *stat
	copy.Uid = 65534
	return &copy
}
