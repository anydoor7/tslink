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
	"time"

	"github.com/monody0007/tslink/internal/authmode"
	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/credentials"
	"github.com/monody0007/tslink/internal/logging"
	"github.com/monody0007/tslink/internal/output"
	"github.com/monody0007/tslink/internal/security"
	"github.com/monody0007/tslink/internal/tailapi"
	"github.com/spf13/cobra"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/tsnet"
)

// clientSecretActivationTimeout bounds the disposable Up used to semantically
// validate a candidate OAuth client secret before the login transaction
// commits.
const clientSecretActivationTimeout = 60 * time.Second

// LoginResult represents the JSON output of a successful login.
type LoginResult struct {
	Method               string                         `json:"method"`
	CredentialBackend    credentials.CredentialBackend  `json:"credential_backend"`
	LoginName            string                         `json:"login_name,omitempty"`
	TagCreated           string                         `json:"tag_created,omitempty"`
	Degraded             bool                           `json:"degraded"`
	TagEnsureError       string                         `json:"tag_ensure_error,omitempty"`
	ACLMutationSkipped   bool                           `json:"acl_mutation_skipped"`
	RemoteSideEffectPlan *security.RemoteSideEffectPlan `json:"remote_side_effect_plan,omitempty"`
}

type loginValidationServer interface {
	Up(context.Context) (*ipnstate.Status, error)
	Close() error
}

// Testable function variables for login credential flow.
var (
	loginStdinReaderFn  = func() *bufio.Reader { return bufio.NewReader(os.Stdin) }
	loginSetAPIKeyFn    = credentials.SetAPIKeyWithBackend
	loginGetAPIKeyFn    = credentials.GetAPIKey
	loginDeleteAPIKeyFn = credentials.DeleteAPIKeyChecked
	loginVerifyAPIKeyFn = func(ctx context.Context, key string) error {
		client, err := credentials.NewTailscaleClientWithAPIKey(key)
		if err != nil || client == nil {
			return fmt.Errorf("invalid API key")
		}
		if _, err := client.Devices().List(ctx); err != nil {
			return fmt.Errorf("API key verification failed: %w", err)
		}
		return nil
	}
	loginSaveClientSecretFn   = credentials.SaveClientSecretWithBackend
	loginGetClientSecretFn    = credentials.GetClientSecret
	loginDeleteClientSecretFn = credentials.DeleteClientSecretChecked
	// loginActivateClientSecretFn semantically proves a candidate OAuth client
	// secret is usable by completing a real, disposable, ephemeral tsnet Up with
	// it. Production wires the real path (activateClientSecretViaUp); tests inject
	// success/failure to exercise ordering without touching a real tailnet. A
	// prefix check alone cannot prove a client secret is usable, so this is the
	// semantic gate that keeps a syntactically-valid-but-unusable secret from
	// retiring a working credential.
	loginActivateClientSecretFn  = activateClientSecretViaUp
	loginEnsureTagsFn            = tailapi.EnsureTags
	loginCleanupLegacyStateFn    = cleanupLoginLegacyState
	loginMarkCredentialUpgradeFn = authmode.MarkCredentialUpgradePending
	loginRemoveAllFn             = os.RemoveAll
	loginNewValidationServerFn   = func(tmpStateDir, authKey string, tags []string) loginValidationServer {
		return newClientSecretValidationServer(tmpStateDir, authKey, tags)
	}
)

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Store an optional credential for durable installs",
	Long: `Store an optional administrative credential for durable TSLink installs.

You do not need this command for the default first run. A one-service quick
share via "tslink serve" enrolls a user-owned, untagged node with one browser
click, no ACL edits, and no administrative credential. Each additional fresh
service has its own node and login URL. User-owned node keys expire and may
eventually need re-authentication.

Use this command only for the tagged, durable multi-service tier. Choose a
credential type:

When upgrading services that already enrolled through Tier 1, restart the
running TSLink server after login. A successful zero-to-credential transition
is recorded atomically; the next credentialed start removes the old per-service
tsnet state before enrollment so the auth key creates the tagged Tier 2 nodes.

  [1] API access token (tskey-api-*)
      Generate at: https://login.tailscale.com/admin/settings/keys
      This is a broad tailnet-admin token, not a scoped API key, and expires
      within 90 days. It is unnecessary for a quick share.
      → Click "Generate access token..."
      Expires periodically — quick setup for API-backed TSLink automation.
      Supports API verification, auth-key derivation, and read-only remote
      evidence. Remote ACL tag mutation requires --manage-acl.

  [2] OAuth client secret (tskey-client-*)
      Generate at: https://login.tailscale.com/admin/settings/oauth
      → Click "+ credential" → choose "OAuth client"
      → Validate scopes and tags for your services
      → Copy the "client secret" (NOT the shorter client ID above it)
      Long-lived node auth. Remote ACL writes require explicit --manage-acl;
      remote device cleanup is protected/manual in this version.

Credentials are stored in the system keychain (macOS Keychain, Linux secret
service, Windows Credential Manager). On systems without keychain support,
they fall back to files in ~/.config/tslink/ with restricted permissions (0600).

	Non-interactive mode:
	  printf %s "$TSLINK_API_KEY" | tslink login --api-key-stdin
	  printf %s "$TSLINK_CLIENT_SECRET" | tslink login --client-secret-stdin
	  # TSLINK_API_KEY / TSLINK_CLIENT_SECRET may also be pre-injected by a
	  # secret manager before this process starts. Do not inline secret values in
	  # shell commands because they can land in shell history.
	  # The compatibility flags --api-key and --client-secret expose values in
	  # process lists; prefer the stdin variants above.

		Examples:
		  tslink login                  Interactive administrative credential prompt

		  # Automation path with a secret manager:
		  op read op://vault/tslink/api-key | tslink login --api-key-stdin`,
	Args: cobra.NoArgs,
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

		// JSON mode requires non-interactive credentials. Interactive tsnet
		// enrollment belongs to `serve --json`, which can keep a daemon child
		// alive while returning its auth URL immediately.
		if jsonOutput(cmd) {
			return fmt.Errorf("--json requires --api-key or --client-secret (interactive login not available in JSON mode)")
		}

		// Interactive Tier 2 credential flow. Do not perform a disposable tsnet
		// login here: it neither validates the administrative credential nor
		// contributes state to a service node.
		cfgDir, err := config.Dir()
		if err != nil {
			return err
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

type loginCredentialMode string

const (
	loginCredentialModeAPIKey       loginCredentialMode = "api-key"
	loginCredentialModeClientSecret loginCredentialMode = "client-secret"
)

type loginCredentialSnapshot struct {
	APIKey       string
	ClientSecret string
}

type loginCredentialStore interface {
	Read(loginCredentialMode) (string, error)
	Write(loginCredentialMode, string) (credentials.CredentialBackend, error)
	Delete(loginCredentialMode) error
}

type defaultLoginCredentialStore struct{}

func (defaultLoginCredentialStore) Read(mode loginCredentialMode) (string, error) {
	switch mode {
	case loginCredentialModeAPIKey:
		return loginGetAPIKeyFn()
	case loginCredentialModeClientSecret:
		return loginGetClientSecretFn()
	default:
		return "", fmt.Errorf("unsupported credential mode")
	}
}

func (defaultLoginCredentialStore) Write(mode loginCredentialMode, value string) (credentials.CredentialBackend, error) {
	switch mode {
	case loginCredentialModeAPIKey:
		return loginSetAPIKeyFn(value)
	case loginCredentialModeClientSecret:
		return loginSaveClientSecretFn(value)
	default:
		return "", fmt.Errorf("unsupported credential mode")
	}
}

func (defaultLoginCredentialStore) Delete(mode loginCredentialMode) error {
	switch mode {
	case loginCredentialModeAPIKey:
		return loginDeleteAPIKeyFn()
	case loginCredentialModeClientSecret:
		return loginDeleteClientSecretFn()
	default:
		return fmt.Errorf("unsupported credential mode")
	}
}

func readLoginCredentialSnapshot(store loginCredentialStore) (loginCredentialSnapshot, error) {
	apiKey, err := store.Read(loginCredentialModeAPIKey)
	if err != nil {
		return loginCredentialSnapshot{}, fmt.Errorf("read existing API credential: %w", err)
	}
	clientSecret, err := store.Read(loginCredentialModeClientSecret)
	if err != nil {
		return loginCredentialSnapshot{}, fmt.Errorf("read existing client-secret credential: %w", err)
	}
	return loginCredentialSnapshot{APIKey: apiKey, ClientSecret: clientSecret}, nil
}

func loginCredentialOtherMode(mode loginCredentialMode) loginCredentialMode {
	if mode == loginCredentialModeAPIKey {
		return loginCredentialModeClientSecret
	}
	return loginCredentialModeAPIKey
}

func loginCredentialModeLabel(mode loginCredentialMode) string {
	switch mode {
	case loginCredentialModeAPIKey:
		return "API key"
	case loginCredentialModeClientSecret:
		return "client secret"
	default:
		return "credential"
	}
}

func validateLoginCredentialCandidate(ctx context.Context, mode loginCredentialMode, value string) error {
	switch mode {
	case loginCredentialModeAPIKey:
		if !strings.HasPrefix(value, "tskey-api-") {
			return fmt.Errorf("API key must start with \"tskey-api-\" prefix")
		}
		if err := loginVerifyAPIKeyFn(ctx, value); err != nil {
			return err
		}
	case loginCredentialModeClientSecret:
		if !strings.HasPrefix(value, "tskey-client-") {
			return fmt.Errorf("client secret must start with \"tskey-client-\" prefix")
		}
		// Semantically prove the candidate secret with a disposable, ephemeral
		// Up BEFORE any persisted write. A prefix-only check let a well-formed
		// but invalid/revoked/wrong-scope secret be committed and delete a
		// working API key, dropping the install into an unauthenticated outage.
		// Activation runs before commit, so a failed candidate never retires the
		// last-known-good credential.
		if err := loginActivateClientSecretFn(ctx, value); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported credential mode")
	}
	return nil
}

// activateClientSecretViaUp is the production semantic-activation path for an
// OAuth client secret. It derives a tsnet auth key from the candidate secret
// (never touching persisted credentials), then completes a real, ephemeral,
// bounded Up on a disposable state directory. A successful Up proves the secret
// is usable; any failure (invalid, revoked, wrong scope, unreachable control
// plane) returns an error so the transaction keeps the previous credential
// active. The secret value is never written to logs or error text.
func activateClientSecretViaUp(ctx context.Context, secret string) error {
	tags := []string{config.GetDefaultTag()}
	authKey, err := credentials.ClientSecretAuthKey(secret, credentials.AuthKeyOptions{
		Tags:        tags,
		Ephemeral:   true,
		Description: "TSLink client-secret validation",
	})
	if err != nil {
		return fmt.Errorf("prepare client secret for validation: %w", err)
	}

	cfgDir, err := config.Dir()
	if err != nil {
		return err
	}
	tmpStateDir, err := os.MkdirTemp(cfgDir, "clientsecret-validate-")
	if err != nil {
		return fmt.Errorf("create validation state dir: %w", err)
	}
	defer loginRemoveAllFn(tmpStateDir)

	// Match the production serve node exactly (server.go newTSNetServerFn):
	// OAuth authkeys require the tags to be advertised on the node, otherwise
	// tsnet rejects the Up with "oauth authkeys require --advertise-tags".
	srv := loginNewValidationServerFn(tmpStateDir, authKey, tags)
	defer srv.Close()

	upCtx, cancel := context.WithTimeout(ctx, clientSecretActivationTimeout)
	defer cancel()
	if _, err := srv.Up(upCtx); err != nil {
		return fmt.Errorf("client secret failed activation; keeping previous credential: %w", err)
	}
	return nil
}

func newClientSecretValidationServer(tmpStateDir, authKey string, tags []string) *tsnet.Server {
	return &tsnet.Server{
		Hostname:      "tslink-auth",
		Dir:           tmpStateDir,
		Ephemeral:     true,
		AuthKey:       authKey,
		AdvertiseTags: append([]string(nil), tags...),
		UserLogf:      logging.TSNetUserLogf,
	}
}

// replaceLoginCredential implements stage -> validate -> commit semantics.
// Same-mode swaps verify the candidate before overwriting the old value.
// Cross-mode swaps keep the previous mode active until the candidate is
// validated and committed, then remove the alternate mode as part of commit.
func replaceLoginCredential(ctx context.Context, store loginCredentialStore, mode loginCredentialMode, value string) (credentials.CredentialBackend, error) {
	previous, err := readLoginCredentialSnapshot(store)
	if err != nil {
		return "", err
	}
	if err := validateLoginCredentialCandidate(ctx, mode, value); err != nil {
		return "", err
	}

	backend, err := store.Write(mode, value)
	if err != nil {
		return "", fmt.Errorf("commit %s credential: %w", loginCredentialModeLabel(mode), err)
	}
	if err := verifyLoginCredentialValue(store, mode, value); err != nil {
		return "", rollbackLoginCredential(store, previous, fmt.Errorf("verify committed %s credential: %w", loginCredentialModeLabel(mode), err))
	}

	other := loginCredentialOtherMode(mode)
	if err := store.Delete(other); err != nil {
		return "", rollbackLoginCredential(store, previous, fmt.Errorf("remove previous %s credential: %w", loginCredentialModeLabel(other), err))
	}
	if err := verifyLoginCredentialInactive(store, other); err != nil {
		return "", rollbackLoginCredential(store, previous, fmt.Errorf("verify previous %s credential inactive: %w", loginCredentialModeLabel(other), err))
	}
	if previous.APIKey == "" && previous.ClientSecret == "" {
		if err := loginMarkCredentialUpgradeFn(); err != nil {
			return "", rollbackLoginCredential(store, previous, fmt.Errorf("record Tier 1 to Tier 2 transition: %w", err))
		}
	}
	return backend, nil
}

func verifyLoginCredentialValue(store loginCredentialStore, mode loginCredentialMode, want string) error {
	got, err := store.Read(mode)
	if err != nil {
		return err
	}
	if got != want {
		return fmt.Errorf("read-back mismatch")
	}
	return nil
}

func verifyLoginCredentialInactive(store loginCredentialStore, mode loginCredentialMode) error {
	got, err := store.Read(mode)
	if err != nil {
		return err
	}
	if got != "" {
		return fmt.Errorf("credential still present")
	}
	return nil
}

func rollbackLoginCredential(store loginCredentialStore, previous loginCredentialSnapshot, cause error) error {
	if err := restoreLoginCredentialSnapshot(store, previous); err != nil {
		return fmt.Errorf("%w; rollback failed: %v", cause, err)
	}
	return cause
}

func restoreLoginCredentialSnapshot(store loginCredentialStore, snapshot loginCredentialSnapshot) error {
	if snapshot.APIKey != "" {
		if _, err := store.Write(loginCredentialModeAPIKey, snapshot.APIKey); err != nil {
			return fmt.Errorf("restore API credential: %w", err)
		}
	} else if err := store.Delete(loginCredentialModeAPIKey); err != nil {
		return fmt.Errorf("clear API credential: %w", err)
	}
	if snapshot.ClientSecret != "" {
		if _, err := store.Write(loginCredentialModeClientSecret, snapshot.ClientSecret); err != nil {
			return fmt.Errorf("restore client-secret credential: %w", err)
		}
	} else if err := store.Delete(loginCredentialModeClientSecret); err != nil {
		return fmt.Errorf("clear client-secret credential: %w", err)
	}

	current, err := readLoginCredentialSnapshot(store)
	if err != nil {
		return fmt.Errorf("verify restored credentials: %w", err)
	}
	if current.APIKey != snapshot.APIKey || current.ClientSecret != snapshot.ClientSecret {
		return fmt.Errorf("restored credential state mismatch")
	}
	return nil
}

func cleanupLoginLegacyState() error {
	var errs []error
	if authKeyPath, err := config.AuthKeyPath(); err == nil {
		if removeErr := os.Remove(authKeyPath); removeErr != nil && !os.IsNotExist(removeErr) {
			errs = append(errs, fmt.Errorf("remove legacy authkey: %w", removeErr))
		}
	} else {
		errs = append(errs, fmt.Errorf("resolve legacy authkey path: %w", err))
	}
	if cfgDir, err := config.Dir(); err == nil {
		if removeErr := os.RemoveAll(filepath.Join(cfgDir, "tsnet-state")); removeErr != nil {
			errs = append(errs, fmt.Errorf("remove legacy tsnet state: %w", removeErr))
		}
	} else {
		errs = append(errs, fmt.Errorf("resolve config dir: %w", err))
	}
	return errors.Join(errs...)
}

func loginManageACL(cmd *cobra.Command) bool {
	manage, _ := cmd.Flags().GetBool("manage-acl")
	return manage
}

func loginMaybeEnsureDefaultACLTag(cmd *cobra.Command, operation string) (tagCreated string, degraded bool, tagEnsureError string, aclMutationSkipped bool, plan *security.RemoteSideEffectPlan) {
	defaultTag := config.GetDefaultTag()
	resources := []string{defaultTag}
	if !loginManageACL(cmd) {
		sideEffectPlan := security.ACLMutationPlan(operation, resources, false)
		if !jsonOutput(cmd) {
			fmt.Fprintf(cmd.ErrOrStderr(), "→ Skipped remote ACL mutation by default; use --manage-acl to apply plan %s for %s\n", sideEffectPlan.ID, strings.Join(resources, ","))
		}
		return "", false, "", true, &sideEffectPlan
	}

	sideEffectPlan := security.ACLMutationPlan(operation, resources, true)
	if err := loginEnsureTagsFn(context.Background(), resources); err != nil {
		degraded = true
		tagEnsureError = err.Error()
		if errors.Is(err, tailapi.ErrNoAPIClient) {
			if !jsonOutput(cmd) {
				fmt.Fprintf(cmd.ErrOrStderr(), "→ Degraded login: skipped ACL tag management: %v. Services using tags may require manual ACL policy setup.\n", err)
			}
		} else if !jsonOutput(cmd) {
			fmt.Fprintf(cmd.ErrOrStderr(), "⚠ Degraded login: could not ensure default ACL tag: %v. Verify API token permissions before relying on --manage-acl.\n", err)
		}
		return "", degraded, tagEnsureError, false, &sideEffectPlan
	}
	return defaultTag, false, "", false, &sideEffectPlan
}

func loginWithAPIKey(cmd *cobra.Command, key string) error {
	backend, err := replaceLoginCredential(context.Background(), defaultLoginCredentialStore{}, loginCredentialModeAPIKey, key)
	if err != nil {
		return err
	}
	if err := loginCleanupLegacyStateFn(); err != nil {
		return fmt.Errorf("credential activated but cleanup failed: %w", err)
	}

	tagCreated, degraded, tagEnsureError, aclMutationSkipped, sideEffectPlan := loginMaybeEnsureDefaultACLTag(cmd, "ensure_default_tag")
	warnCredentialBackendDowngrade(cmd, "API key", backend)

	if jsonOutput(cmd) {
		output.Success("login", LoginResult{
			Method:               "api-key",
			CredentialBackend:    backend,
			TagCreated:           tagCreated,
			Degraded:             degraded,
			TagEnsureError:       tagEnsureError,
			ACLMutationSkipped:   aclMutationSkipped,
			RemoteSideEffectPlan: sideEffectPlan,
		})
	} else {
		printCredentialBackend("API key", backend)
		fmt.Println("→ Auth keys will be derived automatically on 'tslink serve'")
		if tagCreated != "" {
			fmt.Printf("→ Ensured %s exists in tailnet ACL\n", tagCreated)
		}
	}
	return nil
}

func loginWithClientSecret(cmd *cobra.Command, secret string) error {
	backend, err := replaceLoginCredential(context.Background(), defaultLoginCredentialStore{}, loginCredentialModeClientSecret, secret)
	if err != nil {
		return err
	}
	if err := loginCleanupLegacyStateFn(); err != nil {
		return fmt.Errorf("credential activated but cleanup failed: %w", err)
	}

	tagCreated, degraded, tagEnsureError, aclMutationSkipped, sideEffectPlan := loginMaybeEnsureDefaultACLTag(cmd, "ensure_default_tag")
	warnCredentialBackendDowngrade(cmd, "Client secret", backend)

	if jsonOutput(cmd) {
		output.Success("login", LoginResult{
			Method:               "client-secret",
			CredentialBackend:    backend,
			TagCreated:           tagCreated,
			Degraded:             degraded,
			TagEnsureError:       tagEnsureError,
			ACLMutationSkipped:   aclMutationSkipped,
			RemoteSideEffectPlan: sideEffectPlan,
		})
	} else {
		printCredentialBackend("Client secret", backend)
		fmt.Println("→ Long-lived node auth saved")
		fmt.Println("→ Remote ACL writes require --manage-acl; remote device cleanup is protected/manual")
		fmt.Println("→ Validate OAuth scopes and service tags before unattended use")
		if tagCreated != "" {
			fmt.Printf("→ Ensured %s exists in tailnet ACL\n", tagCreated)
		}
	}
	return nil
}

func printCredentialBackend(label string, backend credentials.CredentialBackend) {
	switch backend {
	case credentials.CredentialBackendKeyring:
		fmt.Printf("→ %s saved (system keychain)\n", label)
	case credentials.CredentialBackendFile:
		fmt.Printf("→ %s saved (restricted local file, 0600)\n", label)
	default:
		fmt.Printf("→ %s saved (credential backend: %s)\n", label, backend)
	}
}

func warnCredentialBackendDowngrade(cmd *cobra.Command, label string, backend credentials.CredentialBackend) {
	if backend == credentials.CredentialBackendFile {
		fmt.Fprintf(cmd.ErrOrStderr(), "⚠ Credential storage downgrade: system keyring unavailable; %s saved to a restricted local file (0600).\n", label)
	}
}

func loginCredentialFlow(cmd *cobra.Command, cfgDir string) error {
	reader := loginStdinReaderFn()

	fmt.Print("\n  Choose a credential type:\n\n")
	fmt.Print("    [1] API access token   — quick setup, API-backed auth keys, expires periodically\n")
	fmt.Print("    [2] OAuth client secret — long-lived node auth; remote writes require explicit --manage-acl\n\n")
	fmt.Print("  Enter 1 or 2: ")

	choiceStr, _ := reader.ReadString('\n')
	choiceStr = strings.TrimSpace(choiceStr)

	switch choiceStr {
	case "1":
		fmt.Print("\n  ─── API Access Token ───\n")
		fmt.Print("  Use this for API-backed TSLink automation: API verification,\n")
		fmt.Print("  auth-key derivation, and read-only remote evidence. ACL writes require --manage-acl.\n\n")
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
	loginCmd.Flags().Bool("manage-acl", false, "Opt in to remote Tailscale ACL tag-owner mutation using a machine-readable side-effect plan")
}
