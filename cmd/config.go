package cmd

import (
	"fmt"
	"io"
	"strings"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/output"
	"github.com/anydoor7/tslink/internal/registry"
	"github.com/spf13/cobra"
)

// ConfigSetResult is the JSON payload for config set.
type ConfigSetResult struct {
	Key     string `json:"key"`
	Value   string `json:"value"`
	Cleared bool   `json:"cleared"`
}

// ConfigGetResult is the JSON payload for config get.
type ConfigGetResult struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	IsSet bool   `json:"is_set"`
}

// ConfigListResult is the JSON payload for config list.
type ConfigListResult struct {
	Items []ConfigItem `json:"items"`
}

// ConfigItem represents a single config key-value pair.
type ConfigItem struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	IsSet bool   `json:"is_set"`
}

// validConfigKeys lists all supported global config keys.
var validConfigKeys = []string{"control-url", "access-log-enabled", "access-log-path", "access-log-retention-days", "access-log-max-bytes", "access-log-queue-size"}

// configUpdateGlobalFn is the locked read-modify-write of config.json.
var configUpdateGlobalFn = config.UpdateGlobalConfig

// configSet persists a key-value pair to global config.
func configSet(key, value string, out io.Writer, isJSON bool) error {
	switch key {
	case "access-log-enabled", "access-log-path", "access-log-retention-days", "access-log-max-bytes", "access-log-queue-size":
		if err := updateAccessLogOption(&config.GlobalConfig{}, key, value); err != nil {
			return output.ErrUsage(err.Error())
		}
	case "control-url":
		if err := registry.ValidateControlURL(value); err != nil {
			return output.ErrUsage(err.Error())
		}
	default:
		return fmt.Errorf("unknown config key: %q (valid keys: %s)", key, strings.Join(validConfigKeys, ", "))
	}

	if err := configUpdateGlobalFn(func(cfg *config.GlobalConfig) error {
		if key == "control-url" {
			cfg.ControlURL = value
		} else {
			return updateAccessLogOption(cfg, key, value)
		}
		return nil
	}); err != nil {
		return err
	}

	if isJSON {
		output.Success("config set", ConfigSetResult{Key: key, Value: value, Cleared: value == ""})
		return nil
	}

	if value == "" {
		fmt.Fprintf(out, "→ %s cleared\n", key)
	} else {
		fmt.Fprintf(out, "→ %s = %s\n", key, value)
	}
	return nil
}

// configGet reads a key from global config and writes it to out.
func configGet(key string, out io.Writer, isJSON bool) error {
	cfg, err := config.LoadGlobalConfig()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	switch key {
	case "access-log-enabled", "access-log-path", "access-log-retention-days", "access-log-max-bytes", "access-log-queue-size":
		value, set := accessLogOptionValue(cfg, key)
		if isJSON {
			output.Success("config get", ConfigGetResult{Key: key, Value: value, IsSet: set})
		} else {
			if !set {
				value = "(default)"
			}
			fmt.Fprintln(out, value)
		}
		return nil
	case "control-url":
		if isJSON {
			output.Success("config get", ConfigGetResult{Key: key, Value: cfg.ControlURL, IsSet: cfg.ControlURL != ""})
			return nil
		}
		value := cfg.ControlURL
		if value == "" {
			value = "(not set, using default Tailscale)"
		}
		fmt.Fprintln(out, value)
	default:
		return fmt.Errorf("unknown config key: %q (valid keys: %s)", key, strings.Join(validConfigKeys, ", "))
	}
	return nil
}

// configList writes all config key-value pairs to out.
func configList(out io.Writer, isJSON bool) error {
	cfg, err := config.LoadGlobalConfig()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	if isJSON {
		items := []ConfigItem{
			{Key: "control-url", Value: cfg.ControlURL, IsSet: cfg.ControlURL != ""},
		}
		for _, key := range validConfigKeys[1:] {
			value, set := accessLogOptionValue(cfg, key)
			items = append(items, ConfigItem{Key: key, Value: value, IsSet: set})
		}
		output.Success("config list", ConfigListResult{Items: items})
		return nil
	}

	controlURL := cfg.ControlURL
	if controlURL == "" {
		controlURL = "(not set)"
	}
	fmt.Fprintf(out, "control-url = %s\n", controlURL)
	for _, key := range validConfigKeys[1:] {
		value, set := accessLogOptionValue(cfg, key)
		if !set {
			value = "(default)"
		}
		fmt.Fprintf(out, "%s = %s\n", key, value)
	}
	return nil
}

func init() {
	configCmd := &cobra.Command{
		Use:   "config",
		Short: "Manage global TSLink configuration",
		Long: `View and modify global TSLink settings.

Examples:
  tslink config set control-url https://headscale.example.com
  tslink config get control-url
  tslink config list
  tslink config set control-url ""   # clear (use default Tailscale)`,
		Args: cobra.NoArgs,
		RunE: runCommandGroup,
	}

	setCmd := &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Set a global config value",
		Long: `Set a global configuration value. Settings are stored in
~/.config/tslink/config.json and persist across sessions.

Available keys:

  access-log-enabled         true or false (default true)
  access-log-path            true or false (default true)
  access-log-retention-days  1..3650 (default 30)
  access-log-max-bytes       65536..1073741824 (default 67108864)
  access-log-queue-size      1..65536 (default 1024)
  Empty values reset the access-log option. Restart serve after changing these.

  control-url    Custom Tailscale control server URL (e.g. Headscale).
                 Must be a valid URL. Set to "" to clear and use the
                 default Tailscale coordination server.

Priority order (highest to lowest):
  1. Per-service control_url in registry.json
  2. CLI flag: tslink serve --control-url
  3. Global config: tslink config set control-url
  4. Default: Tailscale managed servers

Examples:
  tslink config set control-url https://headscale.example.com
  tslink config set control-url ""     Clear (revert to default)`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			value := ""
			if len(args) == 2 {
				value = args[1]
			}
			return configSet(args[0], value, cmd.OutOrStdout(), jsonOutput(cmd))
		},
	}

	getCmd := &cobra.Command{
		Use:   "get <key>",
		Short: "Get a global config value",
		Long: `Read a global configuration value. If the key has not been set,
prints "(not set, using default Tailscale)" for control-url.

Available keys: control-url, access-log-enabled, access-log-path, access-log-retention-days, access-log-max-bytes, access-log-queue-size

Examples:
  tslink config get control-url`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return configGet(args[0], cmd.OutOrStdout(), jsonOutput(cmd))
		},
	}

	listCmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List all global config values",
		Long: `Display all global configuration key-value pairs.

Output format:
  control-url = https://headscale.example.com
  control-url = (not set)

Examples:
  tslink config list
  tslink config ls`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return configList(cmd.OutOrStdout(), jsonOutput(cmd))
		},
	}

	configCmd.AddCommand(setCmd, getCmd, listCmd)
	rootCmd.AddCommand(configCmd)
}
