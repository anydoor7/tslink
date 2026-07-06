package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/credentials"
	"github.com/monody0007/tslink/internal/output"
	"github.com/monody0007/tslink/internal/tailapi"
	"github.com/spf13/cobra"
	"tailscale.com/tsnet"
)

// LoginResult represents the JSON output of a successful login.
type LoginResult struct {
	Method         string `json:"method"`
	LoginName      string `json:"login_name,omitempty"`
	TagCreated     string `json:"tag_created,omitempty"`
	Degraded       bool   `json:"degraded"`
	TagEnsureError string `json:"tag_ensure_error,omitempty"`
}

// Testable function variables for login credential flow
var (
	loginStdinReaderFn  = func() *bufio.Reader { return bufio.NewReader(os.Stdin) }
	loginSetAPIKeyFn    = credentials.SetAPIKey
	loginVerifyAPIKeyFn = func(ctx context.Context) error {
		client, err := credentials.NewTailscaleClient()
		if err != nil || client == nil {
			credentials.DeleteAPIKey()
			return fmt.Errorf("invalid API key")
		}
		if _, err := client.Devices().List(ctx); err != nil {
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
      Expires periodically — quick setup for API-backed TSLink automation.
      Supports API verification, ACL tag setup, auth-key derivation, and
      ownership-verified stale-device cleanup attempts.

  [2] OAuth client secret (tskey-client-*)
      Generate at: https://login.tailscale.com/admin/settings/oauth
      → Click "+ credential" → choose "OAuth client"
      → Validate scopes and tags for your services
      → Copy the "client secret" (NOT the shorter client ID above it)
      Long-lived node auth. Without an API token, TSLink skips ACL tag and
      stale-device API automation; validate before unattended use.

Credentials are stored in the system keychain (macOS Keychain, Linux secret
service, Windows Credential Manager). On systems without keychain support,
they fall back to files in ~/.config/tslink/ with restricted permissions (0600).

Non-interactive mode:
  TSLINK_API_KEY="tskey-api-..." tslink login
  TSLINK_CLIENT_SECRET="tskey-client-..." tslink login
  printf %s "$TSLINK_API_KEY" | tslink login --api-key-stdin
  printf %s "$TSLINK_CLIENT_SECRET" | tslink login --client-secret-stdin
  tslink login --api-key "tskey-api-..."              # compatible but visible in process lists
  tslink login --client-secret "tskey-client-..."     # compatible but visible in process lists

Examples:
  tslink login                  Interactive login with browser + credential prompt

  # Or store a key directly (skip interactive login):
  echo -n "tskey-api-..." > ~/.config/tslink/apikey && chmod 600 ~/.config/tslink/apikey`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := config.EnsureDir(); err != nil {
			return err
		}

		apiKey, clientSecret, err := resolveLoginCredentials(cmd)
		if err != nil {
			return err
		}

		if apiKey != "" {
			return loginWithAPIKey(cmd, apiKey)
		}
		if clientSecret != "" {
			return loginWithClientSecret(cmd, clientSecret)
		}

		// JSON mode requires non-interactive credentials
		if jsonOutput(cmd) {
			return fmt.Errorf("--json requires --api-key or --client-secret (interactive login not available in JSON mode)")
		}

		// Interactive flow
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

		return loginCredentialFlow(cmd, cfgDir)
	},
}

func readLoginCredentialStdin(cmd *cobra.Command, name string) (string, error) {
	data, err := io.ReadAll(cmd.InOrStdin())
	if err != nil {
		return "", fmt.Errorf("read %s from stdin: %w", name, err)
	}
	value := strings.TrimSpace(string(data))
	if value == "" {
		return "", fmt.Errorf("%s stdin was empty", name)
	}
	return value, nil
}

func resolveLoginCredentials(cmd *cobra.Command) (apiKey, clientSecret string, err error) {
	apiKeyFlag, _ := cmd.Flags().GetString("api-key")
	clientSecretFlag, _ := cmd.Flags().GetString("client-secret")
	apiKeyStdin, _ := cmd.Flags().GetBool("api-key-stdin")
	clientSecretStdin, _ := cmd.Flags().GetBool("client-secret-stdin")

	explicit := 0
	if apiKeyFlag != "" {
		explicit++
	}
	if clientSecretFlag != "" {
		explicit++
	}
	if apiKeyStdin {
		explicit++
	}
	if clientSecretStdin {
		explicit++
	}
	if explicit > 1 {
		return "", "", fmt.Errorf("provide only one explicit credential source")
	}

	switch {
	case apiKeyFlag != "":
		return apiKeyFlag, "", nil
	case clientSecretFlag != "":
		return "", clientSecretFlag, nil
	case apiKeyStdin:
		value, err := readLoginCredentialStdin(cmd, "api key")
		if err != nil {
			return "", "", err
		}
		return value, "", nil
	case clientSecretStdin:
		value, err := readLoginCredentialStdin(cmd, "client secret")
		if err != nil {
			return "", "", err
		}
		return "", value, nil
	}

	if value := os.Getenv("TSLINK_API_KEY"); value != "" {
		return value, "", nil
	}
	if value := os.Getenv("TSLINK_CLIENT_SECRET"); value != "" {
		return "", value, nil
	}
	return "", "", nil
}

func loginWithAPIKey(cmd *cobra.Command, key string) error {
	if !strings.HasPrefix(key, "tskey-api-") {
		return fmt.Errorf("API key must start with \"tskey-api-\" prefix")
	}

	if err := loginSetAPIKeyFn(key); err != nil {
		return fmt.Errorf("save API key: %w", err)
	}

	if err := loginVerifyAPIKeyFn(context.Background()); err != nil {
		return err
	}

	credentials.DeleteClientSecret()

	// Remove legacy files
	if authKeyPath, e := config.AuthKeyPath(); e == nil {
		os.Remove(authKeyPath)
	}
	cfgDir, _ := config.Dir()
	os.RemoveAll(filepath.Join(cfgDir, "tsnet-state"))

	// Ensure default tag
	tagCreated := ""
	degraded := false
	tagEnsureError := ""
	defaultTag := config.GetDefaultTag()
	if err := loginEnsureTagsFn(context.Background(), []string{defaultTag}); err != nil {
		degraded = true
		tagEnsureError = err.Error()
		if errors.Is(err, tailapi.ErrNoAPIClient) {
			if !jsonOutput(cmd) {
				fmt.Fprintf(os.Stderr, "→ Degraded login: skipped ACL tag management: %v. Services using tags may fail until tag automation is available.\n", err)
			}
		} else if !jsonOutput(cmd) {
			fmt.Fprintf(os.Stderr, "⚠ Degraded login: could not ensure default ACL tag: %v. `tslink serve` may fail for tag-restricted services; verify API token permissions for tag automation.\n", err)
		}
	} else {
		tagCreated = defaultTag
	}

	if jsonOutput(cmd) {
		output.Success("login", LoginResult{Method: "api-key", TagCreated: tagCreated, Degraded: degraded, TagEnsureError: tagEnsureError})
	} else {
		fmt.Println("→ API key saved (system keychain)")
		fmt.Println("→ Auth keys will be derived automatically on 'tslink serve'")
		if tagCreated != "" {
			fmt.Printf("→ Ensured %s exists in tailnet ACL\n", tagCreated)
		}
	}
	return nil
}

func loginWithClientSecret(cmd *cobra.Command, secret string) error {
	if !strings.HasPrefix(secret, "tskey-client-") {
		return fmt.Errorf("client secret must start with \"tskey-client-\" prefix")
	}

	if err := loginSaveClientSecretFn(secret); err != nil {
		return fmt.Errorf("save client secret: %w", err)
	}

	credentials.DeleteAPIKey()

	// Remove legacy files
	if authKeyPath, e := config.AuthKeyPath(); e == nil {
		os.Remove(authKeyPath)
	}
	cfgDir, _ := config.Dir()
	os.RemoveAll(filepath.Join(cfgDir, "tsnet-state"))

	// Ensure default tag
	tagCreated := ""
	degraded := false
	tagEnsureError := ""
	defaultTag := config.GetDefaultTag()
	if err := loginEnsureTagsFn(context.Background(), []string{defaultTag}); err != nil {
		degraded = true
		tagEnsureError = err.Error()
		if errors.Is(err, tailapi.ErrNoAPIClient) {
			if !jsonOutput(cmd) {
				fmt.Fprintf(os.Stderr, "→ Degraded login: skipped ACL tag management: %v. OAuth client-secret mode may start nodes, but tag/device API automation requires an API access token.\n", err)
			}
		} else if !jsonOutput(cmd) {
			fmt.Fprintf(os.Stderr, "⚠ Degraded login: could not ensure default ACL tag: %v. `tslink serve` may fail for tag-restricted services; API access token mode is recommended for tag automation.\n", err)
		}
	} else {
		tagCreated = defaultTag
	}

	if jsonOutput(cmd) {
		output.Success("login", LoginResult{Method: "client-secret", TagCreated: tagCreated, Degraded: degraded, TagEnsureError: tagEnsureError})
	} else {
		fmt.Println("→ Client secret saved (system keychain)")
		fmt.Println("→ Long-lived node auth saved")
		fmt.Println("→ Without an API token, ACL tag and stale-device API automation is skipped")
		fmt.Println("→ Validate OAuth scopes and service tags before unattended use")
		if tagCreated != "" {
			fmt.Printf("→ Ensured %s exists in tailnet ACL\n", tagCreated)
		}
	}
	return nil
}

func loginCredentialFlow(cmd *cobra.Command, cfgDir string) error {
	reader := loginStdinReaderFn()

	fmt.Print("\n  Choose a credential type:\n\n")
	fmt.Print("    [1] API access token   — quick setup, full automation, expires periodically\n")
	fmt.Print("    [2] OAuth client secret — long-lived node auth; tag/device API automation skipped without an API token\n\n")
	fmt.Print("  Enter 1 or 2: ")

	choiceStr, _ := reader.ReadString('\n')
	choiceStr = strings.TrimSpace(choiceStr)

	switch choiceStr {
	case "1":
		fmt.Print("\n  ─── API Access Token ───\n")
		fmt.Print("  Use this for API-backed TSLink automation: API verification, ACL tags,\n")
		fmt.Print("  auth-key derivation, and ownership-verified stale-device cleanup attempts.\n\n")
		fmt.Print("  1. Open: https://login.tailscale.com/admin/settings/keys\n")
		fmt.Print("  2. Click \"Generate access token...\"\n")
		fmt.Print("  3. Copy the token (starts with tskey-api-...)\n\n")
		fmt.Print("  Paste token: ")

		inputKey, _ := reader.ReadString('\n')
		inputKey = strings.TrimSpace(inputKey)
		if inputKey == "" {
			return fmt.Errorf("no token provided")
		}

		fmt.Println("→ Verifying API key...")
		return loginWithAPIKey(cmd, inputKey)

	case "2":
		fmt.Print("\n  ─── OAuth Client Secret ───\n")
		fmt.Print("  1. Open: https://login.tailscale.com/admin/settings/oauth\n")
		fmt.Print("  2. Click \"+ credential\" → choose \"OAuth client\"\n")
		fmt.Print("  3. Validate OAuth scopes and tags for each service before unattended use\n")
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

		fmt.Println("→ Saving client secret...")
		return loginWithClientSecret(cmd, inputKey)

	default:
		return fmt.Errorf("invalid choice: %q — enter 1 or 2", choiceStr)
	}
}

func init() {
	rootCmd.AddCommand(loginCmd)
	loginCmd.Flags().String("api-key", "", "API access token (tskey-api-*) for non-interactive login; visible in process lists, prefer env or --api-key-stdin")
	loginCmd.Flags().String("client-secret", "", "OAuth client secret (tskey-client-*) for non-interactive login; visible in process lists, prefer env or --client-secret-stdin")
	loginCmd.Flags().Bool("api-key-stdin", false, "Read API access token from stdin")
	loginCmd.Flags().Bool("client-secret-stdin", false, "Read OAuth client secret from stdin")
}
