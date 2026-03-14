//go:build linux

package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/spf13/cobra"
)

const systemdServiceName = "tslink.service"

var installCmd = &cobra.Command{
	Use:   "install",
	Short: "Install as systemd user service",
	Long: `Register TSLink as a systemd user service so it starts automatically
for your user session and restarts if it crashes.

Example:
  tslink install`,
	RunE: func(cmd *cobra.Command, args []string) error {
		exe, err := os.Executable()
		if err != nil {
			return fmt.Errorf("find executable: %w", err)
		}
		exe, err = filepath.EvalSymlinks(exe)
		if err != nil {
			return fmt.Errorf("resolve executable path: %w", err)
		}

		servicePath := systemdServicePath()
		if err := os.MkdirAll(filepath.Dir(servicePath), 0o755); err != nil {
			return fmt.Errorf("create systemd user dir: %w", err)
		}

		service := fmt.Sprintf(`[Unit]
Description=TSLink - Tailscale Service Gateway
After=network-online.target

[Service]
Type=simple
ExecStart=%s serve
Restart=on-failure
RestartSec=5

[Install]
WantedBy=default.target
`, exe)
		if err := os.WriteFile(servicePath, []byte(service), 0o644); err != nil {
			return fmt.Errorf("write systemd service: %w", err)
		}

		if output, err := exec.Command("systemctl", "--user", "daemon-reload").CombinedOutput(); err != nil {
			return fmt.Errorf("reload systemd user daemon: %w: %s", err, output)
		}
		if output, err := exec.Command("systemctl", "--user", "enable", systemdServiceName).CombinedOutput(); err != nil {
			return fmt.Errorf("enable systemd user service: %w: %s", err, output)
		}
		if output, err := exec.Command("systemctl", "--user", "start", systemdServiceName).CombinedOutput(); err != nil {
			return fmt.Errorf("start systemd user service: %w: %s", err, output)
		}

		fmt.Printf("→ ✓ systemd user service installed and started: %s\n", servicePath)
		return nil
	},
}

func systemdServicePath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "systemd", "user", systemdServiceName)
}

func init() {
	rootCmd.AddCommand(installCmd)
}
