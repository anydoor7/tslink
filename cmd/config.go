package cmd

import (
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/monody0007/tslink/internal/config"
	"github.com/spf13/cobra"
)

// validConfigKeys lists all supported global config keys.
var validConfigKeys = []string{"control-url"}

// configSet persists a key-value pair to global config.
func configSet(key, value string, out io.Writer) error {
	cfg, err := config.LoadGlobalConfig()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	switch key {
	case "control-url":
		if value != "" {
			if _, err := url.ParseRequestURI(value); err != nil {
				return fmt.Errorf("invalid URL %q: %w", value, err)
			}
		}
		cfg.ControlURL = value
	default:
		return fmt.Errorf("unknown config key: %q (valid keys: %s)", key, strings.Join(validConfigKeys, ", "))
	}

	if err := config.SaveGlobalConfig(cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	if value == "" {
		fmt.Fprintf(out, "→ %s cleared\n", key)
	} else {
		fmt.Fprintf(out, "→ %s = %s\n", key, value)
	}
	return nil
}

// configGet reads a key from global config and writes it to out.
func configGet(key string, out io.Writer) error {
	cfg, err := config.LoadGlobalConfig()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	switch key {
	case "control-url":
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
func configList(out io.Writer) error {
	cfg, err := config.LoadGlobalConfig()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	controlURL := cfg.ControlURL
	if controlURL == "" {
		controlURL = "(not set)"
	}
	fmt.Fprintf(out, "control-url = %s\n", controlURL)
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
	}

	setCmd := &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Set a global config value",
		Long: `Set a global configuration value. Settings are stored in
~/.config/tslink/config.json and persist across sessions.

Available keys:

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
			return configSet(args[0], value, cmd.OutOrStdout())
		},
	}

	getCmd := &cobra.Command{
		Use:   "get <key>",
		Short: "Get a global config value",
		Long: `Read a global configuration value. If the key has not been set,
prints "(not set, using default Tailscale)" for control-url.

Available keys: control-url

Examples:
  tslink config get control-url`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return configGet(args[0], cmd.OutOrStdout())
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
			return configList(cmd.OutOrStdout())
		},
	}

	configCmd.AddCommand(setCmd, getCmd, listCmd)
	rootCmd.AddCommand(configCmd)
}
