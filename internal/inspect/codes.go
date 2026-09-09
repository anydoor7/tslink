package inspect

import "github.com/monody0007/tslink/internal/registry"

const (
	WarningCodeTCPHTTPACLNotApplicable   = "tcp_http_acl_not_applicable"
	WarningCodeTCPAllowedUsersInvalid    = "tcp_allowed_users_invalid"
	WarningCodeHTTPAuthConfigured        = "http_auth_configured"
	WarningCodeMiddlewareNotEnforced     = "middleware_not_enforced"
	WarningCodeServiceTypeUnknown        = "service_type_unknown"
	WarningCodeCustomDomainNotWired      = "custom_domain_not_wired"
	WarningCodeRuntimeSnapshotMissing    = "runtime_snapshot_missing"
	WarningCodeRuntimeSnapshotStale      = "runtime_snapshot_stale"
	WarningCodeRuntimeSnapshotUnreadable = "runtime_snapshot_unreadable"
	WarningCodeFunnelAllowConflict       = registry.CodeFunnelAllowConflict
	WarningCodeFunnelPublicAckRequired   = registry.CodeFunnelPublicAckRequired
	WarningCodeFunnelControlURLConflict  = registry.CodeFunnelControlURLConflict
	WarningCodeFunnelTypeConflict        = registry.CodeFunnelTypeConflict
	WarningCodePathNotFound              = registry.CodePathNotFound
	WarningCodePathNotDirectory          = registry.CodePathNotDirectory
	WarningCodePathNotAccessible         = registry.CodePathNotAccessible

	WarningCodeConfigPathUnavailable               = "config_path_unavailable"
	WarningCodeConfigLoadFailed                    = "config_load_failed"
	WarningCodeRegistryLoadFailed                  = "registry_load_failed"
	WarningCodeRegistryServiceInvalid              = "registry_service_invalid"
	WarningCodeControlURLInvalid                   = "control_url_invalid"
	WarningCodeCredentialNone                      = "credential_none"
	WarningCodeCredentialTier1                     = "credential_tier1"
	WarningCodeCredentialLegacyAuthKey             = "credential_legacy_authkey"
	WarningCodeCredentialNoAPIClient               = "credential_no_api_client"
	WarningCodeCredentialReadFailed                = "credential_read_failed"
	WarningCodeDaemonIdentityUnverified            = "daemon_identity_unverified"
	WarningCodeDaemonBuildSkew                     = "daemon_build_skew"
	WarningCodeDaemonRestartUnavailable            = "daemon_restart_unavailable"
	WarningCodeDaemonNotRunning                    = "daemon_not_running"
	WarningCodeDaemonUnsupervised                  = "daemon_unsupervised"
	WarningCodeTargetProbeSkippedDaemon            = "target_probe_skipped_daemon_not_running"
	WarningCodeDaemonPIDUnreadable                 = "daemon_pid_unreadable"
	WarningCodeTargetProbeSkippedExternal          = "target_probe_skipped_external"
	WarningCodeTargetProbeFailed                   = "target_probe_failed"
	WarningCodeTargetProbeRefused                  = "target_probe_refused"
	WarningCodeTargetProbeTimeout                  = "target_probe_timeout"
	WarningCodeTargetInvalid                       = "target_invalid"
	WarningCodeProxyNonLoopbackTarget              = "proxy_non_loopback_target"
	WarningCodeTCPNonLoopbackTarget                = "tcp_non_loopback_target"
	WarningCodeFilePathMissing                     = "file_path_missing"
	WarningCodeFilePathUnreadable                  = "file_path_unreadable"
	WarningCodeFunnelGlobalControlURLUnknownCompat = "funnel_global_control_url_unknown_compat"
	WarningCodeIdentityResolutionUnknown           = "identity_resolution_unknown"

	WarningCodeCredentialMixedRecommended  = "credential_mixed_recommended"
	WarningCodeCredentialAPITokenOnly      = "credential_api_token_only"
	WarningCodeCredentialOAuthClientOnly   = "credential_oauth_client_only"
	WarningCodeCredentialAPITokenExpiring  = "credential_api_token_expiring"
	WarningCodeCredentialAPITokenExpired   = "credential_api_token_expired"
	WarningCodeCredentialExpiryUnknown     = "credential_expiry_unknown"
	WarningCodeCredentialRemoteUnverified  = "credential_remote_unverified"
	WarningCodeCredentialMetaBackfilled    = "credential_meta_backfilled"
	WarningCodeCredentialAPITokenRejected  = "credential_api_token_rejected"
	WarningCodeCredentialRemoteForbidden   = "credential_remote_forbidden"
	WarningCodeCredentialRemoteUnreachable = "credential_remote_unreachable"

	// Tailscale SSH is a tailscaled feature, not a TSLink feature. These three
	// codes are always informational: they report what the local Tailscale
	// client says about this node so an operator discovers the zero-code remote
	// path, and they never change doctor's health status or exit code.
	WarningCodeTailscaleSSHEnabled  = "tailscale_ssh_enabled"
	WarningCodeTailscaleSSHDisabled = "tailscale_ssh_disabled"
	WarningCodeTailscaleSSHUnknown  = "tailscale_ssh_unknown"
)

type WarningCodeMeta struct {
	Severity    string
	Source      string
	Description string
}

var WarningCodeRegistry = map[string]WarningCodeMeta{
	WarningCodeTCPHTTPACLNotApplicable: {
		Severity:    "warning",
		Source:      "service.type",
		Description: "Raw TCP services are private tsnet routes; TSLink HTTP identity and allow filtering do not apply.",
	},
	WarningCodeTCPAllowedUsersInvalid: {
		Severity:    "error",
		Source:      "service.allowed_users",
		Description: "Raw TCP services cannot enforce allowed_users; remove the allow list or convert the service to HTTP.",
	},
	WarningCodeHTTPAuthConfigured: {
		Severity:    "info",
		Source:      "service.middleware",
		Description: "HTTP authentication is configured and credential values are redacted from public service views.",
	},
	WarningCodeMiddlewareNotEnforced: {
		Severity:    "warning",
		Source:      "service.middleware",
		Description: "Middleware configuration is present but the serve runtime does not enforce it yet.",
	},
	WarningCodeServiceTypeUnknown: {
		Severity:    "warning",
		Source:      "service.type",
		Description: "The registry contains a service type unknown to this TSLink version.",
	},
	WarningCodeCustomDomainNotWired: {
		Severity:    "error",
		Source:      "service.domain",
		Description: "Custom-domain/ACME fields are present but the serve runtime does not wire them.",
	},
	WarningCodeRuntimeSnapshotMissing: {
		Severity:    "warning",
		Source:      "runtime.snapshot",
		Description: "No runtime snapshot is available, so exact runtime endpoints cannot be proven.",
	},
	WarningCodeRuntimeSnapshotStale: {
		Severity:    "warning",
		Source:      "runtime.snapshot",
		Description: "The runtime snapshot is malformed or does not match the current daemon or registry.",
	},
	WarningCodeRuntimeSnapshotUnreadable: {
		Severity:    "warning",
		Source:      "runtime.snapshot",
		Description: "The runtime snapshot could not be read because of a raw filesystem or permission error.",
	},
	WarningCodeFunnelAllowConflict: {
		Severity:    "error",
		Source:      "service.allowed_users",
		Description: "Public Funnel services cannot be combined with TSLink allow lists.",
	},
	WarningCodeFunnelPublicAckRequired: {
		Severity:    "error",
		Source:      "service.public_ack",
		Description: "Public Funnel services require recorded public acknowledgement.",
	},
	WarningCodeFunnelControlURLConflict: {
		Severity:    "error",
		Source:      "service.control_url",
		Description: "Public Funnel services cannot use per-service control_url.",
	},
	WarningCodeFunnelTypeConflict: {
		Severity:    "error",
		Source:      "service.type",
		Description: "Public Funnel services can only be proxy services.",
	},
	WarningCodePathNotFound: {
		Severity:    "error",
		Source:      "service.path",
		Description: "A configured file service directory does not exist.",
	},
	WarningCodePathNotDirectory: {
		Severity:    "error",
		Source:      "service.path",
		Description: "A configured file service path is not a directory.",
	},
	WarningCodePathNotAccessible: {
		Severity:    "error",
		Source:      "service.path",
		Description: "A configured file service path is not accessible to the current user.",
	},
	WarningCodeConfigPathUnavailable: {
		Severity:    "error",
		Source:      "config.path",
		Description: "TSLink config paths could not be discovered locally.",
	},
	WarningCodeConfigLoadFailed: {
		Severity:    "error",
		Source:      "config.global",
		Description: "TSLink global config could not be loaded.",
	},
	WarningCodeRegistryLoadFailed: {
		Severity:    "critical",
		Source:      "registry.load",
		Description: "TSLink registry could not be loaded.",
	},
	WarningCodeRegistryServiceInvalid: {
		Severity:    "error",
		Source:      "registry.service",
		Description: "A registry service failed validation.",
	},
	WarningCodeControlURLInvalid: {
		Severity:    "error",
		Source:      "control_url",
		Description: "A configured control_url is not a valid HTTP or HTTPS URL.",
	},
	WarningCodeCredentialNone: {
		Severity:    "warning",
		Source:      "credentials",
		Description: "Tier 1 interactive enrollment has not started; run tslink serve, or use tslink login only for optional Tier 2.",
	},
	WarningCodeCredentialTier1: {
		Severity:    "info",
		Source:      "credentials",
		Description: "TSLink is using the default Tier 1 interactive-enrollment path without a stored administrative credential.",
	},
	WarningCodeCredentialLegacyAuthKey: {
		Severity:    "warning",
		Source:      "credentials",
		Description: "A legacy reusable auth key is configured; API token or OAuth client secret is preferred.",
	},
	WarningCodeCredentialNoAPIClient: {
		Severity:    "info",
		Source:      "credentials",
		Description: "Configured credentials can start services but do not provide a local Tailscale API client.",
	},
	WarningCodeCredentialReadFailed: {
		Severity:    "error",
		Source:      "credentials",
		Description: "Stored credentials could not be inspected locally.",
	},
	WarningCodeDaemonIdentityUnverified: {Severity: "warning", Source: "daemon", Description: "Daemon process identity is unverified; preserve probes and inspect before restarting."},
	WarningCodeDaemonBuildSkew: {
		Severity:    "warning",
		Source:      "daemon",
		Description: "The running daemon's own reported build differs from this CLI invocation's build, so an upgraded binary is not yet the one actually serving; run 'tslink install' to restart the daemon with the current binary.",
	},
	WarningCodeDaemonRestartUnavailable: {Severity: "warning", Source: "daemon", Description: "Sign-in startup is installed but crash restart is unavailable."},
	WarningCodeDaemonNotRunning: {
		Severity:    "error",
		Source:      "daemon",
		Description: "The TSLink daemon is not currently running.",
	},
	WarningCodeDaemonUnsupervised: {
		Severity: "error", Source: "daemon", Description: "Registered services have no verified supervisor/autostart.",
	},
	WarningCodeTargetProbeSkippedDaemon: {
		Severity: "info", Source: "target_probe", Description: "Backend probe deferred until the TSLink daemon is running.",
	},
	WarningCodeDaemonPIDUnreadable: {
		Severity:    "warning",
		Source:      "daemon.pid",
		Description: "The TSLink daemon appears to be running, but the PID file could not be read.",
	},
	WarningCodeTargetProbeSkippedExternal: {
		Severity:    "warning",
		Source:      "target.probe",
		Description: "External target probing is skipped unless --probe-external is set.",
	},
	WarningCodeTargetProbeFailed: {
		Severity:    "error",
		Source:      "target.probe",
		Description: "A target probe failed.",
	},
	WarningCodeTargetProbeRefused: {
		Severity:    "error",
		Source:      "target.probe",
		Description: "A target probe was refused.",
	},
	WarningCodeTargetProbeTimeout: {
		Severity:    "error",
		Source:      "target.probe",
		Description: "A target probe timed out.",
	},
	WarningCodeTargetInvalid: {
		Severity:    "error",
		Source:      "target",
		Description: "A service target could not be parsed for local diagnostics.",
	},
	WarningCodeProxyNonLoopbackTarget: {
		Severity:    "warning",
		Source:      "service.target",
		Description: "A proxy service points at a non-loopback target.",
	},
	WarningCodeTCPNonLoopbackTarget: {
		Severity:    "warning",
		Source:      "service.target",
		Description: "A TCP service points at a non-loopback target.",
	},
	WarningCodeFilePathMissing: {
		Severity:    "error",
		Source:      "service.path",
		Description: "A file service path does not exist.",
	},
	WarningCodeFilePathUnreadable: {
		Severity:    "error",
		Source:      "service.path",
		Description: "A file service path is not readable as a directory.",
	},
	WarningCodeFunnelGlobalControlURLUnknownCompat: {
		Severity:    "warning",
		Source:      "config.control_url",
		Description: "A global custom control_url is configured while Funnel services exist; compatibility cannot be proven locally.",
	},
	WarningCodeIdentityResolutionUnknown: {
		Severity:    "info",
		Source:      "identity",
		Description: "Local diagnostics cannot prove real Tailscale identity resolution or remote ACL policy.",
	},
	WarningCodeCredentialMixedRecommended: {
		Severity:    "info",
		Source:      "credentials",
		Description: "Both an OAuth client secret (durable daemon auth) and a user-owned API access token (invites) are stored; this is the recommended dual-slot configuration.",
	},
	WarningCodeCredentialAPITokenOnly: {
		Severity:    "warning",
		Source:      "credentials",
		Description: "Only a user-owned API access token is stored: invites work, but daemon node authentication depends on a token that expires within 90 days; add an OAuth client secret for durable daemon auth.",
	},
	WarningCodeCredentialOAuthClientOnly: {
		Severity:    "info",
		Source:      "credentials",
		Description: "Only an OAuth client secret is stored: daemon authentication is durable, but invite operations need a user-owned tskey-api- token.",
	},
	WarningCodeCredentialAPITokenExpiring: {
		Severity:    "warning",
		Source:      "credentials",
		Description: "The stored API access token expires within 14 days; generate a new token and run tslink login --api-key-stdin before it lapses.",
	},
	WarningCodeCredentialAPITokenExpired: {
		Severity:    "error",
		Source:      "credentials",
		Description: "The stored API access token has passed its recorded expiry; remote API calls that depend on it will fail with HTTP 401 until a new token is stored.",
	},
	WarningCodeCredentialExpiryUnknown: {
		Severity:    "warning",
		Source:      "credentials",
		Description: "Credential metadata (credential-meta.json) is missing or unreadable, so token expiry cannot be evaluated locally.",
	},
	WarningCodeCredentialRemoteUnverified: {
		Severity:    "info",
		Source:      "credentials",
		Description: "A stored credential has never been verified against the Tailscale API by this TSLink install; run tslink doctor --probe-remote to record a verification.",
	},
	WarningCodeCredentialMetaBackfilled: {
		Severity:    "info",
		Source:      "credentials",
		Description: "Credential metadata was backfilled for a credential stored before expiry tracking existed; stored_at is the backfill time and expires_at is the assumed 90-day maximum.",
	},
	WarningCodeCredentialAPITokenRejected: {
		Severity:    "error",
		Source:      "credentials",
		Description: "The remote probe was rejected with HTTP 401: the stored credential is expired, revoked, or invalid.",
	},
	WarningCodeCredentialRemoteForbidden: {
		Severity:    "warning",
		Source:      "credentials",
		Description: "The remote probe was refused with HTTP 403: the credential is valid but its user role or OAuth scopes do not permit the probe operation.",
	},
	WarningCodeCredentialRemoteUnreachable: {
		Severity:    "warning",
		Source:      "credentials",
		Description: "The remote probe could not reach the Tailscale API; the credential state remains unproven.",
	},
	WarningCodeTailscaleSSHEnabled: {
		Severity:    "info",
		Source:      "tailscale_ssh",
		Description: "Tailscale SSH is enabled on this node, so `tailscale ssh <this-host> tslink <command>` can drive this install remotely once a tailnet ACL ssh rule permits the caller.",
	},
	WarningCodeTailscaleSSHDisabled: {
		Severity:    "info",
		Source:      "tailscale_ssh",
		Description: "Tailscale SSH is disabled on this node. Run `tailscale set --ssh` here and add a tailnet ACL ssh rule to reach this install with `tailscale ssh <this-host> tslink <command>`; TSLink does not manage this tailscaled feature.",
	},
	WarningCodeTailscaleSSHUnknown: {
		Severity:    "info",
		Source:      "tailscale_ssh",
		Description: "The local Tailscale client state could not be read, so Tailscale SSH enablement on this node is unknown. Check `tailscale status` on this machine.",
	},
}
