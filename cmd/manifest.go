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
	Toolchain             ToolchainInfo               `json:"toolchain"`
	ExitCodes             map[string]int              `json:"exit_codes"`
	RegistrySchema        RegistrySchemaInfo          `json:"registry_schema"`
	APIActions            []string                    `json:"api_actions"`
	CredentialSources     CredentialSources           `json:"credential_sources"`
	HighRiskOperations    []HighRiskOperation         `json:"high_risk_operations"`
	Release               ReleaseInfo                 `json:"release"`
	Commands              []CommandInfo               `json:"commands"`
	Capabilities          security.CapabilityManifest `json:"capabilities"`
}

type ToolchainInfo struct {
	MinimumGoVersion  string `json:"minimum_go_version"`
	GoReleaserVersion string `json:"goreleaser_version"`
	HomebrewArtifact  string `json:"homebrew_artifact"`
}

type RegistrySchemaInfo struct {
	Version             int      `json:"version"`
	ServiceTypes        []string `json:"service_types"`
	RequiredFields      []string `json:"required_fields"`
	UnavailableFeatures []string `json:"unavailable_features"`
}

type CredentialSources struct {
	Recommended []CredentialSource `json:"recommended"`
	Automation  []CredentialSource `json:"automation"`
	Avoid       []CredentialSource `json:"avoid"`
	Storage     []CredentialSource `json:"storage"`
}

type CredentialSource struct {
	ID          string   `json:"id"`
	Command     string   `json:"command,omitempty"`
	Flags       []string `json:"flags,omitempty"`
	Environment []string `json:"environment,omitempty"`
	Boundary    string   `json:"boundary"`
}

type HighRiskOperation struct {
	Command       string   `json:"command"`
	Operation     string   `json:"operation"`
	Default       string   `json:"default"`
	RequiredFlags []string `json:"required_flags"`
	Boundary      string   `json:"boundary"`
}

type ReleaseInfo struct {
	PublicReleaseAvailable bool     `json:"public_release_available"`
	PrebuiltAvailable      bool     `json:"prebuilt_available"`
	HomebrewTapAvailable   bool     `json:"homebrew_tap_available"`
	LocalSourceInstall     string   `json:"local_source_install"`
	ExternalGates          []string `json:"external_gates"`
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
	Scope     string `json:"scope,omitempty"`
}

// Manifest walks the root command and assembles the CLI manifest. It is pure
// and side-effect free; it never executes a command.
func Manifest() CLIManifest {
	m := CLIManifest{
		SchemaVersion:         1,
		RegistrySchemaVersion: registry.CurrentRegistrySchemaVersion,
		Toolchain: ToolchainInfo{
			GoReleaserVersion: "v2.17.0",
			HomebrewArtifact:  "cask",
		},
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
		RegistrySchema: RegistrySchemaInfo{
			Version: registry.CurrentRegistrySchemaVersion,
			ServiceTypes: []string{
				registry.TypeProxy,
				registry.TypeFile,
				registry.TypeTCP,
			},
			RequiredFields: []string{"schema_version", "services[].name", "services[].type"},
			UnavailableFeatures: []string{
				"custom-domain/ACME fields are reserved and rejected with feature_unavailable",
				"middleware schema is reserved and rejected with feature_unavailable",
			},
		},
		APIActions: apiActionNames(),
		CredentialSources: CredentialSources{
			Recommended: []CredentialSource{
				{ID: "interactive_login", Command: "tslink login", Boundary: "normal path; prompts avoid putting secrets in argv or shell history"},
			},
			Automation: []CredentialSource{
				{ID: "api_key_stdin", Command: "tslink login --api-key-stdin", Flags: []string{"--api-key-stdin"}, Boundary: "read API token from stdin; keep secret out of argv"},
				{ID: "client_secret_stdin", Command: "tslink login --client-secret-stdin", Flags: []string{"--client-secret-stdin"}, Boundary: "read OAuth client secret from stdin; keep secret out of argv"},
				{ID: "environment", Environment: []string{"TSLINK_API_KEY", "TSLINK_CLIENT_SECRET"}, Boundary: "acceptable for secret-manager injected automation; do not hard-code in shell history or source"},
			},
			Avoid: []CredentialSource{
				{ID: "argv_flags", Flags: []string{"--api-key", "--client-secret"}, Boundary: "accepted for compatibility, but secrets in argv can be exposed through shell history or local process inspection"},
				{ID: "manual_secret_file_write", Boundary: "direct echo/printf writes can leak through shell history and create files before permissions converge; prefer tslink login stdin paths"},
			},
			Storage: []CredentialSource{
				{ID: "system_keychain", Boundary: "preferred storage: macOS Keychain, Linux Secret Service, or Windows Credential Manager"},
				{ID: "restricted_file_fallback", Boundary: "headless fallback uses user-only restricted files when keychain storage is unavailable"},
			},
		},
		HighRiskOperations: []HighRiskOperation{
			{Command: "tslink login", Operation: "remote ACL tag-owner mutation", Default: "disabled", RequiredFlags: []string{"--manage-acl"}, Boundary: "default login does not rewrite shared tailnet ACL policy"},
			{Command: "tslink serve", Operation: "startup remote ACL tag ensure", Default: "disabled", RequiredFlags: []string{"--manage-acl"}, Boundary: "default serve does not rewrite shared tailnet ACL policy"},
			{Command: "tslink tags delete-remote", Operation: "remote ACL tag-owner deletion", Default: "disabled", RequiredFlags: []string{"--force", "--manage-acl"}, Boundary: "requires destructive confirmation and explicit remote ACL opt-in"},
		},
		Release: ReleaseInfo{
			PublicReleaseAvailable: false,
			PrebuiltAvailable:      false,
			HomebrewTapAvailable:   false,
			LocalSourceInstall:     "go install . from a checked-out source tree before the first public release",
			ExternalGates: []string{
				"hosted exact-SHA CI readback",
				"GitHub release environment reviewer/ruleset readback",
				"first tag/release artifact readback",
				"Homebrew tap readback",
			},
		},
	}
	if cm, err := security.LoadCapabilityManifest(); err == nil {
		m.Capabilities = cm
	}

	var walk func(c *cobra.Command, prefix string)
	walk = func(c *cobra.Command, prefix string) {
		path := strings.TrimSpace(prefix + " " + c.Name())
		info := CommandInfo{Path: path, Short: c.Short}
		info.Flags = commandFlags(c)
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

func commandFlags(c *cobra.Command) []FlagInfo {
	seen := map[string]struct{}{}
	var flags []FlagInfo
	add := func(f *pflag.Flag, scope string) {
		if _, ok := seen[f.Name]; ok {
			return
		}
		seen[f.Name] = struct{}{}
		flags = append(flags, FlagInfo{
			Name:      f.Name,
			Shorthand: f.Shorthand,
			Type:      f.Value.Type(),
			Default:   f.DefValue,
			Scope:     scope,
		})
	}
	c.LocalFlags().VisitAll(func(f *pflag.Flag) { add(f, "local") })
	c.InheritedFlags().VisitAll(func(f *pflag.Flag) { add(f, "inherited") })
	if c == c.Root() {
		c.PersistentFlags().VisitAll(func(f *pflag.Flag) { add(f, "persistent") })
	}
	return flags
}
