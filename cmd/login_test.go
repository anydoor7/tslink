package cmd

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/monody0007/tslink/internal/credentials"
	"github.com/monody0007/tslink/internal/tailapi"
	"github.com/zalando/go-keyring"
	"tailscale.com/client/local"
	"tailscale.com/ipn/ipnstate"
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

	oldVerify := loginVerifyAPIKeyFn
	oldEnsureTags := loginEnsureTagsFn
	t.Cleanup(func() {
		loginVerifyAPIKeyFn = oldVerify
		loginEnsureTagsFn = oldEnsureTags
	})

	loginVerifyAPIKeyFn = func(ctx context.Context, key string) error { return nil }
	loginEnsureTagsFn = func(ctx context.Context, tags []string) error { return nil }
}

// mockClientSecretSuccess makes client secret save succeed, including the
// semantic activation (disposable Up) that now guards client-secret adoption.
// Tests that exercise a usable client secret inject a passing activator here so
// they never attempt a real tailnet Up.
func mockClientSecretSuccess(t *testing.T) {
	t.Helper()
	oldActivate := loginActivateClientSecretFn
	t.Cleanup(func() { loginActivateClientSecretFn = oldActivate })
	loginActivateClientSecretFn = func(context.Context, string) error { return nil }
}

func resetLoginFlags(t *testing.T) {
	t.Helper()
	_ = loginCmd.Flags().Set("api-key", "")
	_ = loginCmd.Flags().Set("client-secret", "")
	_ = loginCmd.Flags().Set("api-key-stdin", "false")
	_ = loginCmd.Flags().Set("client-secret-stdin", "false")
	_ = loginCmd.Flags().Set("manage-acl", "false")
	loginCmd.SetIn(nil)
}

type fakeLoginTSNetServer struct {
	upCalled    bool
	closeCalled bool
}

func (s *fakeLoginTSNetServer) Up(context.Context) (*ipnstate.Status, error) {
	s.upCalled = true
	return &ipnstate.Status{}, nil
}

func (s *fakeLoginTSNetServer) LocalClient() (*local.Client, error) {
	return nil, fmt.Errorf("local client unavailable")
}

func (s *fakeLoginTSNetServer) Close() error {
	s.closeCalled = true
	return nil
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

	oldVerify := loginVerifyAPIKeyFn
	t.Cleanup(func() {
		loginVerifyAPIKeyFn = oldVerify
	})

	loginVerifyAPIKeyFn = func(ctx context.Context, key string) error {
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
	mockClientSecretSuccess(t) // activation succeeds so the save failure is what surfaces

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

func TestLoginCmd_ExplicitClientSecretBeatsEnvAPIKey(t *testing.T) {
	setupLoginTest(t)
	resetLoginFlags(t)
	t.Cleanup(func() {
		resetLoginFlags(t)
	})
	t.Setenv("TSLINK_API_KEY", "tskey-api-from-env")
	mockClientSecretSuccess(t) // client secret is usable; activation succeeds

	oldEnsure := loginEnsureTagsFn
	t.Cleanup(func() {
		loginEnsureTagsFn = oldEnsure
	})
	loginEnsureTagsFn = func(ctx context.Context, tags []string) error {
		return tailapi.ErrNoAPIClient
	}
	if err := loginCmd.Flags().Set("client-secret", "tskey-client-explicit"); err != nil {
		t.Fatalf("set client-secret flag: %v", err)
	}

	if err := loginCmd.RunE(loginCmd, nil); err != nil {
		t.Fatalf("RunE() error = %v", err)
	}
	savedSecret, err := credentials.GetClientSecret()
	if err != nil {
		t.Fatalf("GetClientSecret() error = %v", err)
	}
	if savedSecret != "tskey-client-explicit" {
		t.Fatalf("saved client secret = %q, want explicit flag value", savedSecret)
	}
}

func TestLoginCmd_ReadsAPIKeyFromStdin(t *testing.T) {
	setupLoginTest(t)
	resetLoginFlags(t)
	t.Cleanup(func() {
		resetLoginFlags(t)
	})
	oldVerify := loginVerifyAPIKeyFn
	oldEnsure := loginEnsureTagsFn
	t.Cleanup(func() {
		loginVerifyAPIKeyFn = oldVerify
		loginEnsureTagsFn = oldEnsure
	})
	loginVerifyAPIKeyFn = func(ctx context.Context, key string) error { return nil }
	loginEnsureTagsFn = func(ctx context.Context, tags []string) error { return nil }
	loginCmd.SetIn(strings.NewReader("tskey-api-from-stdin\n"))
	if err := loginCmd.Flags().Set("api-key-stdin", "true"); err != nil {
		t.Fatalf("set api-key-stdin flag: %v", err)
	}

	if err := loginCmd.RunE(loginCmd, nil); err != nil {
		t.Fatalf("RunE() error = %v", err)
	}
	savedKey, err := credentials.GetAPIKey()
	if err != nil {
		t.Fatalf("GetAPIKey() error = %v", err)
	}
	if savedKey != "tskey-api-from-stdin" {
		t.Fatalf("saved API key = %q, want trimmed stdin key", savedKey)
	}
}

func TestLoginCmd_RejectsMixedExplicitCredentials(t *testing.T) {
	setupLoginTest(t)
	resetLoginFlags(t)
	t.Cleanup(func() {
		resetLoginFlags(t)
	})
	if err := loginCmd.Flags().Set("api-key", "tskey-api-explicit"); err != nil {
		t.Fatalf("set api-key flag: %v", err)
	}
	if err := loginCmd.Flags().Set("client-secret", "tskey-client-explicit"); err != nil {
		t.Fatalf("set client-secret flag: %v", err)
	}

	err := loginCmd.RunE(loginCmd, nil)
	if err == nil {
		t.Fatal("RunE() error = nil, want mixed explicit credential error")
	}
	if !strings.Contains(err.Error(), "only one explicit credential source") {
		t.Fatalf("RunE() error = %v, want mixed source error", err)
	}
}

func TestLoginCredentialFlow_DoesNotMutateACLByDefault(t *testing.T) {
	dir := setupLoginTest(t)
	resetLoginFlags(t)
	t.Cleanup(func() { resetLoginFlags(t) })
	mockStdin(t, "1", "tskey-api-test-12345")
	mockAPIKeySuccess(t)

	old := loginEnsureTagsFn
	t.Cleanup(func() { loginEnsureTagsFn = old })
	loginEnsureTagsFn = func(ctx context.Context, tags []string) error {
		t.Fatalf("EnsureTags called by default with %v", tags)
		return nil
	}

	err := loginCredentialFlow(loginCmd, dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoginCredentialFlow_ManageACLCreatesDefaultTag(t *testing.T) {
	dir := setupLoginTest(t)
	resetLoginFlags(t)
	t.Cleanup(func() { resetLoginFlags(t) })
	if err := loginCmd.Flags().Set("manage-acl", "true"); err != nil {
		t.Fatalf("set manage-acl: %v", err)
	}
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
	resetLoginFlags(t)
	t.Cleanup(func() { resetLoginFlags(t) })
	if err := loginCmd.Flags().Set("manage-acl", "true"); err != nil {
		t.Fatalf("set manage-acl: %v", err)
	}
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

func TestLoginWithClientSecret_EnsureTagsFailureReportsDegraded(t *testing.T) {
	setupLoginTest(t)
	resetLoginFlags(t)
	t.Cleanup(func() { resetLoginFlags(t) })
	if err := loginCmd.Flags().Set("manage-acl", "true"); err != nil {
		t.Fatalf("set manage-acl: %v", err)
	}
	mockClientSecretSuccess(t)

	oldEnsure := loginEnsureTagsFn
	oldStderr := os.Stderr
	t.Cleanup(func() {
		loginEnsureTagsFn = oldEnsure
		os.Stderr = oldStderr
	})
	loginEnsureTagsFn = func(ctx context.Context, tags []string) error {
		return fmt.Errorf("Status: 400 requested tags invalid or not permitted")
	}
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe() error = %v", err)
	}
	os.Stderr = w

	if err := loginWithClientSecret(loginCmd, "tskey-client-new"); err != nil {
		t.Fatalf("loginWithClientSecret() error = %v", err)
	}
	w.Close()
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatalf("copy stderr: %v", err)
	}
	if !strings.Contains(buf.String(), "Degraded login") ||
		!strings.Contains(buf.String(), "--manage-acl") ||
		!strings.Contains(buf.String(), "Verify API token permissions") {
		t.Fatalf("stderr = %q, want degraded manage-acl permission warning", buf.String())
	}
}

func TestLoginWithAPIKeyJSONReportsDegradedEnsureTags(t *testing.T) {
	setupLoginTest(t)
	resetLoginFlags(t)
	t.Cleanup(func() {
		resetLoginFlags(t)
		_ = rootCmd.PersistentFlags().Set("json", "false")
	})
	if err := rootCmd.PersistentFlags().Set("json", "true"); err != nil {
		t.Fatalf("set json flag: %v", err)
	}
	if err := loginCmd.Flags().Set("manage-acl", "true"); err != nil {
		t.Fatalf("set manage-acl: %v", err)
	}

	oldVerify := loginVerifyAPIKeyFn
	oldEnsure := loginEnsureTagsFn
	t.Cleanup(func() {
		loginVerifyAPIKeyFn = oldVerify
		loginEnsureTagsFn = oldEnsure
	})
	loginVerifyAPIKeyFn = func(ctx context.Context, key string) error { return nil }
	loginEnsureTagsFn = func(ctx context.Context, tags []string) error {
		return fmt.Errorf("ACL write denied")
	}

	got := captureStdout(t, func() {
		if err := loginWithAPIKey(loginCmd, "tskey-api-new"); err != nil {
			t.Fatalf("loginWithAPIKey() error = %v", err)
		}
	})
	data := dataMap(t, got)
	if data["degraded"] != true {
		t.Fatalf("degraded = %v, want true", data["degraded"])
	}
	if gotErr, ok := data["tag_ensure_error"].(string); !ok || !strings.Contains(gotErr, "ACL write denied") {
		t.Fatalf("tag_ensure_error = %v, want ACL write denied", data["tag_ensure_error"])
	}
	if data["acl_mutation_skipped"] == true {
		t.Fatalf("acl_mutation_skipped = true, want false with --manage-acl")
	}
}

func TestLoginWithAPIKeyJSONDefaultReturnsSideEffectPlanWithoutACLWrite(t *testing.T) {
	setupLoginTest(t)
	resetLoginFlags(t)
	t.Cleanup(func() {
		resetLoginFlags(t)
		_ = rootCmd.PersistentFlags().Set("json", "false")
	})
	if err := rootCmd.PersistentFlags().Set("json", "true"); err != nil {
		t.Fatalf("set json flag: %v", err)
	}

	oldVerify := loginVerifyAPIKeyFn
	oldEnsure := loginEnsureTagsFn
	t.Cleanup(func() {
		loginVerifyAPIKeyFn = oldVerify
		loginEnsureTagsFn = oldEnsure
	})
	loginVerifyAPIKeyFn = func(ctx context.Context, key string) error { return nil }
	loginEnsureTagsFn = func(ctx context.Context, tags []string) error {
		t.Fatalf("EnsureTags called by default with %v", tags)
		return nil
	}

	got := captureStdout(t, func() {
		if err := loginWithAPIKey(loginCmd, "tskey-api-new"); err != nil {
			t.Fatalf("loginWithAPIKey() error = %v", err)
		}
	})
	data := dataMap(t, got)
	if data["acl_mutation_skipped"] != true {
		t.Fatalf("acl_mutation_skipped = %v, want true", data["acl_mutation_skipped"])
	}
	plan, ok := data["remote_side_effect_plan"].(map[string]any)
	if !ok {
		t.Fatalf("remote_side_effect_plan = %T, want object", data["remote_side_effect_plan"])
	}
	if plan["opt_in_flag"] != "--manage-acl" || plan["mutates"] != false {
		t.Fatalf("remote_side_effect_plan = %+v, want disabled --manage-acl plan", plan)
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
	loginVerifyAPIKeyFn = func(ctx context.Context, key string) error { return nil }
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
	resetLoginFlags(t)
	mockClientSecretSuccess(t) // usable secret: activation succeeds, so the stale API key is retired

	if err := credentials.SetAPIKey("tskey-api-stale"); err != nil {
		t.Fatalf("SetAPIKey() error = %v", err)
	}

	oldEnsure := loginEnsureTagsFn
	t.Cleanup(func() { loginEnsureTagsFn = oldEnsure })
	loginEnsureTagsFn = func(ctx context.Context, tags []string) error {
		t.Fatalf("EnsureTags called by default with %v", tags)
		return nil
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

func TestInteractiveLoginServerConstructorIsEphemeral(t *testing.T) {
	dir := t.TempDir()
	srv := newInteractiveLoginServer(dir)
	if srv.Hostname != "tslink-auth" {
		t.Fatalf("Hostname = %q, want tslink-auth", srv.Hostname)
	}
	if srv.Dir != dir {
		t.Fatalf("Dir = %q, want %q", srv.Dir, dir)
	}
	if !srv.Ephemeral {
		t.Fatal("Ephemeral = false, want true for interactive auth helper")
	}
}

func TestInteractiveLoginCleansTemporaryState(t *testing.T) {
	setupLoginTest(t)
	cfgDir := t.TempDir()
	fake := &fakeLoginTSNetServer{}

	oldNew := loginNewTSNetServerFn
	oldRemove := loginRemoveAllFn
	t.Cleanup(func() {
		loginNewTSNetServerFn = oldNew
		loginRemoveAllFn = oldRemove
	})

	var constructedDir string
	var removedDir string
	loginNewTSNetServerFn = func(tmpStateDir string) loginTSNetServer {
		constructedDir = tmpStateDir
		return fake
	}
	loginRemoveAllFn = func(path string) error {
		removedDir = path
		return nil
	}

	if _, err := loginTsnetLoginFn(cfgDir); err != nil {
		t.Fatalf("loginTsnetLoginFn() error = %v", err)
	}
	want := cfgDir + string(os.PathSeparator) + "tsnet-login-tmp"
	if constructedDir != want {
		t.Fatalf("constructed dir = %q, want %q", constructedDir, want)
	}
	if removedDir != want {
		t.Fatalf("removed dir = %q, want %q", removedDir, want)
	}
	if !fake.upCalled || !fake.closeCalled {
		t.Fatalf("fake server up=%v close=%v, want both true", fake.upCalled, fake.closeCalled)
	}
}
