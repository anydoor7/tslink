package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/credentials"
	"github.com/monody0007/tslink/internal/output"
	"github.com/spf13/cobra"
)

var getAPIKeyFn = credentials.GetAPIKey
var deleteAPIKeyFn = credentials.DeleteAPIKey
var hasClientSecretFn = credentials.HasClientSecret
var deleteClientSecretFn = credentials.DeleteClientSecret

// LogoutResult holds the result for JSON output.
type LogoutResult struct {
	WasLoggedIn bool `json:"was_logged_in"`
}

func logoutUser(pidPath, authKeyPath, nodesDir, cfgDir string, isJSON bool, out io.Writer) error {
	if isRunningFn(pidPath) {
		return fmt.Errorf("tslink is currently running — run 'tslink stop' first")
	}

	apiKey, _ := getAPIKeyFn()
	hasCS := hasClientSecretFn()
	_, authErr := os.Stat(authKeyPath)
	_, nodesErr := os.Stat(nodesDir)
	if apiKey == "" && !hasCS && os.IsNotExist(authErr) && os.IsNotExist(nodesErr) {
		if isJSON {
			output.Success("logout", LogoutResult{WasLoggedIn: false})
			return nil
		}
		fmt.Fprintln(out, "→ Not logged in")
		return nil
	}

	deleteAPIKeyFn()
	deleteClientSecretFn()
	os.Remove(authKeyPath)
	os.RemoveAll(nodesDir)
	os.RemoveAll(filepath.Join(cfgDir, "tsnet-state"))

	if isJSON {
		output.Success("logout", LogoutResult{WasLoggedIn: true})
		return nil
	}
	fmt.Fprintln(out, "→ ✓ Logged out")
	return nil
}

var logoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Logout from Tailscale",
	Long: `Clear all Tailscale authentication state and node data.

This command removes:
  - API access token (from system keychain and ~/.config/tslink/apikey)
  - OAuth client secret (from system keychain and ~/.config/tslink/clientsecret)
  - Legacy auth key file (~/.config/tslink/authkey)
  - All tsnet node state (~/.config/tslink/nodes/) including WireGuard keys
  - Legacy tsnet-state directory (if present)

The service registry (~/.config/tslink/registry.json) is NOT removed, so your
service definitions are preserved. Run 'tslink login' to re-authenticate.

The daemon must be stopped before logging out. If it is running, you will be
prompted to run 'tslink stop' first.

	Examples:
	  tslink logout                 Clear credentials and node state
	  tslink stop && tslink logout  Stop daemon then logout`,
	Args: cobra.NoArgs,
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

		return logoutUser(pidPath, authKeyPath, nodesDir, cfgDir, jsonOutput(cmd), cmd.OutOrStdout())
	},
}

func init() {
	rootCmd.AddCommand(logoutCmd)
}
