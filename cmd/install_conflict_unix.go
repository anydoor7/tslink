//go:build darwin || linux

package cmd

import (
	"context"
	"fmt"
	"github.com/anydoor7/tslink/internal/daemon"
	"os"
	"strings"

	"github.com/anydoor7/tslink/internal/output"
)

func detectInstallDaemonConflict(ctx context.Context, recovery string) error {
	pidPath, err := pidPathFn()
	if err != nil {
		return fmt.Errorf("find daemon PID file before install: %w", err)
	}
	if !isRunningFn(pidPath) {
		// The same cut doctor makes, for the same reason and in the same
		// direction: doctor now sends a provably foreign PID here with "run
		// tslink install", so refusing that install would leave the operator
		// with two commands that each point at the other. A PID owned by an
		// unrelated program is a stale file; installing writes a unit and
		// starts our own daemon without touching that process.
		if _, statErr := os.Stat(pidPath); !os.IsNotExist(statErr) && !daemon.IsProcessAbsentFromPIDFile(pidPath) && !daemon.IsForeignProcessFromPIDFile(pidPath) {
			return output.ErrConflict("daemon PID artifact exists but process identity is unverified; inspect the running binary and supervisor before install/restart")
		}
		if err := checkSupervisorProcessScope(ctx); err != nil {
			if strings.Contains(err.Error(), systemdUserManagerUnavailableMessage) {
				return output.ErrConflict(err.Error())
			}
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
