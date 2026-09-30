//go:build !windows

package testenv

import (
	"os"
	"syscall"
	"testing"
)

// TestOwnedByThisUserTellsThisUsersDirectoryFromAnotherUsers checks the real
// owner check the sweep uses: a directory this test made is this user's, and
// / belongs to another user (root).
func TestOwnedByThisUserTellsThisUsersDirectoryFromAnotherUsers(t *testing.T) {
	mine, err := os.Lstat(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !ownedByThisUser(mine) {
		t.Fatalf("ownedByThisUser(a directory this test made) = false, want true for uid %d", os.Getuid())
	}
	rootDir, err := os.Lstat("/")
	if err != nil {
		t.Fatal(err)
	}
	if stat, ok := rootDir.Sys().(*syscall.Stat_t); ok && int(stat.Uid) == os.Getuid() {
		t.Skipf("/ is owned by this process's uid %d (running as root, or in a user namespace that maps it), so this host has no directory of another user to compare with", os.Getuid())
	}
	if ownedByThisUser(rootDir) {
		t.Fatalf("ownedByThisUser(/) = true for uid %d; / belongs to another user", os.Getuid())
	}
}
