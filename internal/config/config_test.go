package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDir(t *testing.T) {
	dir, err := Dir()
	if err != nil {
		t.Fatalf("Dir() error = %v", err)
	}

	home, _ := os.UserHomeDir()
	want := filepath.Join(home, ".config", "tslink")
	if dir != want {
		t.Fatalf("Dir() = %q, want %q", dir, want)
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
	t.Setenv("HOME", t.TempDir())

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
	t.Setenv("HOME", t.TempDir())

	if err := EnsureDir(); err != nil {
		t.Fatalf("first EnsureDir() error = %v", err)
	}
	if err := EnsureDir(); err != nil {
		t.Fatalf("second EnsureDir() error = %v", err)
	}
}

func TestEnsureDir_Error(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

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
	t.Setenv("HOME", t.TempDir())

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
	t.Setenv("HOME", home)

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
	t.Setenv("HOME", home)

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
	t.Setenv("HOME", t.TempDir())

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
	t.Setenv("HOME", t.TempDir())

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
	t.Setenv("HOME", t.TempDir())

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
	t.Setenv("HOME", home)

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
	t.Setenv("HOME", home)

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

func TestLoadGlobalConfig_ReadPermissionError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	path, err := ConfigPath()
	if err != nil {
		t.Fatalf("ConfigPath() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	// Write a valid config file then remove read permission.
	if err := os.WriteFile(path, []byte(`{"control_url":"x"}`), 0o000); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	_, err = LoadGlobalConfig()
	if err == nil {
		t.Fatal("LoadGlobalConfig() error = nil, want permission error")
	}
	if os.IsNotExist(err) {
		t.Fatal("LoadGlobalConfig() returned IsNotExist, want permission error")
	}
}

func TestSaveGlobalConfig_EmptyConfig(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

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
	t.Setenv("HOME", t.TempDir())

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
	t.Setenv("HOME", home)

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
	t.Setenv("HOME", home)

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
	t.Setenv("HOME", home)

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
	t.Setenv("HOME", t.TempDir())

	if err := SaveGlobalConfig(GlobalConfig{ControlURL: "https://test.example.com"}); err != nil {
		t.Fatalf("SaveGlobalConfig() error = %v", err)
	}

	path, _ := ConfigPath()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	perm := info.Mode().Perm()
	if perm != 0o600 {
		t.Fatalf("config file permissions = %o, want 0600", perm)
	}
}

func TestSaveGlobalConfig_IndentedJSON(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

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

// dirErrorTests exercises the error branches of all functions that depend on Dir().
// On macOS with cgo enabled, os.UserHomeDir() never fails (system call fallback),
// so these tests are skipped when HOME="" still returns a valid home directory.
func TestDir_EmptyHomeError(t *testing.T) {
	t.Setenv("HOME", "")
	_, err := Dir()
	if err == nil {
		t.Skip("os.UserHomeDir() does not fail with empty HOME on this platform (cgo fallback)")
	}
}

func TestConfigPath_DirError(t *testing.T) {
	t.Setenv("HOME", "")
	_, err := Dir()
	if err == nil {
		t.Skip("os.UserHomeDir() does not fail with empty HOME on this platform")
	}
	_, err = ConfigPath()
	if err == nil {
		t.Fatal("ConfigPath() error = nil, want error when Dir() fails")
	}
}

func TestRegistryPath_DirError(t *testing.T) {
	t.Setenv("HOME", "")
	_, err := Dir()
	if err == nil {
		t.Skip("os.UserHomeDir() does not fail with empty HOME on this platform")
	}
	_, err = RegistryPath()
	if err == nil {
		t.Fatal("RegistryPath() error = nil, want error when Dir() fails")
	}
}

func TestPIDPath_DirError(t *testing.T) {
	t.Setenv("HOME", "")
	_, err := Dir()
	if err == nil {
		t.Skip("os.UserHomeDir() does not fail with empty HOME on this platform")
	}
	_, err = PIDPath()
	if err == nil {
		t.Fatal("PIDPath() error = nil, want error when Dir() fails")
	}
}

func TestNodesDir_DirError(t *testing.T) {
	t.Setenv("HOME", "")
	_, err := Dir()
	if err == nil {
		t.Skip("os.UserHomeDir() does not fail with empty HOME on this platform")
	}
	_, err = NodesDir()
	if err == nil {
		t.Fatal("NodesDir() error = nil, want error when Dir() fails")
	}
}

func TestAuthKeyPath_DirError(t *testing.T) {
	t.Setenv("HOME", "")
	_, err := Dir()
	if err == nil {
		t.Skip("os.UserHomeDir() does not fail with empty HOME on this platform")
	}
	_, err = AuthKeyPath()
	if err == nil {
		t.Fatal("AuthKeyPath() error = nil, want error when Dir() fails")
	}
}

func TestAPIKeyPath_DirError(t *testing.T) {
	t.Setenv("HOME", "")
	_, err := Dir()
	if err == nil {
		t.Skip("os.UserHomeDir() does not fail with empty HOME on this platform")
	}
	_, err = APIKeyPath()
	if err == nil {
		t.Fatal("APIKeyPath() error = nil, want error when Dir() fails")
	}
}

func TestLogDir_DirError(t *testing.T) {
	t.Setenv("HOME", "")
	_, err := Dir()
	if err == nil {
		t.Skip("os.UserHomeDir() does not fail with empty HOME on this platform")
	}
	_, err = LogDir()
	if err == nil {
		t.Fatal("LogDir() error = nil, want error when Dir() fails")
	}
}

func TestEnsureDir_DirError(t *testing.T) {
	t.Setenv("HOME", "")
	_, err := Dir()
	if err == nil {
		t.Skip("os.UserHomeDir() does not fail with empty HOME on this platform")
	}
	err = EnsureDir()
	if err == nil {
		t.Fatal("EnsureDir() error = nil, want error when Dir() fails")
	}
}

func TestLoadGlobalConfig_DirError(t *testing.T) {
	t.Setenv("HOME", "")
	_, err := Dir()
	if err == nil {
		t.Skip("os.UserHomeDir() does not fail with empty HOME on this platform")
	}
	_, err = LoadGlobalConfig()
	if err == nil {
		t.Fatal("LoadGlobalConfig() error = nil, want error when Dir() fails")
	}
}

func TestSaveGlobalConfig_DirError(t *testing.T) {
	t.Setenv("HOME", "")
	_, err := Dir()
	if err == nil {
		t.Skip("os.UserHomeDir() does not fail with empty HOME on this platform")
	}
	err = SaveGlobalConfig(GlobalConfig{ControlURL: "https://test.example.com"})
	if err == nil {
		t.Fatal("SaveGlobalConfig() error = nil, want error when Dir() fails")
	}
}

func TestEnsureDir_DirPermissions(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	if err := EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}

	dir, _ := Dir()
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	perm := info.Mode().Perm()
	if perm != 0o700 {
		t.Fatalf("config dir permissions = %o, want 0700", perm)
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
	t.Setenv("HOME", "")
	_, err := Dir()
	if err == nil {
		t.Skip("os.UserHomeDir() does not fail with empty HOME on this platform")
	}
	_, err = CertsDir()
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
	t.Setenv("HOME", t.TempDir())

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
	t.Setenv("HOME", home)

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
	t.Setenv("HOME", "")
	_, err := Dir()
	if err == nil {
		t.Skip("os.UserHomeDir() does not fail with empty HOME on this platform")
	}
	_, err = ClientSecretPath()
	if err == nil {
		t.Fatal("ClientSecretPath() error = nil, want error when Dir() fails")
	}
}

func TestSaveGlobalConfig_MarshalError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

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
	t.Setenv("HOME", t.TempDir())
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
	t.Setenv("HOME", t.TempDir())
	os.MkdirAll(filepath.Join(os.Getenv("HOME"), ".config", "tslink"), 0o700)

	tag := GetDefaultTag()
	if tag != "tag:tsmain" {
		t.Fatalf("GetDefaultTag() = %q, want tag:tsmain", tag)
	}
}

func TestGetDefaultTag_Custom(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	os.MkdirAll(filepath.Join(os.Getenv("HOME"), ".config", "tslink"), 0o700)

	SaveGlobalConfig(GlobalConfig{DefaultTag: "tag:myteam"})
	tag := GetDefaultTag()
	if tag != "tag:myteam" {
		t.Fatalf("GetDefaultTag() = %q, want tag:myteam", tag)
	}
}
