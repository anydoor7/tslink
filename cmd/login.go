package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/monody0007/tslink/internal/config"
	"github.com/spf13/cobra"
	"tailscale.com/tsnet"
)

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Login to Tailscale",
	Long: `Authenticate with your Tailscale account and generate an auth key.
Opens a browser for OAuth login. Run this before 'tslink serve'.

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

		// Create reusable auth key
		authKey, err := createAuthKey()
		if err != nil {
			srv.Close()
			return fmt.Errorf("create auth key: %w", err)
		}

		srv.Close()

		// Save auth key
		authKeyPath, err := config.AuthKeyPath()
		if err != nil {
			return err
		}
		if err := os.WriteFile(authKeyPath, []byte(authKey), 0o600); err != nil {
			return fmt.Errorf("save auth key: %w", err)
		}

		// Delete legacy tsnet-state/ if present
		legacyDir := filepath.Join(cfgDir, "tsnet-state")
		os.RemoveAll(legacyDir)

		if loginName != "" {
			fmt.Printf("→ ✓ Logged in: %s\n", loginName)
		} else {
			fmt.Println("→ ✓ Logged in to Tailscale")
		}
		fmt.Println("→ ✓ Auth key saved — services will auto-join your tailnet")
		return nil
	},
}

// createAuthKey attempts to create a reusable auth key.
// Strategy: try `tailscale` CLI first, then prompt user for manual creation.
func createAuthKey() (string, error) {
	// Try using the tailscale CLI to create an auth key
	out, err := exec.Command("tailscale", "api", "post", "/api/v2/tailnet/-/keys",
		"--data", `{"capabilities":{"devices":{"create":{"reusable":true,"ephemeral":false,"preauthorized":true}}}}`).Output()
	if err == nil {
		var resp struct {
			Key string `json:"key"`
		}
		if json.Unmarshal(out, &resp) == nil && resp.Key != "" {
			return resp.Key, nil
		}
	}

	// Fallback: prompt user to create key manually
	fmt.Println("\n→ Could not auto-create auth key.")
	fmt.Println("  Please create a reusable auth key at:")
	fmt.Println("  https://login.tailscale.com/admin/settings/keys")
	fmt.Println("  (Enable: Reusable, Pre-authorized)")
	fmt.Print("\n  Paste your auth key: ")

	var key string
	if _, err := fmt.Scanln(&key); err != nil {
		return "", fmt.Errorf("read auth key: %w", err)
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return "", fmt.Errorf("empty auth key")
	}
	return key, nil
}

func init() {
	rootCmd.AddCommand(loginCmd)
}
