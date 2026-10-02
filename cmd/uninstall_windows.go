//go:build windows

package cmd

import (
	"fmt"
	"github.com/anydoor7/tslink/internal/config"
	"os"
	"time"

	"github.com/anydoor7/tslink/internal/output"
	"github.com/spf13/cobra"
)

type UninstallResult struct {
	Path           string `json:"path"`
	Removed        bool   `json:"removed"`
	ServiceManager string `json:"service_manager"`
	Warning        string `json:"warning,omitempty"`
}

var uninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Remove the Windows scheduled task or Startup fallback",
	Long: `Disable the current user's TSLink scheduled task, gracefully stop its
verified daemon, then remove the task and Startup entry. An unverified process
is never terminated. If shutdown or Task Scheduler inspection fails, retain the
definition for a later retry. A Startup-only install removes autostart without
stopping a manual daemon; use 'tslink stop' to stop it.

Examples:
  tslink uninstall`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return withSupervisorTransaction(cmd.Context(), func() error {
			return runUninstallLocked(cmd, args)
		})
	},
}

// runUninstallLocked requires the per-user supervisor transaction lock.
func runUninstallLocked(cmd *cobra.Command, args []string) error {
	taskPath, err := windowsTaskPath()
	if err != nil {
		return err
	}
	name, err := windowsTaskName()
	if err != nil {
		return err
	}
	task, queryErr := windowsSchedulerFn("query", name, nil)
	if queryErr != nil {
		if _, statErr := os.Stat(taskPath); !os.IsNotExist(statErr) {
			return fmt.Errorf("cannot confirm task removal; definition retained: %w", queryErr)
		}
	} else if task.Exists {
		dir, err := absoluteConfigDir()
		if err != nil {
			return err
		}
		if _, err := windowsTaskSpecFromDefinition([]byte(task.XML), dir); err != nil {
			return fmt.Errorf("refusing to remove foreign scheduler task: %w", err)
		}
		if _, err := windowsSchedulerFn("disable", name, nil); err != nil {
			return err
		}
		pidPath, err := config.PIDPath()
		if err != nil {
			return err
		}
		if isRunningFn(pidPath) {
			pid, err := readPIDFn(pidPath)
			if err != nil {
				return err
			}
			spec, err := windowsTaskSpecFromDefinition([]byte(task.XML), dir)
			if err != nil || !windowsTaskOwnsPIDFn(pid, task.Engines, spec.Executable) {
				return output.ErrConflict("task disabled, but daemon ownership unverified; definition retained; inspect tslink doctor")
			}
			if err := stopDaemonFn(pidPath); err != nil {
				return fmt.Errorf("task disabled; shutdown failed; definition retained: %w", err)
			}
		}
		deadline := time.Now().Add(5 * time.Second)
		for {
			current, err := windowsSchedulerFn("query", name, nil)
			if err != nil {
				return err
			}
			if current.State != 4 && len(current.Engines) == 0 {
				break
			}
			if time.Now().After(deadline) {
				return fmt.Errorf("task disabled but still running; definition retained")
			}
			select {
			case <-cmd.Context().Done():
				return cmd.Context().Err()
			case <-time.After(100 * time.Millisecond):
			}
		}
		if _, err := windowsSchedulerFn("delete", name, nil); err != nil {
			return err
		}
		current, err := windowsSchedulerFn("query", name, nil)
		if err != nil || current.Exists {
			return fmt.Errorf("task removal could not be confirmed; definition retained: %v", err)
		}
		if err := os.Remove(taskPath); err != nil && !os.IsNotExist(err) {
			return err
		}
		startupPath, _ := windowsStartupScriptPath()
		if err := os.Remove(startupPath); err != nil && !os.IsNotExist(err) {
			return err
		}
		if jsonOutput(cmd) {
			output.Success("uninstall", UninstallResult{Path: taskPath, Removed: true, ServiceManager: "windows-task-scheduler"})
			return nil
		}
		fmt.Fprintln(cmd.OutOrStdout(), "→ ✓ Task Scheduler task and Startup entry removed")
		return nil
	}
	// Confirmed absent task, or explicit Startup-only fallback on an unavailable
	// scheduler. Remove stale local task evidence only after confirmed absence.
	if queryErr == nil {
		if err := os.Remove(taskPath); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	startupPath, err := windowsStartupScriptPath()
	if err != nil {
		return err
	}

	if _, err := os.Stat(startupPath); os.IsNotExist(err) {
		if jsonOutput(cmd) {
			output.Success("uninstall", UninstallResult{Path: startupPath, Removed: false, ServiceManager: "windows-startup"})
			return nil
		}
		fmt.Fprintln(cmd.OutOrStdout(), "→ Startup script not installed")
		return nil
	} else if err != nil {
		return fmt.Errorf("stat Startup script: %w", err)
	}

	if err := os.Remove(startupPath); err != nil {
		return fmt.Errorf("remove Startup script: %w", err)
	}

	if jsonOutput(cmd) {
		output.Success("uninstall", UninstallResult{Path: startupPath, Removed: true, ServiceManager: "windows-startup"})
		return nil
	}

	fmt.Fprintln(cmd.OutOrStdout(), "→ ✓ Startup script removed")
	return nil
}

func init() {
	rootCmd.AddCommand(uninstallCmd)
}
