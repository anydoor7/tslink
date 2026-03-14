package credentials

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/monody0007/tslink/internal/config"
	"github.com/zalando/go-keyring"
	tailscale "tailscale.com/client/tailscale"
)

const (
	keychainService = "tslink"
	keychainAPIKey  = "api-key"
)

// Testable seams.
var (
	newTailscaleClientFunc = NewTailscaleClient
	createKeyFunc          = func(client *tailscale.Client, ctx context.Context, caps tailscale.KeyCapabilities) (string, *tailscale.Key, error) {
		return client.CreateKey(ctx, caps)
	}
	authKeyPathFunc = config.AuthKeyPath
)

func init() {
	tailscale.I_Acknowledge_This_API_Is_Unstable = true
}

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
	return tailscale.NewClient("-", tailscale.APIKey(key)), nil
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

	caps := tailscale.KeyCapabilities{
		Devices: tailscale.KeyDeviceCapabilities{
			Create: tailscale.KeyDeviceCreateCapabilities{
				Reusable:      true,
				Ephemeral:     opts.Ephemeral,
				Preauthorized: true,
				Tags:          opts.Tags,
			},
		},
	}

	secret, _, err := createKeyFunc(client, ctx, caps)
	if err != nil {
		return "", fmt.Errorf("derive auth key: %w", err)
	}
	return secret, nil
}

// GetAuthKey returns a usable auth key, trying (in order):
//  1. Derive from API key (if available)
//  2. Read legacy authkey file
func GetAuthKey(ctx context.Context, opts AuthKeyOptions) (string, error) {
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
