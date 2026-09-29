package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/credentials"
	"github.com/monody0007/tslink/internal/output"
	"github.com/spf13/cobra"
)

var getAPIKeyFn = credentials.GetAPIKey
var hasClientSecretFn = credentials.HasClientSecret
var inspectStoredCredentialsFn = credentials.InspectStoredCredentialsStrict

// Logout removes credential values and their metadata inside one credential
// transaction, so the delete and metadata seams are the ...Locked variants.
var logoutMutationTransactionFn = credentials.WithMutationTransaction
var deleteStoredCredentialsFn = credentials.DeleteStoredCredentialsStrictLocked
var deleteStoredCredentialKindFn = credentials.DeleteStoredCredentialKindStrictLocked
var deleteCredentialMetadataFn = credentials.DeleteSlotMetadataLocked
var removeCredentialMetadataFn = credentials.RemoveMetadataFileLocked
var statFileFn = os.Stat
var removeFileFn = os.Remove
var removeAllFn = os.RemoveAll

// LogoutResult holds the result for JSON output.
type LogoutResult struct {
	WasLoggedIn bool `json:"was_logged_in"`
	// Kind is the selected credential slot for a selective logout; empty for
	// the full logout.
	Kind string `json:"kind,omitempty"`
	// DeletedCredentials lists the credential slots removed by this invocation.
	DeletedCredentials []string `json:"deleted_credentials"`
}

type logoutOptions struct {
	PIDPath     string
	AuthKeyPath string
	NodesDir    string
	ConfigDir   string
	// Kind selects one credential slot (api-key or client-secret). Empty means
	// the full logout that also removes node state.
	Kind string
}

func logoutUser(pidPath, authKeyPath, nodesDir, cfgDir string, isJSON bool, out io.Writer) error {
	return logoutUserWithOptions(logoutOptions{PIDPath: pidPath, AuthKeyPath: authKeyPath, NodesDir: nodesDir, ConfigDir: cfgDir}, isJSON, out)
}

func presentCredentialKinds(status credentials.StoredCredentialStatus) []string {
	kinds := []string{}
	if status.APIKey.Present() {
		kinds = append(kinds, credentials.SlotAPIKey)
	}
	if status.ClientSecret.Present() {
		kinds = append(kinds, credentials.SlotClientSecret)
	}
	return kinds
}

func logoutUserWithOptions(opts logoutOptions, isJSON bool, out io.Writer) error {
	if opts.Kind != "" && !credentials.ValidSlot(opts.Kind) {
		return output.ErrUsage(fmt.Sprintf("--kind must be %s or %s", credentials.SlotAPIKey, credentials.SlotClientSecret))
	}
	if isRunningFn(opts.PIDPath) {
		return fmt.Errorf("tslink is currently running — run 'tslink stop' first")
	}
	if opts.Kind != "" {
		return logoutCredentialKind(opts.Kind, isJSON, out)
	}

	credentialStatus, inspectCredentialErr := inspectStoredCredentialsFn()
	authExists, err := pathExists(opts.AuthKeyPath)
	if err != nil {
		return fmt.Errorf("inspect legacy auth key: %w", err)
	}
	nodesExist, err := pathExists(opts.NodesDir)
	if err != nil {
		return fmt.Errorf("inspect node state: %w", err)
	}
	stateDir := filepath.Join(opts.ConfigDir, "tsnet-state")
	stateExists, err := pathExists(stateDir)
	if err != nil {
		return fmt.Errorf("inspect legacy tsnet state: %w", err)
	}
	if !credentialStatus.AnyPresent() && !authExists && !nodesExist && !stateExists && inspectCredentialErr == nil {
		if isJSON {
			output.Success("logout", LogoutResult{WasLoggedIn: false, DeletedCredentials: []string{}})
			return nil
		}
		fmt.Fprintln(out, "→ Not logged in")
		return nil
	}
	deleted := presentCredentialKinds(credentialStatus)

	var cleanupErrs []error
	if inspectCredentialErr != nil {
		cleanupErrs = append(cleanupErrs, fmt.Errorf("inspect credential stores: %w", inspectCredentialErr))
	}
	// A login that commits after this transaction keeps its metadata.
	if err := logoutMutationTransactionFn(func(*credentials.MutationTransaction) error {
		if err := deleteStoredCredentialsFn(); err != nil {
			cleanupErrs = append(cleanupErrs, fmt.Errorf("delete credential stores: %w", err))
		}
		if err := removeCredentialMetadataFn(); err != nil {
			cleanupErrs = append(cleanupErrs, fmt.Errorf("remove credential metadata: %w", err))
		}
		return nil
	}); err != nil {
		cleanupErrs = append(cleanupErrs, fmt.Errorf("delete credential stores: %w", err))
	}
	if authExists {
		if err := removeFileFn(opts.AuthKeyPath); err != nil && !os.IsNotExist(err) {
			cleanupErrs = append(cleanupErrs, fmt.Errorf("remove legacy auth key: %w", err))
		}
	}
	if nodesExist {
		if err := removeAllFn(opts.NodesDir); err != nil && !os.IsNotExist(err) {
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
	if err := verifyLogoutCleanup(opts.AuthKeyPath, opts.NodesDir, stateDir); err != nil {
		return err
	}

	if isJSON {
		output.Success("logout", LogoutResult{WasLoggedIn: true, DeletedCredentials: deleted})
		return nil
	}
	if len(deleted) > 0 {
		fmt.Fprintf(out, "→ ✓ Logged out (deleted: %s)\n", strings.Join(deleted, ", "))
	} else {
		fmt.Fprintln(out, "→ ✓ Logged out")
	}
	return nil
}

// logoutCredentialKind removes exactly one credential slot and its value-free
// metadata. Node state, the legacy auth key, and the other slot are untouched,
// so an operator can rotate or drop one credential without re-enrolling nodes.
func logoutCredentialKind(kind string, isJSON bool, out io.Writer) error {
	status, err := inspectStoredCredentialsFn()
	if err != nil {
		return fmt.Errorf("inspect credential stores: %w", err)
	}
	present := status.APIKey.Present()
	if kind == credentials.SlotClientSecret {
		present = status.ClientSecret.Present()
	}
	if !present {
		if isJSON {
			output.Success("logout", LogoutResult{WasLoggedIn: false, Kind: kind, DeletedCredentials: []string{}})
			return nil
		}
		fmt.Fprintf(out, "→ No %s credential is stored\n", kind)
		return nil
	}

	var cleanupErrs []error
	if err := logoutMutationTransactionFn(func(*credentials.MutationTransaction) error {
		if err := deleteStoredCredentialKindFn(kind); err != nil {
			cleanupErrs = append(cleanupErrs, fmt.Errorf("delete %s credential: %w", kind, err))
		}
		if err := deleteCredentialMetadataFn(kind); err != nil {
			cleanupErrs = append(cleanupErrs, fmt.Errorf("remove %s credential metadata: %w", kind, err))
		}
		return nil
	}); err != nil {
		cleanupErrs = append(cleanupErrs, fmt.Errorf("delete %s credential: %w", kind, err))
	}
	if err := errors.Join(cleanupErrs...); err != nil {
		return err
	}
	after, err := inspectStoredCredentialsFn()
	if err != nil {
		return fmt.Errorf("inspect credential stores after cleanup: %w", err)
	}
	stillPresent := after.APIKey.Present()
	if kind == credentials.SlotClientSecret {
		stillPresent = after.ClientSecret.Present()
	}
	if stillPresent {
		return fmt.Errorf("%s credential still present after cleanup", kind)
	}

	kept := presentCredentialKinds(after)
	if isJSON {
		output.Success("logout", LogoutResult{WasLoggedIn: true, Kind: kind, DeletedCredentials: []string{kind}})
		return nil
	}
	if len(kept) > 0 {
		fmt.Fprintf(out, "→ ✓ Removed %s credential (kept: %s)\n", kind, strings.Join(kept, ", "))
	} else {
		fmt.Fprintf(out, "→ ✓ Removed %s credential\n", kind)
	}
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
	Long: `Clear Tailscale authentication state and node data.

Without --kind this command removes:
  - API access token (from system keychain and ~/.config/tslink/apikey)
  - OAuth client secret (from system keychain and ~/.config/tslink/clientsecret)
  - Value-free credential metadata (~/.config/tslink/credential-meta.json)
  - Legacy auth key file (~/.config/tslink/authkey)
  - All tsnet node state (~/.config/tslink/nodes/) including WireGuard keys
  - Legacy tsnet-state directory (if present)

With --kind api-key or --kind client-secret only that credential slot and its
metadata are removed; the other slot, node state, and the legacy auth key are
kept. Human and JSON output list exactly which slots were deleted.

The service registry (~/.config/tslink/registry.json) is NOT removed, so your
service definitions are preserved. Run 'tslink login' to re-authenticate.

The daemon must be stopped before logging out. If it is running, you will be
prompted to run 'tslink stop' first.

	Examples:
	  tslink logout                    Clear credentials and node state
	  tslink logout --kind api-key     Drop only the expiring access token
	  tslink stop && tslink logout     Stop daemon then logout`,
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
		kind, _ := cmd.Flags().GetString("kind")

		return logoutUserWithOptions(logoutOptions{
			PIDPath:     pidPath,
			AuthKeyPath: authKeyPath,
			NodesDir:    nodesDir,
			ConfigDir:   cfgDir,
			Kind:        strings.TrimSpace(kind),
		}, jsonOutput(cmd), cmd.OutOrStdout())
	},
}

func init() {
	logoutCmd.Flags().String("kind", "", "Remove only one credential slot ("+credentials.SlotAPIKey+" or "+credentials.SlotClientSecret+") and its metadata; node state and the other slot are kept")
	rootCmd.AddCommand(logoutCmd)
}
