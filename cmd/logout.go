package cmd

import (
	"errors"
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
var hasClientSecretFn = credentials.HasClientSecret
var inspectStoredCredentialsFn = credentials.InspectStoredCredentialsStrict
var deleteStoredCredentialsFn = credentials.DeleteStoredCredentialsStrict
var statFileFn = os.Stat
var removeFileFn = os.Remove
var removeAllFn = os.RemoveAll

// LogoutResult holds the result for JSON output.
type LogoutResult struct {
	WasLoggedIn bool `json:"was_logged_in"`
}

func logoutUser(pidPath, authKeyPath, nodesDir, cfgDir string, isJSON bool, out io.Writer) error {
	if isRunningFn(pidPath) {
		return fmt.Errorf("tslink is currently running — run 'tslink stop' first")
	}

	credentialStatus, inspectCredentialErr := inspectStoredCredentialsFn()
	authExists, err := pathExists(authKeyPath)
	if err != nil {
		return fmt.Errorf("inspect legacy auth key: %w", err)
	}
	nodesExist, err := pathExists(nodesDir)
	if err != nil {
		return fmt.Errorf("inspect node state: %w", err)
	}
	stateDir := filepath.Join(cfgDir, "tsnet-state")
	stateExists, err := pathExists(stateDir)
	if err != nil {
		return fmt.Errorf("inspect legacy tsnet state: %w", err)
	}
	if !credentialStatus.AnyPresent() && !authExists && !nodesExist && !stateExists && inspectCredentialErr == nil {
		if isJSON {
			output.Success("logout", LogoutResult{WasLoggedIn: false})
			return nil
		}
		fmt.Fprintln(out, "→ Not logged in")
		return nil
	}

	var cleanupErrs []error
	if inspectCredentialErr != nil {
		cleanupErrs = append(cleanupErrs, fmt.Errorf("inspect credential stores: %w", inspectCredentialErr))
	}
	if err := deleteStoredCredentialsFn(); err != nil {
		cleanupErrs = append(cleanupErrs, fmt.Errorf("delete credential stores: %w", err))
	}
	if authExists {
		if err := removeFileFn(authKeyPath); err != nil && !os.IsNotExist(err) {
			cleanupErrs = append(cleanupErrs, fmt.Errorf("remove legacy auth key: %w", err))
		}
	}
	if nodesExist {
		if err := removeAllFn(nodesDir); err != nil && !os.IsNotExist(err) {
			cleanupErrs = append(cleanupErrs, fmt.Errorf("remove node state: %w", err))
		}
	}
	if stateExists {
		if err := removeAllFn(stateDir); err != nil && !os.IsNotExist(err) {
			cleanupErrs = append(cleanupErrs, fmt.Errorf("remove legacy tsnet state: %w", err))
		}
	}
	if err := errors.Join(cleanupErrs...); err != nil {
		return err
	}
	if err := verifyLogoutCleanup(authKeyPath, nodesDir, stateDir); err != nil {
		return err
	}

	if isJSON {
		output.Success("logout", LogoutResult{WasLoggedIn: true})
		return nil
	}
	fmt.Fprintln(out, "→ ✓ Logged out")
	return nil
}

func pathExists(path string) (bool, error) {
	_, err := statFileFn(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

func verifyLogoutCleanup(authKeyPath, nodesDir, stateDir string) error {
	var errs []error
	if status, err := inspectStoredCredentialsFn(); err != nil {
		errs = append(errs, fmt.Errorf("inspect credential stores after cleanup: %w", err))
	} else if status.AnyPresent() {
		errs = append(errs, fmt.Errorf("credential still present after cleanup"))
	}
	for label, path := range map[string]string{
		"legacy auth key":    authKeyPath,
		"node state":         nodesDir,
		"legacy tsnet state": stateDir,
	} {
		exists, err := pathExists(path)
		if err != nil {
			errs = append(errs, fmt.Errorf("read back %s cleanup: %w", label, err))
			continue
		}
		if exists {
			errs = append(errs, fmt.Errorf("%s still present after cleanup", label))
		}
	}
	return errors.Join(errs...)
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
