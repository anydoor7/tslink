//go:build darwin

package cmd

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"
)

var uninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Remove as macOS LaunchAgent",
	Long: `Remove the TSLink macOS LaunchAgent so it no longer auto-starts.

This command:
  1. Unloads the agent via 'launchctl unload'
  2. Deletes ~/Library/LaunchAgents/com.tslink.daemon.plist

If the LaunchAgent is not installed, prints a message and exits cleanly.
Log files in ~/.config/tslink/logs/ are NOT removed.

Examples:
  tslink uninstall              Remove the LaunchAgent`,
	RunE: func(cmd *cobra.Command, args []string) error {
		path, err := plistPath()
		if err != nil {
			return err
		}

		if _, err := os.Stat(path); os.IsNotExist(err) {
			fmt.Println("→ LaunchAgent not installed")
			return nil
		}

		exec.Command("launchctl", "unload", path).Run()

		if err := os.Remove(path); err != nil {
			return fmt.Errorf("remove plist: %w", err)
		}

		fmt.Println("→ ✓ LaunchAgent removed")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(uninstallCmd)
}
