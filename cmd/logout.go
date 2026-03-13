package cmd

import (
	"fmt"
	"os"

	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/daemon"
	"github.com/spf13/cobra"
)

var logoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Logout from Tailscale",
	Long: `Clear Tailscale authentication state.

Example:
  tslink logout`,
	RunE: func(cmd *cobra.Command, args []string) error {
		pidPath, err := config.PIDPath()
		if err == nil && daemon.IsRunning(pidPath) {
			return fmt.Errorf("tslink is currently running — run 'tslink stop' first")
		}

		nodesDir, err := config.NodesDir()
		if err != nil {
			return err
		}

		if _, err := os.Stat(nodesDir); os.IsNotExist(err) {
			fmt.Println("→ Not logged in")
			return nil
		}

		if err := os.RemoveAll(nodesDir); err != nil {
			return fmt.Errorf("clear auth state: %w", err)
		}

		fmt.Println("→ ✓ Logged out")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(logoutCmd)
}
