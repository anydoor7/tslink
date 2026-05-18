package cmd

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/monody0007/tslink/internal/credentials"
	"github.com/monody0007/tslink/internal/tailapi"
	"github.com/zalando/go-keyring"
)

// setupLoginTest sets up a temp HOME, mock keyring, and config dir for login tests.
func setupLoginTest(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	keyring.MockInit()

	if err := os.MkdirAll(dir+"/.config/tslink", 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	return dir
}

// mockStdin replaces loginStdinReaderFn with a reader that returns the given input lines.
func mockStdin(t *testing.T, lines ...string) {
	t.Helper()
	old := loginStdinReaderFn
	t.Cleanup(func() { loginStdinReaderFn = old })

	input := strings.Join(lines, "\n") + "\n"
	loginStdinReaderFn = func() *bufio.Reader {
		return bufio.NewReader(strings.NewReader(input))
	}
}

// skipTsnetLogin replaces the tsnet login step with a no-op.
func skipTsnetLogin(t *testing.T) {
	t.Helper()
	old := loginTsnetLoginFn
	t.Cleanup(func() { loginTsnetLoginFn = old })
	loginTsnetLoginFn = func(cfgDir string) (string, error) {
		return "test@example.com", nil
	}
}

// mockAPIKeySuccess makes API key save and verify succeed.
func mockAPIKeySuccess(t *testing.T) {
	t.Helper()

	oldSet := loginSetAPIKeyFn
	oldVerify := loginVerifyAPIKeyFn
	t.Cleanup(func() {
		loginSetAPIKeyFn = oldSet
		loginVerifyAPIKeyFn = oldVerify
	})

	loginSetAPIKeyFn = func(key string) error { return nil }
	loginVerifyAPIKeyFn = func(ctx context.Context) error { return nil }
}

// mockClientSecretSuccess makes client secret save succeed.
func mockClientSecretSuccess(t *testing.T) {
	t.Helper()
	old := loginSaveClientSecretFn
	t.Cleanup(func() { loginSaveClientSecretFn = old })
	loginSaveClientSecretFn = func(secret string) error { return nil }
}

func TestLoginCredentialFlow_APIToken_Success(t *testing.T) {
	dir := setupLoginTest(t)
	mockStdin(t, "1", "tskey-api-test-token-12345")
	mockAPIKeySuccess(t)

	err := loginCredentialFlow(loginCmd, dir)
	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
}

func TestLoginCredentialFlow_ClientSecret_Success(t *testing.T) {
	dir := setupLoginTest(t)
	mockStdin(t, "2", "tskey-client-test-secret-12345")
	mockClientSecretSuccess(t)

	err := loginCredentialFlow(loginCmd, dir)
	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
}

func TestLoginCredentialFlow_InvalidChoice(t *testing.T) {
	dir := setupLoginTest(t)
	mockStdin(t, "3")

	err := loginCredentialFlow(loginCmd, dir)
	if err == nil {
		t.Fatal("expected error for invalid choice")
	}
	if !strings.Contains(err.Error(), "invalid choice") {
		t.Fatalf("expected 'invalid choice' error, got: %v", err)
	}
}

func TestLoginCredentialFlow_EmptyChoice(t *testing.T) {
	dir := setupLoginTest(t)
	mockStdin(t, "")

	err := loginCredentialFlow(loginCmd, dir)
	if err == nil {
		t.Fatal("expected error for empty choice")
	}
	if !strings.Contains(err.Error(), "invalid choice") {
		t.Fatalf("expected 'invalid choice' error, got: %v", err)
	}
}

func TestLoginCredentialFlow_APIToken_EmptyKey(t *testing.T) {
	dir := setupLoginTest(t)
	mockStdin(t, "1", "")

	err := loginCredentialFlow(loginCmd, dir)
	if err == nil {
		t.Fatal("expected error for empty token")
	}
	if !strings.Contains(err.Error(), "no token provided") {
		t.Fatalf("expected 'no token provided' error, got: %v", err)
	}
}

func TestLoginCredentialFlow_APIToken_WrongPrefix(t *testing.T) {
	dir := setupLoginTest(t)
	mockStdin(t, "1", "tskey-client-wrong-type")

	err := loginCredentialFlow(loginCmd, dir)
	if err == nil {
		t.Fatal("expected error for wrong prefix")
	}
	if !strings.Contains(err.Error(), "tskey-api-") {
		t.Fatalf("expected prefix error, got: %v", err)
	}
}

func TestLoginCredentialFlow_APIToken_AuthKeyRejected(t *testing.T) {
	dir := setupLoginTest(t)
	mockStdin(t, "1", "tskey-auth-some-auth-key")

	err := loginCredentialFlow(loginCmd, dir)
	if err == nil {
		t.Fatal("expected error for auth key")
	}
	if !strings.Contains(err.Error(), "tskey-api-") {
		t.Fatalf("expected prefix error, got: %v", err)
	}
}

func TestLoginCredentialFlow_ClientSecret_EmptyKey(t *testing.T) {
	dir := setupLoginTest(t)
	mockStdin(t, "2", "")

	err := loginCredentialFlow(loginCmd, dir)
	if err == nil {
		t.Fatal("expected error for empty secret")
	}
	if !strings.Contains(err.Error(), "no secret provided") {
		t.Fatalf("expected 'no secret provided' error, got: %v", err)
	}
}

func TestLoginCredentialFlow_ClientSecret_WrongPrefix(t *testing.T) {
	dir := setupLoginTest(t)
	mockStdin(t, "2", "tskey-api-wrong-type")

	err := loginCredentialFlow(loginCmd, dir)
	if err == nil {
		t.Fatal("expected error for wrong prefix")
	}
	if !strings.Contains(err.Error(), "tskey-client-") {
		t.Fatalf("expected prefix error, got: %v", err)
	}
}

func TestLoginCredentialFlow_ClientSecret_ClientIDRejected(t *testing.T) {
	dir := setupLoginTest(t)
	// User accidentally pastes the short Client ID instead of the secret
	mockStdin(t, "2", "km9GkSnaBK11CNTRL")

	err := loginCredentialFlow(loginCmd, dir)
	if err == nil {
		t.Fatal("expected error for client ID")
	}
	if !strings.Contains(err.Error(), "tskey-client-") {
		t.Fatalf("expected prefix error, got: %v", err)
	}
}

func TestLoginCredentialFlow_APIToken_VerifyFails(t *testing.T) {
	dir := setupLoginTest(t)
	mockStdin(t, "1", "tskey-api-test-token-12345")

	oldSet := loginSetAPIKeyFn
	oldVerify := loginVerifyAPIKeyFn
	t.Cleanup(func() {
		loginSetAPIKeyFn = oldSet
		loginVerifyAPIKeyFn = oldVerify
	})

	loginSetAPIKeyFn = func(key string) error { return nil }
	loginVerifyAPIKeyFn = func(ctx context.Context) error {
		return fmt.Errorf("API key verification failed: Status: 401")
	}

	err := loginCredentialFlow(loginCmd, dir)
	if err == nil {
		t.Fatal("expected verification error")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Fatalf("expected 401 error, got: %v", err)
	}
}

func TestLoginCredentialFlow_ClientSecret_SaveFails(t *testing.T) {
	dir := setupLoginTest(t)
	mockStdin(t, "2", "tskey-client-test-secret-12345")

	old := loginSaveClientSecretFn
	t.Cleanup(func() { loginSaveClientSecretFn = old })
	loginSaveClientSecretFn = func(secret string) error {
		return fmt.Errorf("keychain locked")
	}

	err := loginCredentialFlow(loginCmd, dir)
	if err == nil {
		t.Fatal("expected save error")
	}
	if !strings.Contains(err.Error(), "keychain locked") {
		t.Fatalf("expected keychain error, got: %v", err)
	}
}

func TestLoginCmd_FullFlow_WithMocks(t *testing.T) {
	setupLoginTest(t)
	skipTsnetLogin(t)
	mockStdin(t, "2", "tskey-client-full-flow-test")
	mockClientSecretSuccess(t)

	loginCmd, _, err := rootCmd.Find([]string{"login"})
	if err != nil {
		t.Fatalf("find login command: %v", err)
	}

	err = loginCmd.RunE(loginCmd, nil)
	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
}

func TestLoginCredentialFlow_CreatesDefaultTag(t *testing.T) {
	dir := setupLoginTest(t)
	mockStdin(t, "1", "tskey-api-test-12345")
	mockAPIKeySuccess(t)

	var ensuredTags []string
	old := loginEnsureTagsFn
	t.Cleanup(func() { loginEnsureTagsFn = old })
	loginEnsureTagsFn = func(ctx context.Context, tags []string) error {
		ensuredTags = tags
		return nil
	}

	err := loginCredentialFlow(loginCmd, dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ensuredTags) != 1 || ensuredTags[0] != "tag:tsmain" {
		t.Fatalf("expected EnsureTags([tag:tsmain]), got: %v", ensuredTags)
	}
}

func TestLoginCredentialFlow_EnsureTagsFailureNonFatal(t *testing.T) {
	dir := setupLoginTest(t)
	mockStdin(t, "2", "tskey-client-test-secret-12345")
	mockClientSecretSuccess(t)

	old := loginEnsureTagsFn
	t.Cleanup(func() { loginEnsureTagsFn = old })
	loginEnsureTagsFn = func(ctx context.Context, tags []string) error {
		return fmt.Errorf("ACL write denied")
	}

	// Should NOT return error — EnsureTags failure is non-fatal
	err := loginCredentialFlow(loginCmd, dir)
	if err != nil {
		t.Fatalf("expected success (non-fatal), got: %v", err)
	}
}

func TestLoginWithAPIKeyClearsStaleClientSecret(t *testing.T) {
	setupLoginTest(t)

	if err := credentials.SaveClientSecret("tskey-client-stale"); err != nil {
		t.Fatalf("SaveClientSecret() error = %v", err)
	}

	oldVerify := loginVerifyAPIKeyFn
	oldEnsure := loginEnsureTagsFn
	t.Cleanup(func() {
		loginVerifyAPIKeyFn = oldVerify
		loginEnsureTagsFn = oldEnsure
	})
	loginVerifyAPIKeyFn = func(ctx context.Context) error { return nil }
	loginEnsureTagsFn = func(ctx context.Context, tags []string) error { return nil }

	if err := loginWithAPIKey(loginCmd, "tskey-api-new"); err != nil {
		t.Fatalf("loginWithAPIKey() error = %v", err)
	}

	gotSecret, err := credentials.GetClientSecret()
	if err != nil {
		t.Fatalf("GetClientSecret() error = %v", err)
	}
	if gotSecret != "" {
		t.Fatalf("client secret = %q, want cleared", gotSecret)
	}
	gotAPIKey, err := credentials.GetAPIKey()
	if err != nil {
		t.Fatalf("GetAPIKey() error = %v", err)
	}
	if gotAPIKey != "tskey-api-new" {
		t.Fatalf("api key = %q, want newly selected API key", gotAPIKey)
	}
}

func TestLoginWithAPIKey_ErrorDoesNotLeakKeyMaterial(t *testing.T) {
	setupLoginTest(t)

	// Provide an invalid key that does not start with "tskey-api-".
	badKey := "sk-live-SUPERSECRETKEY1234567890abcdef"
	err := loginWithAPIKey(loginCmd, badKey)
	if err == nil {
		t.Fatal("expected error for wrong prefix")
	}

	errMsg := err.Error()
	// The error message must not contain any part of the actual key.
	if strings.Contains(errMsg, "SUPERSECRET") {
		t.Fatalf("error message leaks key material: %q", errMsg)
	}
	if strings.Contains(errMsg, "sk-live") {
		t.Fatalf("error message leaks key prefix: %q", errMsg)
	}
	if strings.Contains(errMsg, badKey[:10]) {
		t.Fatalf("error message leaks key content: %q", errMsg)
	}
	// Should mention the expected prefix format.
	if !strings.Contains(errMsg, "tskey-api-") {
		t.Fatalf("error message should mention expected prefix, got: %q", errMsg)
	}
}

func TestLoginWithClientSecret_ErrorDoesNotLeakSecretMaterial(t *testing.T) {
	setupLoginTest(t)

	// Provide an invalid secret that does not start with "tskey-client-".
	badSecret := "sk-live-ANOTHERSUPERSECRETVALUE123456"
	err := loginWithClientSecret(loginCmd, badSecret)
	if err == nil {
		t.Fatal("expected error for wrong prefix")
	}

	errMsg := err.Error()
	// The error message must not contain any part of the actual secret.
	if strings.Contains(errMsg, "ANOTHERSUPERSECRET") {
		t.Fatalf("error message leaks secret material: %q", errMsg)
	}
	if strings.Contains(errMsg, "sk-live") {
		t.Fatalf("error message leaks secret prefix: %q", errMsg)
	}
	if strings.Contains(errMsg, badSecret[:10]) {
		t.Fatalf("error message leaks secret content: %q", errMsg)
	}
	// Should mention the expected prefix format.
	if !strings.Contains(errMsg, "tskey-client-") {
		t.Fatalf("error message should mention expected prefix, got: %q", errMsg)
	}
}

func TestLoginWithClientSecretClearsStaleAPIKey(t *testing.T) {
	setupLoginTest(t)

	if err := credentials.SetAPIKey("tskey-api-stale"); err != nil {
		t.Fatalf("SetAPIKey() error = %v", err)
	}

	oldEnsure := loginEnsureTagsFn
	t.Cleanup(func() { loginEnsureTagsFn = oldEnsure })
	loginEnsureTagsFn = func(ctx context.Context, tags []string) error {
		return tailapi.ErrNoAPIClient
	}

	if err := loginWithClientSecret(loginCmd, "tskey-client-new"); err != nil {
		t.Fatalf("loginWithClientSecret() error = %v", err)
	}

	gotAPIKey, err := credentials.GetAPIKey()
	if err != nil {
		t.Fatalf("GetAPIKey() error = %v", err)
	}
	if gotAPIKey != "" {
		t.Fatalf("api key = %q, want cleared", gotAPIKey)
	}
	gotAuth, err := credentials.GetAuthKey(context.Background(), credentials.AuthKeyOptions{Tags: []string{"tag:tsmain"}})
	if err != nil {
		t.Fatalf("GetAuthKey() error = %v", err)
	}
	if !strings.HasPrefix(gotAuth, "tskey-client-new?") {
		t.Fatalf("GetAuthKey() = %q, want newly selected client secret", gotAuth)
	}
}
