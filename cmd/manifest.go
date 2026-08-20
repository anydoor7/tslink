package cmd

import (
	"encoding/json"
	"fmt"
	"runtime"
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
	Platform              PlatformInfo                `json:"platform"`
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
	ErrorCodes            map[string]ErrorCodeInfo    `json:"error_codes"`
}

// PlatformInfo identifies the build target whose live Cobra tree was walked.
// Platform-specific command registrations make the manifest authoritative for
// its GOOS. GOARCH records validated generation provenance but does not identify
// authority within that GOOS because the command tree must be architecture-neutral.
type PlatformInfo struct {
	GOOS   string `json:"goos"`
	GOARCH string `json:"goarch"`
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

type ErrorCodeInfo struct {
	ExitCode    int    `json:"exit_code"`
	Description string `json:"description"`
}

// CommandInfo describes one command in the tree.
type CommandInfo struct {
	Path             string                         `json:"path"`
	Short            string                         `json:"short"`
	Flags            []FlagInfo                     `json:"flags,omitempty"`
	JSONResultFields map[string]JSONResultFieldInfo `json:"json_result_fields,omitempty"`
}

// JSONResultFieldInfo documents command-specific fields inside the shared
// --json result envelope's data object.
type JSONResultFieldInfo struct {
	Type        string   `json:"type"`
	Description string   `json:"description"`
	Values      []string `json:"values,omitempty"`
	Platforms   []string `json:"platforms,omitempty"`
}

// FlagInfo describes one command-local flag.
type FlagInfo struct {
	Name      string   `json:"name"`
	Shorthand string   `json:"shorthand,omitempty"`
	Type      string   `json:"type"`
	Default   string   `json:"default,omitempty"`
	Scope     string   `json:"scope,omitempty"`
	Usage     string   `json:"usage"`
	OneOf     []string `json:"one_of,omitempty"`
	Requires  []string `json:"requires,omitempty"`
	Conflicts []string `json:"conflicts,omitempty"`
	Platforms []string `json:"platforms,omitempty"`
}

const manifestPlatformsAnnotation = "tslink.io/manifest-platforms"

var supportedManifestPlatforms = map[string]struct{}{
	"darwin":  {},
	"linux":   {},
	"windows": {},
}

type CompactCLIManifest struct {
	SchemaVersion int                 `json:"schema_version"`
	Platform      PlatformInfo        `json:"platform"`
	Flags         []string            `json:"flags"`
	Commands      map[string][]string `json:"commands"`
	ErrorCodes    map[string]int      `json:"error_codes"`
}

// Manifest walks the root command and assembles the CLI manifest. It is pure
// and side-effect free; it never executes a command.
func Manifest() CLIManifest {
	m := CLIManifest{
		SchemaVersion:         2,
		Platform:              PlatformInfo{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH},
		RegistrySchemaVersion: registry.CurrentRegistrySchemaVersion,
		Toolchain: ToolchainInfo{
			MinimumGoVersion:  "1.26.3",
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
		ErrorCodes: errorCodeManifest(),
	}
	if cm, err := security.LoadCapabilityManifest(); err == nil {
		m.Capabilities = cm
	}

	var walk func(c *cobra.Command, prefix string)
	walk = func(c *cobra.Command, prefix string) {
		path := strings.TrimSpace(prefix + " " + c.Name())
		info := CommandInfo{
			Path:             path,
			Short:            platformNeutralCommandShort(path, c.Short),
			JSONResultFields: commandJSONResultFields(path),
		}
		info.Flags = commandFlags(c, path)
		sort.Slice(info.Flags, func(i, j int) bool { return info.Flags[i].Name < info.Flags[j].Name })
		m.Commands = append(m.Commands, info)
		children := c.Commands()
		sort.Slice(children, func(i, j int) bool { return children[i].Name() < children[j].Name() })
		for _, sub := range children {
			if (sub.Hidden && sub.Name() != "manifest") || sub.Name() == "help" || sub.Name() == "completion" {
				continue
			}
			walk(sub, path)
		}
	}
	walk(rootCmd, "")
	sort.Slice(m.Commands, func(i, j int) bool { return m.Commands[i].Path < m.Commands[j].Path })
	return m
}

func commandJSONResultFields(commandPath string) map[string]JSONResultFieldInfo {
	switch commandPath {
	case "tslink install":
		return markProseScopedJSONResultFields(map[string]JSONResultFieldInfo{
			"plist_path": {
				Type:        "string",
				Description: "macOS only. Path to the LaunchAgent plist written on success or preserved/restored on failure.",
			},
			"loaded": {
				Type:        "boolean",
				Description: "macOS only. Whether launchctl confirmed the LaunchAgent reached running state with a positive PID.",
			},
			"launchctl_target": {
				Type:        "string",
				Description: "macOS only. The launchd service target confirmed running on success or unavailable on a launchctl_domain_unavailable failure.",
			},
			"launchctl_output": {
				Type:        "string",
				Description: "macOS only. Trimmed launchctl output from bootstrap, fallback, or verification; omitted when launchctl emitted no text.",
			},
			"unavailable_domain": {
				Type:        "string",
				Description: "macOS failure only. The unavailable launchd domain, such as gui/501, when error.code is launchctl_domain_unavailable.",
			},
			"force_available": {
				Type:        "boolean",
				Description: "macOS failure only. True when an explicit --force recovery path exists for launchctl_domain_unavailable.",
			},
			"force_command": {
				Type:        "string",
				Description: "macOS failure only. Exact command that invokes the explicit --force recovery path.",
			},
			"force_risk": {
				Type:        "string",
				Description: "macOS failure only. Residual daemon risk accepted by running force_command.",
			},
			"path": {
				Type:        "string",
				Description: "Linux and Windows only. Path to the platform startup artifact written by this invocation.",
			},
			"installed": {
				Type:        "boolean",
				Description: "Linux and Windows only. Whether the platform startup artifact was installed.",
			},
			"started": {
				Type:        "boolean",
				Description: "Linux and Windows only. Whether the installed service was started by this invocation.",
			},
			"service_manager": {
				Type:        "string",
				Description: "Linux and Windows only. Platform startup mechanism responsible for the artifact.",
			},
			"warning": {
				Type:        "string",
				Description: "Actionable non-fatal warning on a successful install; omitted when no warning applies.",
			},
		})
	case "tslink uninstall":
		return uninstallJSONResultFields()
	default:
		return nil
	}
}

func uninstallJSONResultFields() map[string]JSONResultFieldInfo {
	return markProseScopedJSONResultFields(map[string]JSONResultFieldInfo{
		"plist_path": {
			Type:        "string",
			Description: "macOS only. Path to the LaunchAgent plist inspected or removed by this invocation.",
		},
		"path": {
			Type:        "string",
			Description: "Linux and Windows only. Path to the platform startup artifact inspected or removed by this invocation.",
		},
		"removed": {
			Type:        "boolean",
			Description: "Whether the platform startup artifact was removed by this invocation.",
		},
		"service_manager": {
			Type:        "string",
			Description: "Linux and Windows only. Platform startup mechanism responsible for the artifact.",
		},
		"warning": {
			Type:        "string",
			Description: "Actionable non-fatal warning on a successful forced removal; omitted on failures and when no warning applies.",
		},
		"launchctl_outcome": {
			Type:        "string",
			Description: "macOS only. Discriminates no installed plist, a confirmed bootout, confirmed absence from every domain, and an unconfirmed domain state. Unconfirmed may be returned with removed=true only after explicit --force.",
			Values:      []string{"not_installed", "unloaded", "already_absent", "unconfirmed"},
		},
		"launchctl_target": {
			Type:        "string",
			Description: "macOS only. The domain target that confirmed bootout or remained unavailable for the reported unconfirmed state; empty for not_installed and already_absent.",
		},
		"launchctl_output": {
			Type:        "string",
			Description: "macOS only. Verbatim trimmed launchctl output from the successful target for unloaded, or combined attempted output for already_absent and unconfirmed; omitted when launchctl emitted no text.",
		},
		"unavailable_domain": {
			Type:        "string",
			Description: "macOS failure only. The unavailable launchd domain, such as gui/501, when error.code is launchctl_domain_unavailable.",
		},
		"force_available": {
			Type:        "boolean",
			Description: "macOS failure only. True when an explicit --force recovery path exists for launchctl_domain_unavailable.",
		},
		"force_command": {
			Type:        "string",
			Description: "macOS failure only. Exact command that invokes the explicit --force recovery path.",
		},
		"force_risk": {
			Type:        "string",
			Description: "macOS failure only. Residual daemon risk accepted by running force_command.",
		},
		"detail": {
			Type:        "string",
			Description: "macOS only. TSLink-authored explanation kept separate from launchctl_output; present for already_absent and unconfirmed domain-state remedies.",
		},
	})
}

// markProseScopedJSONResultFields derives the structured qualifier from the
// same sentence that documents the field. The two representations therefore
// cannot drift without changing this one source value. Linux/Windows prose is
// intentionally outside the JSON result field mark set.
func markProseScopedJSONResultFields(fields map[string]JSONResultFieldInfo) map[string]JSONResultFieldInfo {
	for name, field := range fields {
		if strings.HasPrefix(field.Description, "macOS only.") || strings.HasPrefix(field.Description, "macOS failure only.") {
			field.Platforms = []string{"darwin"}
			fields[name] = field
		}
	}
	return fields
}

// mustMarkFlagPlatforms records platform scope on the concrete flag
// registration. An annotation belongs to that command's pflag.Flag object, so
// identically named flags on other commands remain independent. Persistent
// annotations are retained by Cobra when the flag is inherited, causing every
// emitted (command path, flag name) entry to carry the qualifier.
func mustMarkFlagPlatforms(command *cobra.Command, name string, platforms ...string) {
	normalized := normalizeManifestPlatforms(platforms)
	flagSet := command.PersistentFlags()
	if flagSet.Lookup(name) == nil {
		flagSet = command.Flags()
	}
	if err := flagSet.SetAnnotation(name, manifestPlatformsAnnotation, normalized); err != nil {
		panic(fmt.Sprintf("mark %s --%s platforms: %v", command.CommandPath(), name, err))
	}
}

func normalizeManifestPlatforms(platforms []string) []string {
	if len(platforms) == 0 {
		panic("manifest platform mark must contain at least one GOOS")
	}
	seen := make(map[string]struct{}, len(platforms))
	normalized := append([]string(nil), platforms...)
	for _, platform := range normalized {
		if _, ok := supportedManifestPlatforms[platform]; !ok {
			panic(fmt.Sprintf("manifest platform mark contains unsupported GOOS %q", platform))
		}
		if _, ok := seen[platform]; ok {
			panic(fmt.Sprintf("manifest platform mark repeats GOOS %q", platform))
		}
		seen[platform] = struct{}{}
	}
	if len(normalized) == len(supportedManifestPlatforms) {
		panic("manifest platform mark names every supported GOOS; omit the mark instead")
	}
	sort.Strings(normalized)
	return normalized
}

func flagManifestPlatforms(flag *pflag.Flag) []string {
	platforms := flag.Annotations[manifestPlatformsAnnotation]
	if len(platforms) == 0 {
		return nil
	}
	return normalizeManifestPlatforms(platforms)
}

func commandFlags(c *cobra.Command, commandPath string) []FlagInfo {
	seen := map[string]struct{}{}
	var flags []FlagInfo
	add := func(f *pflag.Flag, scope string) {
		if _, ok := seen[f.Name]; ok {
			return
		}
		seen[f.Name] = struct{}{}
		info := FlagInfo{
			Name:      f.Name,
			Shorthand: f.Shorthand,
			Type:      f.Value.Type(),
			Default:   f.DefValue,
			Scope:     scope,
			Usage:     f.Usage,
			Platforms: flagManifestPlatforms(f),
		}
		info.OneOf, info.Requires, info.Conflicts = flagRelationships(commandPath, f.Name)
		flags = append(flags, info)
	}
	c.LocalFlags().VisitAll(func(f *pflag.Flag) { add(f, "local") })
	c.InheritedFlags().VisitAll(func(f *pflag.Flag) { add(f, "inherited") })
	if c == c.Root() {
		c.PersistentFlags().VisitAll(func(f *pflag.Flag) { add(f, "persistent") })
	}
	return flags
}

func platformNeutralCommandShort(path, current string) string {
	switch path {
	case "tslink install":
		return "Install TSLink as the current platform's user startup service"
	case "tslink uninstall":
		return "Remove TSLink from the current platform's user startup service"
	default:
		return current
	}
}

func flagRelationships(commandPath, name string) (oneOf, requires, conflicts []string) {
	if commandPath == "tslink add" {
		switch name {
		case "proxy", "dir", "tcp":
			oneOf = []string{"--proxy", "--dir", "--tcp"}
		case "funnel":
			requires = []string{"--proxy", "--public"}
			conflicts = []string{"--allow", "--control-url"}
		case "public":
			requires = []string{"--funnel"}
		case "allow":
			conflicts = []string{"--tcp", "--funnel"}
		case "domain", "acme-email":
			conflicts = []string{"feature_unavailable"}
		}
	}
	if commandPath == "tslink url" && name == "raw" {
		conflicts = []string{"--json"}
	}
	if commandPath == "tslink list" {
		switch name {
		case "fields":
			conflicts = []string{"--verbose"}
		case "verbose":
			conflicts = []string{"--fields"}
		}
	}
	if commandPath == "tslink status" && name == "name" {
		requires = []string{"--urls"}
	}
	return oneOf, requires, conflicts
}

func errorCodeManifest() map[string]ErrorCodeInfo {
	return map[string]ErrorCodeInfo{
		"internal_error":                        {ExitCode: output.ExitError, Description: "unexpected internal failure"},
		"usage_error":                           {ExitCode: output.ExitUsage, Description: "invalid command syntax or value"},
		"auth_error":                            {ExitCode: output.ExitAuth, Description: "authentication required or rejected"},
		"conflict":                              {ExitCode: output.ExitConflict, Description: "requested state conflicts with existing state"},
		"not_found":                             {ExitCode: output.ExitNotFound, Description: "requested object was not found"},
		registry.CodeServiceTypeAmbiguous:       {ExitCode: output.ExitUsage, Description: "exactly one service type is required"},
		registry.CodeInvalidServiceName:         {ExitCode: output.ExitUsage, Description: "service name is not a valid DNS label"},
		registry.CodeInvalidTag:                 {ExitCode: output.ExitUsage, Description: "ACL tag is invalid"},
		registry.CodeAllowUnsupportedTCP:        {ExitCode: output.ExitUsage, Description: "HTTP allow lists do not apply to raw TCP"},
		registry.CodePathMustBeAbsolute:         {ExitCode: output.ExitUsage, Description: "file service path must be absolute"},
		registry.CodeUnknownConfigKey:           {ExitCode: output.ExitUsage, Description: "configuration key is not supported"},
		registry.CodeURLNotReady:                {ExitCode: output.ExitNotFound, Description: "runtime has not reported an exact tailnet hostname"},
		registry.CodeLaunchctlDomainUnavailable: {ExitCode: output.ExitError, Description: "a launchd domain could not be checked; failure data names the domain, explicit --force command, and residual risk"},
		registry.CodeFeatureUnavailable:         {ExitCode: output.ExitUsage, Description: "reserved feature is not available"},
		registry.CodeFunnelPublicAckRequired:    {ExitCode: output.ExitUsage, Description: "public Funnel acknowledgement is required"},
		registry.CodeFunnelAllowConflict:        {ExitCode: output.ExitConflict, Description: "Funnel conflicts with an allow list"},
		registry.CodeFunnelControlURLConflict:   {ExitCode: output.ExitConflict, Description: "Funnel conflicts with control_url"},
		registry.CodeFunnelTypeConflict:         {ExitCode: output.ExitConflict, Description: "Funnel requires a proxy service"},
	}
}

func CompactManifest() CompactCLIManifest {
	manifest := Manifest()
	compact := CompactCLIManifest{
		SchemaVersion: manifest.SchemaVersion,
		Platform:      manifest.Platform,
		Flags:         []string{"json"},
		Commands:      map[string][]string{},
		ErrorCodes:    map[string]int{},
	}
	for _, command := range manifest.Commands {
		if command.Path == "tslink" {
			continue
		}
		path := strings.TrimPrefix(command.Path, "tslink ")
		flags := make([]string, 0, len(command.Flags))
		for _, flag := range command.Flags {
			if flag.Scope != "inherited" && flag.Name != "json" {
				flags = append(flags, flag.Name)
			}
		}
		compact.Commands[path] = flags
	}
	for code, info := range manifest.ErrorCodes {
		compact.ErrorCodes[code] = info.ExitCode
	}
	return compact
}

func init() {
	manifestCmd := &cobra.Command{
		Use:    "manifest",
		Short:  "Print the installed binary's agent-readable CLI manifest",
		Args:   cobra.NoArgs,
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			compact, _ := cmd.Flags().GetBool("compact")
			var value any = Manifest()
			if compact {
				value = CompactManifest()
			}
			if jsonOutput(cmd) {
				output.Success("manifest", value)
				return nil
			}
			encoder := json.NewEncoder(cmd.OutOrStdout())
			if !compact {
				encoder.SetIndent("", "  ")
			}
			if err := encoder.Encode(value); err != nil {
				return fmt.Errorf("encode manifest: %w", err)
			}
			return nil
		},
	}
	manifestCmd.Flags().Bool("compact", false, "Print only commands, flags, and stable error codes")
	rootCmd.AddCommand(manifestCmd)
}
