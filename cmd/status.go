package cmd

import (
	"fmt"
	"os"

	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/daemon"
	"github.com/monody0007/tslink/internal/registry"
	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show TSLink status",
	Long: `Show the current status of TSLink: whether the daemon is running,
Tailscale authentication state, and number of registered services.

Example:
  tslink status`,
	RunE: func(cmd *cobra.Command, args []string) error {
		pidPath, err := config.PIDPath()
		if err != nil {
			return err
		}
		authKeyPath, err := config.AuthKeyPath()
		if err != nil {
			return err
		}
		regPath, err := config.RegistryPath()
		if err != nil {
			return err
		}

		if daemon.IsRunning(pidPath) {
			pid, _ := daemon.ReadPID(pidPath)
			fmt.Printf("→ tslink: running (pid %d)\n", pid)
		} else {
			fmt.Println("→ tslink: not running")
		}

		if _, err := os.Stat(authKeyPath); os.IsNotExist(err) {
			fmt.Println("→ tailnet: not authenticated (run: tslink login)")
		} else {
			fmt.Println("→ tailnet: authenticated")
		}

		reg, err := registry.Load(regPath)
		if err != nil {
			fmt.Println("→ services: 0 registered")
		} else {
			fmt.Printf("→ services: %d registered\n", len(reg.Services))
		}

		return nil
	},
}

func init() {
	rootCmd.AddCommand(statusCmd)
}
