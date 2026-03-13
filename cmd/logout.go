package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/credentials"
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

		authKeyPath, err := config.AuthKeyPath()
		if err != nil {
			return err
		}
		nodesDir, err := config.NodesDir()
		if err != nil {
			return err
		}

		// Check if logged in (check keychain, apikey file, and legacy authkey)
		apiKey, _ := credentials.GetAPIKey()
		_, authErr := os.Stat(authKeyPath)
		_, nodesErr := os.Stat(nodesDir)
		if apiKey == "" && os.IsNotExist(authErr) && os.IsNotExist(nodesErr) {
			fmt.Println("→ Not logged in")
			return nil
		}

		// Remove credentials from keychain and files
		credentials.DeleteAPIKey()
		os.Remove(authKeyPath)
		os.RemoveAll(nodesDir)

		// Also clean legacy tsnet-state/ if present
		cfgDir, _ := config.Dir()
		os.RemoveAll(filepath.Join(cfgDir, "tsnet-state"))

		fmt.Println("→ ✓ Logged out")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(logoutCmd)
}
