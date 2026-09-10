//go:build windows

package cmd

import (
	"errors"
	"github.com/spf13/cobra"
	"golang.org/x/sys/windows"
	"os"
)

func trySupervisorLock(f *os.File) (bool, error) {
	var ol windows.Overlapped
	err := windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &ol)
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return false, nil
	}
	return err == nil, err
}

// Windows installer has no nested transaction wrapper.
func runInstallLocked(cmd *cobra.Command, args []string) error { return installCmd.RunE(cmd, args) }
