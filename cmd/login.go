package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/credentials"
	"github.com/spf13/cobra"
	"tailscale.com/tsnet"
)

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Login to Tailscale",
	Long: `Authenticate with your Tailscale account.

Opens a browser for OAuth login, then prompts for a credential. TSLink accepts
two credential types:

  API access token (tskey-api-*)
    Used to manage tailnet devices and derive ephemeral auth keys automatically.
    Expires periodically — regenerate at the Tailscale admin console when needed.

  OAuth client secret (tskey-client-*)
    Used directly by tsnet for authentication. Never expires, so no renewal is
    needed. Recommended for long-running or unattended setups.

Credentials are stored in the system keychain (macOS Keychain, Linux secret
service, Windows Credential Manager). On systems without keychain support,
they fall back to files in ~/.config/tslink/ with restricted permissions (0600).

Generate credentials at: https://login.tailscale.com/admin/settings/keys

Examples:
  tslink login                  Interactive login with browser + key prompt

  # Or store a key directly (skip interactive login):
  echo -n "tskey-api-..." > ~/.config/tslink/apikey && chmod 600 ~/.config/tslink/apikey`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := config.EnsureDir(); err != nil {
			return err
		}

		cfgDir, err := config.Dir()
		if err != nil {
			return err
		}

		// Temp state dir for login node
		tmpStateDir := filepath.Join(cfgDir, "tsnet-login-tmp")
		defer os.RemoveAll(tmpStateDir)

		fmt.Println("→ Opening browser for Tailscale login...")

		srv := &tsnet.Server{
			Hostname: "tslink-auth",
			Dir:      tmpStateDir,
		}

		if _, err := srv.Up(context.Background()); err != nil {
			srv.Close()
			return fmt.Errorf("login failed: %w", err)
		}

		// Get login name for display
		loginName := ""
		if lc, lcErr := srv.LocalClient(); lcErr == nil {
			if st, stErr := lc.Status(context.Background()); stErr == nil && st.Self != nil {
				if u, ok := st.User[st.Self.UserID]; ok {
					loginName = u.LoginName
				}
			}
		}

		srv.Close()

		if loginName != "" {
			fmt.Printf("→ Logged in: %s\n", loginName)
		} else {
			fmt.Println("→ Logged in to Tailscale")
		}

		// Ask for API key or client secret
		fmt.Print("\n  Paste your API access token or OAuth client secret\n")
		fmt.Print("  (generate at https://login.tailscale.com/admin/settings/keys)\n")
		fmt.Print("  Key: ")
		var inputKey string
		fmt.Scanln(&inputKey)
		inputKey = strings.TrimSpace(inputKey)
		if inputKey == "" {
			return fmt.Errorf("API key or client secret is required — tslink uses it to manage devices and derive auth keys")
		}

		if strings.HasPrefix(inputKey, "tskey-client-") {
			// OAuth client secret — store directly, no verification via API
			fmt.Println("→ Saving client secret...")
			if err := credentials.SaveClientSecret(inputKey); err != nil {
				return fmt.Errorf("save client secret: %w", err)
			}

			// Remove legacy authkey file (no longer needed)
			if authKeyPath, e := config.AuthKeyPath(); e == nil {
				os.Remove(authKeyPath)
			}

			// Delete legacy tsnet-state/ if present
			legacyDir := filepath.Join(cfgDir, "tsnet-state")
			os.RemoveAll(legacyDir)

			fmt.Println("→ Client secret saved (system keychain)")
			fmt.Println("→ OAuth client secret never expires — no auth key derivation needed")
		} else {
			// API key — verify and store
			fmt.Println("→ Verifying API key...")
			if err := credentials.SetAPIKey(inputKey); err != nil {
				return fmt.Errorf("save API key: %w", err)
			}

			client, err := credentials.NewTailscaleClient()
			if err != nil || client == nil {
				credentials.DeleteAPIKey()
				return fmt.Errorf("invalid API key")
			}
			if _, err := client.Devices(context.Background(), nil); err != nil {
				credentials.DeleteAPIKey()
				return fmt.Errorf("API key verification failed: %w", err)
			}

			// Remove legacy authkey file (no longer needed)
			if authKeyPath, e := config.AuthKeyPath(); e == nil {
				os.Remove(authKeyPath)
			}

			// Delete legacy tsnet-state/ if present
			legacyDir := filepath.Join(cfgDir, "tsnet-state")
			os.RemoveAll(legacyDir)

			fmt.Println("→ API key saved (system keychain)")
			fmt.Println("→ Auth keys will be derived automatically on 'tslink serve'")
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(loginCmd)
}
