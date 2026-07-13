//go:build windows

package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

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
		startupPath, err := windowsStartupScriptPath()
		if err != nil {
			return err
		}

		if _, err := os.Stat(startupPath); os.IsNotExist(err) {
			fmt.Fprintln(cmd.OutOrStdout(), "→ Startup script not installed")
			return nil
		} else if err != nil {
			return fmt.Errorf("stat Startup script: %w", err)
		}

		if err := os.Remove(startupPath); err != nil {
			return fmt.Errorf("remove Startup script: %w", err)
		}

		fmt.Fprintln(cmd.OutOrStdout(), "→ ✓ Startup script removed")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(uninstallCmd)
}
