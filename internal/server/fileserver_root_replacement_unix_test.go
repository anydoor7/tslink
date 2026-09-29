//go:build !windows

package server

import (
	"os"
	"testing"
)

// replaceableRootPath returns the path a handler under test is opened on so
// that replaceRootPath can later point it elsewhere. On unix that is the
// directory itself.
func replaceableRootPath(t *testing.T, dir string) string {
	t.Helper()
	return dir
}

// replaceRootPath moves the directory a handler holds out of the way and puts
// a symlink to replacement at its path.
func replaceRootPath(t *testing.T, rootPath, replacement string) {
	t.Helper()
	if err := os.Rename(rootPath, rootPath+".pinned"); err != nil {
		t.Fatalf("Rename(root) error = %v", err)
	}
	if err := os.Symlink(replacement, rootPath); err != nil {
		t.Fatalf("Symlink(replacement) error = %v", err)
	}
}
