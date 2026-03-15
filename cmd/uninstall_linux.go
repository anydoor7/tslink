//go:build linux

package cmd

import (
	"fmt"
	"os"
	"os/exec"

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

Examples:
  tslink uninstall              Remove the systemd user service`,
	RunE: func(cmd *cobra.Command, args []string) error {
		servicePath := systemdServicePath()

		if _, err := os.Stat(servicePath); os.IsNotExist(err) {
			fmt.Println("→ systemd user service not installed")
			return nil
		}

		exec.Command("systemctl", "--user", "stop", systemdServiceName).Run()
		exec.Command("systemctl", "--user", "disable", systemdServiceName).Run()

		if err := os.Remove(servicePath); err != nil {
			return fmt.Errorf("remove systemd service: %w", err)
		}
		if output, err := exec.Command("systemctl", "--user", "daemon-reload").CombinedOutput(); err != nil {
			return fmt.Errorf("reload systemd user daemon: %w: %s", err, output)
		}

		fmt.Println("→ ✓ systemd user service removed")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(uninstallCmd)
}
