// Package errcode is the one table of TSLink's stable error codes: for each
// code, the process exit code it produces, whether a daemon failure with it
// isolates one service, and what it means. output.ExitCode, the daemon's
// per-service isolation and the manifest's error_codes are all derived from
// this table, and a test scans the product source so an emitted code the table
// does not list fails the tests.
//
// The Go constants that name the codes stay beside the code that raises them
// (registry.CodeX, config.CodeX); the table is keyed by the wire string, which
// never changes once released.
package errcode

import "sort"

// Exit codes. internal/output re-exports them.
const (
	ExitSuccess  = 0
	ExitError    = 1
	ExitUsage    = 2
	ExitAuth     = 3
	ExitConflict = 4
	ExitNotFound = 5
	ExitWarning  = 64
	ExitCritical = 65
)

// Scope says what a failure with a code stops when the daemon meets it.
type Scope int

const (
	// Command codes fail the command that raised them. In the daemon they are
	// not a verdict about one service, so a sync that meets one fails.
	Command Scope = iota
	// Service codes describe one service's configuration or start. The daemon
	// isolates that service, records the failure with its next steps for
	// status and doctor, and keeps serving the others.
	Service
)

// Generic codes name an exit class and nothing more: output.StableErrorCode
// gives them to an error that carries only an exit code.
const (
	InternalError = "internal_error"
	UsageError    = "usage_error"
	AuthError     = "auth_error"
	Conflict      = "conflict"
	NotFound      = "not_found"
)

// Code is one row of the table.
type Code struct {
	Code        string
	Exit        int
	Scope       Scope
	Description string
}

var table = []Code{
	// Generic classes.
	{InternalError, ExitError, Command, "unexpected internal failure"},
	{UsageError, ExitUsage, Command, "invalid command syntax or value"},
	{AuthError, ExitAuth, Command, "authentication or authorization required or rejected"},
	{Conflict, ExitConflict, Command, "requested state conflicts with existing state"},
	{NotFound, ExitNotFound, Command, "requested object was not found"},

	// Service definitions: raised when a service is added or read from
	// registry.json, and when the daemon validates it again before start.
	{"service_type_ambiguous", ExitUsage, Command, "exactly one service type is required"},
	{"invalid_service_name", ExitUsage, Service, "service name is not a valid DNS label"},
	{"invalid_tag", ExitUsage, Service, "ACL tag is invalid"},
	{"invalid_request_limits", ExitUsage, Service, "HTTP request limits are invalid or unlimited uploads lack explicit acknowledgement"},
	{"allow_unsupported_for_tcp", ExitUsage, Service, "HTTP allow lists do not apply to raw TCP"},
	{"people_service_unsupported", ExitUsage, Service, "person grants require private HTTP proxy or file services; TCP and public Funnel cannot be person-scoped"},
	{"invite_failed", ExitError, Command, "one device invitation in a people bundle failed without a more specific code"},
	{"invite_state_failed", ExitError, Command, "durable people invitation state could not be read or saved; reconcile before sending again"},
	{"people_invite_busy", ExitError, Command, "another owner process is doing remote invitation work; local denial remains saved and remote work is deferred"},
	{"person_grant_inactive", ExitError, Command, "person app grant was removed, revoked or expired before sending an invitation"},
	{"invite_reconciliation_failed", ExitError, Command, "remote invitation listing failed; do not create before reconciling the unknown outcome"},
	{"invite_reconciliation_required", ExitError, Command, "unknown invitation POST needs explicit owner-verified app=id or app=none reconciliation"},
	{"invite_reconciliation_conflict", ExitError, Command, "invitation ID is already associated with another person or app operation"},
	{"invite_link_unavailable", ExitError, Command, "completed invitation link is unavailable; no replacement was created"},
	{"invite_cleanup_failed", ExitError, Command, "pending device invitation cleanup failed; local person access remains denied"},
	{"path_must_be_absolute", ExitUsage, Service, "file service path must be absolute"},
	{"path_not_found", ExitUsage, Service, "file service directory does not exist"},
	{"path_not_directory", ExitUsage, Service, "file service path is not a directory"},
	{"path_not_accessible", ExitUsage, Service, "file service path is not accessible to the current user"},
	{"path_exposes_config_dir", ExitUsage, Service, "file service path is, contains, or lies inside TSLink's config directory, which holds node keys and credentials"},
	{"link_local_target_refused", ExitUsage, Service, "proxy or TCP target is a link-local or cloud metadata address, which TSLink refuses"},
	{"unknown_config_key", ExitUsage, Service, "configuration key is not supported"},
	{"invalid_service_config", ExitError, Service, "a registry.json service entry failed validation for a reason without a more specific code; the daemon isolates it and keeps the others"},
	{"registry_reload_invalid", ExitError, Service, "a registry.json edit made a running public Funnel service invalid; the daemon closed its public listener"},
	{"funnel_public_ack_required", ExitUsage, Service, "public Funnel acknowledgement is required"},
	{"funnel_expiry_required", ExitUsage, Service, "a Funnel entry records neither a funnel_expires_at deadline nor \"never\"; it is not made public until one is set"},
	{"funnel_allow_conflict", ExitConflict, Service, "Funnel conflicts with an allow list"},
	{"funnel_control_url_conflict", ExitConflict, Service, "Funnel conflicts with control_url"},
	{"funnel_type_conflict", ExitConflict, Service, "Funnel requires a proxy service"},

	// Service start in the daemon.
	{"credential_control_url_mismatch", ExitConflict, Service, "a credentialed daemon refuses to send an auth key minted through the Tailscale API to a control server that is not Tailscale's; the service is not started"},
	{"funnel_capability_missing", ExitError, Service, "service tsnet node lacks Funnel capability, HTTPS, or allowed port"},
	{"funnel_listen_failed", ExitError, Service, "Funnel capability preflight passed but listener activation failed"},
	{"service_start_timeout", ExitError, Service, "service node did not reach running state before its startup deadline"},

	// Local configuration.
	{"config_load_failed", ExitUsage, Command, "config.json exists but cannot be read strictly (malformed JSON or an unknown key); commands that would persist its settings refuse to guess"},
	{"legacy_config_dir_present", ExitConflict, Command, "Windows only: the config directory is still at the legacy %USERPROFILE%\\.config\\tslink location; TSLink never moves it, and next carries the one manual move command"},

	// Daemon and runtime state.
	{"daemon_not_running", ExitError, Command, "the command needs the TSLink daemon and it is not running; next says how to install and start it"},
	{"daemon_setup_failed", ExitError, Command, "installing or starting the background service failed; next names the logs and doctor to inspect first"},
	{"daemon_supervision_unverified", ExitError, Command, "a daemon is running but its supervisor could not be verified, so TSLink does not take it over"},
	{"launchctl_domain_unavailable", ExitError, Command, "a launchd domain could not be checked; failure data names the domain, explicit --force command, and residual risk"},
	{"runtime_snapshot_missing", ExitError, Command, "no runtime snapshot (runtime.json) is available, so exact runtime endpoints cannot be proven"},
	{"runtime_snapshot_stale", ExitError, Command, "the runtime snapshot is readable but belongs to another daemon or registry, or is partial while the registry is being synchronized"},
	{"runtime_snapshot_unreadable", ExitError, Command, "the runtime snapshot could not be read: a filesystem or permission error, malformed JSON, or a schema_version this build does not understand"},
	{"url_not_ready", ExitNotFound, Command, "runtime has not reported an exact tailnet hostname"},
	{"enrollment_required", ExitAuth, Command, "the node is waiting for interactive Tailscale authorization; next carries the authorization URL to open"},

	// Tailscale API credentials.
	{"api_token_unauthorized", ExitAuth, Service, "Tailscale rejected the stored API credential as unauthenticated (HTTP 401) during ACL, device, auth-key derivation, or login verification; next carries the key-bootstrap steps"},
	{"api_forbidden", ExitAuth, Service, "Tailscale refused the API credential (HTTP 403): its user role or OAuth scopes do not permit the operation"},
	{"login_verify_failed", ExitError, Command, "login could not verify the candidate credential against Tailscale for a reason other than HTTP 401/403; the previous credential was kept"},

	// Invitations.
	{"invite_api_key_required", ExitAuth, Command, "a user-owned tskey-api- token is required and OAuth is not eligible"},
	{"invite_role_invalid", ExitUsage, Command, "invite role is outside the first-party enum"},
	{"invite_recipient_invalid", ExitUsage, Command, "invite recipient is missing"},
	{"invite_not_found", ExitNotFound, Command, "invite or matching service device was not found"},
	{"invite_device_ambiguous", ExitConflict, Command, "multiple hostname candidates remain after ownership resolution"},
	{"invite_device_ownership_unproven", ExitConflict, Command, "TSLink lacks exact stable nodeId proof for the device"},
	{"invite_api_forbidden", ExitAuth, Command, "Tailscale rejected the user-owned token's user permissions with HTTP 403; HTTP 401 (expired, revoked, or invalid token) is reported as invite_api_unauthorized"},
	{"invite_api_unauthorized", ExitAuth, Command, "Tailscale rejected the user-owned tskey-api- token as unauthenticated (HTTP 401): expired, revoked, or invalid; next carries the key-bootstrap steps"},
	{"invite_resend_email_missing", ExitConflict, Command, "an invite created without email cannot be resent"},
	{"invite_id_invalid", ExitUsage, Command, "invite ID is not a bare ASCII decimal string"},
	{"invite_kind_invalid", ExitUsage, Command, "invite namespace is not explicitly user or device"},
	{"invite_rate_limited", ExitError, Command, "Tailscale rate limited the invite operation"},
	{"invite_state_conflict", ExitConflict, Command, "Tailscale rejected the invite operation because current remote state conflicts with it (HTTP 409)"},
	{"invite_request_invalid", ExitUsage, Command, "Tailscale rejected the invite request as another 4xx input error"},
	{"invite_response_invalid", ExitError, Command, "Tailscale returned an invalid invite wire response"},
	{"mcp_elevated_invite_refused", ExitAuth, Command, "through MCP, a user invitation with a role other than member or a device invitation that allows exit-node use needs the owner's opt-in (mcp.allow_elevated_invites in config.json); next names the CLI command a person can run instead"},
	{"mcp_scope_denied", ExitAuth, Command, "MCP identity scope refuses this app, tool, duration or expired binding"},
	{"mcp_audit_unavailable", ExitError, Command, "MCP mutation intent could not be journaled, or its completion receipt failed; completion failures explicitly retain an unknown outcome"},
}

var byCode = func() map[string]Code {
	rows := make(map[string]Code, len(table))
	for _, row := range table {
		if _, duplicate := rows[row.Code]; duplicate {
			panic("errcode: duplicate row " + row.Code)
		}
		rows[row.Code] = row
	}
	return rows
}()

// Lookup returns the table row for a stable code.
func Lookup(code string) (Code, bool) {
	row, ok := byCode[code]
	return row, ok
}

// ExitFor returns the exit code a failure with this stable code produces. A
// code the table does not list exits ExitError; the source scan test keeps
// that case out of the product.
func ExitFor(code string) int {
	if row, ok := byCode[code]; ok {
		return row.Exit
	}
	return ExitError
}

// IsServiceScoped reports whether a daemon failure with this code isolates the
// one service it belongs to instead of failing the sync.
func IsServiceScoped(code string) bool {
	return byCode[code].Scope == Service
}

// Generic returns the generic code of an exit class.
func Generic(exit int) string {
	switch exit {
	case ExitUsage:
		return UsageError
	case ExitAuth:
		return AuthError
	case ExitConflict:
		return Conflict
	case ExitNotFound:
		return NotFound
	default:
		return InternalError
	}
}

// All returns every row, sorted by code.
func All() []Code {
	rows := append([]Code(nil), table...)
	sort.Slice(rows, func(i, j int) bool { return rows[i].Code < rows[j].Code })
	return rows
}
