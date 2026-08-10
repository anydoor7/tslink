//go:build darwin || linux

package cmd

import (
	"fmt"

	"github.com/monody0007/tslink/internal/output"
)

func detectInstallDaemonConflict(recovery string) error {
	pidPath, err := pidPathFn()
	if err != nil {
		return fmt.Errorf("find daemon PID file before install: %w", err)
	}
	if !isRunningFn(pidPath) {
		return nil
	}

	pid, err := readPIDFn(pidPath)
	if err != nil {
		return output.ErrConflict(fmt.Sprintf("a verified TSLink daemon is already running, but its PID could not be read; %s", recovery))
	}
	return output.ErrConflict(fmt.Sprintf(
		"TSLink daemon is already running (pid %d); %s (exit 4 means an existing-state conflict)",
		pid,
		recovery,
	))
}
