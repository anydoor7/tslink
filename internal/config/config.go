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

// Package-level variables allow deterministic error-path injection in tests.
var (
	jsonMarshalIndent = json.MarshalIndent
	defaultConfigDir  = platformDefaultConfigDir
)

// GlobalConfig holds tslink-wide settings persisted in config.json.
type GlobalConfig struct {
	ControlURL string     `json:"control_url,omitempty"`
	DefaultTag string     `json:"default_tag,omitempty"`
	MCP        *MCPConfig `json:"mcp,omitempty"`
}

// MCPConfig configures the optional remote MCP control plane the daemon can
// serve on its own tsnet node. A nil pointer, or Enabled false, means the
// daemon opens no control-plane listener and creates no tsnet node for it.
//
// Allow is the principal list the endpoint authorizes against. It is kept in
// durable configuration rather than on the daemon command line because it is
// the security boundary of the whole control plane: an empty list is a refusal
// to start, never an invitation to everyone.
type MCPConfig struct {
	Enabled  bool     `json:"enabled,omitempty"`
	Allow    []string `json:"allow,omitempty"`
	NodeName string   `json:"node_name,omitempty"`
	// EventsKeepalive is the event stream's heartbeat period as a Go duration
	// string, for example "20s". Empty means the daemon's default. It is
	// configuration rather than a flag because it is a property of the
	// deployment's network path, not of one serve invocation.
	EventsKeepalive string `json:"events_keepalive,omitempty"`
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
	return defaultConfigDir()
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

// NodeOwnershipPath stores durable service-to-StableNodeID ownership proof.
// Unlike runtime.json it survives daemon shutdown and service removal so a
// later reconciliation can safely delete only TSLink-owned devices.
func NodeOwnershipPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "node-ownership.json"), nil
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

// MCPNodeDir returns the tsnet state directory for the remote MCP control
// plane. It lives beside NodesDir rather than inside it so the control plane
// can never collide with, or be mistaken for, a registered service's node
// state: everything under NodesDir is named after a registry entry.
func MCPNodeDir() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "mcp-node"), nil
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

// CredentialMetaPath stores value-free credential bookkeeping (fingerprint,
// stored_at, expires_at, last verification) for each credential slot. It never
// contains credential material.
func CredentialMetaPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "credential-meta.json"), nil
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
