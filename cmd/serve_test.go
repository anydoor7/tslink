package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/credentials"
	"github.com/monody0007/tslink/internal/registry"
	"github.com/monody0007/tslink/internal/server"
	"github.com/monody0007/tslink/internal/tailapi"
	"github.com/spf13/cobra"
	"github.com/zalando/go-keyring"
)

type mockServer struct {
	runErr error
}

func (m *mockServer) Run(ctx context.Context) error {
	return m.runErr
}

type mockServerWithEnsureTags struct {
	ensureTagsFn server.EnsureTagsFunc
	runErr       error
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
		ensureDir    func() error
		migrate      func() bool
		registryPath func() (string, error)
		loadRegistry func(string) (*registry.Registry, error)
		getAuthKey   func(context.Context, credentials.AuthKeyOptions) (string, error)
		checkAuth    func() error
		pidPath      func() (string, error)
		isRunning    func(string) bool
		ensureTags   func(context.Context, []string) error
		cleanup      func(context.Context, []tailapi.CleanupTarget) (tailapi.CleanupResult, error)
		loadGlobal   func() (config.GlobalConfig, error)
		logDir       func() (string, error)
		daemonize    func(string, string, string) (int, error)
		readPID      func(string) (int, error)
		writePID     func(string) error
		removePID    func(string)
		newServer    func(string, string) (serverRunner, error)
		readyTimeout time.Duration
		readyPoll    time.Duration
	}{
		serveEnsureDirFn, serveMigrateFn, serveRegistryPathFn, serveLoadRegistryFn,
		serveGetAuthKeyFn, serveCheckAuthFn, servePIDPathFn, serveIsRunningFn, serveEnsureTagsFn, serveCleanupFn,
		serveLoadGlobalFn, serveLogDirFn, serveDaemonizeFn, serveReadPIDFn,
		serveWritePIDFn, serveRemovePIDFn, serveNewServerFn,
		serveDaemonReadyTimeout, serveDaemonReadyPollInterval,
	}
	t.Cleanup(func() {
		serveEnsureDirFn = old.ensureDir
		serveMigrateFn = old.migrate
		serveRegistryPathFn = old.registryPath
		serveLoadRegistryFn = old.loadRegistry
		serveGetAuthKeyFn = old.getAuthKey
		serveCheckAuthFn = old.checkAuth
		servePIDPathFn = old.pidPath
		serveIsRunningFn = old.isRunning
		serveEnsureTagsFn = old.ensureTags
		serveCleanupFn = old.cleanup
		serveLoadGlobalFn = old.loadGlobal
		serveLogDirFn = old.logDir
		serveDaemonizeFn = old.daemonize
		serveReadPIDFn = old.readPID
		serveWritePIDFn = old.writePID
		serveRemovePIDFn = old.removePID
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

	regPath := filepath.Join(dir, "registry.json")
	pidPath := filepath.Join(dir, "tslink.pid")

	// Write empty registry
	data, _ := json.Marshal(&registry.Registry{})
	os.WriteFile(regPath, data, 0600)

	keyring.MockInit()
	t.Setenv("HOME", dir)

	serveEnsureDirFn = func() error { return nil }
	serveMigrateFn = func() bool { return false }
	serveRegistryPathFn = func() (string, error) { return regPath, nil }
	serveLoadRegistryFn = registry.Load
	serveGetAuthKeyFn = func(ctx context.Context, opts credentials.AuthKeyOptions) (string, error) {
		return "fake-auth-key", nil
	}
	serveCheckAuthFn = func() error { return nil }
	servePIDPathFn = func() (string, error) { return pidPath, nil }
	serveIsRunningFn = func(string) bool { return false }
	serveEnsureTagsFn = func(ctx context.Context, tags []string) error { return nil }
	serveCleanupFn = func(ctx context.Context, targets []tailapi.CleanupTarget) (tailapi.CleanupResult, error) {
		return tailapi.CleanupResult{}, nil
	}
	serveLoadGlobalFn = func() (config.GlobalConfig, error) { return config.GlobalConfig{}, nil }
	serveLogDirFn = func() (string, error) { return dir, nil }
	serveDaemonizeFn = func(out, err, controlURL string) (int, error) { return 99999, nil }
	serveReadPIDFn = func(path string) (int, error) { return 99999, nil }
	serveWritePIDFn = func(path string) error { return os.WriteFile(path, []byte("12345"), 0600) }
	serveRemovePIDFn = func(path string) { os.Remove(path) }
	serveNewServerFn = func(authKey, controlURL string) (serverRunner, error) {
		return &mockServer{}, nil
	}
}

func mockServeDaemonReadyAfterInitialCheck(t *testing.T) {
	t.Helper()
	runningChecks := 0
	serveIsRunningFn = func(string) bool {
		runningChecks++
		return runningChecks > 1
	}
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

	err := runForeground("/tmp/test.pid", "fake-key", "")
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

	err := runForeground(filepath.Join(dir, "test.pid"), "fake-key", "")
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

	err := runForeground(filepath.Join(dir, "test.pid"), "fake-key", "")
	if err == nil || err.Error() != "runtime error" {
		t.Fatalf("expected 'runtime error', got: %v", err)
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

	if err := runForeground(filepath.Join(dir, "test.pid"), "fake-key", ""); err != nil {
		t.Fatalf("runForeground() error = %v", err)
	}
	if !called {
		t.Fatal("serveEnsureTagsFn was not wired into server")
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
	serveCheckAuthFn = func() error {
		return fmt.Errorf("not authenticated")
	}

	cmd := findServeCmd(t)
	err := cmd.RunE(cmd, nil)
	if err == nil || !strings.Contains(err.Error(), "not authenticated") {
		t.Fatalf("expected auth key error, got: %v", err)
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

func TestServeCmd_CleanupErrorStopsStartup(t *testing.T) {
	dir := t.TempDir()
	mockServeDefaults(t, dir)

	serverStarted := false
	serveCleanupFn = func(ctx context.Context, targets []tailapi.CleanupTarget) (tailapi.CleanupResult, error) {
		return tailapi.CleanupResult{}, fmt.Errorf("list devices: network down")
	}
	serveNewServerFn = func(authKey, controlURL string) (serverRunner, error) {
		serverStarted = true
		return &mockServer{}, nil
	}

	cmd := findServeCmd(t)
	err := cmd.RunE(cmd, nil)
	if err == nil || !strings.Contains(err.Error(), "cleanup stale nodes") {
		t.Fatalf("expected cleanup error, got: %v", err)
	}
	if serverStarted {
		t.Fatal("server should not start after cleanup error")
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

func TestServeCmd_WithTagsAndEphemeral(t *testing.T) {
	dir := t.TempDir()
	mockServeDefaults(t, dir)

	// Write registry with tags and ephemeral
	reg := &registry.Registry{
		Services: []registry.Service{
			{Name: "svc1", Type: "proxy", Target: "localhost:3000", Tags: []string{"tag:web"}, Ephemeral: true},
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
	serveDaemonizeFn = func(out, errLog, controlURL string) (int, error) {
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

func TestServeCmd_DaemonModeWaitsForPIDReadiness(t *testing.T) {
	dir := t.TempDir()
	mockServeDefaults(t, dir)
	serveDaemon = true
	mockServeDaemonReadyAfterInitialCheck(t)
	serveDaemonReadyTimeout = 100 * time.Millisecond
	serveDaemonReadyPollInterval = time.Millisecond

	attempts := 0
	serveReadPIDFn = func(path string) (int, error) {
		attempts++
		if attempts < 3 {
			return 0, fmt.Errorf("not ready")
		}
		return 99999, nil
	}

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
	serveReadPIDFn = func(path string) (int, error) {
		return 0, fmt.Errorf("pid file missing")
	}

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

func TestServeCmd_DaemonReadinessPIDMatchRequiresRunningProcess(t *testing.T) {
	dir := t.TempDir()
	mockServeDefaults(t, dir)
	serveDaemon = true
	serveDaemonReadyTimeout = 3 * time.Millisecond
	serveDaemonReadyPollInterval = time.Millisecond

	runningChecks := 0
	serveIsRunningFn = func(string) bool {
		runningChecks++
		return false
	}
	serveReadPIDFn = func(path string) (int, error) {
		return 99999, nil
	}

	cmd := findServeCmd(t)
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	err := cmd.RunE(cmd, nil)
	if err == nil {
		t.Fatal("RunE() error = nil, want readiness failure")
	}
	if !strings.Contains(err.Error(), "daemon is not running") {
		t.Fatalf("RunE() error = %v, want daemon liveness readiness failure", err)
	}
	if runningChecks < 2 {
		t.Fatalf("running checks = %d, want initial check plus readiness confirmation", runningChecks)
	}
	if strings.Contains(buf.String(), "tslink started as daemon") {
		t.Fatalf("reported daemon success despite failed liveness: %s", buf.String())
	}
}

func TestServeCmd_DaemonModeSkipsParentPreflight(t *testing.T) {
	dir := t.TempDir()
	mockServeDefaults(t, dir)
	serveDaemon = true
	mockServeDaemonReadyAfterInitialCheck(t)

	calls := 0
	serveRegistryPathFn = func() (string, error) {
		calls++
		return "", fmt.Errorf("registry path should not be called in daemon parent")
	}
	serveLoadRegistryFn = func(path string) (*registry.Registry, error) {
		calls++
		return nil, fmt.Errorf("load registry should not be called in daemon parent")
	}
	serveEnsureTagsFn = func(ctx context.Context, tags []string) error {
		calls++
		return fmt.Errorf("ensure tags should not be called in daemon parent")
	}
	serveCheckAuthFn = func() error {
		calls++
		return fmt.Errorf("auth check should not be called in daemon parent")
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
		t.Fatalf("parent preflight calls = %d, want 0", calls)
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
	serveDaemonizeFn = func(out, errLog, controlURL string) (int, error) {
		return 0, fmt.Errorf("fork failed")
	}

	cmd := findServeCmd(t)
	err := cmd.RunE(cmd, nil)
	if err == nil || !strings.Contains(err.Error(), "fork failed") {
		t.Fatalf("expected daemonize error, got: %v", err)
	}
}

func TestServeCmd_EnsureTagsOnStartup(t *testing.T) {
	dir := t.TempDir()
	mockServeDefaults(t, dir)

	reg := &registry.Registry{
		Services: []registry.Service{
			{Name: "svc1", Type: "proxy", Target: "localhost:3000", Tags: []string{"tag:tsmain", "tag:shared"}},
		},
	}
	data, _ := json.Marshal(reg)
	os.WriteFile(filepath.Join(dir, "registry.json"), data, 0600)

	var ensuredTags []string
	serveEnsureTagsFn = func(ctx context.Context, tags []string) error {
		ensuredTags = tags
		return nil
	}

	cmd := findServeCmd(t)
	_ = cmd.RunE(cmd, nil)

	if len(ensuredTags) < 2 {
		t.Fatalf("expected at least 2 tags ensured, got: %v", ensuredTags)
	}
}

func TestServeCmd_EnsureTagsError(t *testing.T) {
	dir := t.TempDir()
	mockServeDefaults(t, dir)

	serveEnsureTagsFn = func(ctx context.Context, tags []string) error {
		return fmt.Errorf("ACL write denied")
	}

	cmd := findServeCmd(t)
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
			{Name: "svc1", Type: "proxy", Target: "localhost:3000", Tags: []string{"tag:tsmain"}},
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
			{Name: "legacy", Type: "proxy", Target: "localhost:3000", Tags: []string{"tag:Bad"}},
		},
	}
	data, _ := json.Marshal(reg)
	os.WriteFile(filepath.Join(dir, "registry.json"), data, 0600)

	cmd := findServeCmd(t)
	err := cmd.RunE(cmd, nil)
	if err == nil {
		t.Fatal("RunE() error = nil, want invalid tag error")
	}
	if !strings.Contains(err.Error(), `service "legacy" has invalid tag "tag:Bad"`) {
		t.Fatalf("error = %v, want service/tag context", err)
	}
	if !strings.Contains(err.Error(), "tag:<lowercase-hyphen-name>") || !strings.Contains(err.Error(), "tslink tags set legacy") {
		t.Fatalf("error = %v, want grammar and migration action", err)
	}
}
