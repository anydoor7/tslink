package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/credentials"
	"github.com/monody0007/tslink/internal/lifecycle"
	"github.com/monody0007/tslink/internal/output"
	"github.com/monody0007/tslink/internal/registry"
	"github.com/monody0007/tslink/internal/server"
	"github.com/monody0007/tslink/internal/tailapi"
	"github.com/monody0007/tslink/internal/testenv"
	"github.com/spf13/cobra"
	"github.com/zalando/go-keyring"
)

type cleanupDevicesContractFake struct {
	targets []tailapi.CleanupTarget
	result  tailapi.CleanupResult
	err     error
}

func (f cleanupDevicesContractFake) Cleanup(_ context.Context, targets []tailapi.CleanupTarget) (tailapi.CleanupResult, error) {
	if !reflect.DeepEqual(targets, f.targets) {
		return tailapi.CleanupResult{}, fmt.Errorf("targets = %+v, want %+v", targets, f.targets)
	}
	return f.result, f.err
}

type mockServer struct {
	runErr            error
	runCalled         bool
	credentialed      bool
	credentialModeSet bool
}

func (m *mockServer) Run(ctx context.Context) error {
	m.runCalled = true
	return m.runErr
}

func (m *mockServer) SetCredentialed(credentialed bool) {
	m.credentialed = credentialed
	m.credentialModeSet = true
}

type mockReadyServer struct {
	readyFn func() error
	runErr  error
}

func (m *mockReadyServer) SetReadyFunc(fn func() error) {
	m.readyFn = fn
}

func (m *mockReadyServer) Run(ctx context.Context) error {
	if m.readyFn == nil {
		return fmt.Errorf("ready function was not set")
	}
	if err := m.readyFn(); err != nil {
		return err
	}
	return m.runErr
}

type mockServerWithEnsureTags struct {
	ensureTagsFn server.EnsureTagsFunc
	runErr       error
}

type mockServerWithFunnelProvisioning struct {
	ensureFn  server.EnsureFunnelAttrFunc
	auto      bool
	autoSet   bool
	request   tailapi.FunnelPolicyRequest
	resultErr error
}

type mockServerWithLifecycle struct {
	reconcile server.LifecycleReconcileFunc
	runAt     time.Time
}

func (m *mockServerWithLifecycle) SetLifecycleReconcileFn(fn server.LifecycleReconcileFunc) {
	m.reconcile = fn
}

func (m *mockServerWithLifecycle) Run(ctx context.Context) error {
	if m.reconcile == nil {
		return fmt.Errorf("lifecycle reconciler was not set")
	}
	_, err := m.reconcile(ctx, m.runAt)
	return err
}

func (m *mockServerWithFunnelProvisioning) SetEnsureFunnelAttrFn(fn server.EnsureFunnelAttrFunc) {
	m.ensureFn = fn
}

func (m *mockServerWithFunnelProvisioning) SetAutoProvisionFunnel(enabled bool) {
	m.auto = enabled
	m.autoSet = true
}

func (m *mockServerWithFunnelProvisioning) Run(ctx context.Context) error {
	if m.ensureFn == nil {
		return fmt.Errorf("ensure Funnel function was not set")
	}
	_, err := m.ensureFn(ctx, m.request)
	if err != nil {
		return err
	}
	return m.resultErr
}

func (m *mockServerWithEnsureTags) SetEnsureTagsFn(fn server.EnsureTagsFunc) {
	m.ensureTagsFn = fn
}

func (m *mockServerWithEnsureTags) Run(ctx context.Context) error {
	if m.runErr != nil {
		return m.runErr
	}
	if m.ensureTagsFn == nil {
		return fmt.Errorf("ensure tags function was not set")
	}
	return m.ensureTagsFn(ctx, []string{"tag:hot"})
}

type mockServerWithAuthProvider struct {
	authProvider server.AuthKeyProvider
	service      registry.Service
}

type mockInteractiveServer struct {
	authProvider server.AuthKeyProvider
	authHandoff  server.AuthHandoffFunc
	service      registry.Service
	authURL      string
}

type mockInteractiveEnsureTagsServer struct {
	*mockInteractiveServer
	ensureTagsFn server.EnsureTagsFunc
}

func (m *mockInteractiveEnsureTagsServer) SetEnsureTagsFn(fn server.EnsureTagsFunc) {
	m.ensureTagsFn = fn
}

func (m *mockInteractiveEnsureTagsServer) Run(ctx context.Context) error {
	if m.ensureTagsFn == nil {
		return fmt.Errorf("ensure tags function was not set")
	}
	if err := m.ensureTagsFn(ctx, m.service.Tags); err != nil {
		return err
	}
	return m.mockInteractiveServer.Run(ctx)
}

func (m *mockInteractiveServer) SetAuthKeyProvider(fn server.AuthKeyProvider) {
	m.authProvider = fn
}

func (m *mockInteractiveServer) SetAuthHandoffFunc(fn server.AuthHandoffFunc) {
	m.authHandoff = fn
}

func (m *mockInteractiveServer) Run(ctx context.Context) error {
	if m.authProvider == nil || m.authHandoff == nil {
		return fmt.Errorf("interactive seams were not set")
	}
	authKey, err := m.authProvider(ctx, m.service)
	if err != nil {
		return err
	}
	if authKey != "" {
		return fmt.Errorf("interactive auth key = %q, want empty", authKey)
	}
	return m.authHandoff(ctx, server.AuthHandoff{Service: m.service.Name, AuthURL: m.authURL})
}

func (m *mockServerWithAuthProvider) SetAuthKeyProvider(fn server.AuthKeyProvider) {
	m.authProvider = fn
}

func (m *mockServerWithAuthProvider) Run(ctx context.Context) error {
	if m.authProvider == nil {
		return fmt.Errorf("auth key provider was not set")
	}
	_, err := m.authProvider(ctx, m.service)
	return err
}

// saveServeState saves all serve function variables and returns a cleanup func.
func saveServeState(t *testing.T) {
	t.Helper()
	old := struct {
		ensureDir          func() error
		migrate            func() bool
		registryPath       func() (string, error)
		loadRegistry       func(string) (*registry.Registry, error)
		getAuthKey         func(context.Context, credentials.AuthKeyOptions) (string, error)
		hasCredential      func() (bool, error)
		pidPath            func() (string, error)
		isRunning          func(string) bool
		isPIDRunning       func(int) bool
		ensureTags         func(context.Context, []string) error
		ensureFunnelAttr   server.EnsureFunnelAttrFunc
		cleanup            func(context.Context, []tailapi.CleanupTarget) (tailapi.CleanupResult, error)
		lifecycleReconcile func(context.Context, lifecycle.Options) (lifecycle.Result, error)
		loadGlobal         func() (config.GlobalConfig, error)
		logDir             func() (string, error)
		daemonize          func(string, string, string, bool, bool, bool) (int, error)
		readPID            func(string) (int, error)
		readyPath          func() (string, error)
		authHandoffPath    func() (string, error)
		writeReady         func(string, int) error
		readReady          func(string) (int, error)
		removeReady        func(string)
		saveAuthHandoff    func(string, authHandoffRecord) error
		loadAuthHandoff    func(string) (authHandoffRecord, error)
		removeAuthHandoff  func(string) error
		openBrowser        func(string) error
		ciEnvironmentSet   func() bool
		isTerminal         func() bool
		signalContext      func() (context.Context, context.CancelFunc)
		writePID           func(string) error
		removePID          func(string)
		withPIDLock        func(string, func() error) error
		newServer          func(string, string) (serverRunner, error)
		readyTimeout       time.Duration
		readyPoll          time.Duration
	}{
		serveEnsureDirFn, serveMigrateFn, serveRegistryPathFn, serveLoadRegistryFn,
		serveGetAuthKeyFn, serveHasStoredCredentialFn, servePIDPathFn, serveIsRunningFn, serveIsPIDRunningFn, serveEnsureTagsFn, serveEnsureFunnelAttrFn, serveCleanupFn, serveLifecycleReconcileFn,
		serveLoadGlobalFn, serveLogDirFn, serveDaemonizeFn, serveReadPIDFn,
		serveReadyPathFn, serveAuthHandoffPathFn, serveWriteReadyFn, serveReadReadyFn, serveRemoveReadyFn,
		serveSaveAuthHandoffFn, serveLoadAuthHandoffFn, serveRemoveAuthHandoffFn, serveOpenBrowserFn, serveCIEnvironmentSetFn, serveIsTerminalFn, serveSignalContextFn,
		serveWritePIDFn, serveRemovePIDFn, serveWithPIDLockFn, serveNewServerFn,
		serveDaemonReadyTimeout, serveDaemonReadyPollInterval,
	}
	t.Cleanup(func() {
		serveEnsureDirFn = old.ensureDir
		serveMigrateFn = old.migrate
		serveRegistryPathFn = old.registryPath
		serveLoadRegistryFn = old.loadRegistry
		serveGetAuthKeyFn = old.getAuthKey
		serveHasStoredCredentialFn = old.hasCredential
		servePIDPathFn = old.pidPath
		serveIsRunningFn = old.isRunning
		serveIsPIDRunningFn = old.isPIDRunning
		serveEnsureTagsFn = old.ensureTags
		serveEnsureFunnelAttrFn = old.ensureFunnelAttr
		serveCleanupFn = old.cleanup
		serveLifecycleReconcileFn = old.lifecycleReconcile
		serveLoadGlobalFn = old.loadGlobal
		serveLogDirFn = old.logDir
		serveDaemonizeFn = old.daemonize
		serveReadPIDFn = old.readPID
		serveReadyPathFn = old.readyPath
		serveAuthHandoffPathFn = old.authHandoffPath
		serveWriteReadyFn = old.writeReady
		serveReadReadyFn = old.readReady
		serveRemoveReadyFn = old.removeReady
		serveSaveAuthHandoffFn = old.saveAuthHandoff
		serveLoadAuthHandoffFn = old.loadAuthHandoff
		serveRemoveAuthHandoffFn = old.removeAuthHandoff
		serveOpenBrowserFn = old.openBrowser
		serveCIEnvironmentSetFn = old.ciEnvironmentSet
		serveIsTerminalFn = old.isTerminal
		serveSignalContextFn = old.signalContext
		serveWritePIDFn = old.writePID
		serveRemovePIDFn = old.removePID
		serveWithPIDLockFn = old.withPIDLock
		serveNewServerFn = old.newServer
		serveDaemonReadyTimeout = old.readyTimeout
		serveDaemonReadyPollInterval = old.readyPoll
		serveDaemon = false
	})
}

// mockServeDefaults sets all serve function variables to sane test defaults.
func mockServeDefaults(t *testing.T, dir string) {
	t.Helper()
	saveServeState(t)
	resetRootJSONFlag(t)
	cmd, _, err := rootCmd.Find([]string{"serve"})
	if err != nil {
		t.Fatalf("find serve command: %v", err)
	}
	resetCommandLocalFlags(t, cmd)

	regPath := filepath.Join(dir, "registry.json")
	pidPath := filepath.Join(dir, "tslink.pid")
	readyPath := filepath.Join(dir, "tslink.ready")

	// Write empty registry
	data, _ := json.Marshal(&registry.Registry{})
	os.WriteFile(regPath, data, 0600)

	keyring.MockInit()
	testenv.SetHome(t, dir)
	t.Cleanup(credentials.SetMutationLockPathForTesting(filepath.Join(dir, "credential-test.lock")))

	serveEnsureDirFn = func() error { return nil }
	serveMigrateFn = func() bool { return false }
	oldBackfill := serveBackfillCredentialMetaFn
	t.Cleanup(func() { serveBackfillCredentialMetaFn = oldBackfill })
	serveBackfillCredentialMetaFn = func() ([]string, error) { return nil, nil }
	serveRegistryPathFn = func() (string, error) { return regPath, nil }
	serveLoadRegistryFn = registry.Load
	serveGetAuthKeyFn = func(ctx context.Context, opts credentials.AuthKeyOptions) (string, error) {
		return "fake-auth-key", nil
	}
	serveHasStoredCredentialFn = func() (bool, error) { return true, nil }
	servePIDPathFn = func() (string, error) { return pidPath, nil }
	serveIsRunningFn = func(string) bool { return false }
	serveIsPIDRunningFn = func(int) bool { return true }
	serveEnsureTagsFn = func(ctx context.Context, tags []string) error { return nil }
	serveEnsureFunnelAttrFn = func(context.Context, tailapi.FunnelPolicyRequest) (tailapi.PolicyMutationResult, error) {
		return tailapi.PolicyMutationResult{WriteOutcome: tailapi.PolicyWriteUnchanged}, nil
	}
	serveCleanupFn = func(ctx context.Context, targets []tailapi.CleanupTarget) (tailapi.CleanupResult, error) {
		return tailapi.CleanupResult{}, nil
	}
	serveLoadGlobalFn = func() (config.GlobalConfig, error) { return config.GlobalConfig{}, nil }
	serveLogDirFn = func() (string, error) { return dir, nil }
	serveDaemonizeFn = func(out, err, controlURL string, manageACL, noAutoProvision, mcp bool) (int, error) {
		return 99999, nil
	}
	serveReadPIDFn = func(path string) (int, error) { return 99999, nil }
	serveReadyPathFn = func() (string, error) { return readyPath, nil }
	serveAuthHandoffPathFn = func() (string, error) { return filepath.Join(dir, "auth-handoff.json"), nil }
	serveWriteReadyFn = func(path string, pid int) error {
		return os.WriteFile(path, []byte(fmt.Sprintf("%d", pid)), 0600)
	}
	serveReadReadyFn = func(path string) (int, error) { return 99999, nil }
	serveRemoveReadyFn = func(path string) { os.Remove(path) }
	serveCIEnvironmentSetFn = func() bool { return false }
	serveWritePIDFn = func(path string) error { return os.WriteFile(path, []byte("12345"), 0600) }
	serveRemovePIDFn = func(path string) { os.Remove(path) }
	serveWithPIDLockFn = func(path string, fn func() error) error { return fn() }
	serveNewServerFn = func(authKey, controlURL string) (serverRunner, error) {
		return &mockServer{}, nil
	}
}

func TestMockServeDefaultsKeepsCredentialMutationLockInTempHome(t *testing.T) {
	dir := t.TempDir()
	mockServeDefaults(t, dir)
	if err := credentials.SetAPIKey("tskey-api-placeholder"); err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(dir, "credential-test.lock")
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("fake-keyring credential lock was not created inside temporary home %s: %v", lockPath, err)
	}
}

func mockServeDaemonReadyAfterInitialCheck(t *testing.T) {
	t.Helper()
	readyChecks := 0
	serveReadReadyFn = func(path string) (int, error) {
		readyChecks++
		if readyChecks == 1 {
			return 0, fmt.Errorf("not ready")
		}
		return 99999, nil
	}
	serveReadPIDFn = func(path string) (int, error) { return 99999, nil }
	serveIsPIDRunningFn = func(int) bool { return true }
}

func findServeCmd(t *testing.T) *cobra.Command {
	t.Helper()
	cmd, _, err := rootCmd.Find([]string{"serve"})
	if err != nil {
		t.Fatalf("find serve command: %v", err)
	}
	return cmd
}

// --- runForeground tests ---

func TestRunForeground_WritePIDError(t *testing.T) {
	saveServeState(t)
	serveWritePIDFn = func(path string) error { return fmt.Errorf("permission denied") }

	err := runForeground(filepath.Join(t.TempDir(), "test.pid"), "", "fake-key", "")
	if err == nil || err.Error() != "write PID: permission denied" {
		t.Fatalf("expected 'write PID: permission denied', got: %v", err)
	}
}

func TestRunForeground_ServerNewError(t *testing.T) {
	dir := t.TempDir()
	saveServeState(t)
	serveWritePIDFn = func(path string) error { return os.WriteFile(path, []byte("1"), 0600) }
	serveRemovePIDFn = func(path string) { os.Remove(path) }
	serveNewServerFn = func(authKey, controlURL string) (serverRunner, error) {
		return nil, fmt.Errorf("server init failed")
	}

	err := runForeground(filepath.Join(dir, "test.pid"), "", "fake-key", "")
	if err == nil || err.Error() != "server init failed" {
		t.Fatalf("expected 'server init failed', got: %v", err)
	}
}

func TestRunForeground_ServerRunError(t *testing.T) {
	dir := t.TempDir()
	saveServeState(t)
	serveWritePIDFn = func(path string) error { return os.WriteFile(path, []byte("1"), 0600) }
	serveRemovePIDFn = func(path string) { os.Remove(path) }
	serveNewServerFn = func(authKey, controlURL string) (serverRunner, error) {
		return &mockServer{runErr: fmt.Errorf("runtime error")}, nil
	}

	err := runForeground(filepath.Join(dir, "test.pid"), "", "fake-key", "")
	if err == nil || err.Error() != "runtime error" {
		t.Fatalf("expected 'runtime error', got: %v", err)
	}
}

func runForegroundWithInjectedContext(t *testing.T, ctx context.Context, runErr error) error {
	t.Helper()
	dir := t.TempDir()
	saveServeState(t)
	serveWritePIDFn = func(path string) error { return os.WriteFile(path, []byte("1"), 0o600) }
	serveRemovePIDFn = func(path string) { _ = os.Remove(path) }
	serveWithPIDLockFn = func(path string, fn func() error) error { return fn() }
	serveSignalContextFn = func() (context.Context, context.CancelFunc) { return ctx, func() {} }
	serveNewServerFn = func(authKey, controlURL string) (serverRunner, error) {
		return &mockServer{runErr: runErr}, nil
	}
	return runForeground(filepath.Join(dir, "test.pid"), "", "fake-key", "")
}

func TestRunForegroundTreatsShutdownCancellationAsSuccess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	runErr := fmt.Errorf("initial sync failed: %w", context.Canceled)
	if err := runForegroundWithInjectedContext(t, ctx, runErr); err != nil {
		t.Fatalf("runForeground() error = %v, want clean shutdown", err)
	}
}

func TestRunForegroundPreservesGenuineStartupFailureAfterShutdownRace(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	wantErr := errors.New("bind tcp 127.0.0.1:1: address unavailable")
	if err := runForegroundWithInjectedContext(t, ctx, wantErr); !errors.Is(err, wantErr) {
		t.Fatalf("runForeground() error = %v, want genuine startup failure %v", err, wantErr)
	}
}

func TestRunForegroundPreservesInternalCancellationWithoutShutdown(t *testing.T) {
	runErr := fmt.Errorf("initial sync failed: %w", context.Canceled)
	if err := runForegroundWithInjectedContext(t, context.Background(), runErr); !errors.Is(err, context.Canceled) {
		t.Fatalf("runForeground() error = %v, want internal cancellation failure", err)
	}
}

func TestRunForeground_AllowsPIDFileForCurrentProcess(t *testing.T) {
	dir := t.TempDir()
	saveServeState(t)
	pidPath := filepath.Join(dir, "test.pid")
	if err := os.WriteFile(pidPath, []byte(fmt.Sprintf("%d\n", os.Getpid())), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	serveIsRunningFn = func(path string) bool { return true }
	serveReadPIDFn = func(path string) (int, error) { return os.Getpid(), nil }
	wrote := false
	serveWritePIDFn = func(path string) error {
		wrote = true
		return os.WriteFile(path, []byte(fmt.Sprintf("%d\n", os.Getpid())), 0o600)
	}
	serveRemovePIDFn = func(path string) { os.Remove(path) }
	serveWithPIDLockFn = func(path string, fn func() error) error { return fn() }
	serveNewServerFn = func(authKey, controlURL string) (serverRunner, error) {
		return &mockServer{}, nil
	}

	if err := runForeground(pidPath, "", "fake-key", ""); err != nil {
		t.Fatalf("runForeground() error = %v", err)
	}
	if !wrote {
		t.Fatal("runForeground did not refresh current-process PID file")
	}
}

func TestRunForeground_WiresEnsureTagsFn(t *testing.T) {
	dir := t.TempDir()
	saveServeState(t)
	serveWritePIDFn = func(path string) error { return os.WriteFile(path, []byte("1"), 0600) }
	serveRemovePIDFn = func(path string) { os.Remove(path) }

	called := false
	serveEnsureTagsFn = func(ctx context.Context, tags []string) error {
		called = true
		if len(tags) != 1 || tags[0] != "tag:hot" {
			t.Fatalf("tags = %v, want [tag:hot]", tags)
		}
		return nil
	}
	serveNewServerFn = func(authKey, controlURL string) (serverRunner, error) {
		return &mockServerWithEnsureTags{}, nil
	}

	if err := runForeground(filepath.Join(dir, "test.pid"), "", "fake-key", ""); err != nil {
		t.Fatalf("runForeground() error = %v", err)
	}
	if !called {
		t.Fatal("serveEnsureTagsFn was not wired into server")
	}
}

func TestRunForegroundLifecycleWiringPreservesApplyAndManageACLOptIn(t *testing.T) {
	for _, tc := range []struct {
		name      string
		manageACL bool
	}{
		{name: "default does not manage ACL", manageACL: false},
		{name: "explicit opt-in manages ACL", manageACL: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			mockServeDefaults(t, dir)
			if err := config.EnsureDir(); err != nil {
				t.Fatal(err)
			}
			now := time.Date(2030, 8, 31, 12, 0, 0, 0, time.UTC)
			mock := &mockServerWithLifecycle{runAt: now}
			serveNewServerFn = func(string, string) (serverRunner, error) { return mock, nil }
			var got lifecycle.Options
			serveLifecycleReconcileFn = func(_ context.Context, options lifecycle.Options) (lifecycle.Result, error) {
				got = options
				return lifecycle.Result{}, nil
			}
			if err := runForegroundWithOptions(filepath.Join(dir, "test.pid"), "fake-key", "", foregroundOptions{
				Credentialed: true,
				ManageACL:    tc.manageACL,
			}); err != nil {
				t.Fatal(err)
			}
			if got.DryRun || got.ManageACL != tc.manageACL || got.CheckUnusedACL != tc.manageACL || !got.Now.Equal(now) {
				t.Fatalf("lifecycle options = %+v, manageACL=%t", got, tc.manageACL)
			}
		})
	}
}

func TestServeCmdPropagatesManageACLIntoLifecycleReconciler(t *testing.T) {
	for _, tc := range []struct {
		name      string
		manageACL bool
	}{
		{name: "default remains read only", manageACL: false},
		{name: "explicit opt in reaches lifecycle", manageACL: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv(config.ConfigDirEnv, dir)
			mockServeDefaults(t, dir)

			now := time.Date(2030, 9, 1, 12, 0, 0, 0, time.UTC)
			mock := &mockServerWithLifecycle{runAt: now}
			serveNewServerFn = func(string, string) (serverRunner, error) { return mock, nil }
			var got lifecycle.Options
			serveLifecycleReconcileFn = func(_ context.Context, options lifecycle.Options) (lifecycle.Result, error) {
				got = options
				return lifecycle.Result{}, nil
			}

			cmd := findServeCmd(t)
			if tc.manageACL {
				if err := cmd.Flags().Set("manage-acl", "true"); err != nil {
					t.Fatalf("set manage-acl: %v", err)
				}
			}
			if err := cmd.RunE(cmd, nil); err != nil {
				t.Fatalf("RunE() error = %v", err)
			}
			if got.ManageACL != tc.manageACL || got.CheckUnusedACL != tc.manageACL {
				t.Fatalf("lifecycle options = %+v, manageACL=%t", got, tc.manageACL)
			}
		})
	}
}

func TestRunForeground_DaemonChildWritesReadyAfterServerReady(t *testing.T) {
	dir := t.TempDir()
	saveServeState(t)
	pidPath := filepath.Join(dir, "test.pid")
	readyPath := filepath.Join(dir, "test.ready")

	serveIsRunningFn = func(path string) bool { return false }
	serveReadPIDFn = func(path string) (int, error) { return os.Getpid(), nil }
	serveWritePIDFn = func(path string) error {
		return os.WriteFile(path, []byte(fmt.Sprintf("%d\n", os.Getpid())), 0o600)
	}
	serveRemovePIDFn = func(path string) { os.Remove(path) }
	serveWithPIDLockFn = func(path string, fn func() error) error { return fn() }
	serveNewServerFn = func(authKey, controlURL string) (serverRunner, error) {
		return &mockReadyServer{}, nil
	}

	var readyPID int
	serveWriteReadyFn = func(path string, pid int) error {
		if path != readyPath {
			t.Fatalf("ready path = %q, want %q", path, readyPath)
		}
		readyPID = pid
		return nil
	}
	removedReady := false
	serveRemoveReadyFn = func(path string) {
		if path == readyPath {
			removedReady = true
		}
	}

	if err := runForeground(pidPath, readyPath, "fake-key", ""); err != nil {
		t.Fatalf("runForeground() error = %v", err)
	}
	if readyPID != os.Getpid() {
		t.Fatalf("ready PID = %d, want current pid %d", readyPID, os.Getpid())
	}
	if !removedReady {
		t.Fatal("ready file was not removed on foreground exit")
	}
}

// --- serve command RunE tests ---

func TestServeCmd_EnsureDirError(t *testing.T) {
	dir := t.TempDir()
	mockServeDefaults(t, dir)
	serveEnsureDirFn = func() error { return fmt.Errorf("mkdir failed") }

	cmd := findServeCmd(t)
	err := cmd.RunE(cmd, nil)
	if err == nil || !strings.Contains(err.Error(), "mkdir failed") {
		t.Fatalf("expected ensureDir error, got: %v", err)
	}
}

func TestServeCmd_RegistryPathError(t *testing.T) {
	dir := t.TempDir()
	mockServeDefaults(t, dir)
	serveRegistryPathFn = func() (string, error) { return "", fmt.Errorf("registry path error") }

	cmd := findServeCmd(t)
	err := cmd.RunE(cmd, nil)
	if err == nil || !strings.Contains(err.Error(), "registry path error") {
		t.Fatalf("expected registry path error, got: %v", err)
	}
}

func TestServeCmd_LoadRegistryError(t *testing.T) {
	dir := t.TempDir()
	mockServeDefaults(t, dir)
	serveLoadRegistryFn = func(path string) (*registry.Registry, error) {
		return nil, fmt.Errorf("corrupt registry")
	}

	cmd := findServeCmd(t)
	err := cmd.RunE(cmd, nil)
	if err == nil || !strings.Contains(err.Error(), "corrupt registry") {
		t.Fatalf("expected load registry error, got: %v", err)
	}
}

func TestServeCmd_AuthPreflightError(t *testing.T) {
	dir := t.TempDir()
	mockServeDefaults(t, dir)
	serveHasStoredCredentialFn = func() (bool, error) {
		return false, fmt.Errorf("credential inventory failed")
	}

	cmd := findServeCmd(t)
	err := cmd.RunE(cmd, nil)
	if err == nil || !strings.Contains(err.Error(), "credential inventory failed") {
		t.Fatalf("expected auth key error, got: %v", err)
	}
}

func TestServeCmd_ZeroCredentialSkipsAdminPathAndPresentsStableAuthURL(t *testing.T) {
	dir := t.TempDir()
	mockServeDefaults(t, dir)
	regPath := filepath.Join(dir, "registry.json")
	if _, err := registry.Add(regPath, registry.Service{
		Name:   "web",
		Type:   registry.TypeProxy,
		Target: "http://localhost:3000",
		Tags:   []string{"tag:tsmain"},
	}); err != nil {
		t.Fatalf("registry.Add() error = %v", err)
	}

	serveHasStoredCredentialFn = func() (bool, error) { return false, nil }
	serveGetAuthKeyFn = func(context.Context, credentials.AuthKeyOptions) (string, error) {
		t.Fatal("GetAuthKey called for zero-credential tier")
		return "", nil
	}
	serveEnsureTagsFn = func(context.Context, []string) error {
		t.Fatal("EnsureTags called for zero-credential tier")
		return nil
	}
	serveCleanupFn = func(context.Context, []tailapi.CleanupTarget) (tailapi.CleanupResult, error) {
		t.Fatal("administrative cleanup called for zero-credential tier")
		return tailapi.CleanupResult{}, nil
	}
	interactive := &mockInteractiveServer{
		service: registry.Service{Name: "web", Tags: []string{"tag:tsmain"}},
		authURL: "https://login.tailscale.com/a/test-auth",
	}
	serveNewServerFn = func(authKey, controlURL string) (serverRunner, error) {
		if authKey != "" {
			t.Fatalf("process auth key = %q, want empty", authKey)
		}
		return interactive, nil
	}
	serveIsTerminalFn = func() bool { return true }
	openedURL := ""
	serveOpenBrowserFn = func(authURL string) error {
		openedURL = authURL
		return nil
	}

	cmd := findServeCmd(t)
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("RunE() error = %v", err)
	}
	if openedURL != interactive.authURL {
		t.Fatalf("opened URL = %q, want %q", openedURL, interactive.authURL)
	}
	if !strings.Contains(stdout.String(), "Opened browser for Tailscale login") || !strings.Contains(stdout.String(), interactive.authURL) {
		t.Fatalf("stdout = %q, want truthful open confirmation and auth URL", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestServeCmd_ZeroCredentialServerEnsureTagsCallbackIsNoOp(t *testing.T) {
	dir := t.TempDir()
	mockServeDefaults(t, dir)
	ensureTagsCalled := false
	serveEnsureTagsFn = func(context.Context, []string) error {
		ensureTagsCalled = true
		return errors.New("ACL trap invoked for zero-credential serve")
	}
	regPath := filepath.Join(dir, "registry.json")
	if _, err := registry.Add(regPath, registry.Service{
		Name:   "web",
		Type:   registry.TypeProxy,
		Target: "http://localhost:3000",
		Tags:   []string{"tag:tsmain"},
	}); err != nil {
		t.Fatalf("registry.Add() error = %v", err)
	}

	serveHasStoredCredentialFn = func() (bool, error) { return false, nil }
	interactive := &mockInteractiveEnsureTagsServer{mockInteractiveServer: &mockInteractiveServer{
		service: registry.Service{Name: "web", Tags: []string{"tag:tsmain"}},
		authURL: "https://login.tailscale.com/a/no-acl-trap",
	}}
	serveNewServerFn = func(authKey, controlURL string) (serverRunner, error) {
		return interactive, nil
	}
	serveIsTerminalFn = func() bool { return false }

	cmd := findServeCmd(t)
	if err := cmd.Flags().Set("manage-acl", "true"); err != nil {
		t.Fatalf("set manage-acl: %v", err)
	}
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("RunE() error = %v, zero-credential server callback must ignore registry tags", err)
	}
	if ensureTagsCalled {
		t.Fatal("administrative EnsureTags callback ran for zero-credential serve")
	}
}

func TestPresentAuthHandoffNonInteractivePoliciesNeverOpenBrowser(t *testing.T) {
	dir := t.TempDir()
	mockServeDefaults(t, dir)
	record := newAuthHandoffRecord("web", "https://login.tailscale.com/a/test-auth", 4242)

	tests := []struct {
		name      string
		configure func(*cobra.Command)
	}{
		{
			name: "explicit no-browser",
			configure: func(cmd *cobra.Command) {
				_ = cmd.Flags().Set("no-browser", "true")
				serveIsTerminalFn = func() bool { return true }
			},
		},
		{
			name: "no tty",
			configure: func(cmd *cobra.Command) {
				serveIsTerminalFn = func() bool { return false }
			},
		},
		{
			name: "CI set",
			configure: func(cmd *cobra.Command) {
				serveCIEnvironmentSetFn = func() bool { return true }
				serveIsTerminalFn = func() bool { return true }
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cmd := findServeCmd(t)
			t.Cleanup(func() { _ = cmd.Flags().Set("no-browser", "false") })
			var stdout bytes.Buffer
			cmd.SetOut(&stdout)
			opened := false
			serveOpenBrowserFn = func(string) error {
				opened = true
				return nil
			}
			tc.configure(cmd)
			presentAuthHandoff(cmd, record)
			if opened {
				t.Fatal("browser opener called in non-interactive policy")
			}
			if !strings.Contains(stdout.String(), record.AuthURL) {
				t.Fatalf("stdout = %q, want printed auth URL", stdout.String())
			}
		})
	}
}

func TestPresentAuthHandoffCIEnvironmentSuppressesBrowser(t *testing.T) {
	dir := t.TempDir()
	mockServeDefaults(t, dir)
	serveCIEnvironmentSetFn = func() bool { return true }
	serveIsTerminalFn = func() bool { return true }
	opened := false
	serveOpenBrowserFn = func(string) error {
		opened = true
		return nil
	}
	record := newAuthHandoffRecord("web", "https://login.tailscale.com/a/test-auth", 4242)
	cmd := findServeCmd(t)
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)

	presentAuthHandoff(cmd, record)
	if opened {
		t.Fatal("browser opener called after CI detection")
	}
	if !strings.Contains(stdout.String(), record.AuthURL) {
		t.Fatalf("stdout = %q, want printed auth URL", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want no browser warning when opening was intentionally suppressed", stderr.String())
	}
}

func TestPresentAuthHandoffBrowserFailureFallsBackToURL(t *testing.T) {
	dir := t.TempDir()
	mockServeDefaults(t, dir)
	serveIsTerminalFn = func() bool { return true }
	serveOpenBrowserFn = func(string) error { return fmt.Errorf("opener unavailable") }
	record := newAuthHandoffRecord("web", "https://login.tailscale.com/a/test-auth", 4242)
	cmd := findServeCmd(t)
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)

	presentAuthHandoff(cmd, record)
	if !strings.Contains(stdout.String(), record.AuthURL) {
		t.Fatalf("stdout = %q, want fallback auth URL", stdout.String())
	}
	if !strings.Contains(stderr.String(), "Could not open a browser automatically") {
		t.Fatalf("stderr = %q, want best-effort browser warning", stderr.String())
	}
}

func TestServeCmd_PIDPathError(t *testing.T) {
	dir := t.TempDir()
	mockServeDefaults(t, dir)
	servePIDPathFn = func() (string, error) { return "", fmt.Errorf("pid path error") }

	cmd := findServeCmd(t)
	err := cmd.RunE(cmd, nil)
	if err == nil || !strings.Contains(err.Error(), "pid path error") {
		t.Fatalf("expected pid path error, got: %v", err)
	}
}

func TestServeCmd_AlreadyRunning(t *testing.T) {
	dir := t.TempDir()
	mockServeDefaults(t, dir)
	serveIsRunningFn = func(string) bool { return true }

	cmd := findServeCmd(t)
	err := cmd.RunE(cmd, nil)
	if err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("expected already running error, got: %v", err)
	}
}

func TestServeCmd_CleanupErrorDegradesAndStarts(t *testing.T) {
	dir := t.TempDir()
	mockServeDefaults(t, dir)

	serverStarted := false
	cleanupFake := cleanupDevicesContractFake{targets: []tailapi.CleanupTarget{}, err: fmt.Errorf("list devices: network down")}
	serveCleanupFn = cleanupFake.Cleanup
	serveNewServerFn = func(authKey, controlURL string) (serverRunner, error) {
		serverStarted = true
		return &mockServer{}, nil
	}

	cmd := findServeCmd(t)
	err := cmd.RunE(cmd, nil)
	if err != nil {
		t.Fatalf("RunE() error = %v, want cleanup degradation", err)
	}
	if !serverStarted {
		t.Fatal("server should start when opportunistic cleanup fails")
	}
}

func TestServeCmd_CleanupCredentialAndScopeFailuresDoNotKillServing(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{name: "OAuth secret format", err: errors.New("stored OAuth client secret has invalid format; expected tskey-client-<id>-<secret>")},
		{name: "devices read scope", err: errors.New("list devices rejected with HTTP 403 Forbidden")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			mockServeDefaults(t, dir)
			serveCleanupFn = func(context.Context, []tailapi.CleanupTarget) (tailapi.CleanupResult, error) {
				return tailapi.CleanupResult{}, tc.err
			}
			mock := &mockServer{}
			serveNewServerFn = func(string, string) (serverRunner, error) { return mock, nil }
			cmd := findServeCmd(t)
			if err := cmd.RunE(cmd, nil); err != nil {
				t.Fatalf("RunE() error = %v, want degraded cleanup", err)
			}
			if !mock.runCalled {
				t.Fatalf("server did not run after cleanup failure: %v", tc.err)
			}
		})
	}
}

func TestServeCmd_CleanupSkippedNoAPIClientStarts(t *testing.T) {
	dir := t.TempDir()
	mockServeDefaults(t, dir)

	serverStarted := false
	serveCleanupFn = func(ctx context.Context, targets []tailapi.CleanupTarget) (tailapi.CleanupResult, error) {
		return tailapi.CleanupResult{Skipped: true, SkipReason: tailapi.ErrNoAPIClient.Error()}, tailapi.ErrNoAPIClient
	}
	serveNewServerFn = func(authKey, controlURL string) (serverRunner, error) {
		serverStarted = true
		return &mockServer{}, nil
	}

	cmd := findServeCmd(t)
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("RunE() error = %v, want nil for skipped cleanup", err)
	}
	if !serverStarted {
		t.Fatal("server should start after no-client cleanup skip")
	}
}

func TestServeCmd_CleanupSkippedNoAPIClientNilErrorStarts(t *testing.T) {
	dir := t.TempDir()
	mockServeDefaults(t, dir)

	serverStarted := false
	serveCleanupFn = func(ctx context.Context, targets []tailapi.CleanupTarget) (tailapi.CleanupResult, error) {
		return tailapi.CleanupResult{Skipped: true, SkipReason: tailapi.ErrNoAPIClient.Error()}, nil
	}
	serveNewServerFn = func(authKey, controlURL string) (serverRunner, error) {
		serverStarted = true
		return &mockServer{}, nil
	}

	cmd := findServeCmd(t)
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("RunE() error = %v, want nil for skipped cleanup", err)
	}
	if !serverStarted {
		t.Fatal("server should start after no-client cleanup skip")
	}
}

func TestServeCmd_CleanupUsesServiceTargets(t *testing.T) {
	dir := t.TempDir()
	mockServeDefaults(t, dir)

	regPath := filepath.Join(dir, "registry.json")
	if _, err := registry.Add(regPath, registry.Service{
		Name:   "web",
		Type:   registry.TypeProxy,
		Target: "http://localhost:3000",
		Tags:   []string{"tag:web"},
	}); err != nil {
		t.Fatalf("registry.Add() error = %v", err)
	}

	var gotTargets []tailapi.CleanupTarget
	serveCleanupFn = func(ctx context.Context, targets []tailapi.CleanupTarget) (tailapi.CleanupResult, error) {
		gotTargets = append(gotTargets, targets...)
		return tailapi.CleanupResult{}, nil
	}

	cmd := findServeCmd(t)
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("RunE() error = %v", err)
	}
	if len(gotTargets) != 1 {
		t.Fatalf("cleanup targets = %+v, want one target", gotTargets)
	}
	if gotTargets[0].Hostname != "web" || strings.Join(gotTargets[0].Tags, ",") != "tag:web" {
		t.Fatalf("cleanup target = %+v, want service hostname and tags", gotTargets[0])
	}
}

func TestServeCmd_MigrationMessage(t *testing.T) {
	dir := t.TempDir()
	mockServeDefaults(t, dir)
	serveMigrateFn = func() bool { return true }
	// Make it fail after migration message so we can check output
	serveGetAuthKeyFn = func(ctx context.Context, opts credentials.AuthKeyOptions) (string, error) {
		return "", fmt.Errorf("stop here")
	}

	cmd := findServeCmd(t)
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	_ = cmd.RunE(cmd, nil)
	if !strings.Contains(buf.String(), "migrated API key") {
		t.Fatalf("expected migration message, got: %s", buf.String())
	}
}

func TestServeCmd_JSONMigrationWritesExactlyOneEnvelope(t *testing.T) {
	dir := t.TempDir()
	mockServeDefaults(t, dir)
	serveMigrateFn = func() bool { return true }
	serveDaemon = true
	mockServeDaemonReadyAfterInitialCheck(t)

	cmd := findServeCmd(t)
	setRootJSONFlag(t, true)
	var commandOut, commandErr bytes.Buffer
	cmd.SetOut(&commandOut)
	cmd.SetErr(&commandErr)
	raw := captureStdout(t, func() {
		if err := cmd.RunE(cmd, nil); err != nil {
			t.Fatalf("RunE() error = %v", err)
		}
	})
	wantBytes, err := json.Marshal(output.NewSuccess("serve", ServeResult{
		Daemon:             true,
		PID:                99999,
		CredentialMigrated: true,
	}))
	if err != nil {
		t.Fatal(err)
	}
	want := string(wantBytes) + "\n"
	if raw != want || commandOut.Len() != 0 || commandErr.Len() != 0 {
		t.Fatalf("stdout=%q want=%q commandOut=%q commandErr=%q", raw, want, commandOut.String(), commandErr.String())
	}
}

func TestServeCmd_WithTagsAndEphemeral(t *testing.T) {
	dir := t.TempDir()
	mockServeDefaults(t, dir)

	// Write registry with tags and ephemeral
	reg := &registry.Registry{
		Services: []registry.Service{
			{Name: "svc1", Type: "proxy", Target: "http://localhost:3000", Tags: []string{"tag:web"}, Ephemeral: true},
		},
	}
	data, _ := json.Marshal(reg)
	regPath := filepath.Join(dir, "registry.json")
	os.WriteFile(regPath, data, 0600)

	var capturedOpts credentials.AuthKeyOptions
	serveGetAuthKeyFn = func(ctx context.Context, opts credentials.AuthKeyOptions) (string, error) {
		capturedOpts = opts
		return "fake-key", nil
	}
	serveNewServerFn = func(authKey, controlURL string) (serverRunner, error) {
		if authKey != "" {
			t.Fatalf("process authKey = %q, want empty static key", authKey)
		}
		return &mockServerWithAuthProvider{service: reg.Services[0]}, nil
	}

	cmd := findServeCmd(t)
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("RunE() error = %v", err)
	}

	if !capturedOpts.Ephemeral {
		t.Error("expected ephemeral=true")
	}
	if len(capturedOpts.Tags) != 1 || capturedOpts.Tags[0] != "tag:web" {
		t.Errorf("expected tags=[tag:web], got: %v", capturedOpts.Tags)
	}
	if !strings.Contains(capturedOpts.Description, "svc1") {
		t.Errorf("description = %q, want service name", capturedOpts.Description)
	}
	if capturedOpts.ClientFactory == nil {
		t.Fatal("auth-key client factory = nil, want tailapi loopback override gate")
	}
	t.Setenv(tailapi.APIBaseURLEnv, "http://127.0.0.1:1")
	t.Setenv("TSLINK_API_KEY", "test-placeholder")
	client, err := capturedOpts.ClientFactory()
	if err != nil {
		t.Fatalf("auth-key client factory error = %v", err)
	}
	if client == nil || client.BaseURL == nil {
		t.Fatal("auth-key client BaseURL = nil, want loopback override")
	}
	if got := client.BaseURL.String(); got != "http://127.0.0.1:1" {
		t.Fatalf("auth-key client BaseURL = %q, want loopback override", got)
	}
}

func TestServeCmd_ControlURLFromConfig(t *testing.T) {
	dir := t.TempDir()
	mockServeDefaults(t, dir)
	serveLoadGlobalFn = func() (config.GlobalConfig, error) {
		return config.GlobalConfig{ControlURL: "https://headscale.example.com"}, nil
	}

	var capturedControlURL string
	serveNewServerFn = func(authKey, controlURL string) (serverRunner, error) {
		capturedControlURL = controlURL
		return &mockServer{}, nil
	}

	cmd := findServeCmd(t)
	_ = cmd.RunE(cmd, nil)

	if capturedControlURL != "https://headscale.example.com" {
		t.Errorf("expected controlURL from config, got: %s", capturedControlURL)
	}
}

func TestServeCmd_InvalidFlagControlURLFailsBeforeDaemonize(t *testing.T) {
	dir := t.TempDir()
	mockServeDefaults(t, dir)
	serveDaemon = true

	cmd := findServeCmd(t)
	if err := cmd.Flags().Set("control-url", "/control"); err != nil {
		t.Fatalf("set control-url flag: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Flags().Set("control-url", "") })

	daemonizeCalled := false
	serveDaemonizeFn = func(out, errLog, controlURL string, manageACL, noAutoProvision, mcp bool) (int, error) {
		daemonizeCalled = true
		return 0, fmt.Errorf("daemonize should not be called")
	}
	serveNewServerFn = func(authKey, controlURL string) (serverRunner, error) {
		t.Fatal("server startup should not be reached")
		return nil, nil
	}

	err := cmd.RunE(cmd, nil)
	if err == nil {
		t.Fatal("RunE() error = nil, want invalid control-url error")
	}
	if !strings.Contains(err.Error(), "invalid control-url") || !strings.Contains(err.Error(), "invalid URL") {
		t.Fatalf("RunE() error = %v, want invalid control-url URL error", err)
	}
	if daemonizeCalled {
		t.Fatal("daemonize was called for invalid control-url")
	}
}

func TestServeCmd_InvalidPersistedControlURLFailsBeforeServerStartup(t *testing.T) {
	dir := t.TempDir()
	mockServeDefaults(t, dir)
	serveLoadGlobalFn = func() (config.GlobalConfig, error) {
		return config.GlobalConfig{ControlURL: "/control"}, nil
	}

	serverCalled := false
	serveNewServerFn = func(authKey, controlURL string) (serverRunner, error) {
		serverCalled = true
		return &mockServer{}, nil
	}

	cmd := findServeCmd(t)
	err := cmd.RunE(cmd, nil)
	if err == nil {
		t.Fatal("RunE() error = nil, want invalid control-url error")
	}
	if !strings.Contains(err.Error(), "invalid control-url") || !strings.Contains(err.Error(), "invalid URL") {
		t.Fatalf("RunE() error = %v, want invalid control-url URL error", err)
	}
	if serverCalled {
		t.Fatal("server startup was reached for invalid persisted control-url")
	}
}

func TestServeCmd_DaemonMode(t *testing.T) {
	dir := t.TempDir()
	mockServeDefaults(t, dir)
	serveDaemon = true
	mockServeDaemonReadyAfterInitialCheck(t)

	cmd := findServeCmd(t)
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	err := cmd.RunE(cmd, nil)
	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
	if !strings.Contains(buf.String(), "tslink started as daemon") {
		t.Fatalf("expected daemon message, got: %s", buf.String())
	}
}

func TestServeCmd_DaemonModePropagatesNoAutoProvision(t *testing.T) {
	dir := t.TempDir()
	mockServeDefaults(t, dir)
	serveDaemon = true
	mockServeDaemonReadyAfterInitialCheck(t)

	var captured bool
	serveDaemonizeFn = func(out, errLog, controlURL string, manageACL, noAutoProvision, mcp bool) (int, error) {
		captured = noAutoProvision
		return 99999, nil
	}
	cmd := findServeCmd(t)
	if err := cmd.Flags().Set("no-auto-provision", "true"); err != nil {
		t.Fatalf("set no-auto-provision: %v", err)
	}
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("RunE() error = %v", err)
	}
	if !captured {
		t.Fatal("daemon child did not receive --no-auto-provision")
	}
}

func TestServeCmd_JSONZeroCredentialReturnsImmediateAuthHandoff(t *testing.T) {
	dir := t.TempDir()
	mockServeDefaults(t, dir)
	serveHasStoredCredentialFn = func() (bool, error) { return false, nil }
	serveReadReadyFn = func(string) (int, error) { return 0, os.ErrNotExist }
	serveReadPIDFn = func(string) (int, error) { return 99999, nil }
	serveIsPIDRunningFn = func(int) bool { return true }
	spawned := false
	serveDaemonizeFn = func(out, errLog, controlURL string, manageACL, noAutoProvision, mcp bool) (int, error) {
		spawned = true
		return 99999, nil
	}
	fixedNow := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	oldNow := authHandoffNowFn
	authHandoffNowFn = func() time.Time { return fixedNow }
	t.Cleanup(func() { authHandoffNowFn = oldNow })
	record := newAuthHandoffRecord("web", "https://login.tailscale.com/a/json-auth", 99999)
	serveLoadAuthHandoffFn = func(string) (authHandoffRecord, error) { return record, nil }
	opened := false
	serveIsTerminalFn = func() bool { return true }
	serveOpenBrowserFn = func(string) error {
		opened = true
		return nil
	}

	cmd := findServeCmd(t)
	setRootJSONFlag(t, true)
	raw := captureStdout(t, func() {
		if err := cmd.RunE(cmd, nil); err != nil {
			t.Fatalf("RunE() error = %v", err)
		}
	})

	var response struct {
		Type          string          `json:"type"`
		OK            bool            `json:"ok"`
		SchemaVersion int             `json:"schema_version"`
		Command       string          `json:"command"`
		Code          int             `json:"code"`
		Data          serveAuthResult `json:"data"`
	}
	if err := json.Unmarshal([]byte(raw), &response); err != nil {
		t.Fatalf("unmarshal response: %v\nraw: %s", err, raw)
	}
	if !spawned {
		t.Fatal("zero-credential JSON serve did not keep a daemon child alive")
	}
	if opened {
		t.Fatal("JSON mode attempted to open a browser")
	}
	if response.Type != output.SchemaType || !response.OK || response.SchemaVersion != 1 || response.Command != "serve" || response.Code != 0 {
		t.Fatalf("envelope = %+v, want successful serve schema v1", response)
	}
	if response.Data.Status != authStatusNeedsLogin || response.Data.AuthURL != record.AuthURL || response.Data.Poll != "tslink status --json" {
		t.Fatalf("data = %+v, want stable needs_login handoff", response.Data)
	}
	if !response.Data.ExpiresAt.Equal(fixedNow.Add(authHandoffConservativeLifetime)) {
		t.Fatalf("expires_at = %s, want conservative expiry", response.Data.ExpiresAt)
	}
}

func TestServeCmd_JSONCredentialedStaysForeground(t *testing.T) {
	dir := t.TempDir()
	mockServeDefaults(t, dir)
	serveHasStoredCredentialFn = func() (bool, error) { return true, nil }
	serveDaemonizeFn = func(string, string, string, bool, bool, bool) (int, error) {
		t.Fatal("credentialed JSON serve was daemonized")
		return 0, nil
	}
	foreground := &mockServer{}
	serveNewServerFn = func(authKey, controlURL string) (serverRunner, error) {
		return foreground, nil
	}

	cmd := findServeCmd(t)
	setRootJSONFlag(t, true)
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("RunE() error = %v", err)
	}
	if !foreground.runCalled {
		t.Fatal("credentialed JSON serve did not run the foreground server")
	}
	if !foreground.credentialModeSet || !foreground.credentialed {
		t.Fatalf("credential mode set=%v credentialed=%v, want stored-credential tier", foreground.credentialModeSet, foreground.credentialed)
	}
}

func TestServeCmd_DaemonConflictPreservesLiveAuthHandoff(t *testing.T) {
	dir := t.TempDir()
	mockServeDefaults(t, dir)
	serveDaemon = true
	serveIsRunningFn = func(string) bool { return true }

	readyRemoved := false
	handoffRemoved := false
	serveRemoveReadyFn = func(string) { readyRemoved = true }
	serveRemoveAuthHandoffFn = func(string) error {
		handoffRemoved = true
		return nil
	}
	serveDaemonizeFn = func(string, string, string, bool, bool, bool) (int, error) {
		t.Fatal("daemonize called despite live daemon conflict")
		return 0, nil
	}

	cmd := findServeCmd(t)
	err := cmd.RunE(cmd, nil)
	if err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("RunE() error = %v, want live daemon conflict", err)
	}
	if readyRemoved || handoffRemoved {
		t.Fatalf("startup signal removal = ready:%v handoff:%v, want live daemon evidence preserved", readyRemoved, handoffRemoved)
	}
}

func TestRunForegroundZeroCredentialPublishesAuthHandoff(t *testing.T) {
	dir := t.TempDir()
	mockServeDefaults(t, dir)
	interactive := &mockInteractiveServer{
		service: registry.Service{Name: "web", Tags: []string{"tag:tsmain"}},
		authURL: "https://login.tailscale.com/a/child-auth",
	}
	serveNewServerFn = func(authKey, controlURL string) (serverRunner, error) {
		if authKey != "" {
			t.Fatalf("process auth key = %q, want empty", authKey)
		}
		return interactive, nil
	}
	serveGetAuthKeyFn = func(context.Context, credentials.AuthKeyOptions) (string, error) {
		t.Fatal("GetAuthKey called for zero-credential foreground child")
		return "", nil
	}
	var saved authHandoffRecord
	serveSaveAuthHandoffFn = func(path string, record authHandoffRecord) error {
		if path != filepath.Join(dir, "auth-handoff.json") {
			t.Fatalf("auth handoff path = %q", path)
		}
		saved = record
		return nil
	}
	var presented authHandoffRecord

	err := runForegroundWithOptions(filepath.Join(dir, "tslink.pid"), "", "", foregroundOptions{
		AuthHandoffPath: filepath.Join(dir, "auth-handoff.json"),
		Credentialed:    false,
		PresentAuth:     func(record authHandoffRecord) { presented = record },
	})
	if err != nil {
		t.Fatalf("runForegroundWithOptions() error = %v", err)
	}
	if saved.Status != authStatusNeedsLogin || saved.Service != "web" || saved.AuthURL != interactive.authURL || saved.DaemonPID != os.Getpid() {
		t.Fatalf("saved handoff = %+v, want child-owned needs_login record", saved)
	}
	if presented.AuthURL != saved.AuthURL || presented.Service != saved.Service {
		t.Fatalf("presented handoff = %+v, want saved handoff %+v", presented, saved)
	}
}

func TestServeCmd_DaemonModeDoesNotDeletePIDAfterGuardAllows(t *testing.T) {
	dir := t.TempDir()
	mockServeDefaults(t, dir)
	serveDaemon = true
	mockServeDaemonReadyAfterInitialCheck(t)

	pidPath, err := servePIDPathFn()
	if err != nil {
		t.Fatalf("servePIDPathFn() error = %v", err)
	}
	identityPath := pidPath + ".identity"
	if err := os.WriteFile(pidPath, []byte(fmt.Sprintf("%d\n", os.Getpid())), 0o600); err != nil {
		t.Fatalf("WriteFile(pid) error = %v", err)
	}
	if err := os.WriteFile(identityPath, []byte("live-daemon-evidence\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(identity) error = %v", err)
	}

	removeCalls := 0
	serveRemovePIDFn = func(string) { removeCalls++ }
	serveDaemonizeFn = func(out, errLog, controlURL string, manageACL, noAutoProvision, mcp bool) (int, error) {
		for _, path := range []string{pidPath, identityPath} {
			if _, err := os.Stat(path); err != nil {
				t.Fatalf("daemonize observed deleted live-daemon evidence %q: %v", path, err)
			}
		}
		return 99999, nil
	}

	cmd := findServeCmd(t)
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("RunE() error = %v", err)
	}
	if removeCalls != 0 {
		t.Fatalf("serveRemovePIDFn calls = %d, want 0 after an inconclusive guard result", removeCalls)
	}
	for _, path := range []string{pidPath, identityPath} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("live-daemon evidence %q was not preserved: %v", path, err)
		}
	}
}

func TestServeCmd_DaemonModeForwardsControlURL(t *testing.T) {
	dir := t.TempDir()
	mockServeDefaults(t, dir)
	serveDaemon = true
	mockServeDaemonReadyAfterInitialCheck(t)

	cmd := findServeCmd(t)
	if err := cmd.Flags().Set("control-url", "https://headscale.example.com"); err != nil {
		t.Fatalf("set control-url flag: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Flags().Set("control-url", "") })

	var capturedControlURL string
	serveDaemonizeFn = func(out, errLog, controlURL string, manageACL, noAutoProvision, mcp bool) (int, error) {
		capturedControlURL = controlURL
		return 99999, nil
	}

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("RunE() error = %v", err)
	}
	if capturedControlURL != "https://headscale.example.com" {
		t.Fatalf("controlURL = %q, want explicit flag value", capturedControlURL)
	}
}

// TestServeCmd_DaemonModePropagatesManageACL is the serve-level guard:
// `serve --daemon` must pass its --manage-acl opt-in through to the
// daemonize child, and default daemon mode must NOT opt in. The old daemon
// branch never forwarded the flag, so `serve --daemon --manage-acl` was a silent
// no-op.
func TestServeCmd_DaemonModePropagatesManageACL(t *testing.T) {
	cases := []struct {
		name     string
		setFlag  bool
		wantSent bool
	}{
		{"default daemon mode does not opt in", false, false},
		{"opted-in daemon mode carries the flag", true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			mockServeDefaults(t, dir)
			serveDaemon = true
			mockServeDaemonReadyAfterInitialCheck(t)

			cmd := findServeCmd(t)
			if tc.setFlag {
				if err := cmd.Flags().Set("manage-acl", "true"); err != nil {
					t.Fatalf("set manage-acl flag: %v", err)
				}
				t.Cleanup(func() { _ = cmd.Flags().Set("manage-acl", "false") })
			}

			var captured bool
			serveDaemonizeFn = func(out, errLog, controlURL string, manageACL, noAutoProvision, mcp bool) (int, error) {
				captured = manageACL
				return 99999, nil
			}

			var buf bytes.Buffer
			cmd.SetOut(&buf)
			if err := cmd.RunE(cmd, nil); err != nil {
				t.Fatalf("RunE() error = %v", err)
			}
			if captured != tc.wantSent {
				t.Fatalf("daemonize manageACL = %v, want %v", captured, tc.wantSent)
			}
		})
	}
}

func TestServeCmd_DaemonModeWaitsForBusinessReadySignal(t *testing.T) {
	dir := t.TempDir()
	mockServeDefaults(t, dir)
	serveDaemon = true
	serveDaemonReadyTimeout = 100 * time.Millisecond
	serveDaemonReadyPollInterval = time.Millisecond

	attempts := 0
	serveReadReadyFn = func(path string) (int, error) {
		attempts++
		if attempts < 3 {
			return 0, fmt.Errorf("not ready")
		}
		return 99999, nil
	}
	serveReadPIDFn = func(path string) (int, error) { return 99999, nil }
	serveIsPIDRunningFn = func(pid int) bool { return true }

	cmd := findServeCmd(t)
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("RunE() error = %v", err)
	}
	if attempts < 3 {
		t.Fatalf("readiness attempts = %d, want at least 3", attempts)
	}
	if !strings.Contains(buf.String(), "tslink started as daemon") {
		t.Fatalf("expected daemon success after readiness, got: %s", buf.String())
	}
}

func TestServeCmd_DaemonReadinessFailureDoesNotReportSuccess(t *testing.T) {
	dir := t.TempDir()
	mockServeDefaults(t, dir)
	serveDaemon = true
	serveDaemonReadyTimeout = time.Millisecond
	serveDaemonReadyPollInterval = time.Millisecond
	serveReadReadyFn = func(path string) (int, error) {
		return 0, fmt.Errorf("ready file missing")
	}
	serveIsPIDRunningFn = func(pid int) bool { return true }

	cmd := findServeCmd(t)
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	err := cmd.RunE(cmd, nil)
	if err == nil {
		t.Fatal("RunE() error = nil, want readiness failure")
	}
	if !strings.Contains(err.Error(), "daemon startup did not complete") || !strings.Contains(err.Error(), "tslink.err.log") {
		t.Fatalf("RunE() error = %v, want readiness failure with log paths", err)
	}
	if strings.Contains(buf.String(), "tslink started as daemon") {
		t.Fatalf("reported daemon success despite readiness failure: %s", buf.String())
	}
}

func TestServeCmd_DaemonChildExitBeforeReadyDoesNotReportSuccess(t *testing.T) {
	failures := []string{
		"credential preflight failure",
		"initial sync failure",
		"listener failure",
		"child exit before ready",
	}
	for _, name := range failures {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			mockServeDefaults(t, dir)
			serveDaemon = true
			serveDaemonReadyTimeout = 100 * time.Millisecond
			serveDaemonReadyPollInterval = time.Millisecond
			serveReadReadyFn = func(path string) (int, error) {
				return 0, fmt.Errorf("%s: no ready signal", name)
			}
			serveIsPIDRunningFn = func(pid int) bool { return false }

			cmd := findServeCmd(t)
			var buf bytes.Buffer
			cmd.SetOut(&buf)
			err := cmd.RunE(cmd, nil)
			if err == nil {
				t.Fatal("RunE() error = nil, want daemon startup failure")
			}
			if !strings.Contains(err.Error(), "exited before readiness") {
				t.Fatalf("RunE() error = %v, want child-exit-before-ready error", err)
			}
			if strings.Contains(buf.String(), "tslink started as daemon") {
				t.Fatalf("reported daemon success despite %s: %s", name, buf.String())
			}
		})
	}
}

func TestServeCmd_DaemonReadinessPIDMatchRequiresRunningProcess(t *testing.T) {
	dir := t.TempDir()
	mockServeDefaults(t, dir)
	serveDaemon = true
	serveDaemonReadyTimeout = 3 * time.Millisecond
	serveDaemonReadyPollInterval = time.Millisecond

	runningChecks := 0
	serveIsPIDRunningFn = func(int) bool {
		runningChecks++
		return false
	}
	serveReadPIDFn = func(path string) (int, error) {
		return 99999, nil
	}
	serveReadReadyFn = func(path string) (int, error) {
		return 99999, nil
	}

	cmd := findServeCmd(t)
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	err := cmd.RunE(cmd, nil)
	if err == nil {
		t.Fatal("RunE() error = nil, want readiness failure")
	}
	if !strings.Contains(err.Error(), "exited before readiness") && !strings.Contains(err.Error(), "not running") {
		t.Fatalf("RunE() error = %v, want daemon liveness readiness failure", err)
	}
	if runningChecks == 0 {
		t.Fatalf("running checks = %d, want readiness liveness confirmation", runningChecks)
	}
	if strings.Contains(buf.String(), "tslink started as daemon") {
		t.Fatalf("reported daemon success despite failed liveness: %s", buf.String())
	}
}

func TestServeCmd_DaemonModeSkipsHeavyweightParentPreflight(t *testing.T) {
	dir := t.TempDir()
	mockServeDefaults(t, dir)
	serveDaemon = true
	mockServeDaemonReadyAfterInitialCheck(t)

	calls := 0
	serveEnsureTagsFn = func(ctx context.Context, tags []string) error {
		calls++
		return fmt.Errorf("ensure tags should not be called in daemon parent")
	}
	serveHasStoredCredentialFn = func() (bool, error) {
		calls++
		return false, fmt.Errorf("auth check should not be called in daemon parent")
	}
	serveCleanupFn = func(ctx context.Context, targets []tailapi.CleanupTarget) (tailapi.CleanupResult, error) {
		calls++
		return tailapi.CleanupResult{}, fmt.Errorf("cleanup should not be called in daemon parent")
	}

	cmd := findServeCmd(t)
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("RunE() error = %v", err)
	}
	if calls != 0 {
		t.Fatalf("heavyweight parent preflight calls = %d, want 0", calls)
	}
}

func TestServeCmd_DaemonModeValidatesRegistryBeforeDaemonize(t *testing.T) {
	dir := t.TempDir()
	mockServeDefaults(t, dir)
	serveDaemon = true

	reg := &registry.Registry{
		Services: []registry.Service{
			{
				Name:         "public-app",
				Type:         registry.TypeProxy,
				Target:       "http://localhost:3000",
				Funnel:       true,
				AllowedUsers: []string{"alice@example.com"},
			},
		},
	}
	data, _ := json.Marshal(reg)
	if err := os.WriteFile(filepath.Join(dir, "registry.json"), data, 0o600); err != nil {
		t.Fatalf("write registry: %v", err)
	}

	daemonizeCalled := false
	serveDaemonizeFn = func(out, errLog, controlURL string, manageACL, noAutoProvision, mcp bool) (int, error) {
		daemonizeCalled = true
		return 0, fmt.Errorf("daemonize should not be called")
	}
	removePIDCalled := false
	serveRemovePIDFn = func(path string) {
		removePIDCalled = true
	}

	cmd := findServeCmd(t)
	err := cmd.RunE(cmd, nil)
	if err == nil {
		t.Fatal("RunE() error = nil, want funnel allowed_users error")
	}
	if !strings.Contains(err.Error(), registry.ErrFunnelAllowedUsers) {
		t.Fatalf("RunE() error = %v, want funnel allowed_users error", err)
	}
	if !strings.Contains(err.Error(), registry.CodeFunnelAllowConflict) {
		t.Fatalf("RunE() error = %v, want stable code %s", err, registry.CodeFunnelAllowConflict)
	}
	if code, ok := registry.ErrorCode(err); !ok || code != registry.CodeFunnelAllowConflict {
		t.Fatalf("ErrorCode() = %q, %v; want %s, true", code, ok, registry.CodeFunnelAllowConflict)
	}
	if daemonizeCalled {
		t.Fatal("daemonize was called after invalid registry")
	}
	if removePIDCalled {
		t.Fatal("stale PID was removed before hard-fail registry validation")
	}
}

func TestLoadValidatedRegistryForServeSkipsLegacyFunnelMissingPublicAck(t *testing.T) {
	dir := t.TempDir()
	mockServeDefaults(t, dir)

	regPath, err := serveRegistryPathFn()
	if err != nil {
		t.Fatalf("serveRegistryPathFn() error = %v", err)
	}
	reg := &registry.Registry{
		Services: []registry.Service{
			{
				Name:   "public-app",
				Type:   registry.TypeProxy,
				Target: "http://localhost:3000",
				Funnel: true,
			},
			{
				Name:   "valid-app",
				Type:   registry.TypeProxy,
				Target: "http://localhost:3001",
			},
		},
	}
	data, _ := json.Marshal(reg)
	if err := os.WriteFile(regPath, data, 0o600); err != nil {
		t.Fatalf("write registry: %v", err)
	}

	var logBuf bytes.Buffer
	oldLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, nil)))
	t.Cleanup(func() { slog.SetDefault(oldLogger) })

	got, err := loadValidatedRegistryForServe()
	if err != nil {
		t.Fatalf("loadValidatedRegistryForServe() error = %v", err)
	}
	if len(got.Services) != 1 || got.Services[0].Name != "valid-app" {
		t.Fatalf("services = %+v, want only valid-app", got.Services)
	}
	logs := logBuf.String()
	if !strings.Contains(logs, "skipping service with invalid startup config") ||
		!strings.Contains(logs, "public-app") ||
		!strings.Contains(logs, "tslink add public-app --funnel --public") {
		t.Fatalf("logs = %s, want skip warning with service name and remediation", logs)
	}
}

func TestServeCmd_DaemonModeFunnelControlURLIncludesStableCode(t *testing.T) {
	dir := t.TempDir()
	mockServeDefaults(t, dir)
	serveDaemon = true

	reg := &registry.Registry{
		Services: []registry.Service{
			{
				Name:       "public-app",
				Type:       registry.TypeProxy,
				Target:     "http://localhost:3000",
				Funnel:     true,
				ControlURL: "https://headscale.example.com",
			},
		},
	}
	data, _ := json.Marshal(reg)
	if err := os.WriteFile(filepath.Join(dir, "registry.json"), data, 0o600); err != nil {
		t.Fatalf("write registry: %v", err)
	}

	cmd := findServeCmd(t)
	err := cmd.RunE(cmd, nil)
	if err == nil {
		t.Fatal("RunE() error = nil, want funnel control_url error")
	}
	if !strings.Contains(err.Error(), registry.ErrFunnelControlURL) {
		t.Fatalf("RunE() error = %v, want funnel control_url error", err)
	}
	if !strings.Contains(err.Error(), registry.CodeFunnelControlURLConflict) {
		t.Fatalf("RunE() error = %v, want stable code %s", err, registry.CodeFunnelControlURLConflict)
	}
	if code, ok := registry.ErrorCode(err); !ok || code != registry.CodeFunnelControlURLConflict {
		t.Fatalf("ErrorCode() = %q, %v; want %s, true", code, ok, registry.CodeFunnelControlURLConflict)
	}
}

func TestServeCmd_DaemonModeFunnelNonProxyTypesIncludeStableCode(t *testing.T) {
	cases := []registry.Service{
		{
			Name:   "public-files",
			Type:   registry.TypeFile,
			Path:   "/tmp/public-files",
			Funnel: true,
		},
		{
			Name:   "public-db",
			Type:   registry.TypeTCP,
			Target: "localhost:5432",
			Port:   5432,
			Funnel: true,
		},
	}
	for _, svc := range cases {
		t.Run(svc.Type, func(t *testing.T) {
			dir := t.TempDir()
			mockServeDefaults(t, dir)
			serveDaemon = true

			reg := &registry.Registry{Services: []registry.Service{svc}}
			data, _ := json.Marshal(reg)
			if err := os.WriteFile(filepath.Join(dir, "registry.json"), data, 0o600); err != nil {
				t.Fatalf("write registry: %v", err)
			}

			daemonizeCalled := false
			serveDaemonizeFn = func(out, errLog, controlURL string, manageACL, noAutoProvision, mcp bool) (int, error) {
				daemonizeCalled = true
				return 0, fmt.Errorf("daemonize should not be called")
			}

			cmd := findServeCmd(t)
			err := cmd.RunE(cmd, nil)
			if err == nil {
				t.Fatal("RunE() error = nil, want funnel type conflict error")
			}
			if !strings.Contains(err.Error(), registry.ErrFunnelTypeConflict) {
				t.Fatalf("RunE() error = %v, want funnel type conflict error", err)
			}
			if !strings.Contains(err.Error(), registry.CodeFunnelTypeConflict) {
				t.Fatalf("RunE() error = %v, want stable code %s", err, registry.CodeFunnelTypeConflict)
			}
			if code, ok := registry.ErrorCode(err); !ok || code != registry.CodeFunnelTypeConflict {
				t.Fatalf("ErrorCode() = %q, %v; want %s, true", code, ok, registry.CodeFunnelTypeConflict)
			}
			if daemonizeCalled {
				t.Fatal("daemonize was called after invalid registry")
			}
		})
	}
}

func TestServeCmd_DaemonLogDirError(t *testing.T) {
	dir := t.TempDir()
	mockServeDefaults(t, dir)
	serveDaemon = true
	serveLogDirFn = func() (string, error) { return "", fmt.Errorf("log dir error") }

	cmd := findServeCmd(t)
	err := cmd.RunE(cmd, nil)
	if err == nil || !strings.Contains(err.Error(), "log dir error") {
		t.Fatalf("expected log dir error, got: %v", err)
	}
}

func TestServeCmd_DaemonizeError(t *testing.T) {
	dir := t.TempDir()
	mockServeDefaults(t, dir)
	serveDaemon = true
	serveDaemonizeFn = func(out, errLog, controlURL string, manageACL, noAutoProvision, mcp bool) (int, error) {
		return 0, fmt.Errorf("fork failed")
	}

	cmd := findServeCmd(t)
	err := cmd.RunE(cmd, nil)
	if err == nil || !strings.Contains(err.Error(), "fork failed") {
		t.Fatalf("expected daemonize error, got: %v", err)
	}
}

func TestServeCmd_DoesNotEnsureTagsByDefault(t *testing.T) {
	dir := t.TempDir()
	mockServeDefaults(t, dir)

	reg := &registry.Registry{
		Services: []registry.Service{
			{Name: "svc1", Type: "proxy", Target: "http://localhost:3000", Tags: []string{"tag:tsmain", "tag:shared"}},
		},
	}
	data, _ := json.Marshal(reg)
	os.WriteFile(filepath.Join(dir, "registry.json"), data, 0600)

	var ensuredTags []string
	serveEnsureTagsFn = func(ctx context.Context, tags []string) error {
		t.Fatalf("EnsureTags called by default with %v", tags)
		return nil
	}

	cmd := findServeCmd(t)
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("RunE() error = %v", err)
	}

	if len(ensuredTags) != 0 {
		t.Fatalf("ensured tags = %v, want none by default", ensuredTags)
	}
}

func TestServeCmd_DefaultRuntimeEnsureTagsIsNoop(t *testing.T) {
	dir := t.TempDir()
	mockServeDefaults(t, dir)

	serveEnsureTagsFn = func(ctx context.Context, tags []string) error {
		t.Fatalf("real EnsureTags called by default with %v", tags)
		return nil
	}
	serveNewServerFn = func(authKey, controlURL string) (serverRunner, error) {
		return &mockServerWithEnsureTags{}, nil
	}

	cmd := findServeCmd(t)
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("RunE() error = %v", err)
	}
}

func TestServeCmd_WiresFunnelProvisioningScopeAndKillSwitch(t *testing.T) {
	cases := []struct {
		name         string
		manageACL    bool
		noAuto       bool
		wantAuto     bool
		wantTagCount int
	}{
		{name: "default auto provisioning only", wantAuto: true},
		{name: "manage ACL fuses ordinary tags", manageACL: true, wantAuto: true, wantTagCount: 2},
		{name: "daemon kill switch wins", noAuto: true, wantAuto: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			mockServeDefaults(t, dir)
			request := tailapi.FunnelPolicyRequest{
				Tags: []string{"tag:tsmain", registry.FunnelTag}, Target: registry.FunnelTag, Owners: []string{"tag:tsmain"},
			}
			mock := &mockServerWithFunnelProvisioning{request: request}
			var captured tailapi.FunnelPolicyRequest
			serveEnsureFunnelAttrFn = func(ctx context.Context, got tailapi.FunnelPolicyRequest) (tailapi.PolicyMutationResult, error) {
				captured = got
				return tailapi.PolicyMutationResult{WriteOutcome: tailapi.PolicyWriteUnchanged}, nil
			}
			serveNewServerFn = func(authKey, controlURL string) (serverRunner, error) { return mock, nil }

			cmd := findServeCmd(t)
			if tc.manageACL {
				if err := cmd.Flags().Set("manage-acl", "true"); err != nil {
					t.Fatalf("set manage-acl: %v", err)
				}
			}
			if tc.noAuto {
				if err := cmd.Flags().Set("no-auto-provision", "true"); err != nil {
					t.Fatalf("set no-auto-provision: %v", err)
				}
			}
			if err := cmd.RunE(cmd, nil); err != nil {
				t.Fatalf("RunE() error = %v", err)
			}
			if !mock.autoSet || mock.auto != tc.wantAuto {
				t.Fatalf("auto provisioning setter = (%v,%v), want (true,%v)", mock.autoSet, mock.auto, tc.wantAuto)
			}
			if len(captured.Tags) != tc.wantTagCount {
				t.Fatalf("underlying Funnel writer tags = %v, want count %d", captured.Tags, tc.wantTagCount)
			}
			if captured.Target != registry.FunnelTag || len(captured.Owners) != 1 || captured.Owners[0] != "tag:tsmain" {
				t.Fatalf("underlying Funnel request = %+v, want target and caller-derived owner preserved", captured)
			}
		})
	}
}

func TestServeCmd_ManageACLEnsuresTagsOnStartup(t *testing.T) {
	dir := t.TempDir()
	mockServeDefaults(t, dir)

	reg := &registry.Registry{
		Services: []registry.Service{
			{Name: "svc1", Type: "proxy", Target: "http://localhost:3000", Tags: []string{"tag:tsmain", "tag:shared"}},
		},
	}
	data, _ := json.Marshal(reg)
	os.WriteFile(filepath.Join(dir, "registry.json"), data, 0600)

	var ensuredTags []string
	serveEnsureTagsFn = func(ctx context.Context, tags []string) error {
		ensuredTags = append([]string(nil), tags...)
		return nil
	}
	serveNewServerFn = func(authKey, controlURL string) (serverRunner, error) {
		return &mockServerWithEnsureTags{}, nil
	}

	cmd := findServeCmd(t)
	if err := cmd.Flags().Set("manage-acl", "true"); err != nil {
		t.Fatalf("set manage-acl: %v", err)
	}
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("RunE() error = %v", err)
	}

	if len(ensuredTags) != 1 || ensuredTags[0] != "tag:hot" {
		t.Fatalf("ensure phase tags = %v, want injected server callback", ensuredTags)
	}
}

func TestServeCmd_EnsureTagsError(t *testing.T) {
	dir := t.TempDir()
	mockServeDefaults(t, dir)

	serveEnsureTagsFn = func(ctx context.Context, tags []string) error {
		return fmt.Errorf("ACL write denied")
	}
	serveNewServerFn = func(authKey, controlURL string) (serverRunner, error) {
		return &mockServerWithEnsureTags{}, nil
	}

	cmd := findServeCmd(t)
	if err := cmd.Flags().Set("manage-acl", "true"); err != nil {
		t.Fatalf("set manage-acl: %v", err)
	}
	err := cmd.RunE(cmd, nil)
	if err == nil || !strings.Contains(err.Error(), "ACL write denied") {
		t.Fatalf("expected ACL error, got: %v", err)
	}
}

func TestServeCmd_EnsureTagsNoAPIClientSkipped(t *testing.T) {
	dir := t.TempDir()
	mockServeDefaults(t, dir)

	reg := &registry.Registry{
		Services: []registry.Service{
			{Name: "svc1", Type: "proxy", Target: "http://localhost:3000", Tags: []string{"tag:tsmain"}},
		},
	}
	data, _ := json.Marshal(reg)
	os.WriteFile(filepath.Join(dir, "registry.json"), data, 0600)

	serverStarted := false
	serveEnsureTagsFn = func(ctx context.Context, tags []string) error {
		return tailapi.ErrNoAPIClient
	}
	serveNewServerFn = func(authKey, controlURL string) (serverRunner, error) {
		serverStarted = true
		return &mockServer{}, nil
	}

	cmd := findServeCmd(t)
	if err := cmd.Flags().Set("manage-acl", "true"); err != nil {
		t.Fatalf("set manage-acl: %v", err)
	}
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("RunE() error = %v, want nil for skipped tag ensure", err)
	}
	if !serverStarted {
		t.Fatal("server should start after no-client tag ensure skip")
	}
}

func TestServeCmd_InvalidTagIncludesServiceContext(t *testing.T) {
	dir := t.TempDir()
	mockServeDefaults(t, dir)

	reg := &registry.Registry{
		Services: []registry.Service{
			{Name: "legacy", Type: "proxy", Target: "http://localhost:3000", Tags: []string{"tag:Bad"}},
		},
	}
	data, _ := json.Marshal(reg)
	os.WriteFile(filepath.Join(dir, "registry.json"), data, 0600)

	cmd := findServeCmd(t)
	err := cmd.RunE(cmd, nil)
	if err == nil {
		t.Fatal("RunE() error = nil, want invalid tag error")
	}
	if !strings.Contains(err.Error(), `service "legacy": invalid tag "tag:Bad"`) {
		t.Fatalf("error = %v, want service/tag context", err)
	}
	if !strings.Contains(err.Error(), "tag:<lowercase-hyphen-name>") || !strings.Contains(err.Error(), "edit registry.json") {
		t.Fatalf("error = %v, want grammar and registry remediation", err)
	}
}

func TestRegistryHasActiveFunnelAtUsesCurrentWallClock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.json")
	deadline := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	if _, err := registry.Add(path, registry.Service{Name: "public", Type: registry.TypeProxy, Target: "http://localhost:3000", Funnel: true, PublicAck: true, FunnelExpiresAt: &deadline}); err != nil {
		t.Fatal(err)
	}
	if active, err := registryHasActiveFunnelAt(path, deadline.Add(-time.Second)); err != nil || !active {
		t.Fatalf("before deadline active=%t err=%v", active, err)
	}
	if active, err := registryHasActiveFunnelAt(path, deadline); err != nil || active {
		t.Fatalf("at deadline active=%t err=%v", active, err)
	}
}

// authKeyDescriptionSafeCharset is the character set a real Tailscale
// create-key round trip accepted for the auth-key description: letters,
// digits, spaces and hyphens. The double quote that fmt's %q verb emits is
// outside it and made the API answer "description had invalid characters".
var authKeyDescriptionSafeCharset = regexp.MustCompile(`^[A-Za-z0-9 -]+$`)

// TestServeAuthKeyDescriptionStaysWithinAPISafeCharset asserts on the
// Description the serve wiring's real auth-key provider closure builds, captured
// at the serveGetAuthKeyFn seam that credentials.GetAuthKey would otherwise
// receive. It deliberately never rebuilds the string in the test: a hand-written
// literal is exactly how the original bug stayed invisible.
func TestServeAuthKeyDescriptionStaysWithinAPISafeCharset(t *testing.T) {
	// The MCP control-plane node bypasses `tslink add`, so pin that its name
	// lives inside the registry grammar the charset argument relies on.
	if err := registry.ValidateName(server.DefaultMCPNodeName); err != nil {
		t.Fatalf("DefaultMCPNodeName %q fails registry.ValidateName: %v", server.DefaultMCPNodeName, err)
	}
	services := []registry.Service{
		{Name: "svc1", Type: registry.TypeProxy, Target: "http://localhost:3000", Tags: []string{"tag:web"}},
		{Name: "my-svc", Type: registry.TypeProxy, Target: "http://localhost:3000", Tags: []string{"tag:web"}, Ephemeral: true},
		// Same shape internal/server/mcp_controlplane.go hands the provider for
		// the control-plane node (name, proxy type, default tag, ephemeral).
		{Name: server.DefaultMCPNodeName, Type: registry.TypeProxy, Target: "http://localhost:3000", Tags: []string{"tag:tsmain"}, Ephemeral: true},
	}
	for _, svc := range services {
		t.Run(svc.Name, func(t *testing.T) {
			dir := t.TempDir()
			mockServeDefaults(t, dir)
			data, err := json.Marshal(&registry.Registry{Services: []registry.Service{svc}})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "registry.json"), data, 0o600); err != nil {
				t.Fatal(err)
			}

			var captured []credentials.AuthKeyOptions
			serveGetAuthKeyFn = func(_ context.Context, opts credentials.AuthKeyOptions) (string, error) {
				captured = append(captured, opts)
				return "fake-key", nil
			}
			serveNewServerFn = func(string, string) (serverRunner, error) {
				return &mockServerWithAuthProvider{service: svc}, nil
			}

			cmd := findServeCmd(t)
			if err := cmd.RunE(cmd, nil); err != nil {
				t.Fatalf("RunE() error = %v", err)
			}
			if len(captured) != 1 {
				t.Fatalf("auth key derivations = %d, want exactly 1", len(captured))
			}
			desc := captured[0].Description
			if !strings.Contains(desc, svc.Name) {
				t.Fatalf("description %q does not name the service %q", desc, svc.Name)
			}
			if !authKeyDescriptionSafeCharset.MatchString(desc) {
				t.Fatalf("auth key description %q contains characters outside [A-Za-z0-9 -]; the Tailscale create-key API rejects such a description with \"description had invalid characters\"", desc)
			}
		})
	}
}

func TestValidateMCPNodeName(t *testing.T) {
	cases := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{"empty means default", "", false},
		{"whitespace only means default", "   ", false},
		{"lowercase hyphenated", "ops-mcp", false},
		{"surrounding whitespace is trimmed like the daemon does", " ops-mcp ", false},
		{"double quote", `ops"mcp`, true},
		{"inner space", "ops mcp", true},
		{"uppercase", "OpsMCP", true},
		{"leading hyphen", "-ops", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateMCPNodeName(tc.value)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("validateMCPNodeName(%q) = nil, want error", tc.value)
				}
				if !strings.Contains(err.Error(), "invalid mcp node_name") {
					t.Fatalf("validateMCPNodeName(%q) error = %q, want it to name mcp node_name", tc.value, err)
				}
				var coded *output.CodeError
				if !errors.As(err, &coded) || coded.Code != output.ExitUsage {
					t.Fatalf("validateMCPNodeName(%q) error = %#v, want usage exit code", tc.value, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("validateMCPNodeName(%q) error = %v, want nil", tc.value, err)
			}
		})
	}
}

// TestServeCmdRejectsInvalidMCPNodeNameBeforeStart proves a hand-edited mcp
// node_name is refused in the command layer, before any server is constructed
// and therefore before the name could reach an auth-key description or a tsnet
// hostname.
func TestServeCmdRejectsInvalidMCPNodeNameBeforeStart(t *testing.T) {
	dir := t.TempDir()
	mockServeDefaults(t, dir)
	serveLoadGlobalFn = func() (config.GlobalConfig, error) {
		return config.GlobalConfig{MCP: &config.MCPConfig{Enabled: true, Allow: []string{"alice@example.com"}, NodeName: `ops"mcp`}}, nil
	}
	newServerCalls := 0
	serveNewServerFn = func(string, string) (serverRunner, error) {
		newServerCalls++
		return nil, errors.New("server must not be constructed for an invalid mcp node_name")
	}

	cmd := findServeCmd(t)
	err := cmd.RunE(cmd, nil)
	if err == nil || !strings.Contains(err.Error(), "invalid mcp node_name") {
		t.Fatalf("RunE() error = %v, want an invalid mcp node_name usage error", err)
	}
	if newServerCalls != 0 {
		t.Fatalf("serveNewServerFn calls = %d, want 0", newServerCalls)
	}
}
