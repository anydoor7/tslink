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

	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/credentials"
	"github.com/monody0007/tslink/internal/registry"
	"github.com/spf13/cobra"
	"github.com/zalando/go-keyring"
)

type mockServer struct {
	runErr error
}

func (m *mockServer) Run(ctx context.Context) error {
	return m.runErr
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
		pidPath      func() (string, error)
		isRunning    func(string) bool
		cleanup      func(context.Context, []string) error
		loadGlobal   func() (config.GlobalConfig, error)
		logDir       func() (string, error)
		daemonize    func(string, string) (int, error)
		writePID     func(string) error
		removePID    func(string)
		newServer    func(string, string) (serverRunner, error)
	}{
		serveEnsureDirFn, serveMigrateFn, serveRegistryPathFn, serveLoadRegistryFn,
		serveGetAuthKeyFn, servePIDPathFn, serveIsRunningFn, serveCleanupFn,
		serveLoadGlobalFn, serveLogDirFn, serveDaemonizeFn,
		serveWritePIDFn, serveRemovePIDFn, serveNewServerFn,
	}
	t.Cleanup(func() {
		serveEnsureDirFn = old.ensureDir
		serveMigrateFn = old.migrate
		serveRegistryPathFn = old.registryPath
		serveLoadRegistryFn = old.loadRegistry
		serveGetAuthKeyFn = old.getAuthKey
		servePIDPathFn = old.pidPath
		serveIsRunningFn = old.isRunning
		serveCleanupFn = old.cleanup
		serveLoadGlobalFn = old.loadGlobal
		serveLogDirFn = old.logDir
		serveDaemonizeFn = old.daemonize
		serveWritePIDFn = old.writePID
		serveRemovePIDFn = old.removePID
		serveNewServerFn = old.newServer
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
	servePIDPathFn = func() (string, error) { return pidPath, nil }
	serveIsRunningFn = func(string) bool { return false }
	serveCleanupFn = func(ctx context.Context, names []string) error { return nil }
	serveLoadGlobalFn = func() (config.GlobalConfig, error) { return config.GlobalConfig{}, nil }
	serveLogDirFn = func() (string, error) { return dir, nil }
	serveDaemonizeFn = func(out, err string) (int, error) { return 99999, nil }
	serveWritePIDFn = func(path string) error { return os.WriteFile(path, []byte("12345"), 0600) }
	serveRemovePIDFn = func(path string) { os.Remove(path) }
	serveNewServerFn = func(authKey, controlURL string) (serverRunner, error) {
		return &mockServer{}, nil
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

func TestServeCmd_GetAuthKeyError(t *testing.T) {
	dir := t.TempDir()
	mockServeDefaults(t, dir)
	serveGetAuthKeyFn = func(ctx context.Context, opts credentials.AuthKeyOptions) (string, error) {
		return "", fmt.Errorf("not authenticated")
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

	cmd := findServeCmd(t)
	// Will call runForeground which uses mock server
	_ = cmd.RunE(cmd, nil)

	if !capturedOpts.Ephemeral {
		t.Error("expected ephemeral=true")
	}
	if len(capturedOpts.Tags) != 1 || capturedOpts.Tags[0] != "tag:web" {
		t.Errorf("expected tags=[tag:web], got: %v", capturedOpts.Tags)
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
	serveDaemonizeFn = func(out, err string) (int, error) {
		return 0, fmt.Errorf("fork failed")
	}

	cmd := findServeCmd(t)
	err := cmd.RunE(cmd, nil)
	if err == nil || !strings.Contains(err.Error(), "fork failed") {
		t.Fatalf("expected daemonize error, got: %v", err)
	}
}
