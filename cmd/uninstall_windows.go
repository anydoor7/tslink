//go:build windows

package cmd

import (
	"fmt"
	"os"

	"github.com/monody0007/tslink/internal/output"
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
	Short: "Remove from Windows Startup",
	Long: `Remove the TSLink Startup entry so it no longer launches in the
background when you sign in.

This command:
  1. Deletes the VBScript at %APPDATA%\Microsoft\Windows\Start Menu\Programs\Startup\tslink.vbs

If the Startup script is not installed, prints a message and exits cleanly.

Note: This only removes the autostart script. If TSLink is currently running,
use 'tslink stop' first to stop the daemon.

To verify the script was removed:
  dir "%APPDATA%\Microsoft\Windows\Start Menu\Programs\Startup\tslink.vbs"

	Examples:
	  tslink uninstall              Remove the Startup script
	  tslink stop && tslink uninstall   Stop daemon then remove autostart`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return withSupervisorTransaction(cmd.Context(), func() error {
			return runUninstallLocked(cmd, args)
		})
	},
}

// runUninstallLocked requires the per-user supervisor transaction lock.
func runUninstallLocked(cmd *cobra.Command, args []string) error {
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
