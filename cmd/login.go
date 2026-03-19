package cmd

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/credentials"
	"github.com/monody0007/tslink/internal/tailapi"
	"github.com/spf13/cobra"
	"tailscale.com/tsnet"
)

// Testable function variables for login credential flow
var (
	loginStdinReaderFn = func() *bufio.Reader { return bufio.NewReader(os.Stdin) }
	loginSetAPIKeyFn   = credentials.SetAPIKey
	loginVerifyAPIKeyFn = func(ctx context.Context) error {
		client, err := credentials.NewTailscaleClient()
		if err != nil || client == nil {
			credentials.DeleteAPIKey()
			return fmt.Errorf("invalid API key")
		}
		if _, err := client.Devices(ctx, nil); err != nil {
			credentials.DeleteAPIKey()
			return fmt.Errorf("API key verification failed: %w", err)
		}
		return nil
	}
	loginSaveClientSecretFn = credentials.SaveClientSecret
	loginEnsureTagsFn       = tailapi.EnsureTags
	loginTsnetLoginFn       = func(cfgDir string) (string, error) {
		tmpStateDir := filepath.Join(cfgDir, "tsnet-login-tmp")
		defer os.RemoveAll(tmpStateDir)

		fmt.Println("→ Opening browser for Tailscale login...")

		srv := &tsnet.Server{
			Hostname: "tslink-auth",
			Dir:      tmpStateDir,
		}

		if _, err := srv.Up(context.Background()); err != nil {
			srv.Close()
			return "", fmt.Errorf("login failed: %w", err)
		}

		loginName := ""
		if lc, lcErr := srv.LocalClient(); lcErr == nil {
			if st, stErr := lc.Status(context.Background()); stErr == nil && st.Self != nil {
				if u, ok := st.User[st.Self.UserID]; ok {
					loginName = u.LoginName
				}
			}
		}

		srv.Close()
		return loginName, nil
	}
)

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Login to Tailscale",
	Long: `Authenticate with your Tailscale account.

Opens a browser for OAuth login, then guides you to choose a credential type:

  [1] API access token (tskey-api-*)
      Generate at: https://login.tailscale.com/admin/settings/keys
      → Click "Generate access token..."
      Expires periodically — simple to set up, good for testing.

  [2] OAuth client secret (tskey-client-*)
      Generate at: https://login.tailscale.com/admin/settings/oauth
      → Click "+ credential" → choose "OAuth client"
      → Set scope to "all" (or customize per need)
      → Copy the "client secret" (NOT the shorter client ID above it)
      Never expires — recommended for long-running or unattended setups.

Credentials are stored in the system keychain (macOS Keychain, Linux secret
service, Windows Credential Manager). On systems without keychain support,
they fall back to files in ~/.config/tslink/ with restricted permissions (0600).

Examples:
  tslink login                  Interactive login with browser + credential prompt

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

		// Browser-based OAuth login
		loginName, err := loginTsnetLoginFn(cfgDir)
		if err != nil {
			return err
		}

		if loginName != "" {
			fmt.Printf("→ Logged in: %s\n", loginName)
		} else {
			fmt.Println("→ Logged in to Tailscale")
		}

		return loginCredentialFlow(cfgDir)
	},
}

func loginCredentialFlow(cfgDir string) error {
	reader := loginStdinReaderFn()

	fmt.Print("\n  Choose a credential type:\n\n")
	fmt.Print("    [1] API access token   — quick setup, expires periodically\n")
	fmt.Print("    [2] OAuth client secret — recommended, never expires\n\n")
	fmt.Print("  Enter 1 or 2: ")

	choiceStr, _ := reader.ReadString('\n')
	choiceStr = strings.TrimSpace(choiceStr)

	switch choiceStr {
	case "1":
		fmt.Print("\n  ─── API Access Token ───\n")
		fmt.Print("  1. Open: https://login.tailscale.com/admin/settings/keys\n")
		fmt.Print("  2. Click \"Generate access token...\"\n")
		fmt.Print("  3. Copy the token (starts with tskey-api-...)\n\n")
		fmt.Print("  Paste token: ")

		inputKey, _ := reader.ReadString('\n')
		inputKey = strings.TrimSpace(inputKey)
		if inputKey == "" {
			return fmt.Errorf("no token provided")
		}
		if !strings.HasPrefix(inputKey, "tskey-api-") {
			return fmt.Errorf("expected an API access token (tskey-api-...), got: %s...", inputKey[:min(20, len(inputKey))])
		}

		fmt.Println("→ Verifying API key...")
		if err := loginSetAPIKeyFn(inputKey); err != nil {
			return fmt.Errorf("save API key: %w", err)
		}

		if err := loginVerifyAPIKeyFn(context.Background()); err != nil {
			return err
		}

		// Remove legacy files
		if authKeyPath, e := config.AuthKeyPath(); e == nil {
			os.Remove(authKeyPath)
		}
		legacyDir := filepath.Join(cfgDir, "tsnet-state")
		os.RemoveAll(legacyDir)

		fmt.Println("→ API key saved (system keychain)")
		fmt.Println("→ Auth keys will be derived automatically on 'tslink serve'")

		// Auto-create default tag in tailnet ACL
		if err := loginEnsureTagsFn(context.Background(), []string{config.GetDefaultTag()}); err != nil {
			fmt.Fprintf(os.Stderr, "⚠ Could not create default tag in ACL: %v\n", err)
		} else {
			fmt.Printf("→ Ensured %s exists in tailnet ACL\n", config.GetDefaultTag())
		}

	case "2":
		fmt.Print("\n  ─── OAuth Client Secret ───\n")
		fmt.Print("  1. Open: https://login.tailscale.com/admin/settings/oauth\n")
		fmt.Print("  2. Click \"+ credential\" → choose \"OAuth client\"\n")
		fmt.Print("  3. Set scope to \"all\" (recommended for personal use)\n")
		fmt.Print("  4. Click \"Create\" — you will see two values:\n")
		fmt.Print("       Client ID:     km9GkS...  (short, NOT this one)\n")
		fmt.Print("       Client secret: tskey-client-...  ← copy THIS one\n")
		fmt.Print("     ⚠ The secret is shown only once!\n\n")
		fmt.Print("  Paste client secret: ")

		inputKey, _ := reader.ReadString('\n')
		inputKey = strings.TrimSpace(inputKey)
		if inputKey == "" {
			return fmt.Errorf("no secret provided")
		}
		if !strings.HasPrefix(inputKey, "tskey-client-") {
			return fmt.Errorf("expected an OAuth client secret (tskey-client-...), got: %s...", inputKey[:min(20, len(inputKey))])
		}

		fmt.Println("→ Saving client secret...")
		if err := loginSaveClientSecretFn(inputKey); err != nil {
			return fmt.Errorf("save client secret: %w", err)
		}

		// Remove legacy files
		if authKeyPath, e := config.AuthKeyPath(); e == nil {
			os.Remove(authKeyPath)
		}
		legacyDir := filepath.Join(cfgDir, "tsnet-state")
		os.RemoveAll(legacyDir)

		fmt.Println("→ Client secret saved (system keychain)")
		fmt.Println("→ Never expires — no renewal needed")

		// Auto-create default tag in tailnet ACL
		if err := loginEnsureTagsFn(context.Background(), []string{config.GetDefaultTag()}); err != nil {
			fmt.Fprintf(os.Stderr, "⚠ Could not create default tag in ACL: %v\n", err)
		} else {
			fmt.Printf("→ Ensured %s exists in tailnet ACL\n", config.GetDefaultTag())
		}

	default:
		return fmt.Errorf("invalid choice: %q — enter 1 or 2", choiceStr)
	}
	return nil
}

func init() {
	rootCmd.AddCommand(loginCmd)
}
