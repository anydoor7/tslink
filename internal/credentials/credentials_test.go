package credentials

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/monody0007/tslink/internal/config"
	"github.com/zalando/go-keyring"
	tailscale "tailscale.com/client/tailscale"
)

func setup(t *testing.T) {
	t.Helper()
	keyring.MockInit()
	t.Setenv("HOME", t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}
}

func apiKeyPath(t *testing.T) string {
	t.Helper()
	path, err := config.APIKeyPath()
	if err != nil {
		t.Fatalf("APIKeyPath() error = %v", err)
	}
	return path
}

func authKeyPath(t *testing.T) string {
	t.Helper()
	path, err := config.AuthKeyPath()
	if err != nil {
		t.Fatalf("AuthKeyPath() error = %v", err)
	}
	return path
}

func TestSetAPIKey_Keychain(t *testing.T) {
	setup(t)

	path := apiKeyPath(t)
	if err := os.WriteFile(path, []byte("legacy-key\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if err := SetAPIKey("keychain-key"); err != nil {
		t.Fatalf("SetAPIKey() error = %v", err)
	}

	got, err := GetAPIKey()
	if err != nil {
		t.Fatalf("GetAPIKey() error = %v", err)
	}
	if got != "keychain-key" {
		t.Fatalf("GetAPIKey() = %q, want %q", got, "keychain-key")
	}

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected %q to be removed, stat err = %v", path, err)
	}
}

func TestSetAPIKey_FileFallback(t *testing.T) {
	setup(t)
	keyring.MockInitWithError(errors.New("no keychain"))

	path := apiKeyPath(t)
	if err := SetAPIKey("file-key"); err != nil {
		t.Fatalf("SetAPIKey() error = %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if got := string(data); got != "file-key" {
		t.Fatalf("file contents = %q, want %q", got, "file-key")
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("file perms = %o, want %o", got, 0o600)
	}
}

func TestGetAPIKey_KeychainFirst(t *testing.T) {
	setup(t)

	if err := keyring.Set(keychainService, keychainAPIKey, "from-keychain"); err != nil {
		t.Fatalf("keyring.Set() error = %v", err)
	}
	if err := os.WriteFile(apiKeyPath(t), []byte("from-file\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	got, err := GetAPIKey()
	if err != nil {
		t.Fatalf("GetAPIKey() error = %v", err)
	}
	if got != "from-keychain" {
		t.Fatalf("GetAPIKey() = %q, want %q", got, "from-keychain")
	}
}

func TestGetAPIKey_FileFallback(t *testing.T) {
	setup(t)

	if err := os.WriteFile(apiKeyPath(t), []byte("from-file\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	got, err := GetAPIKey()
	if err != nil {
		t.Fatalf("GetAPIKey() error = %v", err)
	}
	if got != "from-file" {
		t.Fatalf("GetAPIKey() = %q, want %q", got, "from-file")
	}
}

func TestGetAPIKey_NoKey(t *testing.T) {
	setup(t)

	got, err := GetAPIKey()
	if err != nil {
		t.Fatalf("GetAPIKey() error = %v", err)
	}
	if got != "" {
		t.Fatalf("GetAPIKey() = %q, want empty string", got)
	}
}

func TestGetAPIKey_FileReadError(t *testing.T) {
	setup(t)

	if err := os.Mkdir(apiKeyPath(t), 0o700); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}

	_, err := GetAPIKey()
	if err == nil {
		t.Fatal("GetAPIKey() error = nil, want error")
	}
}

func TestDeleteAPIKey(t *testing.T) {
	setup(t)

	if err := SetAPIKey("secret"); err != nil {
		t.Fatalf("SetAPIKey() error = %v", err)
	}

	DeleteAPIKey()

	got, err := GetAPIKey()
	if err != nil {
		t.Fatalf("GetAPIKey() error = %v", err)
	}
	if got != "" {
		t.Fatalf("GetAPIKey() = %q, want empty string", got)
	}
}

func TestDeleteAPIKey_Both(t *testing.T) {
	setup(t)

	if err := keyring.Set(keychainService, keychainAPIKey, "from-keychain"); err != nil {
		t.Fatalf("keyring.Set() error = %v", err)
	}
	path := apiKeyPath(t)
	if err := os.WriteFile(path, []byte("from-file\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	DeleteAPIKey()

	if _, err := keyring.Get(keychainService, keychainAPIKey); err == nil {
		t.Fatal("expected keychain entry to be deleted")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected %q to be removed, stat err = %v", path, err)
	}
}

func TestMigrateFromLegacy_Success(t *testing.T) {
	setup(t)

	path := apiKeyPath(t)
	if err := os.WriteFile(path, []byte("legacy-key\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if migrated := MigrateFromLegacy(); !migrated {
		t.Fatal("MigrateFromLegacy() = false, want true")
	}

	got, err := keyring.Get(keychainService, keychainAPIKey)
	if err != nil {
		t.Fatalf("keyring.Get() error = %v", err)
	}
	if got != "legacy-key" {
		t.Fatalf("keyring value = %q, want %q", got, "legacy-key")
	}

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected %q to be removed, stat err = %v", path, err)
	}
}

func TestMigrateFromLegacy_NoFile(t *testing.T) {
	setup(t)

	if migrated := MigrateFromLegacy(); migrated {
		t.Fatal("MigrateFromLegacy() = true, want false")
	}
}

func TestMigrateFromLegacy_EmptyFile(t *testing.T) {
	setup(t)

	if err := os.WriteFile(apiKeyPath(t), []byte(" \n\t "), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if migrated := MigrateFromLegacy(); migrated {
		t.Fatal("MigrateFromLegacy() = true, want false")
	}
}

func TestMigrateFromLegacy_KeychainFail(t *testing.T) {
	setup(t)

	path := apiKeyPath(t)
	if err := os.WriteFile(path, []byte("legacy-key\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	keyring.MockInitWithError(errors.New("no keychain"))

	if migrated := MigrateFromLegacy(); migrated {
		t.Fatal("MigrateFromLegacy() = true, want false")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if got := strings.TrimSpace(string(data)); got != "legacy-key" {
		t.Fatalf("file contents = %q, want %q", got, "legacy-key")
	}
}

func TestNewTailscaleClient_WithKey(t *testing.T) {
	setup(t)

	if err := SetAPIKey("api-key"); err != nil {
		t.Fatalf("SetAPIKey() error = %v", err)
	}

	client, err := NewTailscaleClient()
	if err != nil {
		t.Fatalf("NewTailscaleClient() error = %v", err)
	}
	if client == nil {
		t.Fatal("NewTailscaleClient() returned nil client")
	}
	if got := client.Tailnet(); got != "-" {
		t.Fatalf("client.Tailnet() = %q, want %q", got, "-")
	}
}

func TestNewTailscaleClient_NoKey(t *testing.T) {
	setup(t)

	client, err := NewTailscaleClient()
	if err != nil {
		t.Fatalf("NewTailscaleClient() error = %v", err)
	}
	if client != nil {
		t.Fatal("NewTailscaleClient() returned non-nil client")
	}
}

func TestNewTailscaleClient_GetAPIKeyError(t *testing.T) {
	setup(t)

	if err := os.Mkdir(apiKeyPath(t), 0o700); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}

	client, err := NewTailscaleClient()
	if err == nil {
		t.Fatal("NewTailscaleClient() error = nil, want error")
	}
	if client != nil {
		t.Fatal("NewTailscaleClient() returned non-nil client on error")
	}
}

func TestDeriveAuthKey_NoKey(t *testing.T) {
	setup(t)

	_, err := DeriveAuthKey(context.Background(), AuthKeyOptions{})
	if err == nil {
		t.Fatal("DeriveAuthKey() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "no API key configured") {
		t.Fatalf("DeriveAuthKey() error = %v, want missing API key error", err)
	}
}

func TestGetAuthKey_LegacyFallback(t *testing.T) {
	setup(t)

	if err := os.WriteFile(authKeyPath(t), []byte("legacy-auth\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	got, err := GetAuthKey(context.Background(), AuthKeyOptions{})
	if err != nil {
		t.Fatalf("GetAuthKey() error = %v", err)
	}
	if got != "legacy-auth" {
		t.Fatalf("GetAuthKey() = %q, want %q", got, "legacy-auth")
	}
}

func TestGetAuthKey_EmptyLegacy(t *testing.T) {
	setup(t)

	if err := os.WriteFile(authKeyPath(t), []byte(" \n\t "), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	_, err := GetAuthKey(context.Background(), AuthKeyOptions{})
	if err == nil {
		t.Fatal("GetAuthKey() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "empty auth key") {
		t.Fatalf("GetAuthKey() error = %v, want empty auth key error", err)
	}
}

func TestGetAuthKey_LegacyReadError(t *testing.T) {
	setup(t)

	if err := os.Mkdir(authKeyPath(t), 0o700); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}

	_, err := GetAuthKey(context.Background(), AuthKeyOptions{})
	if err == nil {
		t.Fatal("GetAuthKey() error = nil, want error")
	}
}

func TestGetAuthKey_NoAuth(t *testing.T) {
	setup(t)

	_, err := GetAuthKey(context.Background(), AuthKeyOptions{})
	if err == nil {
		t.Fatal("GetAuthKey() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "not authenticated") {
		t.Fatalf("GetAuthKey() error = %v, want not authenticated error", err)
	}
}

func TestMigrateFromLegacy_ReadError(t *testing.T) {
	setup(t)

	if err := os.Mkdir(apiKeyPath(t), 0o700); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}

	if migrated := MigrateFromLegacy(); migrated {
		t.Fatal("MigrateFromLegacy() = true, want false")
	}
}

func TestDeriveAuthKey_APICallError(t *testing.T) {
	setup(t)

	if err := SetAPIKey("tskey-api-fake-key"); err != nil {
		t.Fatalf("SetAPIKey() error = %v", err)
	}

	// DeriveAuthKey will create a client (key is set) then call CreateKey()
	// which will fail because no real Tailscale API is available
	_, err := DeriveAuthKey(context.Background(), AuthKeyOptions{})
	if err == nil {
		t.Fatal("DeriveAuthKey() error = nil, want error (no real API)")
	}
	if !strings.Contains(err.Error(), "derive auth key") {
		t.Fatalf("DeriveAuthKey() error = %v, want 'derive auth key' error", err)
	}
}

func TestGetAuthKey_WithAPIKey_DeriveError(t *testing.T) {
	setup(t)

	if err := SetAPIKey("tskey-api-fake-key"); err != nil {
		t.Fatalf("SetAPIKey() error = %v", err)
	}

	// GetAuthKey with an API key will try DeriveAuthKey, which fails without real API
	_, err := GetAuthKey(context.Background(), AuthKeyOptions{})
	if err == nil {
		t.Fatal("GetAuthKey() error = nil, want error from DeriveAuthKey")
	}
}

// --- Tests for uncovered error paths ---

func TestSetAPIKey_FileFallback_PathError(t *testing.T) {
	// When keychain fails AND config.APIKeyPath() fails, SetAPIKey returns error
	keyring.MockInitWithError(errors.New("no keychain"))
	t.Setenv("HOME", "")

	err := SetAPIKey("some-key")
	if err == nil {
		t.Fatal("SetAPIKey() error = nil, want error when HOME is unset")
	}
}

func TestGetAPIKey_PathError(t *testing.T) {
	// When keychain has no key AND config.APIKeyPath() fails
	keyring.MockInit()
	t.Setenv("HOME", "")

	_, err := GetAPIKey()
	if err == nil {
		t.Fatal("GetAPIKey() error = nil, want error when HOME is unset")
	}
}

func TestDeriveAuthKey_ClientError(t *testing.T) {
	// When NewTailscaleClient returns an error (GetAPIKey fails)
	keyring.MockInit()
	t.Setenv("HOME", t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}

	// Create a directory at the apikey path to cause ReadFile to fail
	path := apiKeyPath(t)
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}

	_, err := DeriveAuthKey(context.Background(), AuthKeyOptions{})
	if err == nil {
		t.Fatal("DeriveAuthKey() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "create client") {
		t.Fatalf("DeriveAuthKey() error = %v, want 'create client' error", err)
	}
}

func TestGetAuthKey_GetAPIKeyError(t *testing.T) {
	// When GetAPIKey itself returns an error
	keyring.MockInit()
	t.Setenv("HOME", t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}

	// Create a directory at the apikey path to cause ReadFile to fail
	path := apiKeyPath(t)
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}

	_, err := GetAuthKey(context.Background(), AuthKeyOptions{})
	if err == nil {
		t.Fatal("GetAuthKey() error = nil, want error from GetAPIKey")
	}
}

func TestGetAuthKey_AuthKeyPathError(t *testing.T) {
	setup(t)

	// No API key set, so GetAuthKey falls through to legacy path.
	// Override authKeyPathFunc to simulate a failure.
	origAuthKeyPath := authKeyPathFunc
	t.Cleanup(func() { authKeyPathFunc = origAuthKeyPath })
	authKeyPathFunc = func() (string, error) {
		return "", errors.New("auth key path unavailable")
	}

	_, err := GetAuthKey(context.Background(), AuthKeyOptions{})
	if err == nil {
		t.Fatal("GetAuthKey() error = nil, want error from AuthKeyPath")
	}
	if !strings.Contains(err.Error(), "auth key path unavailable") {
		t.Fatalf("GetAuthKey() error = %v, want 'auth key path unavailable'", err)
	}
}

func TestMigrateFromLegacy_PathError(t *testing.T) {
	// When config.APIKeyPath() itself fails
	keyring.MockInit()
	t.Setenv("HOME", "")

	if migrated := MigrateFromLegacy(); migrated {
		t.Fatal("MigrateFromLegacy() = true, want false when HOME is unset")
	}
}

// --- ClientSecret tests ---

func clientSecretPath(t *testing.T) string {
	t.Helper()
	path, err := config.ClientSecretPath()
	if err != nil {
		t.Fatalf("ClientSecretPath() error = %v", err)
	}
	return path
}

func TestSaveClientSecret_Keychain(t *testing.T) {
	setup(t)

	path := clientSecretPath(t)
	// Write a file to ensure it gets cleaned up on keychain success
	if err := os.WriteFile(path, []byte("old-secret\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if err := SaveClientSecret("tskey-client-my-secret"); err != nil {
		t.Fatalf("SaveClientSecret() error = %v", err)
	}

	got, err := GetClientSecret()
	if err != nil {
		t.Fatalf("GetClientSecret() error = %v", err)
	}
	if got != "tskey-client-my-secret" {
		t.Fatalf("GetClientSecret() = %q, want %q", got, "tskey-client-my-secret")
	}

	// File should be removed since keychain succeeded
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected %q to be removed, stat err = %v", path, err)
	}
}

func TestSaveClientSecret_InvalidPrefix(t *testing.T) {
	setup(t)

	err := SaveClientSecret("tskey-api-wrong-prefix")
	if err == nil {
		t.Fatal("SaveClientSecret() error = nil, want error for invalid prefix")
	}
	if !strings.Contains(err.Error(), "tskey-client-") {
		t.Fatalf("SaveClientSecret() error = %v, want prefix error", err)
	}
}

func TestSaveClientSecret_FileFallback(t *testing.T) {
	setup(t)
	keyring.MockInitWithError(errors.New("no keychain"))

	path := clientSecretPath(t)
	if err := SaveClientSecret("tskey-client-file-secret"); err != nil {
		t.Fatalf("SaveClientSecret() error = %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if got := string(data); got != "tskey-client-file-secret" {
		t.Fatalf("file contents = %q, want %q", got, "tskey-client-file-secret")
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("file perms = %o, want %o", got, 0o600)
	}
}

func TestSaveClientSecret_FileFallback_PathError(t *testing.T) {
	keyring.MockInitWithError(errors.New("no keychain"))
	t.Setenv("HOME", "")

	err := SaveClientSecret("tskey-client-some-key")
	if err == nil {
		t.Fatal("SaveClientSecret() error = nil, want error when HOME is unset")
	}
}

func TestGetClientSecret_KeychainFirst(t *testing.T) {
	setup(t)

	if err := keyring.Set(keychainService, keychainClientSecret, "from-keychain"); err != nil {
		t.Fatalf("keyring.Set() error = %v", err)
	}
	if err := os.WriteFile(clientSecretPath(t), []byte("from-file\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	got, err := GetClientSecret()
	if err != nil {
		t.Fatalf("GetClientSecret() error = %v", err)
	}
	if got != "from-keychain" {
		t.Fatalf("GetClientSecret() = %q, want %q", got, "from-keychain")
	}
}

func TestGetClientSecret_FileFallback(t *testing.T) {
	setup(t)

	if err := os.WriteFile(clientSecretPath(t), []byte("from-file\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	got, err := GetClientSecret()
	if err != nil {
		t.Fatalf("GetClientSecret() error = %v", err)
	}
	if got != "from-file" {
		t.Fatalf("GetClientSecret() = %q, want %q", got, "from-file")
	}
}

func TestGetClientSecret_NoSecret(t *testing.T) {
	setup(t)

	got, err := GetClientSecret()
	if err != nil {
		t.Fatalf("GetClientSecret() error = %v", err)
	}
	if got != "" {
		t.Fatalf("GetClientSecret() = %q, want empty string", got)
	}
}

func TestGetClientSecret_FileReadError(t *testing.T) {
	setup(t)

	// Create a directory at the file path to cause ReadFile to fail
	if err := os.Mkdir(clientSecretPath(t), 0o700); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}

	_, err := GetClientSecret()
	if err == nil {
		t.Fatal("GetClientSecret() error = nil, want error")
	}
}

func TestGetClientSecret_PathError(t *testing.T) {
	keyring.MockInit()
	t.Setenv("HOME", "")

	_, err := GetClientSecret()
	if err == nil {
		t.Fatal("GetClientSecret() error = nil, want error when HOME is unset")
	}
}

func TestHasClientSecret_True(t *testing.T) {
	setup(t)

	if err := SaveClientSecret("tskey-client-test"); err != nil {
		t.Fatalf("SaveClientSecret() error = %v", err)
	}

	if !HasClientSecret() {
		t.Fatal("HasClientSecret() = false, want true")
	}
}

func TestHasClientSecret_False(t *testing.T) {
	setup(t)

	if HasClientSecret() {
		t.Fatal("HasClientSecret() = true, want false")
	}
}

func TestDeleteClientSecret(t *testing.T) {
	setup(t)

	if err := SaveClientSecret("tskey-client-secret"); err != nil {
		t.Fatalf("SaveClientSecret() error = %v", err)
	}

	DeleteClientSecret()

	got, err := GetClientSecret()
	if err != nil {
		t.Fatalf("GetClientSecret() error = %v", err)
	}
	if got != "" {
		t.Fatalf("GetClientSecret() = %q, want empty string", got)
	}
}

func TestDeleteClientSecret_Both(t *testing.T) {
	setup(t)

	if err := keyring.Set(keychainService, keychainClientSecret, "from-keychain"); err != nil {
		t.Fatalf("keyring.Set() error = %v", err)
	}
	path := clientSecretPath(t)
	if err := os.WriteFile(path, []byte("from-file\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	DeleteClientSecret()

	if _, err := keyring.Get(keychainService, keychainClientSecret); err == nil {
		t.Fatal("expected keychain entry to be deleted")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected %q to be removed, stat err = %v", path, err)
	}
}

func TestGetAuthKey_ClientSecretFirst(t *testing.T) {
	setup(t)

	// Set both API key and client secret
	if err := SetAPIKey("tskey-api-fake"); err != nil {
		t.Fatalf("SetAPIKey() error = %v", err)
	}
	if err := SaveClientSecret("tskey-client-my-secret"); err != nil {
		t.Fatalf("SaveClientSecret() error = %v", err)
	}

	got, err := GetAuthKey(context.Background(), AuthKeyOptions{})
	if err != nil {
		t.Fatalf("GetAuthKey() error = %v", err)
	}
	if got != "tskey-client-my-secret" {
		t.Fatalf("GetAuthKey() = %q, want %q (client secret should take priority)", got, "tskey-client-my-secret")
	}
}

func TestGetAuthKey_ClientSecretOnly(t *testing.T) {
	setup(t)

	if err := SaveClientSecret("tskey-client-only"); err != nil {
		t.Fatalf("SaveClientSecret() error = %v", err)
	}

	got, err := GetAuthKey(context.Background(), AuthKeyOptions{})
	if err != nil {
		t.Fatalf("GetAuthKey() error = %v", err)
	}
	if got != "tskey-client-only" {
		t.Fatalf("GetAuthKey() = %q, want %q", got, "tskey-client-only")
	}
}

func TestGetAuthKey_ClientSecretError(t *testing.T) {
	setup(t)

	// Create a directory at the client secret file path to cause GetClientSecret to fail
	if err := os.Mkdir(clientSecretPath(t), 0o700); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}

	_, err := GetAuthKey(context.Background(), AuthKeyOptions{})
	if err == nil {
		t.Fatal("GetAuthKey() error = nil, want error from GetClientSecret")
	}
}

func TestDeriveAuthKey_Success(t *testing.T) {
	setup(t)

	if err := SetAPIKey("tskey-api-fake"); err != nil {
		t.Fatalf("SetAPIKey() error = %v", err)
	}

	// Override createKeyFunc to simulate a successful CreateKey call.
	origCreateKey := createKeyFunc
	t.Cleanup(func() { createKeyFunc = origCreateKey })
	createKeyFunc = func(_ *tailscale.Client, _ context.Context, caps tailscale.KeyCapabilities) (string, *tailscale.Key, error) {
		// Verify capabilities are passed correctly.
		if !caps.Devices.Create.Reusable {
			t.Error("expected Reusable=true")
		}
		if !caps.Devices.Create.Preauthorized {
			t.Error("expected Preauthorized=true")
		}
		if !caps.Devices.Create.Ephemeral {
			t.Error("expected Ephemeral=true")
		}
		if len(caps.Devices.Create.Tags) != 1 || caps.Devices.Create.Tags[0] != "tag:test" {
			t.Errorf("Tags = %v, want [tag:test]", caps.Devices.Create.Tags)
		}
		return "tskey-auth-derived", nil, nil
	}

	secret, err := DeriveAuthKey(context.Background(), AuthKeyOptions{
		Tags:      []string{"tag:test"},
		Ephemeral: true,
	})
	if err != nil {
		t.Fatalf("DeriveAuthKey() error = %v", err)
	}
	if secret != "tskey-auth-derived" {
		t.Fatalf("DeriveAuthKey() = %q, want %q", secret, "tskey-auth-derived")
	}
}

func TestGetAuthKey_DeriveSuccess(t *testing.T) {
	setup(t)

	if err := SetAPIKey("tskey-api-fake"); err != nil {
		t.Fatalf("SetAPIKey() error = %v", err)
	}

	// Override createKeyFunc to simulate a successful CreateKey call.
	origCreateKey := createKeyFunc
	t.Cleanup(func() { createKeyFunc = origCreateKey })
	createKeyFunc = func(_ *tailscale.Client, _ context.Context, _ tailscale.KeyCapabilities) (string, *tailscale.Key, error) {
		return "tskey-auth-from-derive", nil, nil
	}

	got, err := GetAuthKey(context.Background(), AuthKeyOptions{})
	if err != nil {
		t.Fatalf("GetAuthKey() error = %v", err)
	}
	if got != "tskey-auth-from-derive" {
		t.Fatalf("GetAuthKey() = %q, want %q", got, "tskey-auth-from-derive")
	}
}
