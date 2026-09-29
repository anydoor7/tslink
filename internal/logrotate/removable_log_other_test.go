//go:build !windows

package logrotate

import (
	"os"
	"testing"
)

// openRemovableSupervisedLog is openSupervisedLog: unix lets a path be removed
// or renamed while a descriptor is open on it, which is the state the degraded
// tests need.
func openRemovableSupervisedLog(t *testing.T, path string, content []byte) *os.File {
	t.Helper()
	return openSupervisedLog(t, path, content)
}
