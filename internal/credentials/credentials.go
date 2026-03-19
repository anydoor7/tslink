package credentials

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/monody0007/tslink/internal/config"
	"github.com/zalando/go-keyring"
	tailscale "tailscale.com/client/tailscale/v2"
)

const (
	keychainService      = "tslink"
	keychainAPIKey       = "api-key"
	keychainClientSecret = "client-secret"
)

// Testable seams.
var (
	newTailscaleClientFunc = NewTailscaleClient
	createKeyFunc          = func(client *tailscale.Client, ctx context.Context, req tailscale.CreateKeyRequest) (*tailscale.Key, error) {
		return client.Keys().CreateAuthKey(ctx, req)
	}
	authKeyPathFunc = config.AuthKeyPath
)

// SetAPIKey stores the API key. Prefers macOS Keychain; falls back to file (0600).
func SetAPIKey(key string) error {
	if err := keyring.Set(keychainService, keychainAPIKey, key); err == nil {
		// Keychain succeeded — remove file copy if it exists
		if path, e := config.APIKeyPath(); e == nil {
			os.Remove(path)
		}
		return nil
	}
	// Fallback: write to file
	path, err := config.APIKeyPath()
	if err != nil {
		return err
	}
	return os.WriteFile(path, []byte(key), 0o600)
}

// GetAPIKey retrieves the API key from keychain or file.
// Returns ("", nil) if no key is stored.
func GetAPIKey() (string, error) {
	// Keychain first
	if key, err := keyring.Get(keychainService, keychainAPIKey); err == nil && key != "" {
		return key, nil
	}
	// File fallback
	path, err := config.APIKeyPath()
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(path)
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
	_ = keyring.Delete(keychainService, keychainAPIKey)
	if path, err := config.APIKeyPath(); err == nil {
		os.Remove(path)
	}
}

// SaveClientSecret stores an OAuth client secret. The key must start with "tskey-client-".
// Prefers macOS Keychain; falls back to file (0600).
func SaveClientSecret(secret string) error {
	if !strings.HasPrefix(secret, "tskey-client-") {
		return fmt.Errorf("invalid client secret: must start with 'tskey-client-'")
	}
	if err := keyring.Set(keychainService, keychainClientSecret, secret); err == nil {
		// Keychain succeeded — remove file copy if it exists
		if path, e := config.ClientSecretPath(); e == nil {
			os.Remove(path)
		}
		return nil
	}
	// Fallback: write to file
	path, err := config.ClientSecretPath()
	if err != nil {
		return err
	}
	return os.WriteFile(path, []byte(secret), 0o600)
}

// GetClientSecret retrieves the client secret from keychain or file.
// Returns ("", nil) if no client secret is stored.
func GetClientSecret() (string, error) {
	// Keychain first
	if secret, err := keyring.Get(keychainService, keychainClientSecret); err == nil && secret != "" {
		return secret, nil
	}
	// File fallback
	path, err := config.ClientSecretPath()
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(path)
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
	_ = keyring.Delete(keychainService, keychainClientSecret)
	if path, err := config.ClientSecretPath(); err == nil {
		os.Remove(path)
	}
}

// NewTailscaleClient creates a Tailscale API client from stored API key.
// Returns (nil, nil) if no API key is configured.
func NewTailscaleClient() (*tailscale.Client, error) {
	key, err := GetAPIKey()
	if err != nil {
		return nil, err
	}
	if key == "" {
		return nil, nil
	}
	return &tailscale.Client{Tailnet: "-", APIKey: key}, nil
}

// AuthKeyOptions configures the derived auth key.
type AuthKeyOptions struct {
	Tags      []string
	Ephemeral bool
}

// DeriveAuthKey creates a reusable, pre-authorized auth key from the API key.
func DeriveAuthKey(ctx context.Context, opts AuthKeyOptions) (string, error) {
	client, err := newTailscaleClientFunc()
	if err != nil {
		return "", fmt.Errorf("create client: %w", err)
	}
	if client == nil {
		return "", fmt.Errorf("no API key configured — run 'tslink login' first")
	}

	var caps tailscale.KeyCapabilities
	caps.Devices.Create.Reusable = true
	caps.Devices.Create.Ephemeral = opts.Ephemeral
	caps.Devices.Create.Preauthorized = true
	caps.Devices.Create.Tags = opts.Tags

	req := tailscale.CreateKeyRequest{
		Capabilities: caps,
	}

	key, err := createKeyFunc(client, ctx, req)
	if err != nil {
		return "", fmt.Errorf("derive auth key: %w", err)
	}
	return key.Key, nil
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
		return clientSecret, nil
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
	b, err := os.ReadFile(authKeyPath)
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
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	key := strings.TrimSpace(string(b))
	if key == "" {
		return false
	}

	// Attempt keychain migration
	if err := keyring.Set(keychainService, keychainAPIKey, key); err != nil {
		return false // keychain not available, keep the file
	}
	os.Remove(path)
	return true
}
