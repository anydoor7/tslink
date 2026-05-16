//go:build darwin

package cmd

import (
	"fmt"
	"os"

	"github.com/monody0007/tslink/internal/output"
	"github.com/spf13/cobra"
)

// UninstallResult is the JSON payload for the uninstall command.
type UninstallResult struct {
	PlistPath       string `json:"plist_path"`
	Removed         bool   `json:"removed"`
	LaunchctlTarget string `json:"launchctl_target"`
	LaunchctlOutput string `json:"launchctl_output,omitempty"`
	Warning         string `json:"warning,omitempty"`
}

var uninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Remove as macOS LaunchAgent",
	Long: `Remove the TSLink macOS LaunchAgent so it no longer auto-starts.

This command:
  1. Unloads the agent via 'launchctl bootout gui/$(id -u)/com.tslink.daemon'
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
			if jsonOutput(cmd) {
				output.Success("uninstall", UninstallResult{PlistPath: path, Removed: false, LaunchctlTarget: launchctlServiceTarget()})
				return nil
			}
			fmt.Fprintln(cmd.OutOrStdout(), "→ LaunchAgent not installed")
			return nil
		} else if err != nil {
			return fmt.Errorf("stat plist: %w", err)
		}

		target := launchctlServiceTarget()
		bootoutOutput, bootoutErr := launchctlCombinedOutput("bootout", target)
		warning := ""
		if bootoutErr != nil {
			warning = launchctlWarning("LaunchAgent plist removed but launchctl bootout failed", bootoutErr, bootoutOutput)
		}

		if err := os.Remove(path); err != nil {
			return fmt.Errorf("remove plist: %w", err)
		}

		if jsonOutput(cmd) {
			output.Success("uninstall", UninstallResult{
				PlistPath:       path,
				Removed:         true,
				LaunchctlTarget: target,
				LaunchctlOutput: string(bootoutOutput),
				Warning:         warning,
			})
			return nil
		}

		if warning != "" {
			fmt.Fprintf(cmd.OutOrStdout(), "→ ⚠ %s\n", warning)
		}
		fmt.Fprintln(cmd.OutOrStdout(), "→ ✓ LaunchAgent removed")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(uninstallCmd)
}
