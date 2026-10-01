package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/anydoor7/tslink/internal/testenv"
)

func stubDefaultConfigDirError(t *testing.T) {
	t.Helper()
	t.Setenv(ConfigDirEnv, "")
	orig := defaultConfigDir
	defaultConfigDir = func() (string, error) {
		return "", errors.New("synthetic default config directory failure")
	}
	t.Cleanup(func() { defaultConfigDir = orig })
}

func TestDir(t *testing.T) {
	t.Setenv(ConfigDirEnv, "")
	want := filepath.Join(t.TempDir(), "default", "tslink")
	orig := defaultConfigDir
	defaultConfigDir = func() (string, error) { return want, nil }
	t.Cleanup(func() { defaultConfigDir = orig })
	dir, err := Dir()
	if err != nil {
		t.Fatalf("Dir() error = %v", err)
	}
	if dir != want {
		t.Fatalf("Dir() = %q, want %q", dir, want)
	}
}

func TestDirPrefersTSLinkConfigDir(t *testing.T) {
	override := filepath.Join(t.TempDir(), "isolated", "tslink")
	testenv.SetHome(t, filepath.Join(t.TempDir(), "must-not-be-used"))
	t.Setenv(ConfigDirEnv, override)

	dir, err := Dir()
	if err != nil {
		t.Fatalf("Dir() error = %v", err)
	}
	if dir != override {
		t.Fatalf("Dir() = %q, want TSLINK_CONFIG_DIR %q", dir, override)
	}
}

func TestRegistryPath(t *testing.T) {
	path, err := RegistryPath()
	if err != nil {
		t.Fatalf("RegistryPath() error = %v", err)
	}
	if !strings.HasSuffix(path, "registry.json") {
		t.Fatalf("RegistryPath() = %q, want suffix registry.json", path)
	}
}

func TestRuntimeSnapshotPath(t *testing.T) {
	path, err := RuntimeSnapshotPath()
	if err != nil {
		t.Fatalf("RuntimeSnapshotPath() error = %v", err)
	}
	if !strings.HasSuffix(path, "runtime.json") {
		t.Fatalf("RuntimeSnapshotPath() = %q, want suffix runtime.json", path)
	}
}

func TestNodeOwnershipPath(t *testing.T) {
	path, err := NodeOwnershipPath()
	if err != nil {
		t.Fatalf("NodeOwnershipPath() error = %v", err)
	}
	if !strings.HasSuffix(path, "node-ownership.json") {
		t.Fatalf("NodeOwnershipPath() = %q, want suffix node-ownership.json", path)
	}
}

func TestAuthHandoffPath(t *testing.T) {
	path, err := AuthHandoffPath()
	if err != nil {
		t.Fatalf("AuthHandoffPath() error = %v", err)
	}
	if !strings.HasSuffix(path, filepath.Join("tslink", "auth-handoff.json")) {
		t.Fatalf("AuthHandoffPath() = %q, want suffix auth-handoff.json", path)
	}
}

func TestPIDPath(t *testing.T) {
	path, err := PIDPath()
	if err != nil {
		t.Fatalf("PIDPath() error = %v", err)
	}
	if !strings.HasSuffix(path, "tslink.pid") {
		t.Fatalf("PIDPath() = %q, want suffix tslink.pid", path)
	}
}

func TestNodesDir(t *testing.T) {
	dir, err := NodesDir()
	if err != nil {
		t.Fatalf("NodesDir() error = %v", err)
	}
	if !strings.HasSuffix(dir, filepath.Join("tslink", "nodes")) {
		t.Fatalf("NodesDir() = %q, want suffix %q", dir, filepath.Join("tslink", "nodes"))
	}
}

func TestAuthKeyPath(t *testing.T) {
	path, err := AuthKeyPath()
	if err != nil {
		t.Fatalf("AuthKeyPath() error = %v", err)
	}
	if !strings.HasSuffix(path, filepath.Join("tslink", "authkey")) {
		t.Fatalf("AuthKeyPath() = %q, want suffix %q", path, filepath.Join("tslink", "authkey"))
	}
}

func TestLogDir(t *testing.T) {
	dir, err := LogDir()
	if err != nil {
		t.Fatalf("LogDir() error = %v", err)
	}
	if !strings.HasSuffix(dir, "logs") {
		t.Fatalf("LogDir() = %q, want suffix logs", dir)
	}
}

func TestAllPathsSharePrefix(t *testing.T) {
	dir, _ := Dir()
	regPath, _ := RegistryPath()
	pidPath, _ := PIDPath()
	nodesDir, _ := NodesDir()
	authKeyPath, _ := AuthKeyPath()
	logDir, _ := LogDir()

	for _, p := range []string{regPath, pidPath, nodesDir, authKeyPath, logDir} {
		if !strings.HasPrefix(p, dir) {
			t.Fatalf("path %q does not start with Dir() %q", p, dir)
		}
	}
}

func TestEnsureDir(t *testing.T) {
	testenv.SetHome(t, t.TempDir())

	if err := EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}

	dir, _ := Dir()
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("config dir does not exist after EnsureDir: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("config dir is not a directory")
	}

	logDir, _ := LogDir()
	info, err = os.Stat(logDir)
	if err != nil {
		t.Fatalf("log dir does not exist after EnsureDir: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("log dir is not a directory")
	}

	nodesDir, _ := NodesDir()
	info, err = os.Stat(nodesDir)
	if err != nil {
		t.Fatalf("nodes dir does not exist after EnsureDir: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("nodes dir is not a directory")
	}
}

func TestAPIKeyPath(t *testing.T) {
	path, err := APIKeyPath()
	if err != nil {
		t.Fatalf("APIKeyPath() error = %v", err)
	}
	if !strings.HasSuffix(path, filepath.Join("tslink", "apikey")) {
		t.Fatalf("APIKeyPath() = %q, want suffix %q", path, filepath.Join("tslink", "apikey"))
	}
}

func TestEnsureDir_Idempotent(t *testing.T) {
	testenv.SetHome(t, t.TempDir())

	if err := EnsureDir(); err != nil {
		t.Fatalf("first EnsureDir() error = %v", err)
	}
	if err := EnsureDir(); err != nil {
		t.Fatalf("second EnsureDir() error = %v", err)
	}
}

func TestEnsureDir_Error(t *testing.T) {
	home := t.TempDir()
	testenv.SetHome(t, home)

	if err := os.MkdirAll(filepath.Join(home, ".config"), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.Remove(filepath.Join(home, ".config")); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, ".config"), []byte("not-a-dir"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if err := EnsureDir(); err == nil {
		t.Fatal("EnsureDir() error = nil, want error")
	}
}

func TestConfigPath(t *testing.T) {
	path, err := ConfigPath()
	if err != nil {
		t.Fatalf("ConfigPath() error = %v", err)
	}
	if !strings.HasSuffix(path, "config.json") {
		t.Fatalf("ConfigPath() = %q, want suffix config.json", path)
	}
}

func TestLoadGlobalConfig_MissingFile(t *testing.T) {
	testenv.SetHome(t, t.TempDir())

	cfg, err := LoadGlobalConfig()
	if err != nil {
		t.Fatalf("LoadGlobalConfig() error = %v, want nil for missing file", err)
	}
	if cfg != (GlobalConfig{}) {
		t.Fatalf("LoadGlobalConfig() = %+v, want zero-value GlobalConfig", cfg)
	}
}

func TestLoadGlobalConfig_ValidJSON(t *testing.T) {
	home := t.TempDir()
	testenv.SetHome(t, home)

	path, err := ConfigPath()
	if err != nil {
		t.Fatalf("ConfigPath() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	want := GlobalConfig{ControlURL: "https://example.com"}
	data, _ := json.Marshal(want)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	cfg, err := LoadGlobalConfig()
	if err != nil {
		t.Fatalf("LoadGlobalConfig() error = %v", err)
	}
	if cfg != want {
		t.Fatalf("LoadGlobalConfig() = %+v, want %+v", cfg, want)
	}
}

func TestLoadGlobalConfig_InvalidJSON(t *testing.T) {
	home := t.TempDir()
	testenv.SetHome(t, home)

	path, err := ConfigPath()
	if err != nil {
		t.Fatalf("ConfigPath() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(path, []byte("{invalid json"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	_, err = LoadGlobalConfig()
	if err == nil {
		t.Fatal("LoadGlobalConfig() error = nil, want error for invalid JSON")
	}
}

func TestSaveGlobalConfig_WritesToDisk(t *testing.T) {
	testenv.SetHome(t, t.TempDir())

	want := GlobalConfig{ControlURL: "https://control.example.com"}
	if err := SaveGlobalConfig(want); err != nil {
		t.Fatalf("SaveGlobalConfig() error = %v", err)
	}

	path, _ := ConfigPath()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}

	var got GlobalConfig
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if got != want {
		t.Fatalf("saved config = %+v, want %+v", got, want)
	}
}

func TestSaveGlobalConfig_RoundTrip(t *testing.T) {
	testenv.SetHome(t, t.TempDir())

	want := GlobalConfig{ControlURL: "https://headscale.example.com"}
	if err := SaveGlobalConfig(want); err != nil {
		t.Fatalf("SaveGlobalConfig() error = %v", err)
	}

	got, err := LoadGlobalConfig()
	if err != nil {
		t.Fatalf("LoadGlobalConfig() error = %v", err)
	}
	if got != want {
		t.Fatalf("round-trip config = %+v, want %+v", got, want)
	}
}

func TestSaveGlobalConfig_CreatesParentDir(t *testing.T) {
	testenv.SetHome(t, t.TempDir())

	// Parent dir does not exist yet; SaveGlobalConfig should create it.
	if err := SaveGlobalConfig(GlobalConfig{ControlURL: "https://test.example.com"}); err != nil {
		t.Fatalf("SaveGlobalConfig() error = %v", err)
	}

	path, _ := ConfigPath()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("config file does not exist after SaveGlobalConfig: %v", err)
	}
}

func TestSaveGlobalConfig_MkdirAllError(t *testing.T) {
	home := t.TempDir()
	testenv.SetHome(t, home)

	// Place a regular file where the config directory should be, so MkdirAll fails.
	configParent := filepath.Join(home, ".config")
	if err := os.WriteFile(configParent, []byte("blocker"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	err := SaveGlobalConfig(GlobalConfig{ControlURL: "https://test.example.com"})
	if err == nil {
		t.Fatal("SaveGlobalConfig() error = nil, want error when MkdirAll fails")
	}
}

func TestSaveGlobalConfig_WriteFileError(t *testing.T) {
	home := t.TempDir()
	testenv.SetHome(t, home)

	// Create the config dir but make it read-only so WriteFile fails.
	path, err := ConfigPath()
	if err != nil {
		t.Fatalf("ConfigPath() error = %v", err)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	// Place a directory where the config file should be, so WriteFile fails.
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatalf("MkdirAll(configFile) error = %v", err)
	}

	err = SaveGlobalConfig(GlobalConfig{ControlURL: "https://test.example.com"})
	if err == nil {
		t.Fatal("SaveGlobalConfig() error = nil, want error when WriteFile fails")
	}
}

func TestLoadGlobalConfig_ConvergesFilePermissions(t *testing.T) {
	home := t.TempDir()
	testenv.SetHome(t, home)

	path, err := ConfigPath()
	if err != nil {
		t.Fatalf("ConfigPath() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	if err := os.WriteFile(path, []byte(`{"control_url":"x"}`), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	cfg, err := LoadGlobalConfig()
	if err != nil {
		t.Fatalf("LoadGlobalConfig() error = %v", err)
	}
	if cfg.ControlURL != "x" {
		t.Fatalf("ControlURL = %q, want x", cfg.ControlURL)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if runtime.GOOS != "windows" {
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("mode = %o, want 600", got)
		}
	} else if !info.Mode().IsRegular() {
		t.Fatalf("config mode = %v, want regular file on Windows", info.Mode())
	}
}

func TestSaveGlobalConfig_EmptyConfig(t *testing.T) {
	testenv.SetHome(t, t.TempDir())

	// Save a zero-value config (omitempty should produce minimal JSON).
	if err := SaveGlobalConfig(GlobalConfig{}); err != nil {
		t.Fatalf("SaveGlobalConfig() error = %v", err)
	}

	path, _ := ConfigPath()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}

	// Should be valid JSON.
	var got GlobalConfig
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if got != (GlobalConfig{}) {
		t.Fatalf("saved config = %+v, want zero-value", got)
	}

	// File should end with a newline.
	if len(data) == 0 || data[len(data)-1] != '\n' {
		t.Fatal("saved config file does not end with newline")
	}
}

func TestSaveGlobalConfig_OverwriteExisting(t *testing.T) {
	testenv.SetHome(t, t.TempDir())

	// Write initial config.
	initial := GlobalConfig{ControlURL: "https://first.example.com"}
	if err := SaveGlobalConfig(initial); err != nil {
		t.Fatalf("SaveGlobalConfig(initial) error = %v", err)
	}

	// Overwrite with different config.
	updated := GlobalConfig{ControlURL: "https://second.example.com"}
	if err := SaveGlobalConfig(updated); err != nil {
		t.Fatalf("SaveGlobalConfig(updated) error = %v", err)
	}

	got, err := LoadGlobalConfig()
	if err != nil {
		t.Fatalf("LoadGlobalConfig() error = %v", err)
	}
	if got != updated {
		t.Fatalf("LoadGlobalConfig() = %+v, want %+v", got, updated)
	}
}

func TestEnsureDir_LogDirError(t *testing.T) {
	home := t.TempDir()
	testenv.SetHome(t, home)

	// Create the base config dir but place a file where logs dir should go.
	dir, _ := Dir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	logsPath := filepath.Join(dir, "logs")
	if err := os.WriteFile(logsPath, []byte("blocker"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	err := EnsureDir()
	if err == nil {
		t.Fatal("EnsureDir() error = nil, want error when logs dir creation fails")
	}
}

func TestEnsureDir_NodesDirError(t *testing.T) {
	home := t.TempDir()
	testenv.SetHome(t, home)

	// Create the base config dir and logs dir, but block nodes dir.
	dir, _ := Dir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	logDir, _ := LogDir()
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		t.Fatalf("MkdirAll(logDir) error = %v", err)
	}
	nodesPath := filepath.Join(dir, "nodes")
	if err := os.WriteFile(nodesPath, []byte("blocker"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	err := EnsureDir()
	if err == nil {
		t.Fatal("EnsureDir() error = nil, want error when nodes dir creation fails")
	}
}

func TestLoadGlobalConfig_EmptyFile(t *testing.T) {
	home := t.TempDir()
	testenv.SetHome(t, home)

	path, err := ConfigPath()
	if err != nil {
		t.Fatalf("ConfigPath() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	// Write empty file — this is invalid JSON.
	if err := os.WriteFile(path, []byte(""), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	_, err = LoadGlobalConfig()
	if err == nil {
		t.Fatal("LoadGlobalConfig() error = nil, want error for empty file")
	}
}

func TestConfigPath_ContainsDir(t *testing.T) {
	path, err := ConfigPath()
	if err != nil {
		t.Fatalf("ConfigPath() error = %v", err)
	}
	dir, err := Dir()
	if err != nil {
		t.Fatalf("Dir() error = %v", err)
	}
	if !strings.HasPrefix(path, dir) {
		t.Fatalf("ConfigPath() %q does not start with Dir() %q", path, dir)
	}
}

func TestAllPathsSharePrefix_IncludesConfigAndAPIKey(t *testing.T) {
	dir, _ := Dir()
	configPath, _ := ConfigPath()
	apiKeyPath, _ := APIKeyPath()

	for name, p := range map[string]string{"ConfigPath": configPath, "APIKeyPath": apiKeyPath} {
		if !strings.HasPrefix(p, dir) {
			t.Fatalf("%s %q does not start with Dir() %q", name, p, dir)
		}
	}
}

func TestSaveGlobalConfig_FilePermissions(t *testing.T) {
	testenv.SetHome(t, t.TempDir())

	if err := SaveGlobalConfig(GlobalConfig{ControlURL: "https://test.example.com"}); err != nil {
		t.Fatalf("SaveGlobalConfig() error = %v", err)
	}

	path, _ := ConfigPath()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if runtime.GOOS != "windows" {
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("config file permissions = %o, want 0600", perm)
		}
	} else if !info.Mode().IsRegular() {
		t.Fatalf("config mode = %v, want regular file on Windows", info.Mode())
	}
}

func TestSaveGlobalConfig_IndentedJSON(t *testing.T) {
	testenv.SetHome(t, t.TempDir())

	cfg := GlobalConfig{ControlURL: "https://test.example.com"}
	if err := SaveGlobalConfig(cfg); err != nil {
		t.Fatalf("SaveGlobalConfig() error = %v", err)
	}

	path, _ := ConfigPath()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}

	// Verify it's indented (contains newlines within the JSON, not compact).
	if !strings.Contains(string(data), "\n  ") {
		t.Fatal("saved config is not indented")
	}
}

// These tests inject the platform default resolver failure directly. That keeps
// the error branches deterministic without escaping through a real user profile.
func TestDir_EmptyHomeError(t *testing.T) {
	stubDefaultConfigDirError(t)
	_, err := Dir()
	if err == nil {
		t.Fatal("Dir() error = nil, want default resolver error")
	}
}

func TestConfigPath_DirError(t *testing.T) {
	stubDefaultConfigDirError(t)
	_, err := ConfigPath()
	if err == nil {
		t.Fatal("ConfigPath() error = nil, want error when Dir() fails")
	}
}

func TestRegistryPath_DirError(t *testing.T) {
	stubDefaultConfigDirError(t)
	_, err := RegistryPath()
	if err == nil {
		t.Fatal("RegistryPath() error = nil, want error when Dir() fails")
	}
}

func TestPIDPath_DirError(t *testing.T) {
	stubDefaultConfigDirError(t)
	_, err := PIDPath()
	if err == nil {
		t.Fatal("PIDPath() error = nil, want error when Dir() fails")
	}
}

func TestNodesDir_DirError(t *testing.T) {
	stubDefaultConfigDirError(t)
	_, err := NodesDir()
	if err == nil {
		t.Fatal("NodesDir() error = nil, want error when Dir() fails")
	}
}

func TestAuthKeyPath_DirError(t *testing.T) {
	stubDefaultConfigDirError(t)
	_, err := AuthKeyPath()
	if err == nil {
		t.Fatal("AuthKeyPath() error = nil, want error when Dir() fails")
	}
}

func TestAPIKeyPath_DirError(t *testing.T) {
	stubDefaultConfigDirError(t)
	_, err := APIKeyPath()
	if err == nil {
		t.Fatal("APIKeyPath() error = nil, want error when Dir() fails")
	}
}

func TestLogDir_DirError(t *testing.T) {
	stubDefaultConfigDirError(t)
	_, err := LogDir()
	if err == nil {
		t.Fatal("LogDir() error = nil, want error when Dir() fails")
	}
}

func TestEnsureDir_DirError(t *testing.T) {
	stubDefaultConfigDirError(t)
	err := EnsureDir()
	if err == nil {
		t.Fatal("EnsureDir() error = nil, want error when Dir() fails")
	}
}

func TestLoadGlobalConfig_DirError(t *testing.T) {
	stubDefaultConfigDirError(t)
	_, err := LoadGlobalConfig()
	if err == nil {
		t.Fatal("LoadGlobalConfig() error = nil, want error when Dir() fails")
	}
}

func TestSaveGlobalConfig_DirError(t *testing.T) {
	stubDefaultConfigDirError(t)
	err := SaveGlobalConfig(GlobalConfig{ControlURL: "https://test.example.com"})
	if err == nil {
		t.Fatal("SaveGlobalConfig() error = nil, want error when Dir() fails")
	}
}

func TestEnsureDir_DirPermissions(t *testing.T) {
	testenv.SetHome(t, t.TempDir())

	if err := EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}

	dir, _ := Dir()
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if runtime.GOOS != "windows" {
		if perm := info.Mode().Perm(); perm != 0o700 {
			t.Fatalf("config dir permissions = %o, want 0700", perm)
		}
	} else if !info.IsDir() {
		t.Fatalf("config mode = %v, want directory on Windows", info.Mode())
	}
}

func TestCertsDir(t *testing.T) {
	dir, err := CertsDir()
	if err != nil {
		t.Fatalf("CertsDir() error = %v", err)
	}
	if !strings.HasSuffix(dir, filepath.Join("tslink", "certs")) {
		t.Fatalf("CertsDir() = %q, want suffix %q", dir, filepath.Join("tslink", "certs"))
	}
}

func TestCertsDir_DirError(t *testing.T) {
	stubDefaultConfigDirError(t)
	_, err := CertsDir()
	if err == nil {
		t.Fatal("CertsDir() error = nil, want error when Dir() fails")
	}
}

func TestCertsDir_SharesPrefix(t *testing.T) {
	dir, _ := Dir()
	certsDir, _ := CertsDir()
	if !strings.HasPrefix(certsDir, dir) {
		t.Fatalf("CertsDir() %q does not start with Dir() %q", certsDir, dir)
	}
}

func TestEnsureDir_CreatesCertsDir(t *testing.T) {
	testenv.SetHome(t, t.TempDir())

	if err := EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}

	certsDir, _ := CertsDir()
	info, err := os.Stat(certsDir)
	if err != nil {
		t.Fatalf("certs dir does not exist after EnsureDir: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("certs dir is not a directory")
	}
}

func TestEnsureDir_CertsDirError(t *testing.T) {
	home := t.TempDir()
	testenv.SetHome(t, home)

	// Create the base config dir, logs dir, and nodes dir, but block certs dir.
	dir, _ := Dir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	logDir, _ := LogDir()
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		t.Fatalf("MkdirAll(logDir) error = %v", err)
	}
	nodesDir, _ := NodesDir()
	if err := os.MkdirAll(nodesDir, 0o700); err != nil {
		t.Fatalf("MkdirAll(nodesDir) error = %v", err)
	}
	certsPath := filepath.Join(dir, "certs")
	if err := os.WriteFile(certsPath, []byte("blocker"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	err := EnsureDir()
	if err == nil {
		t.Fatal("EnsureDir() error = nil, want error when certs dir creation fails")
	}
}

func TestClientSecretPath(t *testing.T) {
	path, err := ClientSecretPath()
	if err != nil {
		t.Fatalf("ClientSecretPath() error = %v", err)
	}
	if !strings.HasSuffix(path, filepath.Join("tslink", "clientsecret")) {
		t.Fatalf("ClientSecretPath() = %q, want suffix %q", path, filepath.Join("tslink", "clientsecret"))
	}
}

func TestClientSecretPath_DirError(t *testing.T) {
	stubDefaultConfigDirError(t)
	_, err := ClientSecretPath()
	if err == nil {
		t.Fatal("ClientSecretPath() error = nil, want error when Dir() fails")
	}
}

func TestSaveGlobalConfig_MarshalError(t *testing.T) {
	testenv.SetHome(t, t.TempDir())

	// Inject a failing marshal function.
	orig := jsonMarshalIndent
	jsonMarshalIndent = func(v any, prefix, indent string) ([]byte, error) {
		return nil, errors.New("synthetic marshal error")
	}
	t.Cleanup(func() { jsonMarshalIndent = orig })

	err := SaveGlobalConfig(GlobalConfig{ControlURL: "https://test.example.com"})
	if err == nil {
		t.Fatal("SaveGlobalConfig() error = nil, want marshal error")
	}
	if !strings.Contains(err.Error(), "synthetic marshal error") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGlobalConfig_DefaultTag(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	os.MkdirAll(filepath.Join(os.Getenv("HOME"), ".config", "tslink"), 0o700)

	want := GlobalConfig{DefaultTag: "tag:myteam"}
	if err := SaveGlobalConfig(want); err != nil {
		t.Fatalf("SaveGlobalConfig() error = %v", err)
	}
	got, err := LoadGlobalConfig()
	if err != nil {
		t.Fatalf("LoadGlobalConfig() error = %v", err)
	}
	if got.DefaultTag != want.DefaultTag {
		t.Fatalf("DefaultTag = %q, want %q", got.DefaultTag, want.DefaultTag)
	}
}

func TestGetDefaultTag_Unset(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	os.MkdirAll(filepath.Join(os.Getenv("HOME"), ".config", "tslink"), 0o700)

	tag := GetDefaultTag()
	if tag != "tag:tsmain" {
		t.Fatalf("GetDefaultTag() = %q, want tag:tsmain", tag)
	}
}

func TestGetDefaultTag_Custom(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	os.MkdirAll(filepath.Join(os.Getenv("HOME"), ".config", "tslink"), 0o700)

	SaveGlobalConfig(GlobalConfig{DefaultTag: "tag:myteam"})
	tag := GetDefaultTag()
	if tag != "tag:myteam" {
		t.Fatalf("GetDefaultTag() = %q, want tag:myteam", tag)
	}
}
