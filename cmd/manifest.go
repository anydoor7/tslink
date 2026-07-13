package cmd

import (
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/monody0007/tslink/internal/output"
	"github.com/monody0007/tslink/internal/registry"
	"github.com/monody0007/tslink/internal/security"
)

// CLIManifest is the single machine-readable source of truth for the shipped
// command surface. It is generated from the live Cobra tree plus the output
// exit codes, registry schema version, and security capability manifest, so the
// documentation site can parity-check the exported fixture instead of
// hand-maintaining a second copy of these facts.
type CLIManifest struct {
	SchemaVersion         int                         `json:"schema_version"`
	RegistrySchemaVersion int                         `json:"registry_schema_version"`
	ExitCodes             map[string]int              `json:"exit_codes"`
	Commands              []CommandInfo               `json:"commands"`
	Capabilities          security.CapabilityManifest `json:"capabilities"`
}

// CommandInfo describes one command in the tree.
type CommandInfo struct {
	Path  string     `json:"path"`
	Short string     `json:"short"`
	Flags []FlagInfo `json:"flags,omitempty"`
}

// FlagInfo describes one command-local flag.
type FlagInfo struct {
	Name      string `json:"name"`
	Shorthand string `json:"shorthand,omitempty"`
	Type      string `json:"type"`
	Default   string `json:"default,omitempty"`
}

// Manifest walks the root command and assembles the CLI manifest. It is pure
// and side-effect free; it never executes a command.
func Manifest() CLIManifest {
	m := CLIManifest{
		SchemaVersion:         1,
		RegistrySchemaVersion: registry.CurrentRegistrySchemaVersion,
		ExitCodes: map[string]int{
			"success":   output.ExitSuccess,
			"error":     output.ExitError,
			"usage":     output.ExitUsage,
			"auth":      output.ExitAuth,
			"conflict":  output.ExitConflict,
			"not_found": output.ExitNotFound,
			"warning":   output.ExitWarning,
			"critical":  output.ExitCritical,
		},
	}
	if cm, err := security.LoadCapabilityManifest(); err == nil {
		m.Capabilities = cm
	}

	var walk func(c *cobra.Command, prefix string)
	walk = func(c *cobra.Command, prefix string) {
		path := strings.TrimSpace(prefix + " " + c.Name())
		info := CommandInfo{Path: path, Short: c.Short}
		c.LocalFlags().VisitAll(func(f *pflag.Flag) {
			info.Flags = append(info.Flags, FlagInfo{
				Name:      f.Name,
				Shorthand: f.Shorthand,
				Type:      f.Value.Type(),
				Default:   f.DefValue,
			})
		})
		sort.Slice(info.Flags, func(i, j int) bool { return info.Flags[i].Name < info.Flags[j].Name })
		m.Commands = append(m.Commands, info)
		children := c.Commands()
		sort.Slice(children, func(i, j int) bool { return children[i].Name() < children[j].Name() })
		for _, sub := range children {
			if sub.Hidden || sub.Name() == "help" || sub.Name() == "completion" {
				continue
			}
			walk(sub, path)
		}
	}
	walk(rootCmd, "")
	sort.Slice(m.Commands, func(i, j int) bool { return m.Commands[i].Path < m.Commands[j].Path })
	return m
}
