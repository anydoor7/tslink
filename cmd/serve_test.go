package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
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
		ensureDir          func() error
		migrate            func() bool
		registryPath       func() (string, error)
		loadRegistry       func(string) (*registry.Registry, error)
		getAuthKey         func(context.Context, credentials.AuthKeyOptions) (string, error)
		checkAuth          func() error
		pidPath            func() (string, error)
		isRunning          func(string) bool
		isPIDRunning       func(int) bool
		ensureTags         func(context.Context, []string) error
		cleanup            func(context.Context, []tailapi.CleanupTarget) (tailapi.CleanupResult, error)
		loadGlobal         func() (config.GlobalConfig, error)
		logDir             func() (string, error)
		daemonize          func(string, string, string, bool) (int, error)
		readPID            func(string) (int, error)
		readyPath          func() (string, error)
		writeReady         func(string, int) error
		readReady          func(string) (int, error)
		removeReady        func(string)
		writePID           func(string) error
		writePIDForProcess func(string, int) error
		removePID          func(string)
		withPIDLock        func(string, func() error) error
		newServer          func(string, string) (serverRunner, error)
		readyTimeout       time.Duration
		readyPoll          time.Duration
	}{
		serveEnsureDirFn, serveMigrateFn, serveRegistryPathFn, serveLoadRegistryFn,
		serveGetAuthKeyFn, serveCheckAuthFn, servePIDPathFn, serveIsRunningFn, serveIsPIDRunningFn, serveEnsureTagsFn, serveCleanupFn,
		serveLoadGlobalFn, serveLogDirFn, serveDaemonizeFn, serveReadPIDFn,
		serveReadyPathFn, serveWriteReadyFn, serveReadReadyFn, serveRemoveReadyFn,
		serveWritePIDFn, serveWritePIDForProcessFn, serveRemovePIDFn, serveWithPIDLockFn, serveNewServerFn,
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
		serveIsPIDRunningFn = old.isPIDRunning
		serveEnsureTagsFn = old.ensureTags
		serveCleanupFn = old.cleanup
		serveLoadGlobalFn = old.loadGlobal
		serveLogDirFn = old.logDir
		serveDaemonizeFn = old.daemonize
		serveReadPIDFn = old.readPID
		serveReadyPathFn = old.readyPath
		serveWriteReadyFn = old.writeReady
		serveReadReadyFn = old.readReady
		serveRemoveReadyFn = old.removeReady
		serveWritePIDFn = old.writePID
		serveWritePIDForProcessFn = old.writePIDForProcess
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

	regPath := filepath.Join(dir, "registry.json")
	pidPath := filepath.Join(dir, "tslink.pid")
	readyPath := filepath.Join(dir, "tslink.ready")

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
	serveIsPIDRunningFn = func(int) bool { return true }
	serveEnsureTagsFn = func(ctx context.Context, tags []string) error { return nil }
	serveCleanupFn = func(ctx context.Context, targets []tailapi.CleanupTarget) (tailapi.CleanupResult, error) {
		return tailapi.CleanupResult{}, nil
	}
	serveLoadGlobalFn = func() (config.GlobalConfig, error) { return config.GlobalConfig{}, nil }
	serveLogDirFn = func() (string, error) { return dir, nil }
	serveDaemonizeFn = func(out, err, controlURL string, manageACL bool) (int, error) { return 99999, nil }
	serveReadPIDFn = func(path string) (int, error) { return 99999, nil }
	serveReadyPathFn = func() (string, error) { return readyPath, nil }
	serveWriteReadyFn = func(path string, pid int) error {
		return os.WriteFile(path, []byte(fmt.Sprintf("%d", pid)), 0600)
	}
	serveReadReadyFn = func(path string) (int, error) { return 99999, nil }
	serveRemoveReadyFn = func(path string) { os.Remove(path) }
	serveWritePIDFn = func(path string) error { return os.WriteFile(path, []byte("12345"), 0600) }
	serveWritePIDForProcessFn = func(path string, pid int) error {
		return os.WriteFile(path, []byte(fmt.Sprintf("%d", pid)), 0600)
	}
	serveRemovePIDFn = func(path string) { os.Remove(path) }
	serveWithPIDLockFn = func(path string, fn func() error) error { return fn() }
	serveNewServerFn = func(authKey, controlURL string) (serverRunner, error) {
		return &mockServer{}, nil
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
	_ = cmd.Flags().Set("manage-acl", "false")
	return cmd
}

// --- runForeground tests ---

func TestRunForeground_WritePIDError(t *testing.T) {
	saveServeState(t)
	serveWritePIDFn = func(path string) error { return fmt.Errorf("permission denied") }

	err := runForeground("/tmp/test.pid", "", "fake-key", "")
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
	serveDaemonizeFn = func(out, errLog, controlURL string, manageACL bool) (int, error) {
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
	serveDaemonizeFn = func(out, errLog, controlURL string, manageACL bool) (int, error) {
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

// TestServeCmd_DaemonModePropagatesManageACL is the serve-level manage-acl propagation
// guard: `serve --daemon` must pass its --manage-acl opt-in through to the
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
			serveDaemonizeFn = func(out, errLog, controlURL string, manageACL bool) (int, error) {
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
	serveDaemonizeFn = func(out, errLog, controlURL string, manageACL bool) (int, error) {
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
			serveDaemonizeFn = func(out, errLog, controlURL string, manageACL bool) (int, error) {
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
	serveDaemonizeFn = func(out, errLog, controlURL string, manageACL bool) (int, error) {
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
		ensuredTags = tags
		return nil
	}

	cmd := findServeCmd(t)
	if err := cmd.Flags().Set("manage-acl", "true"); err != nil {
		t.Fatalf("set manage-acl: %v", err)
	}
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
