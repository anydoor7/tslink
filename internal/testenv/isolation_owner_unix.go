//go:build !windows

package testenv

import (
	"os"
	"syscall"
)

// ownedByThisUser reports whether info, an Lstat result, belongs to the user
// this process runs as.
func ownedByThisUser(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(stat.Uid) == os.Getuid()
}
