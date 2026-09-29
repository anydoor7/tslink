package credentials

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"runtime"
	"strconv"
	"strings"

	"github.com/monody0007/tslink/internal/atomicfile"
	"github.com/monody0007/tslink/internal/config"
	"github.com/zalando/go-keyring"
	tailscale "tailscale.com/client/tailscale/v2"
)

const (
	keychainService      = "tslink"
	keychainAPIKey       = "api-key"
	keychainClientSecret = "client-secret"

	derivedAuthKeyExpirySeconds = 10 * 60
)

// ErrUserOwnedAPIKeyRequired means an operation cannot use a tailnet-owned
// OAuth client and needs an API access token tied to an inviting user.
var ErrUserOwnedAPIKeyRequired = errors.New("user-owned Tailscale API access token required")

// Testable seams.
var (
	newTailscaleClientFunc = NewTailscaleClient
	createKeyFunc          = func(client *tailscale.Client, ctx context.Context, req tailscale.CreateKeyRequest) (*tailscale.Key, error) {
		return client.Keys().CreateAuthKey(ctx, req)
	}
	authKeyPathFunc                      = config.AuthKeyPath
	enforceCredentialFilePermissionsFunc = func() bool { return runtime.GOOS != "windows" }
	fileCredentialFallbackEnabledFunc    = func() bool { return runtime.GOOS != "windows" }
	keyringEnabledFunc                   = func() bool { return os.Getenv("TSLINK_DISABLE_KEYRING") != "1" }
	keyringGetFunc                       = keyring.Get
	keyringSetFunc                       = keyring.Set
	keyringDeleteFunc                    = keyring.Delete
	apiKeyPathFunc                       = config.APIKeyPath
	clientSecretPathFunc                 = config.ClientSecretPath
	credentialFileWriteFunc              = atomicfile.WriteFile
)

// CredentialBackend identifies the storage location that accepted a
// credential. It contains no credential material and is safe to report.
type CredentialBackend string

const (
	CredentialBackendKeyring CredentialBackend = "keyring"
	CredentialBackendFile    CredentialBackend = "file"
)

// CredentialLocationStatus is a value-free presence report for one credential
// storage location. It deliberately never carries credential material.
type CredentialLocationStatus struct {
	Enabled bool
	Present bool
}

// CredentialKindStatus is a value-free presence report for all storage
// locations of one credential kind.
type CredentialKindStatus struct {
	Keyring CredentialLocationStatus
	File    CredentialLocationStatus
}

func (s CredentialKindStatus) Present() bool {
	return s.Keyring.Present || s.File.Present
}

// StoredCredentialStatus is the strict credential inventory used by destructive
// cleanup paths. It distinguishes not-found from unreadable/disabled stores.
type StoredCredentialStatus struct {
	APIKey       CredentialKindStatus
	ClientSecret CredentialKindStatus
}

func (s StoredCredentialStatus) AnyPresent() bool {
	return s.APIKey.Present() || s.ClientSecret.Present()
}

func readCredentialFile(path string) ([]byte, error) {
	if enforceCredentialFilePermissionsFunc() {
		info, err := os.Lstat(path)
		if err != nil {
			return nil, err
		}
		oldMode := info.Mode().Perm()
		if err := atomicfile.ConvergePrivateFile(path); err != nil {
			return nil, err
		}
		if oldMode != atomicfile.PrivateFileMode {
			slog.Warn("repaired insecure credential file permissions", "path", path, "old_mode", oldMode, "new_mode", atomicfile.PrivateFileMode)
		}
	}
	return os.ReadFile(path)
}

// InspectStoredCredentialsStrict reports whether API-key/client-secret
// credentials are present in every supported store. It fails closed when the
// keyring is disabled or unreadable because an old credential may remain there.
func InspectStoredCredentialsStrict() (StoredCredentialStatus, error) {
	api, apiErr := inspectCredentialStrict("API key", keychainAPIKey, apiKeyPathFunc)
	secret, secretErr := inspectCredentialStrict("OAuth client secret", keychainClientSecret, clientSecretPathFunc)
	return StoredCredentialStatus{APIKey: api, ClientSecret: secret}, errors.Join(apiErr, secretErr)
}

func inspectCredentialStrict(label, keychainKey string, pathFunc func() (string, error)) (CredentialKindStatus, error) {
	var status CredentialKindStatus
	var errs []error
	if keyringEnabledFunc() {
		status.Keyring.Enabled = true
		value, err := keyringGetFunc(keychainService, keychainKey)
		switch {
		case err == nil:
			status.Keyring.Present = strings.TrimSpace(value) != ""
		case errors.Is(err, keyring.ErrNotFound):
		default:
			errs = append(errs, fmt.Errorf("%s keyring unreadable: %w", label, err))
		}
	} else {
		errs = append(errs, fmt.Errorf("%s keyring disabled; cannot prove no residual keyring credential remains", label))
	}

	status.File.Enabled = true
	path, err := pathFunc()
	if err != nil {
		errs = append(errs, fmt.Errorf("%s file path: %w", label, err))
		return status, errors.Join(errs...)
	}
	b, err := readCredentialFile(path)
	switch {
	case err == nil:
		status.File.Present = strings.TrimSpace(string(b)) != ""
	case os.IsNotExist(err):
	default:
		errs = append(errs, fmt.Errorf("%s file unreadable: %w", label, err))
	}
	return status, errors.Join(errs...)
}

// DeleteStoredCredentialsStrict removes API-key/client-secret credentials from
// every supported store and reads back each store. It keeps going after errors
// and returns their join so callers never report success while residual
// credential risk remains.
func DeleteStoredCredentialsStrict() error {
	unlock, err := acquireCredentialMutationLock()
	if err != nil {
		return err
	}
	defer unlock()
	return DeleteStoredCredentialsStrictLocked()
}

// DeleteStoredCredentialsStrictLocked is DeleteStoredCredentialsStrict for a
// caller that already holds the credential mutation lock, such as logout
// removing values and metadata in one transaction.
func DeleteStoredCredentialsStrictLocked() error {
	return errors.Join(
		deleteCredentialStrict("API key", keychainAPIKey, apiKeyPathFunc),
		deleteCredentialStrict("OAuth client secret", keychainClientSecret, clientSecretPathFunc),
	)
}

func deleteCredentialStrict(label, keychainKey string, pathFunc func() (string, error)) error {
	var errs []error
	if err := deleteKeyringCredentialStrict(label, keychainKey); err != nil {
		errs = append(errs, err)
	}

	path, err := pathFunc()
	if err != nil {
		errs = append(errs, fmt.Errorf("%s file path: %w", label, err))
		return errors.Join(errs...)
	}
	if err := deleteCredentialFilePathStrict(label, path); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func deleteKeyringCredentialStrict(label, keychainKey string) error {
	var errs []error
	if keyringEnabledFunc() {
		if err := keyringDeleteFunc(keychainService, keychainKey); err != nil && !errors.Is(err, keyring.ErrNotFound) {
			errs = append(errs, fmt.Errorf("%s keyring delete: %w", label, err))
		}
		value, err := keyringGetFunc(keychainService, keychainKey)
		switch {
		case err == nil && strings.TrimSpace(value) != "":
			errs = append(errs, fmt.Errorf("%s keyring credential still present after cleanup", label))
		case err == nil:
		case errors.Is(err, keyring.ErrNotFound):
		default:
			errs = append(errs, fmt.Errorf("%s keyring readback: %w", label, err))
		}
	} else {
		errs = append(errs, fmt.Errorf("%s keyring disabled; cannot delete or prove absence of residual keyring credential", label))
	}
	return errors.Join(errs...)
}

func deleteCredentialFilePathStrict(label, path string) error {
	var errs []error
	if removeErr := os.Remove(path); removeErr != nil && !os.IsNotExist(removeErr) {
		errs = append(errs, fmt.Errorf("%s file delete: %w", label, removeErr))
	}
	if _, statErr := os.Stat(path); statErr == nil {
		errs = append(errs, fmt.Errorf("%s file credential still present after cleanup", label))
	} else if !os.IsNotExist(statErr) {
		errs = append(errs, fmt.Errorf("%s file readback: %w", label, statErr))
	}
	return errors.Join(errs...)
}

func storeCredentialWithBackend(
	label, keychainKey, value string,
	pathFunc func() (string, error),
) (CredentialBackend, error) {
	unlock, err := acquireCredentialMutationLock()
	if err != nil {
		return "", err
	}
	defer unlock()
	return storeCredentialWithBackendLocked(label, keychainKey, value, pathFunc)
}

func storeCredentialWithBackendLocked(
	label, keychainKey, value string,
	pathFunc func() (string, error),
) (CredentialBackend, error) {
	if keyringEnabledFunc() {
		if err := keyringSetFunc(keychainService, keychainKey, value); err == nil {
			// Keyring succeeded, so the file copy must no longer be authoritative.
			if path, pathErr := pathFunc(); pathErr == nil {
				_ = os.Remove(path)
			}
			return CredentialBackendKeyring, nil
		}
	}

	// Complete every fall-back precondition before changing either store.
	if !fileCredentialFallbackEnabledFunc() {
		return "", fmt.Errorf("file credential fallback is disabled on Windows because TSLink cannot prove a user-only DACL locally; enable Windows Credential Manager/keyring access and retry")
	}
	path, err := pathFunc()
	if err != nil {
		return "", err
	}
	if err := credentialFileWriteFunc(path, []byte(value)); err != nil {
		return "", err
	}

	// Keyring-first readers can use the file only after stale keyring state is
	// both deleted and proven absent. If cleanup fails, remove the new file so
	// the last-known-good keyring credential remains the sole authority.
	if keyringEnabledFunc() {
		if err := deleteKeyringCredentialStrict(label, keychainKey); err != nil {
			rollbackErr := deleteCredentialFilePathStrict(label, path)
			return "", errors.Join(
				fmt.Errorf("%s keyring write failed and stale keyring cleanup failed; refusing file fallback: %w", label, err),
				rollbackErr,
			)
		}
	}
	return CredentialBackendFile, nil
}

// SetAPIKey stores the API key. It is the source-compatible wrapper for callers
// that do not need to report the selected backend.
func SetAPIKey(key string) error {
	_, err := SetAPIKeyWithBackend(key)
	return err
}

// SetAPIKeyWithBackend stores the API key and reports the backend that actually
// accepted it. A failed keyring write may fall back to a private file only after
// every file precondition is satisfied. It reports file success only after any
// stale keyring value has been removed and readback proves it absent.
func SetAPIKeyWithBackend(key string) (CredentialBackend, error) {
	return storeCredentialWithBackend("API key", keychainAPIKey, key, apiKeyPathFunc)
}

// GetAPIKey retrieves the API key from keychain or file.
// Returns ("", nil) if no key is stored.
func GetAPIKey() (string, error) {
	// Keychain first
	if keyringEnabledFunc() {
		if key, err := keyringGetFunc(keychainService, keychainAPIKey); err == nil && key != "" {
			return key, nil
		}
	}
	// File fallback
	path, err := apiKeyPathFunc()
	if err != nil {
		return "", err
	}
	b, err := readCredentialFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	key := strings.TrimSpace(string(b))
	return key, nil
}

// DeleteAPIKey removes the API key from all storage locations.
func DeleteAPIKey() {
	_ = DeleteAPIKeyChecked()
}

// DeleteAPIKeyChecked removes the API key from all storage locations and
// surfaces cleanup failures for callers that need transactional semantics.
func DeleteAPIKeyChecked() error {
	unlock, err := acquireCredentialMutationLock()
	if err != nil {
		return err
	}
	defer unlock()
	return deleteAPIKeyCheckedLocked()
}

func deleteAPIKeyCheckedLocked() error {
	var errs []error
	if keyringEnabledFunc() {
		if err := keyringDeleteFunc(keychainService, keychainAPIKey); err != nil && !errors.Is(err, keyring.ErrNotFound) {
			errs = append(errs, err)
		}
	}
	if path, err := apiKeyPathFunc(); err == nil {
		if removeErr := os.Remove(path); removeErr != nil && !os.IsNotExist(removeErr) {
			errs = append(errs, removeErr)
		}
	} else {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// SaveClientSecret stores an OAuth client secret. It is the source-compatible
// wrapper for callers that do not need to report the selected backend.
func SaveClientSecret(secret string) error {
	_, err := SaveClientSecretWithBackend(secret)
	return err
}

// SaveClientSecretWithBackend stores an OAuth client secret and reports the
// backend that actually accepted it. The key must start with "tskey-client-".
// File fallback follows the same write-new, strictly-delete-old transaction as
// API keys.
func SaveClientSecretWithBackend(secret string) (CredentialBackend, error) {
	if !strings.HasPrefix(secret, "tskey-client-") {
		return "", fmt.Errorf("invalid client secret: must start with 'tskey-client-'")
	}
	return storeCredentialWithBackend("OAuth client secret", keychainClientSecret, secret, clientSecretPathFunc)
}

// GetClientSecret retrieves the client secret from keychain or file.
// Returns ("", nil) if no client secret is stored.
func GetClientSecret() (string, error) {
	// Keychain first
	if keyringEnabledFunc() {
		if secret, err := keyringGetFunc(keychainService, keychainClientSecret); err == nil && secret != "" {
			return secret, nil
		}
	}
	// File fallback
	path, err := clientSecretPathFunc()
	if err != nil {
		return "", err
	}
	b, err := readCredentialFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	secret := strings.TrimSpace(string(b))
	return secret, nil
}

// HasClientSecret returns true if a client secret is stored.
func HasClientSecret() bool {
	secret, err := GetClientSecret()
	return err == nil && secret != ""
}

// DeleteClientSecret removes the client secret from all storage locations.
func DeleteClientSecret() {
	_ = DeleteClientSecretChecked()
}

// DeleteClientSecretChecked removes the OAuth client secret from all storage
// locations and surfaces cleanup failures for transactional credential swaps.
func DeleteClientSecretChecked() error {
	unlock, err := acquireCredentialMutationLock()
	if err != nil {
		return err
	}
	defer unlock()
	return deleteClientSecretCheckedLocked()
}

func deleteClientSecretCheckedLocked() error {
	var errs []error
	if keyringEnabledFunc() {
		if err := keyringDeleteFunc(keychainService, keychainClientSecret); err != nil && !errors.Is(err, keyring.ErrNotFound) {
			errs = append(errs, err)
		}
	}
	if path, err := clientSecretPathFunc(); err == nil {
		if removeErr := os.Remove(path); removeErr != nil && !os.IsNotExist(removeErr) {
			errs = append(errs, removeErr)
		}
	} else {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// NewTailscaleClient creates a Tailscale API client from stored credentials.
// OAuth client secrets take precedence over API keys, matching GetAuthKey.
// Returns (nil, nil) if no API credential is configured.
func NewTailscaleClient() (*tailscale.Client, error) {
	secret, err := GetClientSecret()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(secret) != "" {
		return newTailscaleClientWithOAuthSecret(secret)
	}

	key, err := GetAPIKey()
	if err != nil {
		return nil, err
	}
	return NewTailscaleClientWithAPIKey(key)
}

// NewTailscaleClientWithUserOwnedAPIKey creates a Tailscale API client from
// the stored user-owned API access token only. It deliberately does not fall
// back to an OAuth client secret: invite mutations require an inviting user,
// while OAuth clients are tailnet-owned and have no user identity.
func NewTailscaleClientWithUserOwnedAPIKey() (*tailscale.Client, error) {
	key, err := GetAPIKey()
	if err != nil {
		return nil, fmt.Errorf("read user-owned Tailscale API access token: %w", err)
	}
	key = strings.TrimSpace(key)
	if key != "" {
		if !strings.HasPrefix(key, "tskey-api-") {
			return nil, fmt.Errorf("%w; the stored API credential is not a tskey-api- token; generate one at %s and pipe it to `tslink login --api-key-stdin`", ErrUserOwnedAPIKeyRequired, KeysPageURL)
		}
		return NewTailscaleClientWithAPIKey(key)
	}

	secret, err := GetClientSecret()
	if err != nil {
		return nil, fmt.Errorf("check stored OAuth client secret after no API access token was found: %w", err)
	}
	if strings.TrimSpace(secret) != "" {
		return nil, fmt.Errorf("%w; only an OAuth client secret is configured, but invites require a user-owned tskey-api- token tied to an inviting user; generate one at %s and pipe it to `tslink login --api-key-stdin` (the OAuth client secret is kept)", ErrUserOwnedAPIKeyRequired, KeysPageURL)
	}
	return nil, fmt.Errorf("%w; generate one at %s and pipe a user-owned tskey-api- token to `tslink login --api-key-stdin`", ErrUserOwnedAPIKeyRequired, KeysPageURL)
}

// newTailscaleClientWithOAuthSecret constructs an API client from the
// Tailscale-generated secret format tskey-client-<id>-<random>. Tailscale's
// token endpoint requires the embedded client ID as a separate parameter.
func newTailscaleClientWithOAuthSecret(secret string) (*tailscale.Client, error) {
	credential, _, _ := strings.Cut(strings.TrimSpace(secret), "?")
	parts := strings.Split(credential, "-")
	if len(parts) != 4 || parts[0] != "tskey" || parts[1] != "client" || parts[2] == "" || parts[3] == "" {
		return nil, fmt.Errorf("stored OAuth client secret has invalid format; expected tskey-client-<id>-<secret>")
	}

	return &tailscale.Client{
		Tailnet: "-",
		Auth: &tailscale.OAuth{
			ClientID:     parts[2],
			ClientSecret: credential,
		},
	}, nil
}

// NewTailscaleClientWithAPIKey creates a Tailscale API client from an explicit
// candidate key without reading or mutating persisted credentials.
func NewTailscaleClientWithAPIKey(key string) (*tailscale.Client, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, nil
	}
	return &tailscale.Client{Tailnet: "-", APIKey: key}, nil
}

// TailscaleClientFactory constructs the REST client used to derive an auth key.
// Command callers can supply the tailapi loopback-override gate without creating
// an import cycle back from credentials to tailapi.
type TailscaleClientFactory func() (*tailscale.Client, error)

// AuthKeyOptions configures the derived auth key.
type AuthKeyOptions struct {
	Tags          []string
	Ephemeral     bool
	Description   string
	ClientFactory TailscaleClientFactory
}

// DeriveAuthKey creates a short-lived, single-use, pre-authorized auth key from the API key.
func DeriveAuthKey(ctx context.Context, opts AuthKeyOptions) (string, error) {
	clientFactory := opts.ClientFactory
	if clientFactory == nil {
		clientFactory = newTailscaleClientFunc
	}
	client, err := clientFactory()
	if err != nil {
		return "", fmt.Errorf("create client: %w", err)
	}
	if client == nil {
		return "", fmt.Errorf("no API key configured — run 'tslink login' first")
	}

	var caps tailscale.KeyCapabilities
	caps.Devices.Create.Reusable = false
	caps.Devices.Create.Ephemeral = opts.Ephemeral
	caps.Devices.Create.Preauthorized = true
	caps.Devices.Create.Tags = opts.Tags

	description := strings.TrimSpace(opts.Description)
	if description == "" {
		description = "TSLink service startup auth key"
	}
	req := tailscale.CreateKeyRequest{
		Capabilities:  caps,
		ExpirySeconds: derivedAuthKeyExpirySeconds,
		Description:   description,
	}

	key, err := createKeyFunc(client, ctx, req)
	if err != nil {
		// 401/403 become stable coded errors so the daemon can persist the
		// failure into runtime.json with recovery steps instead of an opaque
		// log line.
		return "", ClassifyAPIError("derive auth key", err)
	}
	return key.Key, nil
}

// HasStoredCredential reports whether any supported auth credential is configured
// without deriving or consuming a fresh auth key.
func HasStoredCredential() (bool, error) {
	clientSecret, err := GetClientSecret()
	if err != nil {
		return false, err
	}
	if clientSecret != "" {
		return true, nil
	}

	apiKey, err := GetAPIKey()
	if err != nil {
		return false, err
	}
	if apiKey != "" {
		return true, nil
	}

	authKeyPath, err := authKeyPathFunc()
	if err != nil {
		return false, err
	}
	b, err := readCredentialFile(authKeyPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return strings.TrimSpace(string(b)) != "", nil
}

// RequireStoredCredential returns a user-facing auth error if no credential exists.
func RequireStoredCredential() error {
	ok, err := HasStoredCredential()
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("not authenticated — run 'tslink login' first")
	}
	return nil
}

// ClientSecretAuthKey builds a tsnet auth key from a candidate OAuth client
// secret without reading or mutating any persisted credential. It is used to
// semantically validate a client secret via a disposable, ephemeral Up before
// the login transaction commits and retires the previous credential.
func ClientSecretAuthKey(clientSecret string, opts AuthKeyOptions) (string, error) {
	return clientSecretAuthKey(clientSecret, opts)
}

func clientSecretAuthKey(clientSecret string, opts AuthKeyOptions) (string, error) {
	if len(opts.Tags) == 0 {
		return "", fmt.Errorf("client secret auth requires service tags — configure at least one tag for this service")
	}

	base, rawQuery, _ := strings.Cut(clientSecret, "?")
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		return "", fmt.Errorf("parse client secret auth attributes: %w", err)
	}
	values.Set("ephemeral", strconv.FormatBool(opts.Ephemeral))
	values.Set("preauthorized", "true")

	return base + "?" + values.Encode(), nil
}

// GetAuthKey returns a usable auth key, trying (in order):
//  1. Client secret (OAuth long-lived credential, used directly)
//  2. Derive from API key (if available)
//  3. Read legacy authkey file
func GetAuthKey(ctx context.Context, opts AuthKeyOptions) (string, error) {
	// Try client secret first (never expires, used directly as auth key)
	clientSecret, csErr := GetClientSecret()
	if csErr != nil {
		return "", csErr
	}
	if clientSecret != "" {
		return clientSecretAuthKey(clientSecret, opts)
	}

	// Try deriving from API key
	apiKey, err := GetAPIKey()
	if err != nil {
		return "", err
	}
	if apiKey != "" {
		secret, err := DeriveAuthKey(ctx, opts)
		if err != nil {
			return "", err
		}
		return secret, nil
	}

	// Legacy fallback: read authkey file
	authKeyPath, err := authKeyPathFunc()
	if err != nil {
		return "", err
	}
	b, err := readCredentialFile(authKeyPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("not authenticated — run 'tslink login' first")
		}
		return "", err
	}
	key := strings.TrimSpace(string(b))
	if key == "" {
		return "", fmt.Errorf("empty auth key — run 'tslink login' first")
	}
	return key, nil
}

// MigrateFromLegacy moves a file-based API key into the system keychain.
// Safe to call even if there's nothing to migrate.
func MigrateFromLegacy() (migrated bool) {
	unlock, err := acquireCredentialMutationLock()
	if err != nil {
		slog.Warn("legacy credential migration skipped: credential transaction lock unavailable", "error", err)
		return false
	}
	defer unlock()

	path, err := apiKeyPathFunc()
	if err != nil {
		return false
	}
	b, err := readCredentialFile(path)
	if err != nil {
		return false
	}
	key := strings.TrimSpace(string(b))
	if key == "" {
		return false
	}

	if !keyringEnabledFunc() {
		return false
	}
	stored, err := keyringGetFunc(keychainService, keychainAPIKey)
	switch {
	case err == nil && stored != "":
		// The keyring is already authoritative. A different file value may be
		// stale after rotation, so neither overwrite the keyring nor delete the
		// conflicting file without an explicit operator decision.
		if stored != key {
			return false
		}
	case err == nil, errors.Is(err, keyring.ErrNotFound):
		if err := keyringSetFunc(keychainService, keychainAPIKey, key); err != nil {
			return false
		}
		stored, err = keyringGetFunc(keychainService, keychainAPIKey)
		if err != nil || stored != key {
			return false
		}
	default:
		// An unreadable keyring might contain a newer credential. Refuse to
		// replace it based on the mere presence of an old file.
		return false
	}
	if err := os.Remove(path); err != nil {
		return false
	}
	return true
}
