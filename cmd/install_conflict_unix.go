//go:build darwin || linux

package cmd

import (
	"fmt"
	"github.com/monody0007/tslink/internal/daemon"
	"os"

	"github.com/monody0007/tslink/internal/output"
)

func detectInstallDaemonConflict(recovery string) error {
	pidPath, err := pidPathFn()
	if err != nil {
		return fmt.Errorf("find daemon PID file before install: %w", err)
	}
	if !isRunningFn(pidPath) {
		if _, statErr := os.Stat(pidPath); !os.IsNotExist(statErr) && !daemon.IsProcessAbsentFromPIDFile(pidPath) {
			return output.ErrConflict("daemon PID artifact exists but process identity is unverified; inspect the running binary and supervisor before install/restart")
		}
		if err := checkSupervisorProcessScope(); err != nil {
			return output.ErrConflict("cannot safely replace the supervisor while daemon identity is unverified: " + err.Error())
		}
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
