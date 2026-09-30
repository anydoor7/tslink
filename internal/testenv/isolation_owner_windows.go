//go:build windows

package testenv

import "os"

// ownedByThisUser reports true. Every Windows user has a TMPDIR of their own
// (%LOCALAPPDATA%\Temp, or the system temp directory for SYSTEM), so the
// sweep never meets another user's roots there.
func ownedByThisUser(os.FileInfo) bool {
	return true
}
