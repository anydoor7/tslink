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

Example:
  tslink uninstall`,
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
