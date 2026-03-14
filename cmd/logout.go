package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/credentials"
	"github.com/spf13/cobra"
)

var getAPIKeyFn = credentials.GetAPIKey
var deleteAPIKeyFn = credentials.DeleteAPIKey
var hasClientSecretFn = credentials.HasClientSecret
var deleteClientSecretFn = credentials.DeleteClientSecret

func logoutUser(pidPath, authKeyPath, nodesDir, cfgDir string, out io.Writer) error {
	if isRunningFn(pidPath) {
		return fmt.Errorf("tslink is currently running — run 'tslink stop' first")
	}

	apiKey, _ := getAPIKeyFn()
	hasCS := hasClientSecretFn()
	_, authErr := os.Stat(authKeyPath)
	_, nodesErr := os.Stat(nodesDir)
	if apiKey == "" && !hasCS && os.IsNotExist(authErr) && os.IsNotExist(nodesErr) {
		fmt.Fprintln(out, "→ Not logged in")
		return nil
	}

	deleteAPIKeyFn()
	deleteClientSecretFn()
	os.Remove(authKeyPath)
	os.RemoveAll(nodesDir)
	os.RemoveAll(filepath.Join(cfgDir, "tsnet-state"))

	fmt.Fprintln(out, "→ ✓ Logged out")
	return nil
}

var logoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Logout from Tailscale",
	Long: `Clear Tailscale authentication state.

Example:
  tslink logout`,
	RunE: func(cmd *cobra.Command, args []string) error {
		pidPath, err := config.PIDPath()
		if err != nil {
			return err
		}
		authKeyPath, err := config.AuthKeyPath()
		if err != nil {
			return err
		}
		nodesDir, err := config.NodesDir()
		if err != nil {
			return err
		}
		cfgDir, _ := config.Dir()

		return logoutUser(pidPath, authKeyPath, nodesDir, cfgDir, cmd.OutOrStdout())
	},
}

func init() {
	rootCmd.AddCommand(logoutCmd)
}
