package inspect

import "github.com/monody0007/tslink/internal/registry"

const (
	WarningCodeTCPHTTPACLNotApplicable  = "tcp_http_acl_not_applicable"
	WarningCodeTCPAllowedUsersInvalid   = "tcp_allowed_users_invalid"
	WarningCodeHTTPAuthConfigured       = "http_auth_configured"
	WarningCodeServiceTypeUnknown       = "service_type_unknown"
	WarningCodeFunnelAllowConflict      = registry.CodeFunnelAllowConflict
	WarningCodeFunnelControlURLConflict = registry.CodeFunnelControlURLConflict
	WarningCodeFunnelTypeConflict       = registry.CodeFunnelTypeConflict
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
	WarningCodeServiceTypeUnknown: {
		Severity:    "warning",
		Source:      "service.type",
		Description: "The registry contains a service type unknown to this TSLink version.",
	},
	WarningCodeFunnelAllowConflict: {
		Severity:    "error",
		Source:      "service.allowed_users",
		Description: "Public Funnel services cannot be combined with TSLink allow lists.",
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
}
