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
)

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

// SetAPIKey stores the API key. Prefers macOS Keychain; falls back to file (0600).
func SetAPIKey(key string) error {
	if keyringEnabledFunc() {
		if err := keyring.Set(keychainService, keychainAPIKey, key); err == nil {
			// Keychain succeeded — remove file copy if it exists
			if path, e := config.APIKeyPath(); e == nil {
				os.Remove(path)
			}
			return nil
		}
	}
	// Fallback: write to file
	path, err := config.APIKeyPath()
	if err != nil {
		return err
	}
	if !fileCredentialFallbackEnabledFunc() {
		return fmt.Errorf("file credential fallback is disabled on Windows because TSLink cannot prove a user-only DACL locally; enable Windows Credential Manager/keyring access and retry")
	}
	return atomicfile.WriteFile(path, []byte(key))
}

// GetAPIKey retrieves the API key from keychain or file.
// Returns ("", nil) if no key is stored.
func GetAPIKey() (string, error) {
	// Keychain first
	if keyringEnabledFunc() {
		if key, err := keyring.Get(keychainService, keychainAPIKey); err == nil && key != "" {
			return key, nil
		}
	}
	// File fallback
	path, err := config.APIKeyPath()
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
	var errs []error
	if keyringEnabledFunc() {
		if err := keyring.Delete(keychainService, keychainAPIKey); err != nil && !errors.Is(err, keyring.ErrNotFound) {
			errs = append(errs, err)
		}
	}
	if path, err := config.APIKeyPath(); err == nil {
		if removeErr := os.Remove(path); removeErr != nil && !os.IsNotExist(removeErr) {
			errs = append(errs, removeErr)
		}
	} else {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// SaveClientSecret stores an OAuth client secret. The key must start with "tskey-client-".
// Prefers macOS Keychain; falls back to file (0600).
func SaveClientSecret(secret string) error {
	if !strings.HasPrefix(secret, "tskey-client-") {
		return fmt.Errorf("invalid client secret: must start with 'tskey-client-'")
	}
	if keyringEnabledFunc() {
		if err := keyring.Set(keychainService, keychainClientSecret, secret); err == nil {
			// Keychain succeeded — remove file copy if it exists
			if path, e := config.ClientSecretPath(); e == nil {
				os.Remove(path)
			}
			return nil
		}
	}
	// Fallback: write to file
	path, err := config.ClientSecretPath()
	if err != nil {
		return err
	}
	if !fileCredentialFallbackEnabledFunc() {
		return fmt.Errorf("file credential fallback is disabled on Windows because TSLink cannot prove a user-only DACL locally; enable Windows Credential Manager/keyring access and retry")
	}
	return atomicfile.WriteFile(path, []byte(secret))
}

// GetClientSecret retrieves the client secret from keychain or file.
// Returns ("", nil) if no client secret is stored.
func GetClientSecret() (string, error) {
	// Keychain first
	if keyringEnabledFunc() {
		if secret, err := keyring.Get(keychainService, keychainClientSecret); err == nil && secret != "" {
			return secret, nil
		}
	}
	// File fallback
	path, err := config.ClientSecretPath()
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
	var errs []error
	if keyringEnabledFunc() {
		if err := keyring.Delete(keychainService, keychainClientSecret); err != nil && !errors.Is(err, keyring.ErrNotFound) {
			errs = append(errs, err)
		}
	}
	if path, err := config.ClientSecretPath(); err == nil {
		if removeErr := os.Remove(path); removeErr != nil && !os.IsNotExist(removeErr) {
			errs = append(errs, removeErr)
		}
	} else {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// NewTailscaleClient creates a Tailscale API client from stored API key.
// Returns (nil, nil) if no API key is configured.
func NewTailscaleClient() (*tailscale.Client, error) {
	key, err := GetAPIKey()
	if err != nil {
		return nil, err
	}
	return NewTailscaleClientWithAPIKey(key)
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

// AuthKeyOptions configures the derived auth key.
type AuthKeyOptions struct {
	Tags        []string
	Ephemeral   bool
	Description string
}

// DeriveAuthKey creates a short-lived, single-use, pre-authorized auth key from the API key.
func DeriveAuthKey(ctx context.Context, opts AuthKeyOptions) (string, error) {
	client, err := newTailscaleClientFunc()
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
		return "", fmt.Errorf("derive auth key: %w", err)
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
	path, err := config.APIKeyPath()
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

	// Attempt keychain migration
	if keyringEnabledFunc() {
		if err := keyring.Set(keychainService, keychainAPIKey, key); err != nil {
			return false // keychain not available, keep the file
		}
	} else {
		return false
	}
	os.Remove(path)
	return true
}
