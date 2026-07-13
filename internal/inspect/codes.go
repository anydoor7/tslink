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

	WarningCodeConfigPathUnavailable               = "config_path_unavailable"
	WarningCodeConfigLoadFailed                    = "config_load_failed"
	WarningCodeRegistryLoadFailed                  = "registry_load_failed"
	WarningCodeRegistryServiceInvalid              = "registry_service_invalid"
	WarningCodeControlURLInvalid                   = "control_url_invalid"
	WarningCodeCredentialNone                      = "credential_none"
	WarningCodeCredentialLegacyAuthKey             = "credential_legacy_authkey"
	WarningCodeCredentialNoAPIClient               = "credential_no_api_client"
	WarningCodeCredentialReadFailed                = "credential_read_failed"
	WarningCodeDaemonNotRunning                    = "daemon_not_running"
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
		Severity:    "error",
		Source:      "credentials",
		Description: "No supported TSLink credential is configured.",
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
	WarningCodeDaemonNotRunning: {
		Severity:    "warning",
		Source:      "daemon",
		Description: "The TSLink daemon is not currently running.",
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
}
