//go:build !windows

package atomicfile

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestWriteFileInExistingDirAcceptsMkdirAll0755AcrossUmasks(t *testing.T) {
	for _, umask := range []int{0o022, 0o002, 0o000, 0o077, 0o007} {
		t.Run(fmt.Sprintf("umask-%04o", umask), func(t *testing.T) {
			restoreAtomicFileHooks(t)
			oldUmask := syscall.Umask(umask)
			defer syscall.Umask(oldUmask)

			dir := filepath.Join(t.TempDir(), "shared")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatalf("MkdirAll(shared) error = %v", err)
			}
			target := filepath.Join(dir, "state.json")
			if err := WriteFileInExistingDir(target, []byte("state\n"), PrivateFileMode); err != nil {
				t.Fatalf("WriteFileInExistingDir() error = %v for umask %04o", err, umask)
			}
		})
	}
}

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

func TestWriteFileInExistingDirReportsSpecialParentModeBits(t *testing.T) {
	for _, tc := range []struct {
		name string
		mode os.FileMode
		want string
	}{
		{name: "setgid", mode: 0o775 | os.ModeSetgid, want: "(2775)"},
		{name: "sticky", mode: 0o777 | os.ModeSticky, want: "(1777)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			restoreAtomicFileHooks(t)
			dir := filepath.Join(t.TempDir(), "unsafe")
			if err := os.Mkdir(dir, 0o755); err != nil {
				t.Fatalf("Mkdir(unsafe) error = %v", err)
			}
			if err := os.Chmod(dir, tc.mode); err != nil {
				t.Fatalf("Chmod(unsafe, %04o) error = %v", tc.mode, err)
			}

			err := WriteFileInExistingDir(filepath.Join(dir, "state.json"), []byte("state\n"), PrivateFileMode)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("WriteFileInExistingDir() error = %v, want full mode %s", err, tc.want)
			}
		})
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
