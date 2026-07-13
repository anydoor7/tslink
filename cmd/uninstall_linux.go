//go:build linux

package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

var uninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Remove as systemd user service",
	Long: `Remove TSLink as a systemd user service so it no longer starts
automatically for your user session.

This command:
  1. Stops the service via 'systemctl --user stop tslink'
  2. Disables it via 'systemctl --user disable tslink'
  3. Deletes the unit file at ~/.config/systemd/user/tslink.service
  4. Runs 'systemctl --user daemon-reload' to clean up systemd state

If the service is not installed, prints a message and exits cleanly.

To check if the service is still active after removal:
  systemctl --user status tslink

If you enabled lingering only for TSLink, disable it after uninstall:
  loginctl disable-linger "$USER"

	Examples:
	  tslink uninstall              Remove the systemd user service`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		servicePath, err := systemdServicePath()
		if err != nil {
			return err
		}

		if _, err := os.Stat(servicePath); os.IsNotExist(err) {
			fmt.Fprintln(cmd.OutOrStdout(), "→ systemd user service not installed")
			return nil
		} else if err != nil {
			return fmt.Errorf("stat systemd service: %w", err)
		}

		var warnings []string
		if output, err := systemctlCombinedOutput("--user", "stop", systemdServiceName); err != nil {
			warnings = append(warnings, fmt.Sprintf("stop systemd user service: %v%s", err, commandOutputSuffix(output)))
		}
		if output, err := systemctlCombinedOutput("--user", "disable", systemdServiceName); err != nil {
			warnings = append(warnings, fmt.Sprintf("disable systemd user service: %v%s", err, commandOutputSuffix(output)))
		}

		if err := os.Remove(servicePath); err != nil {
			return fmt.Errorf("remove systemd service: %w", err)
		}
		if output, err := systemctlCombinedOutput("--user", "daemon-reload"); err != nil {
			return fmt.Errorf("reload systemd user daemon: %w: %s", err, output)
		}

		if len(warnings) > 0 {
			fmt.Fprintf(cmd.ErrOrStderr(), "→ ⚠ %s\n", strings.Join(warnings, "; "))
		}
		fmt.Fprintln(cmd.OutOrStdout(), "→ ✓ systemd user service removed")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(uninstallCmd)
}
