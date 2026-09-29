package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/monody0007/tslink/internal/authmode"
	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/credentials"
	"github.com/monody0007/tslink/internal/logging"
	"github.com/monody0007/tslink/internal/output"
	"github.com/monody0007/tslink/internal/registry"
	"github.com/monody0007/tslink/internal/security"
	"github.com/monody0007/tslink/internal/tailapi"
	"github.com/spf13/cobra"
	"golang.org/x/term"
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
	CredentialKind       string                         `json:"credential_kind"`
	Fingerprint          string                         `json:"fingerprint"`
	StoredAt             *time.Time                     `json:"stored_at,omitempty"`
	ExpiresAt            *time.Time                     `json:"expires_at,omitempty"`
	ExpiresAtSource      string                         `json:"expires_at_source,omitempty"`
	Rotated              bool                           `json:"rotated"`
	PreviousFingerprint  string                         `json:"previous_fingerprint,omitempty"`
	RetiredCredential    string                         `json:"retired_credential,omitempty"`
	LoginName            string                         `json:"login_name,omitempty"`
	TagCreated           string                         `json:"tag_created,omitempty"`
	Degraded             bool                           `json:"degraded"`
	TagEnsureError       string                         `json:"tag_ensure_error,omitempty"`
	ACLMutationSkipped   bool                           `json:"acl_mutation_skipped"`
	RemoteSideEffectPlan *security.RemoteSideEffectPlan `json:"remote_side_effect_plan,omitempty"`
}

// LoginBootstrapResult is the JSON output of the standalone
// --open-keys-page / --open-oauth-page helpers. It never contains a credential.
type LoginBootstrapResult struct {
	Page   string   `json:"page"`
	URL    string   `json:"url"`
	Opened bool     `json:"opened"`
	Next   []string `json:"next"`
}

const (
	loginBootstrapPageKeys  = "keys"
	loginBootstrapPageOAuth = "oauth"
)

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
			// 401/403 become api_token_unauthorized / api_forbidden with
			// recovery steps; everything else stays a plain verification error.
			return credentials.ClassifyAPIError("API key verification failed", err)
		}
		return nil
	}
	loginSaveClientSecretFn    = credentials.SaveClientSecretWithBackend
	loginGetClientSecretFn     = credentials.GetClientSecret
	loginDeleteClientSecretFn  = credentials.DeleteClientSecretChecked
	loginReadSlotMetaFn        = credentials.ReadSlotMetadata
	loginWriteSlotMetaFn       = credentials.WriteSlotMetadataLocked  // only inside the login transaction
	loginDeleteSlotMetaFn      = credentials.DeleteSlotMetadataLocked // only inside the login transaction
	loginMutationTransactionFn = credentials.WithMutationTransaction
	loginNowFn                 = func() time.Time { return time.Now().UTC() }
	loginOpenBrowserFn         = openBrowser
	loginCIEnvironmentSetFn    = ciEnvironmentSet
	loginIsTerminalFn          = func() bool { return term.IsTerminal(int(os.Stdin.Fd())) }
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

Use this command only for the tagged, durable multi-service tier. TSLink keeps
two independent credential slots, and the recommended setup stores both:

  [1] API access token (tskey-api-*)          slot: api-key
      Generate at: ` + credentials.KeysPageURL + `
      This is a broad, user-owned tailnet-admin token that expires within 90
      days. It is the only credential Tailscale accepts for invites (an
      inviting user is required), and it can also derive auth keys and read
      remote evidence. Remote ACL tag mutation requires --manage-acl.
      → Click "Generate access token..."

  [2] OAuth client secret (tskey-client-*)    slot: client-secret
      Generate at: ` + credentials.OAuthPageURL + `
      → Click "+ credential" → choose "OAuth client"
      → Validate scopes and tags for your services
      → Copy the "client secret" (NOT the shorter client ID above it)
      Tailnet-owned and does not expire: the durable credential for daemon node
      authentication. Remote ACL writes require explicit --manage-acl; remote
      device cleanup requires durable exact TSLink NodeID ownership proof.

Logging in fills one slot and leaves the other untouched. Logging in again to
the same slot rotates it (the previous fingerprint is reported). Pass
--retire-other only when the other slot must be deleted as part of the commit.

Expiry tracking: the api-key slot records expires_at in value-free metadata
(~/.config/tslink/credential-meta.json). Give the real expiry with
--expires-in 90d or --expires-at <RFC3339>; without either, TSLink assumes
Tailscale's 90-day maximum and marks it expires_at_source=assumed_max. Status
and doctor report the slot as expiring ` + strconv.Itoa(int(credentials.ExpiringSoonThreshold.Hours()/24)) + ` days before expires_at and as
expired afterwards; renewing is another login to the same slot.

--open-keys-page / --open-oauth-page open the admin page only when this flag
is given explicitly, stdin is an interactive terminal, and CI is unset;
otherwise they print the URL and the three bootstrap steps and exit 0.

When upgrading services that already enrolled through Tier 1, restart the
running TSLink server after login. A successful zero-to-credential transition
is recorded atomically; the next credentialed start removes the old per-service
tsnet state before enrollment so the auth key creates the tagged Tier 2 nodes.

Credentials are stored in the system keychain (macOS Keychain, Linux secret
service, Windows Credential Manager). On systems without keychain support,
they fall back to files in ~/.config/tslink/ with restricted permissions (0600).
Metadata never contains the credential value, only a sha256 fingerprint.

	Non-interactive mode:
	  printf %s "$TSLINK_API_KEY" | tslink login --api-key-stdin --expires-in 90d
	  printf %s "$TSLINK_CLIENT_SECRET" | tslink login --client-secret-stdin
	  # TSLINK_API_KEY / TSLINK_CLIENT_SECRET may also be pre-injected by a
	  # secret manager before this process starts. Do not inline secret values in
	  # shell commands because they can land in shell history.
	  # The compatibility flags --api-key and --client-secret expose values in
	  # process lists; prefer the stdin variants above.

		Examples:
		  tslink login                  Interactive administrative credential prompt
		  tslink login --open-keys-page Open the access-token page, then paste the token

		  # Automation path with a secret manager:
		  op read op://vault/tslink/api-key | tslink login --api-key-stdin --expires-in 90d`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := config.EnsureDir(); err != nil {
			return err
		}

		if page, err := resolveLoginBootstrapPage(cmd); err != nil {
			return err
		} else if page != "" {
			return loginOpenBootstrapPage(cmd, page)
		}

		if _, _, err := resolveLoginExpiry(cmd); err != nil {
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
			return output.ErrUsage("--json requires --api-key or --client-secret (interactive login not available in JSON mode)")
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
		return "", output.ErrUsage(fmt.Sprintf("%s stdin was empty", name))
	}
	return value, nil
}

func loginExplicitCredentialSourceCount(cmd *cobra.Command) int {
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
	return explicit
}

func resolveLoginCredentials(cmd *cobra.Command) (apiKey, clientSecret string, err error) {
	apiKeyFlag, _ := cmd.Flags().GetString("api-key")
	clientSecretFlag, _ := cmd.Flags().GetString("client-secret")
	apiKeyStdin, _ := cmd.Flags().GetBool("api-key-stdin")
	clientSecretStdin, _ := cmd.Flags().GetBool("client-secret-stdin")

	if loginExplicitCredentialSourceCount(cmd) > 1 {
		return "", "", output.ErrUsage("provide only one explicit credential source")
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

// resolveLoginBootstrapPage returns which admin page the standalone
// --open-keys-page / --open-oauth-page helper should present, or "" when neither
// flag was given. The helper never reads a credential, so combining it with a
// credential source is a usage error rather than a silent no-op.
func resolveLoginBootstrapPage(cmd *cobra.Command) (string, error) {
	openKeys, _ := cmd.Flags().GetBool("open-keys-page")
	openOAuth, _ := cmd.Flags().GetBool("open-oauth-page")
	switch {
	case openKeys && openOAuth:
		return "", output.ErrUsage("--open-keys-page and --open-oauth-page are mutually exclusive")
	case !openKeys && !openOAuth:
		return "", nil
	}
	if loginExplicitCredentialSourceCount(cmd) > 0 {
		return "", output.ErrUsage("--open-keys-page/--open-oauth-page is a standalone bootstrap helper; run it without a credential source, then log in with the generated value")
	}
	if openKeys {
		return loginBootstrapPageKeys, nil
	}
	return loginBootstrapPageOAuth, nil
}

// loginBrowserAllowed mirrors the serve auth-handoff policy: a browser is
// opened only for an interactive terminal outside CI and outside --json.
func loginBrowserAllowed(cmd *cobra.Command) bool {
	return !jsonOutput(cmd) && !loginCIEnvironmentSetFn() && loginIsTerminalFn()
}

func loginOpenBootstrapPage(cmd *cobra.Command, page string) error {
	var pageURL string
	var next []string
	switch page {
	case loginBootstrapPageKeys:
		pageURL, next = credentials.KeysPageURL, credentials.NextAPIKeyBootstrap()
	case loginBootstrapPageOAuth:
		pageURL, next = credentials.OAuthPageURL, credentials.NextOAuthBootstrap()
	default:
		return fmt.Errorf("unknown bootstrap page %q", page)
	}

	opened := false
	if loginBrowserAllowed(cmd) {
		if err := loginOpenBrowserFn(pageURL); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "→ Could not open a browser automatically: %v\n", err)
		} else {
			opened = true
		}
	}
	if jsonOutput(cmd) {
		output.Success("login", LoginBootstrapResult{Page: page, URL: pageURL, Opened: opened, Next: next})
		return nil
	}
	out := cmd.OutOrStdout()
	if opened {
		fmt.Fprintf(out, "→ Opened browser: %s\n", pageURL)
	} else {
		fmt.Fprintf(out, "→ Open: %s\n", pageURL)
	}
	for i, step := range next {
		fmt.Fprintf(out, "  %d. %s\n", i+1, step)
	}
	return nil
}

// parseLoginExpiresIn accepts Go durations plus a day suffix (90d, 30d).
func parseLoginExpiresIn(raw string) (time.Duration, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, errors.New("empty duration")
	}
	if strings.HasSuffix(raw, "d") {
		days, err := strconv.Atoi(strings.TrimSuffix(raw, "d"))
		if err != nil {
			return 0, fmt.Errorf("invalid day count %q", raw)
		}
		if days <= 0 {
			return 0, fmt.Errorf("duration must be positive, got %q", raw)
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, err
	}
	if d <= 0 {
		return 0, fmt.Errorf("duration must be positive, got %q", raw)
	}
	return d, nil
}

// resolveLoginExpiry reads --expires-in / --expires-at. Both absent means the
// api-key slot falls back to the assumed 90-day maximum.
func resolveLoginExpiry(cmd *cobra.Command) (*time.Time, string, error) {
	expiresIn, _ := cmd.Flags().GetString("expires-in")
	expiresAt, _ := cmd.Flags().GetString("expires-at")
	switch {
	case expiresIn != "" && expiresAt != "":
		return nil, "", output.ErrUsage("--expires-in and --expires-at are mutually exclusive")
	case expiresIn != "":
		d, err := parseLoginExpiresIn(expiresIn)
		if err != nil {
			return nil, "", output.ErrUsage(fmt.Sprintf("invalid --expires-in %q: %v (use 90d, 30d, or a Go duration such as 720h)", expiresIn, err))
		}
		expiry := loginNowFn().Add(d)
		return &expiry, credentials.ExpirySourceUser, nil
	case expiresAt != "":
		parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(expiresAt))
		if err != nil {
			return nil, "", output.ErrUsage(fmt.Sprintf("invalid --expires-at %q: expected RFC3339 such as 2026-12-02T00:00:00Z", expiresAt))
		}
		if !parsed.After(loginNowFn()) {
			return nil, "", output.ErrUsage(fmt.Sprintf("--expires-at %q is not in the future", expiresAt))
		}
		expiry := parsed.UTC()
		return &expiry, credentials.ExpirySourceUser, nil
	}
	return nil, "", nil
}

type loginCredentialMode string

const (
	loginCredentialModeAPIKey       loginCredentialMode = credentials.SlotAPIKey
	loginCredentialModeClientSecret loginCredentialMode = credentials.SlotClientSecret
)

type loginCredentialSnapshot struct {
	APIKey           string
	ClientSecret     string
	APIKeyMeta       *credentials.SlotMetadata
	ClientSecretMeta *credentials.SlotMetadata
}

func (s loginCredentialSnapshot) value(mode loginCredentialMode) string {
	if mode == loginCredentialModeAPIKey {
		return s.APIKey
	}
	return s.ClientSecret
}

func (s loginCredentialSnapshot) meta(mode loginCredentialMode) *credentials.SlotMetadata {
	if mode == loginCredentialModeAPIKey {
		return s.APIKeyMeta
	}
	return s.ClientSecretMeta
}

// loginCredentialStore abstracts the two value stores and their value-free
// metadata so the commit transaction can be exercised in memory.
type loginCredentialStore interface {
	Read(loginCredentialMode) (string, error)
	Write(loginCredentialMode, string) (credentials.CredentialBackend, error)
	Delete(loginCredentialMode) error
	ReadMeta(loginCredentialMode) (*credentials.SlotMetadata, error)
	WriteMeta(loginCredentialMode, credentials.SlotMetadata) error
	DeleteMeta(loginCredentialMode) error
}

type defaultLoginCredentialStore struct {
	transaction *credentials.MutationTransaction
}

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

func (store defaultLoginCredentialStore) Write(mode loginCredentialMode, value string) (credentials.CredentialBackend, error) {
	switch mode {
	case loginCredentialModeAPIKey:
		if store.transaction != nil {
			return store.transaction.SetAPIKeyWithBackend(value)
		}
		return loginSetAPIKeyFn(value)
	case loginCredentialModeClientSecret:
		if store.transaction != nil {
			return store.transaction.SaveClientSecretWithBackend(value)
		}
		return loginSaveClientSecretFn(value)
	default:
		return "", fmt.Errorf("unsupported credential mode")
	}
}

func (store defaultLoginCredentialStore) Delete(mode loginCredentialMode) error {
	switch mode {
	case loginCredentialModeAPIKey:
		if store.transaction != nil {
			return store.transaction.DeleteAPIKeyChecked()
		}
		return loginDeleteAPIKeyFn()
	case loginCredentialModeClientSecret:
		if store.transaction != nil {
			return store.transaction.DeleteClientSecretChecked()
		}
		return loginDeleteClientSecretFn()
	default:
		return fmt.Errorf("unsupported credential mode")
	}
}

func (defaultLoginCredentialStore) ReadMeta(mode loginCredentialMode) (*credentials.SlotMetadata, error) {
	meta, err := loginReadSlotMetaFn(string(mode))
	if err != nil {
		// A corrupt metadata file must never block a login; the commit rewrites
		// the slot record and self-heals the file.
		if errors.Is(err, credentials.ErrMetadataCorrupt) {
			return nil, nil
		}
		return nil, err
	}
	return meta, nil
}

func (defaultLoginCredentialStore) WriteMeta(mode loginCredentialMode, meta credentials.SlotMetadata) error {
	return loginWriteSlotMetaFn(string(mode), meta)
}

func (defaultLoginCredentialStore) DeleteMeta(mode loginCredentialMode) error {
	return loginDeleteSlotMetaFn(string(mode))
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
	apiMeta, err := store.ReadMeta(loginCredentialModeAPIKey)
	if err != nil {
		return loginCredentialSnapshot{}, fmt.Errorf("read existing API credential metadata: %w", err)
	}
	clientMeta, err := store.ReadMeta(loginCredentialModeClientSecret)
	if err != nil {
		return loginCredentialSnapshot{}, fmt.Errorf("read existing client-secret credential metadata: %w", err)
	}
	return loginCredentialSnapshot{APIKey: apiKey, ClientSecret: clientSecret, APIKeyMeta: apiMeta, ClientSecretMeta: clientMeta}, nil
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

// loginVerifyFailure gives a non-coded verification failure the stable
// login_verify_failed code. Coded 401/403 failures pass through unchanged.
func loginVerifyFailure(mode loginCredentialMode, err error) error {
	if _, coded := registry.ErrorCode(err); coded {
		return err
	}
	var next []string
	if mode == loginCredentialModeAPIKey {
		next = append([]string{"Check that this host can reach api.tailscale.com, then retry"}, credentials.NextAPIKeyBootstrap()...)
	} else {
		next = append([]string{"Check that this host can reach the Tailscale control plane, then retry"}, credentials.NextOAuthBootstrap()...)
	}
	return &registry.StableCodeError{Code: registry.CodeLoginVerifyFailed, Next: next, Err: err}
}

func validateLoginCredentialCandidate(ctx context.Context, mode loginCredentialMode, value string) error {
	switch mode {
	case loginCredentialModeAPIKey:
		if !strings.HasPrefix(value, "tskey-api-") {
			return output.ErrUsage("API key must start with \"tskey-api-\" prefix")
		}
		if err := loginVerifyAPIKeyFn(ctx, value); err != nil {
			return loginVerifyFailure(mode, err)
		}
	case loginCredentialModeClientSecret:
		if !strings.HasPrefix(value, "tskey-client-") {
			return output.ErrUsage("client secret must start with \"tskey-client-\" prefix")
		}
		// Semantically prove the candidate secret with a disposable, ephemeral
		// Up BEFORE any persisted write. A prefix-only check let a well-formed
		// but invalid/revoked/wrong-scope secret be committed and delete a
		// working API key, dropping the install into an unauthenticated outage.
		// Activation runs before commit, so a failed candidate never retires the
		// last-known-good credential.
		if err := loginActivateClientSecretFn(ctx, value); err != nil {
			return loginVerifyFailure(mode, err)
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

// loginReplaceOptions parameterizes one credential commit.
type loginReplaceOptions struct {
	// RetireOther deletes the other slot after this one is verified and
	// committed. The default keeps both slots (dual-slot coexistence).
	RetireOther bool
	// ExpiresAt / ExpiresAtSource record the operator-supplied api-key expiry;
	// nil falls back to the assumed 90-day maximum.
	ExpiresAt       *time.Time
	ExpiresAtSource string
	Now             time.Time
}

// loginCommitResult is the value-free outcome of a successful commit.
type loginCommitResult struct {
	Backend             credentials.CredentialBackend
	Metadata            credentials.SlotMetadata
	Rotated             bool
	PreviousFingerprint string
	Retired             loginCredentialMode
}

// replaceLoginCredential is the default-option wrapper kept for callers that
// only need the backend: same-slot rotation, no retirement of the other slot.
func replaceLoginCredential(ctx context.Context, store loginCredentialStore, mode loginCredentialMode, value string) (credentials.CredentialBackend, error) {
	result, err := commitLoginCredential(ctx, store, mode, value, loginReplaceOptions{Now: loginNowFn()})
	if err != nil {
		return "", err
	}
	return result.Backend, nil
}

// commitLoginCredential implements stage -> validate -> commit semantics.
// Same-slot swaps verify the candidate before overwriting the old value.
// The other slot is left in place unless RetireOther is set, in which case it
// is removed only after the candidate is validated and committed. Value-free
// metadata is written after the value read-back succeeds, and every failure
// after the first write rolls both values and metadata back to the snapshot.
func commitLoginCredential(ctx context.Context, store loginCredentialStore, mode loginCredentialMode, value string, opts loginReplaceOptions) (loginCommitResult, error) {
	if liveStore, ok := store.(defaultLoginCredentialStore); ok && liveStore.transaction == nil {
		// Candidate validation may contact the tailnet. Keep that bounded work
		// outside the local credential lock, then take the lock before reading
		// the snapshot used for commit and rollback.
		if err := validateLoginCredentialCandidate(ctx, mode, value); err != nil {
			return loginCommitResult{}, err
		}
		var result loginCommitResult
		err := loginMutationTransactionFn(func(transaction *credentials.MutationTransaction) error {
			liveStore.transaction = transaction
			var commitErr error
			result, commitErr = commitLoginCredentialValidated(ctx, liveStore, mode, value, opts, true)
			return commitErr
		})
		return result, credentialLockConflict(err)
	}
	return commitLoginCredentialValidated(ctx, store, mode, value, opts, false)
}

func commitLoginCredentialValidated(ctx context.Context, store loginCredentialStore, mode loginCredentialMode, value string, opts loginReplaceOptions, candidateValidated bool) (loginCommitResult, error) {
	if opts.Now.IsZero() {
		opts.Now = loginNowFn()
	}
	previous, err := readLoginCredentialSnapshot(store)
	if err != nil {
		return loginCommitResult{}, err
	}
	if !candidateValidated {
		if err := validateLoginCredentialCandidate(ctx, mode, value); err != nil {
			return loginCommitResult{}, err
		}
	}

	backend, err := store.Write(mode, value)
	if err != nil {
		return loginCommitResult{}, fmt.Errorf("commit %s credential: %w", loginCredentialModeLabel(mode), err)
	}
	if err := verifyLoginCredentialValue(store, mode, value); err != nil {
		return loginCommitResult{}, rollbackLoginCredential(store, previous, fmt.Errorf("verify committed %s credential: %w", loginCredentialModeLabel(mode), err))
	}

	meta, err := credentials.NewSlotMetadata(string(mode), value, credentials.StoredOptions{
		Now:             opts.Now,
		ExpiresAt:       opts.ExpiresAt,
		ExpiresAtSource: opts.ExpiresAtSource,
		Verified:        true,
	})
	if err != nil {
		return loginCommitResult{}, rollbackLoginCredential(store, previous, fmt.Errorf("describe committed %s credential: %w", loginCredentialModeLabel(mode), err))
	}
	if err := store.WriteMeta(mode, meta); err != nil {
		return loginCommitResult{}, rollbackLoginCredential(store, previous, fmt.Errorf("record %s credential metadata: %w", loginCredentialModeLabel(mode), err))
	}

	result := loginCommitResult{Backend: backend, Metadata: meta}
	if prev := previous.value(mode); prev != "" && prev != value {
		result.Rotated = true
		if prevMeta := previous.meta(mode); prevMeta != nil && prevMeta.Fingerprint != "" {
			result.PreviousFingerprint = prevMeta.Fingerprint
		} else {
			result.PreviousFingerprint = credentials.Fingerprint(prev)
		}
	}

	if opts.RetireOther {
		other := loginCredentialOtherMode(mode)
		if previous.value(other) != "" || previous.meta(other) != nil {
			if err := store.Delete(other); err != nil {
				return loginCommitResult{}, rollbackLoginCredential(store, previous, fmt.Errorf("remove previous %s credential: %w", loginCredentialModeLabel(other), err))
			}
			if err := verifyLoginCredentialInactive(store, other); err != nil {
				return loginCommitResult{}, rollbackLoginCredential(store, previous, fmt.Errorf("verify previous %s credential inactive: %w", loginCredentialModeLabel(other), err))
			}
			if err := store.DeleteMeta(other); err != nil {
				return loginCommitResult{}, rollbackLoginCredential(store, previous, fmt.Errorf("remove previous %s credential metadata: %w", loginCredentialModeLabel(other), err))
			}
			result.Retired = other
		}
	}
	if previous.APIKey == "" && previous.ClientSecret == "" {
		if err := loginMarkCredentialUpgradeFn(); err != nil {
			return loginCommitResult{}, rollbackLoginCredential(store, previous, fmt.Errorf("record Tier 1 to Tier 2 transition: %w", err))
		}
	}
	return result, nil
}

// credentialLockConflict gives credential-lock contention the CLI's retryable
// conflict exit code while keeping its stable code, message and next steps.
func credentialLockConflict(err error) error {
	var coded *registry.StableCodeError
	if !errors.Is(err, credentials.ErrMutationLockBusy) || !errors.As(err, &coded) {
		return err
	}
	return &registry.StableCodeError{Code: coded.Code, Next: coded.NextCommands(), Err: &output.CodeError{Code: output.ExitConflict, Message: err.Error()}}
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
	for _, mode := range []loginCredentialMode{loginCredentialModeAPIKey, loginCredentialModeClientSecret} {
		if meta := snapshot.meta(mode); meta != nil {
			if err := store.WriteMeta(mode, *meta); err != nil {
				return fmt.Errorf("restore %s credential metadata: %w", loginCredentialModeLabel(mode), err)
			}
		} else if err := store.DeleteMeta(mode); err != nil {
			return fmt.Errorf("clear %s credential metadata: %w", loginCredentialModeLabel(mode), err)
		}
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

func loginRetireOther(cmd *cobra.Command) bool {
	retire, _ := cmd.Flags().GetBool("retire-other")
	return retire
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

func loginReplaceOptionsFromFlags(cmd *cobra.Command, mode loginCredentialMode) (loginReplaceOptions, error) {
	opts := loginReplaceOptions{RetireOther: loginRetireOther(cmd), Now: loginNowFn()}
	expiresAt, source, err := resolveLoginExpiry(cmd)
	if err != nil {
		return loginReplaceOptions{}, err
	}
	if expiresAt != nil {
		if mode != loginCredentialModeAPIKey {
			return loginReplaceOptions{}, output.ErrUsage("--expires-in/--expires-at apply only to API access tokens; OAuth client secrets do not expire")
		}
		opts.ExpiresAt = expiresAt
		opts.ExpiresAtSource = source
	}
	return opts, nil
}

func loginResultFromCommit(method string, result loginCommitResult) LoginResult {
	storedAt := result.Metadata.StoredAt
	return LoginResult{
		Method:              method,
		CredentialBackend:   result.Backend,
		CredentialKind:      result.Metadata.Kind,
		Fingerprint:         result.Metadata.Fingerprint,
		StoredAt:            &storedAt,
		ExpiresAt:           cloneTimePointer(result.Metadata.ExpiresAt),
		ExpiresAtSource:     result.Metadata.ExpiresAtSource,
		Rotated:             result.Rotated,
		PreviousFingerprint: result.PreviousFingerprint,
		RetiredCredential:   string(result.Retired),
	}
}

func loginWithAPIKey(cmd *cobra.Command, key string) error {
	opts, err := loginReplaceOptionsFromFlags(cmd, loginCredentialModeAPIKey)
	if err != nil {
		return err
	}
	commit, err := commitLoginCredential(context.Background(), defaultLoginCredentialStore{}, loginCredentialModeAPIKey, key, opts)
	if err != nil {
		return err
	}
	if err := loginCleanupLegacyStateFn(); err != nil {
		return fmt.Errorf("credential activated but cleanup failed: %w", err)
	}

	tagCreated, degraded, tagEnsureError, aclMutationSkipped, sideEffectPlan := loginMaybeEnsureDefaultACLTag(cmd, "ensure_default_tag")
	warnCredentialBackendDowngrade(cmd, "API key", commit.Backend)

	if jsonOutput(cmd) {
		result := loginResultFromCommit("api-key", commit)
		result.TagCreated = tagCreated
		result.Degraded = degraded
		result.TagEnsureError = tagEnsureError
		result.ACLMutationSkipped = aclMutationSkipped
		result.RemoteSideEffectPlan = sideEffectPlan
		output.Success("login", result)
	} else {
		printCredentialCommit("API key", commit)
		fmt.Println("→ Auth keys will be derived automatically on 'tslink serve'")
		if tagCreated != "" {
			fmt.Printf("→ Ensured %s exists in tailnet ACL\n", tagCreated)
		}
	}
	return nil
}

func loginWithClientSecret(cmd *cobra.Command, secret string) error {
	opts, err := loginReplaceOptionsFromFlags(cmd, loginCredentialModeClientSecret)
	if err != nil {
		return err
	}
	commit, err := commitLoginCredential(context.Background(), defaultLoginCredentialStore{}, loginCredentialModeClientSecret, secret, opts)
	if err != nil {
		return err
	}
	if err := loginCleanupLegacyStateFn(); err != nil {
		return fmt.Errorf("credential activated but cleanup failed: %w", err)
	}

	tagCreated, degraded, tagEnsureError, aclMutationSkipped, sideEffectPlan := loginMaybeEnsureDefaultACLTag(cmd, "ensure_default_tag")
	warnCredentialBackendDowngrade(cmd, "Client secret", commit.Backend)

	if jsonOutput(cmd) {
		result := loginResultFromCommit("client-secret", commit)
		result.TagCreated = tagCreated
		result.Degraded = degraded
		result.TagEnsureError = tagEnsureError
		result.ACLMutationSkipped = aclMutationSkipped
		result.RemoteSideEffectPlan = sideEffectPlan
		output.Success("login", result)
	} else {
		printCredentialCommit("Client secret", commit)
		fmt.Println("→ Long-lived node auth saved")
		fmt.Println("→ Remote ACL writes require --manage-acl; remote device cleanup requires exact recorded NodeID ownership")
		fmt.Println("→ Validate OAuth scopes and service tags before unattended use")
		if tagCreated != "" {
			fmt.Printf("→ Ensured %s exists in tailnet ACL\n", tagCreated)
		}
	}
	return nil
}

func credentialBackendLabel(backend credentials.CredentialBackend) string {
	switch backend {
	case credentials.CredentialBackendKeyring:
		return "system keychain"
	case credentials.CredentialBackendFile:
		return "restricted local file, 0600"
	default:
		return "credential backend: " + string(backend)
	}
}

func expirySourceLabel(source string) string {
	switch source {
	case credentials.ExpirySourceAssumedMax:
		return "assumed max"
	case credentials.ExpirySourceUser:
		return "set at login"
	default:
		return source
	}
}

// loginCommitSummary renders the value-free facts of a commit: backend,
// expiry (with its provenance), rotation, and any retired slot.
func loginCommitSummary(label string, result loginCommitResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "→ %s saved (%s)", label, credentialBackendLabel(result.Backend))
	if result.Metadata.ExpiresAt != nil {
		fmt.Fprintf(&b, ", expires %s (%s)", result.Metadata.ExpiresAt.UTC().Format("2006-01-02"), expirySourceLabel(result.Metadata.ExpiresAtSource))
	} else if result.Metadata.Kind == credentials.KindOAuthClientSecret {
		b.WriteString(", does not expire")
	}
	if result.Rotated {
		fmt.Fprintf(&b, "; rotated from %s", result.PreviousFingerprint)
	}
	if result.Metadata.Fingerprint != "" {
		fmt.Fprintf(&b, "; fingerprint %s", result.Metadata.Fingerprint)
	}
	return b.String()
}

func printCredentialCommit(label string, result loginCommitResult) {
	fmt.Println(loginCommitSummary(label, result))
	if result.Retired != "" {
		fmt.Printf("→ retired: %s\n", result.Retired)
	}
}

func printCredentialBackend(label string, backend credentials.CredentialBackend) {
	fmt.Printf("→ %s saved (%s)\n", label, credentialBackendLabel(backend))
}

func warnCredentialBackendDowngrade(cmd *cobra.Command, label string, backend credentials.CredentialBackend) {
	if backend == credentials.CredentialBackendFile {
		fmt.Fprintf(cmd.ErrOrStderr(), "⚠ Credential storage downgrade: system keyring unavailable; %s saved to a restricted local file (0600).\n", label)
	}
}

func loginCredentialFlow(cmd *cobra.Command, cfgDir string) error {
	reader := loginStdinReaderFn()

	fmt.Print("\n  Choose a credential type:\n\n")
	fmt.Print("    [1] API access token   — user-owned, expires within 90 days; required for invites, derives auth keys\n")
	fmt.Print("    [2] OAuth client secret — tailnet-owned, does not expire; durable daemon node auth (remote writes require --manage-acl)\n")
	fmt.Print("    Both slots can coexist; logging in fills one and keeps the other.\n\n")
	fmt.Print("  Enter 1 or 2: ")

	choiceStr, _ := reader.ReadString('\n')
	choiceStr = strings.TrimSpace(choiceStr)

	switch choiceStr {
	case "1":
		fmt.Print("\n  ─── API Access Token ───\n")
		fmt.Print("  Use this for invites and API-backed TSLink automation: API verification,\n")
		fmt.Print("  auth-key derivation, and read-only remote evidence. ACL writes require --manage-acl.\n\n")
		fmt.Printf("  1. Open: %s   (or: tslink login --open-keys-page)\n", credentials.KeysPageURL)
		fmt.Print("  2. Click \"Generate access token...\"\n")
		fmt.Print("  3. Copy the token (starts with tskey-api-...)\n\n")
		fmt.Print("  Paste token: ")

		inputKey, _ := reader.ReadString('\n')
		inputKey = strings.TrimSpace(inputKey)
		if inputKey == "" {
			return output.ErrUsage("no token provided")
		}

		fmt.Println("→ Verifying API key...")
		return loginWithAPIKey(cmd, inputKey)

	case "2":
		fmt.Print("\n  ─── OAuth Client Secret ───\n")
		fmt.Printf("  1. Open: %s   (or: tslink login --open-oauth-page)\n", credentials.OAuthPageURL)
		fmt.Print("  2. Click \"+ credential\" → choose \"OAuth client\"\n")
		fmt.Print("  3. Validate OAuth scopes and tags for each service before unattended use\n")
		fmt.Print("  4. Click \"Create\" — you will see two values:\n")
		fmt.Print("       Client ID:     kABC123...  (short, NOT this one)\n")
		fmt.Print("       Client secret: tskey-client-...  ← copy THIS one\n")
		fmt.Print("     ⚠ The secret is shown only once.\n\n")
		fmt.Print("  Paste client secret: ")

		inputKey, _ := reader.ReadString('\n')
		inputKey = strings.TrimSpace(inputKey)
		if inputKey == "" {
			return output.ErrUsage("no secret provided")
		}

		fmt.Println("→ Saving client secret...")
		return loginWithClientSecret(cmd, inputKey)

	default:
		return output.ErrUsage(fmt.Sprintf("invalid choice: %q — enter 1 or 2", choiceStr))
	}
}

func init() {
	rootCmd.AddCommand(loginCmd)
	loginCmd.Flags().String("api-key", "", "API access token (tskey-api-*) for non-interactive login; visible in process lists, prefer env or --api-key-stdin")
	loginCmd.Flags().String("client-secret", "", "OAuth client secret (tskey-client-*) for non-interactive login; visible in process lists, prefer env or --client-secret-stdin")
	loginCmd.Flags().Bool("api-key-stdin", false, "Read API access token from stdin")
	loginCmd.Flags().Bool("client-secret-stdin", false, "Read OAuth client secret from stdin")
	loginCmd.Flags().Bool("manage-acl", false, "Opt in to remote Tailscale ACL tag-owner mutation using a machine-readable side-effect plan")
	loginCmd.Flags().Bool("retire-other", false, "Delete the other credential slot after this one is verified and committed; by default the api-key and client-secret slots coexist")
	loginCmd.Flags().String("expires-in", "", "Record the API access token expiry as a duration from now (90d, 30d, or a Go duration); api-key only. Without --expires-in/--expires-at TSLink assumes the 90-day maximum and records expires_at_source=assumed_max")
	loginCmd.Flags().String("expires-at", "", "Record the API access token expiry as an RFC3339 timestamp; api-key only and mutually exclusive with --expires-in")
	loginCmd.Flags().Bool("open-keys-page", false, "Standalone helper: open "+credentials.KeysPageURL+" in a browser only when stdin is an interactive terminal and CI is unset; otherwise print the URL and bootstrap steps. Exits 0 without storing a credential")
	loginCmd.Flags().Bool("open-oauth-page", false, "Standalone helper: open "+credentials.OAuthPageURL+" in a browser only when stdin is an interactive terminal and CI is unset; otherwise print the URL and bootstrap steps. Exits 0 without storing a credential")
}
