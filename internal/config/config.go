package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"

	"github.com/monody0007/tslink/internal/atomicfile"
	"github.com/monody0007/tslink/internal/filelock"
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

// fallbackDefaultTag is the default tag when config.json sets none.
const fallbackDefaultTag = "tag:tsmain"

// GetDefaultTag returns the configured default tag, falling back to
// "tag:tsmain" when config.json is missing, unreadable, or sets none. It is
// for callers that only read or display the tag. A caller that persists the
// default tag into registry.json or a credential must use DefaultTag, which
// refuses a config.json it cannot read instead of substituting a tag.
func GetDefaultTag() string {
	cfg, err := loadGlobalConfig(false)
	if err != nil || cfg.DefaultTag == "" {
		return fallbackDefaultTag
	}
	return cfg.DefaultTag
}

// DefaultTag returns the default tag for callers that persist it: the
// configured one, or "tag:tsmain" when config.json is absent or sets none.
// When config.json exists but cannot be parsed, or has a key TSLink does not
// know (a typo such as default_tags), it returns a ConfigLoadError rather than
// a tag the user did not configure.
func DefaultTag() (string, error) {
	cfg, err := LoadGlobalConfig()
	if err != nil {
		return "", err
	}
	if cfg.DefaultTag == "" {
		return fallbackDefaultTag, nil
	}
	return cfg.DefaultTag, nil
}

// CodeConfigLoadFailed is the stable code of ConfigLoadError.
const CodeConfigLoadFailed = "config_load_failed"

// ConfigLoadError reports a config.json that exists but cannot be decoded
// under the strict policy registry.json also follows: malformed JSON, an
// unknown key, or trailing data.
type ConfigLoadError struct {
	Path    string
	Problem string
}

func (e *ConfigLoadError) Error() string {
	return fmt.Sprintf("config.json at %q cannot be loaded: %s", e.Path, e.Problem)
}

func (e *ConfigLoadError) StableCode() string { return CodeConfigLoadFailed }

// NextCommands returns the recovery steps for a config.json TSLink refuses.
func (e *ConfigLoadError) NextCommands() []string {
	return []string{
		fmt.Sprintf("Fix %s: %s (known keys: control_url, default_tag, mcp)", e.Path, e.Problem),
		"tslink doctor --json",
	}
}

// ConfigPath returns the path to the global config file.
func ConfigPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

// LoadGlobalConfig reads the global config file. Returns zero-value config if
// not found. Decoding is strict, like registry.json: a key TSLink does not know
// is a ConfigLoadError rather than silently ignored, so a typo is reported and
// a rewrite cannot drop a key it did not read.
func LoadGlobalConfig() (GlobalConfig, error) {
	return loadGlobalConfig(true)
}

func loadGlobalConfig(strict bool) (GlobalConfig, error) {
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
	if !strict {
		if err := json.Unmarshal(data, &cfg); err != nil {
			return GlobalConfig{}, err
		}
		return cfg, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return GlobalConfig{}, &ConfigLoadError{Path: path, Problem: configDecodeProblem(err)}
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return GlobalConfig{}, &ConfigLoadError{Path: path, Problem: "unexpected data after the JSON object"}
	}
	return cfg, nil
}

var unknownConfigFieldRegexp = regexp.MustCompile(`^json: unknown field "([^"]+)"$`)

func configDecodeProblem(err error) string {
	if matches := unknownConfigFieldRegexp.FindStringSubmatch(err.Error()); len(matches) == 2 {
		return fmt.Sprintf("unknown key %q", matches[1])
	}
	if errors.Is(err, io.EOF) {
		return "the file is empty"
	}
	return err.Error()
}

// globalConfigAfterLoadHook runs between the read and the write of
// UpdateGlobalConfig. Tests use it to interleave a second writer.
var globalConfigAfterLoadHook func()

// UpdateGlobalConfig applies mutate to config.json as one read-modify-write
// under config.json.lock, so two writers (config set and tags set-default)
// cannot erase each other's change. The read is strict, so a config.json this
// version cannot fully read is refused rather than rewritten without the
// keys it did not understand.
func UpdateGlobalConfig(mutate func(*GlobalConfig) error) error {
	path, err := ConfigPath()
	if err != nil {
		return err
	}
	if err := atomicfile.EnsurePrivateDir(filepath.Dir(path)); err != nil {
		return err
	}
	lockPath := path + ".lock"
	if err := atomicfile.ConvergePrivateFile(lockPath); err != nil {
		return err
	}
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lockFile.Close()
	if err := filelock.Lock(lockFile); err != nil {
		return err
	}
	defer filelock.Unlock(lockFile)

	cfg, err := LoadGlobalConfig()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if globalConfigAfterLoadHook != nil {
		globalConfigAfterLoadHook()
	}
	if err := mutate(&cfg); err != nil {
		return err
	}
	if err := SaveGlobalConfig(cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	return nil
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

// Base names of the config-directory files that hold credential state.
//
// They are named here rather than inline in each path helper because a second
// consumer needs to recognise these files by name without asking for their
// absolute paths: the daemon watches its whole config directory with fsnotify
// and has to decide, per event, whether the file that changed is one of these.
// Two spellings of the same name would make that consumer silently stop
// matching the day a path helper changed.
const (
	AuthHandoffFileName    = "auth-handoff.json"
	APIKeyFileName         = "apikey"
	ClientSecretFileName   = "clientsecret"
	CredentialMetaFileName = "credential-meta.json"
)

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
	return NodeOwnershipPathIn(dir), nil
}

// NodeOwnershipPathIn names the ownership ledger inside an arbitrary config
// directory, for callers that already hold one (see NodesDirIn).
func NodeOwnershipPathIn(configDir string) string {
	return filepath.Join(configDir, "node-ownership.json")
}

// NodeIdentitiesDirIn names the directory of per-service node identity
// records (node-identities/<service>.json) inside a config directory.
func NodeIdentitiesDirIn(configDir string) string {
	return filepath.Join(configDir, "node-identities")
}

// AuthHandoffPath returns the path used to publish a pending interactive
// tsnet enrollment from a daemon child to CLI/status consumers.
func AuthHandoffPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, AuthHandoffFileName), nil
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
	return NodesDirIn(dir), nil
}

// NodesDirIn names the tsnet state directory inside an arbitrary config
// directory, so a caller that already knows which config directory it is
// operating on does not have to re-derive it from the environment. Callers that
// hold a path into a config directory -- a registry path, say -- must use this
// rather than NodesDir: the two answer differently whenever the caller is not
// operating on the process default, and silently mixing them means writing to
// one config directory while deleting from another.
func NodesDirIn(configDir string) string {
	return filepath.Join(configDir, "nodes")
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
	return filepath.Join(dir, APIKeyFileName), nil
}

func ClientSecretPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, ClientSecretFileName), nil
}

// CredentialMetaPath stores value-free credential bookkeeping (fingerprint,
// stored_at, expires_at, last verification) for each credential slot. It never
// contains credential material.
func CredentialMetaPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, CredentialMetaFileName), nil
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
