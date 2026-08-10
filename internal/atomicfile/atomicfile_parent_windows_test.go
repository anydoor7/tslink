//go:build windows

package atomicfile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteFileInExistingDirAllowsWindowsParentWithoutDACLValidation(t *testing.T) {
	restoreAtomicFileHooks(t)
	dir := t.TempDir()
	info, err := os.Lstat(dir)
	if err != nil {
		t.Fatalf("Lstat(parent) error = %v", err)
	}
	parentInfo := windowsDirectoryInfo{FileInfo: info}
	if got := parentInfo.Mode().Perm(); got != 0o777 {
		t.Fatalf("synthetic Windows parent mode = %04o, want 0777", got)
	}

	origLstat := lstatFn
	lstatFn = func(path string) (os.FileInfo, error) {
		if path == dir {
			return parentInfo, nil
		}
		return origLstat(path)
	}
	target := filepath.Join(dir, "state.json")

	if err := WriteFileInExistingDir(target, []byte("state\n"), PrivateFileMode); err != nil {
		t.Fatalf("WriteFileInExistingDir() error = %v, want Windows parent DACL no-op", err)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile(target) error = %v", err)
	}
	if string(data) != "state\n" {
		t.Fatalf("ReadFile(target) = %q, want %q", data, "state\n")
	}
}

type windowsDirectoryInfo struct {
	os.FileInfo
}

func (windowsDirectoryInfo) Mode() os.FileMode {
	return os.ModeDir | 0o777
}
