//go:build windows

package server

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// replaceableRootPath returns the path a handler under test is opened on so
// that replaceRootPath can later point it elsewhere. Windows refuses to rename
// or delete a directory while a handle to it is open, and refuses to rename
// its parent too, so the unix staging (move the held directory away) cannot
// run here. Replacing a junction that leads to the held directory is allowed,
// because the handle is on the target rather than on the junction, so the
// handler is opened through a junction instead.
func replaceableRootPath(t *testing.T, dir string) string {
	t.Helper()
	link := filepath.Join(filepath.Dir(dir), filepath.Base(dir)+".link")
	makeServedRootJunction(t, link, dir)
	return link
}

// replaceRootPath swaps the junction at rootPath for one that leads to
// replacement. os.Remove on a junction removes the junction, not its target.
func replaceRootPath(t *testing.T, rootPath, replacement string) {
	t.Helper()
	if err := os.Remove(rootPath); err != nil {
		t.Fatalf("Remove(root junction) error = %v", err)
	}
	makeServedRootJunction(t, rootPath, replacement)
}

// makeServedRootJunction creates a directory junction with mklink /J, which
// needs no privilege, unlike a symlink for a non-elevated user.
func makeServedRootJunction(t *testing.T, link, target string) {
	t.Helper()
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
		t.Fatalf("mklink /J %s %s: %v: %s", link, target, err, out)
	}
}
