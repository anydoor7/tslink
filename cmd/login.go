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

Opens a browser for OAuth login, then asks for your API key.
The API key is used to manage tailnet devices and derive auth keys automatically.

Generate an API key at: https://login.tailscale.com/admin/settings/keys

Example:
  tslink login`,
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

		// Ask for API key (the only key we need now)
		fmt.Print("\n  Paste your API access token\n")
		fmt.Print("  (generate at https://login.tailscale.com/admin/settings/keys)\n")
		fmt.Print("  API key: ")
		var apiKey string
		fmt.Scanln(&apiKey)
		apiKey = strings.TrimSpace(apiKey)
		if apiKey == "" {
			return fmt.Errorf("API key is required — tslink uses it to manage devices and derive auth keys")
		}

		// Verify the key works by trying to list devices
		fmt.Println("→ Verifying API key...")
		if err := credentials.SetAPIKey(apiKey); err != nil {
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
		return nil
	},
}

func init() {
	rootCmd.AddCommand(loginCmd)
}
