package cmd

import (
	"encoding/json"
	"fmt"
	"runtime"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/monody0007/tslink/internal/credentials"
	"github.com/monody0007/tslink/internal/inspect"
	"github.com/monody0007/tslink/internal/output"
	"github.com/monody0007/tslink/internal/registry"
	tsruntime "github.com/monody0007/tslink/internal/runtime"
	"github.com/monody0007/tslink/internal/security"
	"github.com/monody0007/tslink/internal/tailapi"
)

// CLIManifest is the single machine-readable source of truth for the shipped
// command surface. It is generated from the live Cobra tree plus the output
// exit codes, registry schema version, and security capability manifest, so the
// documentation site can parity-check the exported fixture instead of
// hand-maintaining a second copy of these facts.
type CLIManifest struct {
	SchemaVersion         int                             `json:"schema_version"`
	Platform              PlatformInfo                    `json:"platform"`
	SupportedPlatforms    []string                        `json:"supported_platforms"`
	RegistrySchemaVersion int                             `json:"registry_schema_version"`
	Toolchain             ToolchainInfo                   `json:"toolchain"`
	ExitCodes             map[string]int                  `json:"exit_codes"`
	RegistrySchema        RegistrySchemaInfo              `json:"registry_schema"`
	CredentialSources     CredentialSources               `json:"credential_sources"`
	HighRiskOperations    []HighRiskOperation             `json:"high_risk_operations"`
	RemoteSideEffectPlans []security.RemoteSideEffectPlan `json:"remote_side_effect_plans"`
	Release               ReleaseInfo                     `json:"release"`
	Commands              []CommandInfo                   `json:"commands"`
	Capabilities          security.CapabilityManifest     `json:"capabilities"`
	ErrorCodes            map[string]ErrorCodeInfo        `json:"error_codes"`
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
	Version        int      `json:"version"`
	ServiceTypes   []string `json:"service_types"`
	RequiredFields []string `json:"required_fields"`
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

// SupportedManifestPlatforms returns a sorted copy of the GOOS set accepted by
// manifest platform qualifiers. The full manifest and in-repository validators
// derive from this authority so widening Product support cannot leave a silent
// stale platform list behind.
func SupportedManifestPlatforms() []string {
	platforms := make([]string, 0, len(supportedManifestPlatforms))
	for platform := range supportedManifestPlatforms {
		platforms = append(platforms, platform)
	}
	sort.Strings(platforms)
	return platforms
}

// CompactCLIManifest is a live, platform-specific command summary. It carries
// the full manifest's schema version for envelope evolution, but Commands holds
// flag names only and deliberately has no entry-qualifier shape. Because the
// running binary supplies Platform and only its live command tree, absence of a
// platforms qualifier in this compact form has no full-manifest semantics.
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
		SupportedPlatforms:    SupportedManifestPlatforms(),
		RegistrySchemaVersion: registry.CurrentRegistrySchemaVersion,
		Toolchain: ToolchainInfo{
			MinimumGoVersion:  "1.26.6",
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
			Version:        registry.CurrentRegistrySchemaVersion,
			ServiceTypes:   serviceTypeValues(),
			RequiredFields: []string{"schema_version", "services[].name", "services[].type"},
		},
		CredentialSources: CredentialSources{
			Recommended: []CredentialSource{
				{ID: "interactive_login", Command: "tslink login", Boundary: "normal path; prompts avoid putting secrets in argv or shell history"},
				{ID: "keys_page_bootstrap", Command: "tslink login --open-keys-page", Flags: []string{"--open-keys-page", "--open-oauth-page"}, Boundary: "standalone helper that opens " + credentials.KeysPageURL + " (or the OAuth page) only for an interactive terminal outside CI; otherwise prints the URL and the three bootstrap steps and exits 0 without storing anything"},
			},
			Automation: []CredentialSource{
				{ID: "api_key_stdin", Command: "tslink login --api-key-stdin", Flags: []string{"--api-key-stdin", "--expires-in", "--expires-at"}, Boundary: "read API token from stdin; keep secret out of argv. The api-key slot expires within 90 days: pass --expires-in or --expires-at to record the real expiry, otherwise expires_at_source=assumed_max; status --json reports credential_expiry_state and renewing is another login to the same slot"},
				{ID: "client_secret_stdin", Command: "tslink login --client-secret-stdin", Flags: []string{"--client-secret-stdin"}, Boundary: "read OAuth client secret from stdin; keep secret out of argv; the client-secret slot does not expire"},
				{ID: "environment", Environment: []string{"TSLINK_API_KEY", "TSLINK_CLIENT_SECRET"}, Boundary: "acceptable for secret-manager injected automation; do not hard-code in shell history or source"},
			},
			Avoid: []CredentialSource{
				{ID: "argv_flags", Flags: []string{"--api-key", "--client-secret"}, Boundary: "accepted for compatibility, but secrets in argv can be exposed through shell history or local process inspection"},
				{ID: "manual_secret_file_write", Boundary: "direct echo/printf writes can leak through shell history and create files before permissions converge; prefer tslink login stdin paths"},
			},
			Storage: []CredentialSource{
				{ID: "system_keychain", Boundary: "preferred storage: macOS Keychain, Linux Secret Service, or Windows Credential Manager"},
				{ID: "restricted_file_fallback", Boundary: "headless fallback uses user-only restricted files when keychain storage is unavailable"},
				{ID: "dual_slot_coexistence", Command: "tslink login --retire-other", Flags: []string{"--retire-other"}, Boundary: "the api-key (invites, expires) and client-secret (durable daemon auth) slots coexist; login fills one slot and keeps the other unless --retire-other is passed; logout --kind removes one slot"},
				{ID: "value_free_metadata", Boundary: "credential-meta.json (0600) stores only sha256 fingerprints, stored_at, expires_at, expires_at_source, and last verification per slot; never the credential value"},
			},
		},
		HighRiskOperations: []HighRiskOperation{
			{Command: "tslink login", Operation: "remote ACL tag-owner mutation", Default: "disabled", RequiredFlags: []string{"--manage-acl"}, Boundary: "default login does not rewrite shared tailnet ACL policy"},
			{Command: "tslink serve", Operation: "startup ordinary remote ACL tag ensure", Default: "disabled", RequiredFlags: []string{"--manage-acl"}, Boundary: "ordinary tagOwners creation remains disabled without --manage-acl; acknowledged Funnel services use the separately audited plan"},
			{Command: "tslink serve", Operation: "Funnel shared tag owner and exact nodeAttrs auto-provisioning", Default: "enabled", RequiredFlags: []string{}, Boundary: "default-on only for acknowledged Funnel services; disable process-wide with --no-auto-provision or per service with no_auto_provision"},
			{Command: "tslink serve", Operation: "remote MCP control plane on a dedicated tailnet-only node", Default: "disabled", RequiredFlags: []string{"--mcp"}, Boundary: "off unless --mcp or config.json mcp.enabled; every authorized tailnet peer reaching https://<node>.<tailnet>.ts.net/mcp can register and remove services, publish Funnel and send or revoke invitations; authorization against config.json mcp.allow is mandatory and an empty list refuses startup; never published through Funnel and never bound to a host interface; node lifetime depends on the login path: with a stored credential the derived auth key carries the ephemeral capability and tsnet logs in ephemeral, so a clean daemon stop removes the node from the tailnet and no cleanup ledger entry is needed, while after a crash the node waits for Tailscale's ephemeral garbage collection; with zero credentials the node is persistent and user-owned, one browser authorization survives restarts, and disabling --mcp leaves a device to delete by hand in the Tailscale admin console"},
			{Command: "tslink cleanup", Operation: "owned device deletion", Default: "dry-run", RequiredFlags: []string{"--dry-run=false"}, Boundary: "device deletion requires durable exact NodeID proof; hostname is discovery-only"},
			{Command: "tslink cleanup", Operation: "legacy device ownership adoption", Default: "disabled", RequiredFlags: []string{"--adopt", "--force", "--dry-run=false"}, Boundary: "preview is read-only and simulates reconciliation; writing requires one literal hostname with exactly one TSLink-tagged remote match; a service absent from registry.json records reviewed retired_at provenance for its exact NodeID proof, while a currently registered service records active proof without retired_at"},
			{Command: "tslink cleanup", Operation: "shared Funnel ACL status", Default: "disabled", RequiredFlags: []string{"--manage-acl"}, Boundary: "reports local use or skips; local absence never authorizes tailnet-wide grant deletion"},
			{Command: "tslink tags delete-remote", Operation: "remote ACL tag-owner deletion", Default: "disabled", RequiredFlags: []string{"--force", "--manage-acl"}, Boundary: "requires destructive confirmation and explicit remote ACL opt-in"},
			{Command: "tslink invite user", Operation: "tailnet user invitation", Default: "explicit named recipient", Boundary: "requires a user-owned tskey-api- token; --print-link selects self-delivery"},
			{Command: "tslink invite device", Operation: "external device sharing", Default: "explicit named recipient and service", Boundary: "requires a user-owned tskey-api- token and exact TSLink nodeId ownership proof"},
			{Command: "tslink invite revoke", Operation: "invite revocation", Default: "explicit invite kind and numeric ID", RequiredFlags: []string{"--kind"}, Boundary: "explicit user/device namespace selection; device invite revocation also requires exact TSLink nodeId ownership proof"},
			{Command: "tslink invite resend", Operation: "invite email resend", Default: "explicit invite kind and numeric ID", RequiredFlags: []string{"--kind"}, Boundary: "explicit user/device namespace selection; requires a user-owned tskey-api- token and an invite originally created with email"},
		},
		RemoteSideEffectPlans: []security.RemoteSideEffectPlan{
			security.ACLMutationPlan("ensure_tags", []string{tailapi.DefaultTag}, false),
			security.FunnelAutoProvisionPlan(registry.FunnelTag, nil, true),
			security.InviteMutationPlan(tailapi.InviteKindUser, "create", "<invite-id>", "", 0),
			security.InviteMutationPlan(tailapi.InviteKindDevice, "create", "<invite-id>", "<service>", 0),
			security.InviteMutationPlan(tailapi.InviteKindUser, "revoke", "<invite-id>", "", 0),
			security.InviteMutationPlan(tailapi.InviteKindDevice, "revoke", "<invite-id>", "<service>", 0),
			security.InviteMutationPlan(tailapi.InviteKindUser, "resend", "<invite-id>", "", 0),
			security.InviteMutationPlan(tailapi.InviteKindDevice, "resend", "<invite-id>", "<service>", 0),
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
		resultFields := commandJSONResultFields(path)
		mustValidateJSONResultFieldPlatformMarks(path, resultFields)
		info := CommandInfo{
			Path:             path,
			Short:            platformNeutralCommandShort(path, c.Short),
			JSONResultFields: resultFields,
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
	case "tslink serve":
		return map[string]JSONResultFieldInfo{
			"credential_migrated": {
				Type:        "boolean",
				Description: "True when this invocation migrated a legacy API credential to the system keychain; omitted otherwise.",
			},
		}
	case "tslink add":
		return map[string]JSONResultFieldInfo{
			"funnel_expires_at": {Type: "string", Description: "Persisted public Funnel deadline; omitted for tailnet-only services and explicit never."},
			"funnel_rearmed":    {Type: "boolean", Description: "True when an expired preserved Funnel deadline was re-armed with the default 24h TTL."},
			"replaced_fields":   {Type: "array", Description: "registry.json fields a replacing add changed or dropped, sorted; empty when the add created the service. Warnings access_changed_on_replace and identity_reset_on_replace flag access and node-identity changes."},
		}
	case "tslink list":
		fields := agentServiceRuntimeJSONResultFields()
		fields["services[].state"] = JSONResultFieldInfo{
			Type:        "string",
			Description: "Slim list runtime state: exact, pending, or failed.",
			Values:      listStateValues(),
		}
		fields["devices"] = JSONResultFieldInfo{
			Type:        "array",
			Description: "--tailnet only: every TSLink-tagged device in the shared tailnet, sorted by hostname. Read-only; node identity values are never emitted.",
		}
		fields["devices[].locally_registered"] = JSONResultFieldInfo{
			Type:        "boolean",
			Description: "--tailnet only: true when this machine's registry.json holds a service whose name equals the device hostname exactly.",
		}
		fields["devices[].origin"] = JSONResultFieldInfo{
			Type:        "string",
			Description: "--tailnet only: which side of the machine boundary the hostname came from.",
			Values:      listTailnetOriginValues(),
		}
		fields["devices[].local_service"] = JSONResultFieldInfo{
			Type:        "string",
			Description: "--tailnet only: the locally registered service this hostname belongs to, for both an exact match and a <service>-N tsnet collision variant; omitted when this machine's registry knows nothing about it.",
		}
		fields["devices[].tags"] = JSONResultFieldInfo{
			Type:        "array",
			Description: "--tailnet only: the device's tailnet ACL tags, at least one of which matched the TSLink tag filter.",
		}
		fields["devices[].last_seen_at"] = JSONResultFieldInfo{
			Type:        "string",
			Description: "--tailnet only: RFC3339 last-seen time reported by the Tailscale API; omitted while connected_to_control is true, because the API then reports no last-seen value.",
		}
		fields["devices[].created_at"] = JSONResultFieldInfo{
			Type:        "string",
			Description: "--tailnet only: RFC3339 node creation time reported by the Tailscale API; omitted when the API reports none.",
		}
		fields["registered_count"] = JSONResultFieldInfo{
			Type:        "integer",
			Description: "--tailnet only: devices whose hostname exactly matches a service in this machine's registry.",
		}
		fields["unregistered_count"] = JSONResultFieldInfo{
			Type:        "integer",
			Description: "--tailnet only: devices this machine's registry does not name exactly; they are another machine's services or orphans.",
		}
		fields["cleanup_authority"] = JSONResultFieldInfo{
			Type:        "string",
			Description: "--tailnet only: constant statement that device deletion is authorized solely by an exact NodeID recorded in this machine's local node-ownership.json, so unregistered rows cannot be cleaned up from here.",
		}
		return fields
	case "tslink login":
		return map[string]JSONResultFieldInfo{
			"credential_kind":      {Type: "string", Description: "Kind of the committed credential slot.", Values: []string{credentials.KindAPIAccessToken, credentials.KindOAuthClientSecret}},
			"fingerprint":          {Type: "string", Description: "First eight hex characters of sha256(credential); identifies the credential without revealing it."},
			"stored_at":            {Type: "string", Description: "RFC3339 time the credential was committed."},
			"expires_at":           {Type: "string", Description: "RFC3339 expiry recorded for the api-key slot; omitted for OAuth client secrets, which do not expire."},
			"expires_at_source":    {Type: "string", Description: "Provenance of expires_at: user when given with --expires-in/--expires-at, assumed_max when TSLink assumed the 90-day maximum.", Values: []string{credentials.ExpirySourceUser, credentials.ExpirySourceAssumedMax}},
			"rotated":              {Type: "boolean", Description: "True when the same slot previously held a different credential."},
			"previous_fingerprint": {Type: "string", Description: "Fingerprint of the replaced credential; present only when rotated."},
			"retired_credential":   {Type: "string", Description: "Other slot deleted as part of this commit; present only with --retire-other and a populated other slot.", Values: []string{credentials.SlotAPIKey, credentials.SlotClientSecret}},
			"page":                 {Type: "string", Description: "Bootstrap helper only (--open-keys-page/--open-oauth-page): which admin page was presented.", Values: []string{loginBootstrapPageKeys, loginBootstrapPageOAuth}},
			"url":                  {Type: "string", Description: "Bootstrap helper only: the admin page URL to open."},
			"opened":               {Type: "boolean", Description: "Bootstrap helper only: true when a browser was opened; always false under --json."},
			"next":                 {Type: "array", Description: "Bootstrap helper only: the three-step credential bootstrap sequence."},
		}
	case "tslink logout":
		return map[string]JSONResultFieldInfo{
			"kind":                {Type: "string", Description: "Selected credential slot for a selective logout; omitted for the full logout.", Values: []string{credentials.SlotAPIKey, credentials.SlotClientSecret}},
			"deleted_credentials": {Type: "array", Description: "Credential slots removed by this invocation; empty when nothing was stored."},
		}
	case "tslink doctor":
		return map[string]JSONResultFieldInfo{
			"tailscale_ssh.state": {
				Type:        "string",
				Description: "Tailscale SSH enablement for this node, read from the local Tailscale client. Informational: it never changes status, health_status, or health_exit_code.",
				Values:      doctorTailscaleSSHStateValues(),
			},
			"tailscale_ssh.acl_rule_required": {
				Type:        "boolean",
				Description: "Constant true: enabling Tailscale SSH on the node is not sufficient by itself; the tailnet ACL also needs an ssh rule admitting the caller, which no local check can observe.",
			},
			"daemon.build_version": {
				Type:        "string",
				Description: "The running daemon's own reported build identity (release version, or module version plus VCS revision for a source build). Empty when the daemon predates build-identity reporting or could not determine its own build when it started; reporting-only, it is never used to decide daemon.running.",
			},
			"daemon.executable": {
				Type:        "string",
				Description: "The running daemon's own executable path at the moment it wrote its PID/identity sidecar. Informational only; never used to verify process identity, unlike the Go-module comparison identity verification relies on.",
			},
			"daemon.build_skew": {
				Type:        "boolean",
				Description: "True when the running daemon's reported build differs from this CLI invocation's own build, or one side could determine its build and the other could not. False, including when neither side could determine its own build. Never changes daemon.running, health_status, or health_exit_code beyond the daemon_build_skew warning itself.",
			},
		}
	case "tslink status":
		fields := agentServiceRuntimeJSONResultFields()
		fields["credential_expiry_state"] = JSONResultFieldInfo{
			Type:        "string",
			Description: "Worst per-slot credential expiry state; expiring means the api-key slot has 14 days or fewer left, and next then carries the key-bootstrap steps.",
			Values:      credentialExpiryStateValues(),
		}
		fields["credentials.api_key"] = JSONResultFieldInfo{
			Type:        "object",
			Description: "Value-free api-key slot report: present, fingerprint, stored_at, expires_at, expires_at_source, days_left, expiry_state, last_verified_at, last_verified_result.",
		}
		fields["credentials.client_secret"] = JSONResultFieldInfo{
			Type:        "object",
			Description: "Value-free client-secret slot report with the same fields as credentials.api_key; expires_at is omitted because OAuth client secrets do not expire.",
		}
		fields["credentials.metadata_error"] = JSONResultFieldInfo{
			Type:        "string",
			Description: "Sanitized reason credential-meta.json could not be trusted; present slots then report expiry_state unknown.",
		}
		fields["global_error"] = JSONResultFieldInfo{
			Type:        "object",
			Description: "Stable daemon-wide runtime failure showing that the current registry control plane is non-authoritative; omitted when reconciliation is healthy.",
		}
		fields["next"] = JSONResultFieldInfo{
			Type:        "array",
			Description: "Machine continuation commands when the successful status still requires enrollment or polling.",
		}
		fields["services[].status"] = JSONResultFieldInfo{
			Type:        "string",
			Description: "Pollable service status in the default status result, including failed for a recorded bounded startup failure.",
		}
		fields["services[].runtime_state"] = JSONResultFieldInfo{
			Type:        "string",
			Description: "Detailed runtime state in status --urls output.",
			Values:      statusRuntimeStateValues(),
		}
		fields["services[].ownership_proof"] = JSONResultFieldInfo{
			Type:        "boolean",
			Description: "True only when the durable ownership ledger contains exact StableNodeID cleanup proof for the service; NodeIDs are never emitted.",
		}
		fields["daemon_state"] = JSONResultFieldInfo{
			Type:        "string",
			Description: "Additive daemon liveness state: running, absent, or unknown; daemon_running retains its existing boolean semantics.",
			Values:      []string{daemonStateRunning, daemonStateAbsent, daemonStateUnknown},
		}
		fields["ownership_proof_available"] = JSONResultFieldInfo{
			Type:        "boolean",
			Description: "True when the durable ownership ledger was read successfully, allowing false per-service ownership_proof values to mean proof is genuinely absent.",
		}
		return fields
	case "tslink cleanup":
		return map[string]JSONResultFieldInfo{
			"dry_run":                        {Type: "boolean", Description: "True when no registry or device change was applied."},
			"registry_changed":               {Type: "boolean", Description: "True when expired Funnel services were persisted as tailnet-only."},
			"expired_funnels":                {Type: "array", Description: "Service names whose public Funnel deadline has elapsed."},
			"devices_matched":                {Type: "array", Description: "Hostnames discovered by exact NodeID or protected hostname matching; NodeIDs are never emitted."},
			"devices_would_delete":           {Type: "array", Description: "Hostnames selected by durable exact NodeID ownership proof during dry-run; NodeIDs are never emitted."},
			"devices_deleted":                {Type: "array", Description: "Hostnames deleted by exact NodeID during apply; NodeIDs are never emitted."},
			"devices_protected":              {Type: "array", Description: "Hostname matches lacking exact NodeID ownership proof; never deleted."},
			"devices_adopted":                {Type: "array", Description: "Explicit literal hostnames whose single remote match was recorded as exact ownership proof; NodeIDs are never emitted."},
			"adoption.service_name":          {Type: "string", Description: "Literal TSLink-tagged hostname selected for adoption preview or apply; omitted when --adopt is absent."},
			"adoption.matches":               {Type: "integer", Description: "Full-list count of exact-hostname devices remaining after the TSLink tag filter."},
			"adoption.written":               {Type: "boolean", Description: "True only when exact ownership proof was persisted; false for the default dry-run preview."},
			"error.data.matches":             {Type: "integer", Description: "Structured filtered exact-hostname match count on an --adopt cardinality conflict."},
			"device_cleanup_skipped":         {Type: "boolean", Description: "True when ownership proof was unavailable, registry.json was structurally untrusted, an orphan lacked retired_at provenance after possible registry rollback, no API client was available, hostname-only matches were protected, or remote cleanup failed."},
			"device_skip_reason":             {Type: "string", Description: "Human-readable non-secret reason for skipped device cleanup; structural registry distrust takes priority over ownership-ledger unavailability when both apply; detail may vary with the sorted orphan set; omitted otherwise."},
			"device_skip_unknown_provenance": {Type: "array", Description: "Sorted service names whose orphan ownership rows lack retired_at provenance; omitted for other skip classes."},
			"acl_action":                     {Type: "string", Description: "Shared Funnel ACL status; local cleanup never deletes the global grant."},
			"warnings":                       {Type: "array", Description: "Non-fatal remote cleanup failures without secret or NodeID values."},
		}
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
	case "tslink invite user", "tslink invite device":
		return map[string]JSONResultFieldInfo{
			"invite_url": {
				Type:        "string",
				Description: "Invite acceptance URL returned verbatim by the Tailscale API; TSLink never constructs it.",
			},
			"emailed": {
				Type:        "boolean",
				Description: "Explicit delivery fact: true when Tailscale sent or resent email, false for --print-link self-delivery.",
			},
			"remote_side_effect_plan": {
				Type:        "object",
				Description: "Auditable plan for the outward-facing invite mutation.",
			},
		}
	case "tslink invite resend":
		return map[string]JSONResultFieldInfo{
			"emailed": {
				Type:        "boolean",
				Description: "True after Tailscale accepted the email resend; resend output never repeats the bearer invite URL.",
			},
			"remote_side_effect_plan": {
				Type:        "object",
				Description: "Auditable plan for the outward-facing invite resend.",
			},
		}
	case "tslink invite list":
		return map[string]JSONResultFieldInfo{
			"complete":       {Type: "boolean", Description: "True only when every requested device target was checked without error; false means device results are partial."},
			"user_invites":   {Type: "array", Description: "Open tailnet user invites."},
			"device_invites": {Type: "array", Description: "Invites for devices with exact TSLink nodeId ownership proof."},
			"device_targets": {Type: "array", Description: "Per-service device-invite check result; checked with invite_count distinguishes an empty result from a structured error."},
			"count":          {Type: "integer", Description: "Total number of returned user and device invites."},
		}
	case "tslink invite revoke":
		return map[string]JSONResultFieldInfo{
			"revoked":                 {Type: "boolean", Description: "True only after the Tailscale API accepted the revocation."},
			"remote_side_effect_plan": {Type: "object", Description: "Auditable plan for the outward-facing invite revocation."},
		}
	default:
		return nil
	}
}

func agentServiceRuntimeJSONResultFields() map[string]JSONResultFieldInfo {
	return map[string]JSONResultFieldInfo{
		"services[].funnel_requested": {
			Type:        "boolean",
			Description: "Configuration intent: whether this service requests public Tailscale Funnel exposure.",
		},
		"services[].funnel_active": {
			Type:        "boolean",
			Description: "Runtime fact: true only after the service node passes Funnel capability checks and ListenFunnel succeeds.",
		},
		"services[].funnel_state": {
			Type:        "string",
			Description: "Stable reason separating Funnel intent from runtime activation.",
			Values:      funnelStateValues(),
		},
		"services[].funnel_expires_at": {
			Type:        "string",
			Description: "RFC3339 public Funnel deadline; omitted for a Funnel stored as never and for one whose lifetime was never decided (funnel_expiry_required).",
		},
		"services[].funnel_remaining": {
			Type:        "string",
			Description: "Wall-clock duration remaining, never, or 0s after expiration.",
		},
		"services[].error": {
			Type:        "object",
			Description: "Stable per-service runtime failure with code, message, and actionable next steps; omitted without a recorded failure.",
		},
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
			Values:      uninstallLaunchctlOutcomeValues(),
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

func serviceTypeValues() []string {
	return []string{registry.TypeProxy, registry.TypeFile, registry.TypeTCP}
}

func listStateValues() []string {
	return []string{inspect.EndpointStateExact, listStatePending, tsruntime.ServiceRuntimeFailed}
}

func listTailnetOriginValues() []string {
	return []string{listOriginLocalRegistry, listOriginLocalNameVariant, listOriginUnregistered}
}

func doctorTailscaleSSHStateValues() []string {
	return []string{doctorTailscaleSSHEnabled, doctorTailscaleSSHDisabled, doctorTailscaleSSHUnknown}
}

func statusRuntimeStateValues() []string {
	return []string{statusEndpointStateUnknown, tsruntime.ServiceRuntimeRunning, tsruntime.ServiceRuntimeFailed}
}

func credentialExpiryStateValues() []string {
	return []string{
		credentials.ExpiryStateNone,
		credentials.ExpiryStateOK,
		credentials.ExpiryStateExpiring,
		credentials.ExpiryStateExpired,
		credentials.ExpiryStateUnknown,
	}
}

func funnelStateValues() []string {
	return []string{
		tsruntime.FunnelStateNotRequested,
		tsruntime.FunnelStateRequestedUnknown,
		tsruntime.FunnelStateActive,
		tsruntime.FunnelStateCapabilityMissing,
		tsruntime.FunnelStateListenFailed,
		tsruntime.FunnelStateStartTimeout,
	}
}

const (
	launchctlOutcomeNotInstalled  = "not_installed"
	launchctlOutcomeUnloaded      = "unloaded"
	launchctlOutcomeAlreadyAbsent = "already_absent"
	launchctlOutcomeUnconfirmed   = "unconfirmed"
)

func uninstallLaunchctlOutcomeValues() []string {
	return []string{
		launchctlOutcomeNotInstalled,
		launchctlOutcomeUnloaded,
		launchctlOutcomeAlreadyAbsent,
		launchctlOutcomeUnconfirmed,
	}
}

// resultFieldPlatformTripwires are matched case-insensitively against the
// description of any field that carries no platform qualifier. "darwin" is in
// the list because that is the token this codebase uses everywhere the mark
// itself appears -- in Platforms, in supportedManifestPlatforms, in
// check-manifest-platforms' flags -- so a contributor who reads the mark on the
// line above and writes "Only populated on darwin." is doing the natural thing.
//
// This list is a stopgap. The ground truth for which platform a result field
// exists on is the per-GOOS struct (InstallResult in install_darwin.go versus
// install_linux.go / install_windows.go), not the sentence documenting it;
// deriving the marks from those under three go/build contexts would stop prose
// from being load-bearing at all. Until then, prose that names no listed word
// still fails open.
var resultFieldPlatformTripwires = []string{
	"macOS", "darwin", "Windows", "Linux", "launchd", "launchctl", "systemd", "plist", "LaunchAgent",
}

// markProseScopedJSONResultFields derives the structured qualifier from the
// leading sentence that documents the field. Any other platform-bearing prose
// fails closed: an unmarked field would otherwise over-claim availability on
// every supported platform.
func markProseScopedJSONResultFields(fields map[string]JSONResultFieldInfo) map[string]JSONResultFieldInfo {
	for name, field := range fields {
		switch {
		case strings.HasPrefix(field.Description, "macOS only."), strings.HasPrefix(field.Description, "macOS failure only."):
			field.Platforms = normalizeManifestPlatforms([]string{"darwin"})
		case strings.HasPrefix(field.Description, "Linux and Windows only."):
			field.Platforms = normalizeManifestPlatforms([]string{"linux", "windows"})
		}
		fields[name] = field
	}
	return fields
}

func mustValidateJSONResultFieldPlatformMarks(commandPath string, fields map[string]JSONResultFieldInfo) {
	for name, field := range fields {
		if len(field.Platforms) > 0 {
			continue
		}
		lowered := strings.ToLower(field.Description)
		for _, word := range resultFieldPlatformTripwires {
			if strings.Contains(lowered, strings.ToLower(word)) {
				panic(fmt.Sprintf("mark %s JSON result field %s: description contains platform word %q without a recognized scope clause", commandPath, name, word))
			}
		}
	}
}

// mustMarkFlagPlatforms records platform scope on the concrete flag
// registration and rejects inherited parent flags that the named command does
// not own. Independently registered same-name flags remain isolated. pflag
// intentionally shares *Flag pointers when callers share a FlagSet, so that
// pattern also shares annotations; check-manifest-platforms is the backstop for
// a resulting stale mark. Persistent annotations are retained by Cobra when a
// locally owned flag is inherited by descendants.
func mustMarkFlagPlatforms(command *cobra.Command, name string, platforms ...string) {
	normalized := normalizeManifestPlatforms(platforms)
	flagSet := command.PersistentFlags()
	if flagSet.Lookup(name) == nil {
		if command.LocalFlags().Lookup(name) == nil {
			panic(fmt.Sprintf("mark %s --%s: not registered on this command", command.CommandPath(), name))
		}
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
		if commandPath == "tslink mcp" && f.Name == "json" {
			return
		}
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
		case "no-auto-provision":
			requires = []string{"--funnel"}
		case "allow":
			conflicts = []string{"--tcp", "--funnel"}
		}
	}
	if commandPath == "tslink url" && name == "raw" {
		conflicts = []string{"--json"}
	}
	if commandPath == "tslink list" {
		switch name {
		case "fields":
			conflicts = []string{"--verbose", "--tailnet"}
		case "verbose":
			conflicts = []string{"--fields", "--tailnet"}
		case "name", "type":
			conflicts = []string{"--tailnet"}
		case "tailnet":
			conflicts = []string{"--name", "--type", "--fields", "--verbose"}
		}
	}
	if commandPath == "tslink login" {
		switch name {
		case "expires-in":
			conflicts = []string{"--expires-at", "--client-secret", "--client-secret-stdin"}
		case "expires-at":
			conflicts = []string{"--expires-in", "--client-secret", "--client-secret-stdin"}
		case "open-keys-page":
			conflicts = []string{"--open-oauth-page", "--api-key", "--api-key-stdin", "--client-secret", "--client-secret-stdin"}
		case "open-oauth-page":
			conflicts = []string{"--open-keys-page", "--api-key", "--api-key-stdin", "--client-secret", "--client-secret-stdin"}
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
		registry.CodePathNotFound:               {ExitCode: output.ExitUsage, Description: "file service directory does not exist"},
		registry.CodePathNotDirectory:           {ExitCode: output.ExitUsage, Description: "file service path is not a directory"},
		registry.CodePathNotAccessible:          {ExitCode: output.ExitUsage, Description: "file service path is not accessible to the current user"},
		registry.CodePathExposesConfigDir:       {ExitCode: output.ExitUsage, Description: "file service path is, contains, or lies inside TSLink's config directory, which holds node keys and credentials"},
		registry.CodeUnknownConfigKey:           {ExitCode: output.ExitUsage, Description: "configuration key is not supported"},
		registry.CodeConfigLoadFailed:           {ExitCode: output.ExitUsage, Description: "config.json exists but cannot be read strictly (malformed JSON or an unknown key); commands that would persist its settings refuse to guess"},
		registry.CodeURLNotReady:                {ExitCode: output.ExitNotFound, Description: "runtime has not reported an exact tailnet hostname"},
		registry.CodeLaunchctlDomainUnavailable: {ExitCode: output.ExitError, Description: "a launchd domain could not be checked; failure data names the domain, explicit --force command, and residual risk"},
		registry.CodeFunnelPublicAckRequired:    {ExitCode: output.ExitUsage, Description: "public Funnel acknowledgement is required"},
		registry.CodeFunnelExpiryRequired:       {ExitCode: output.ExitUsage, Description: "a Funnel entry records neither a funnel_expires_at deadline nor \"never\"; it is not made public until one is set"},
		registry.CodeFunnelAllowConflict:        {ExitCode: output.ExitConflict, Description: "Funnel conflicts with an allow list"},
		registry.CodeFunnelControlURLConflict:   {ExitCode: output.ExitConflict, Description: "Funnel conflicts with control_url"},
		registry.CodeFunnelTypeConflict:         {ExitCode: output.ExitConflict, Description: "Funnel requires a proxy service"},
		registry.CodeCredentialURLMismatch:      {ExitCode: output.ExitConflict, Description: "a credentialed daemon refuses to send an auth key minted through the Tailscale API to a control server that is not Tailscale's; the service is not started"},
		registry.CodeFunnelCapabilityMissing:    {ExitCode: output.ExitError, Description: "service tsnet node lacks Funnel capability, HTTPS, or allowed port"},
		registry.CodeFunnelListenFailed:         {ExitCode: output.ExitError, Description: "Funnel capability preflight passed but listener activation failed"},
		registry.CodeServiceStartTimeout:        {ExitCode: output.ExitError, Description: "service node did not reach running state before its startup deadline"},
		registry.CodeInviteAPIKeyRequired:       {ExitCode: output.ExitAuth, Description: "a user-owned tskey-api- token is required and OAuth is not eligible"},
		registry.CodeInviteRoleInvalid:          {ExitCode: output.ExitUsage, Description: "invite role is outside the first-party enum"},
		registry.CodeInviteRecipientInvalid:     {ExitCode: output.ExitUsage, Description: "invite recipient is missing"},
		registry.CodeInviteNotFound:             {ExitCode: output.ExitNotFound, Description: "invite or matching service device was not found"},
		registry.CodeInviteDeviceAmbiguous:      {ExitCode: output.ExitConflict, Description: "multiple hostname candidates remain after ownership resolution"},
		registry.CodeInviteOwnershipUnproven:    {ExitCode: output.ExitConflict, Description: "TSLink lacks exact stable nodeId proof for the device"},
		registry.CodeInviteAPIForbidden:         {ExitCode: output.ExitAuth, Description: "Tailscale rejected the user-owned token's user permissions with HTTP 403; HTTP 401 (expired, revoked, or invalid token) is reported as invite_api_unauthorized"},
		registry.CodeInviteAPIUnauthorized:      {ExitCode: output.ExitAuth, Description: "Tailscale rejected the user-owned tskey-api- token as unauthenticated (HTTP 401): expired, revoked, or invalid; next carries the key-bootstrap steps"},
		registry.CodeAPITokenUnauthorized:       {ExitCode: output.ExitAuth, Description: "Tailscale rejected the stored API credential as unauthenticated (HTTP 401) during ACL, device, auth-key derivation, or login verification; next carries the key-bootstrap steps"},
		registry.CodeAPIForbidden:               {ExitCode: output.ExitAuth, Description: "Tailscale refused the API credential (HTTP 403): its user role or OAuth scopes do not permit the operation"},
		registry.CodeLoginVerifyFailed:          {ExitCode: output.ExitError, Description: "login could not verify the candidate credential against Tailscale for a reason other than HTTP 401/403; the previous credential was kept"},
		registry.CodeInviteResendEmailMissing:   {ExitCode: output.ExitConflict, Description: "an invite created without email cannot be resent"},
		registry.CodeInviteIDInvalid:            {ExitCode: output.ExitUsage, Description: "invite ID is not a bare ASCII decimal string"},
		registry.CodeInviteKindInvalid:          {ExitCode: output.ExitUsage, Description: "invite namespace is not explicitly user or device"},
		registry.CodeInviteRateLimited:          {ExitCode: output.ExitError, Description: "Tailscale rate limited the invite operation"},
		registry.CodeInviteStateConflict:        {ExitCode: output.ExitConflict, Description: "Tailscale rejected the invite operation because current remote state conflicts with it (HTTP 409)"},
		registry.CodeInviteRequestInvalid:       {ExitCode: output.ExitUsage, Description: "Tailscale rejected the invite request as another 4xx input error"},
		registry.CodeInviteResponseInvalid:      {ExitCode: output.ExitError, Description: "Tailscale returned an invalid invite wire response"},
		registry.CodeLegacyConfigDirPresent:     {ExitCode: output.ExitConflict, Description: "Windows only: the config directory is still at the legacy %USERPROFILE%\\.config\\tslink location; TSLink never moves it, and next carries the one manual move command"},
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
	hasChildren := make(map[string]bool)
	for _, parent := range manifest.Commands {
		for _, candidate := range manifest.Commands {
			if strings.HasPrefix(candidate.Path, parent.Path+" ") {
				hasChildren[parent.Path] = true
				break
			}
		}
	}
	for _, command := range manifest.Commands {
		if command.Path == "tslink" || command.Path == "tslink manifest" {
			// The hidden manifest generator is transport metadata, not an
			// executable product action an agent needs echoed inside itself.
			continue
		}
		path := strings.TrimPrefix(command.Path, "tslink ")
		flags := make([]string, 0, len(command.Flags))
		for _, flag := range command.Flags {
			if flag.Scope != "inherited" && flag.Name != "json" {
				flags = append(flags, flag.Name)
			}
		}
		if len(flags) == 0 && hasChildren[command.Path] {
			continue
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
