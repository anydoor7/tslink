package config

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/monody0007/tslink/internal/atomicfile"
)

// ConfigDirEnv overrides the default per-user configuration directory. It is
// primarily intended for automation and isolated agent verification.
const ConfigDirEnv = "TSLINK_CONFIG_DIR"

// jsonMarshalIndent is a package-level variable to allow test injection.
var jsonMarshalIndent = json.MarshalIndent

// GlobalConfig holds tslink-wide settings persisted in config.json.
type GlobalConfig struct {
	ControlURL string `json:"control_url,omitempty"`
	DefaultTag string `json:"default_tag,omitempty"`
}

// GetDefaultTag returns the configured default tag, falling back to "tag:tsmain".
func GetDefaultTag() string {
	cfg, err := LoadGlobalConfig()
	if err != nil || cfg.DefaultTag == "" {
		return "tag:tsmain"
	}
	return cfg.DefaultTag
}

// ConfigPath returns the path to the global config file.
func ConfigPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

// LoadGlobalConfig reads the global config file. Returns zero-value config if not found.
func LoadGlobalConfig() (GlobalConfig, error) {
	path, err := ConfigPath()
	if err != nil {
		return GlobalConfig{}, err
	}
	if err := atomicfile.ConvergePrivateFile(path); err != nil {
		return GlobalConfig{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return GlobalConfig{}, nil
		}
		return GlobalConfig{}, err
	}
	var cfg GlobalConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return GlobalConfig{}, err
	}
	return cfg, nil
}

// SaveGlobalConfig writes the global config to disk.
func SaveGlobalConfig(cfg GlobalConfig) error {
	path, err := ConfigPath()
	if err != nil {
		return err
	}
	data, err := jsonMarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return atomicfile.WriteFile(path, data)
}

func Dir() (string, error) {
	if dir := os.Getenv(ConfigDirEnv); dir != "" {
		return filepath.Clean(dir), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "tslink"), nil
}

func RegistryPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "registry.json"), nil
}

func RuntimeSnapshotPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "runtime.json"), nil
}

// AuthHandoffPath returns the path used to publish a pending interactive
// tsnet enrollment from a daemon child to CLI/status consumers.
func AuthHandoffPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "auth-handoff.json"), nil
}

func PIDPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "tslink.pid"), nil
}

func NodesDir() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "nodes"), nil
}

func AuthKeyPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "authkey"), nil
}

func APIKeyPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "apikey"), nil
}

func ClientSecretPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "clientsecret"), nil
}

func LogDir() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "logs"), nil
}

func CertsDir() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "certs"), nil
}

func EnsureDir() error {
	dir, err := Dir()
	if err != nil {
		return err
	}
	logDir := filepath.Join(dir, "logs")
	nodesDir := filepath.Join(dir, "nodes")
	certsDir := filepath.Join(dir, "certs")
	for _, d := range []string{dir, logDir, nodesDir, certsDir} {
		if err := atomicfile.EnsurePrivateDir(d); err != nil {
			return err
		}
	}
	return nil
}
