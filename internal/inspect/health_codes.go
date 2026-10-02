package inspect

const (
	WarningCodeAppProbeFailed     = "app_probe_failed"
	WarningCodeNodeKeyExpiring    = "node_key_expiring"
	WarningCodeNodeKeyCritical    = "node_key_expiry_critical"
	WarningCodeNodeKeyExpired     = "node_key_expired"
	WarningCodeNodeKeyUnknown     = "node_key_expiry_unknown"
	WarningCodeCredentialCritical = "credential_expiry_critical"
)

func init() {
	for code, meta := range map[string]WarningCodeMeta{
		WarningCodeAppProbeFailed:     {Severity: "error", Source: "app_probe", Description: "HTTP business probe failed; inspect the backend app and its configured health endpoint."},
		WarningCodeNodeKeyExpiring:    {Severity: "warning", Source: "node_key", Description: "Service node key expires within 14 days; review this node in the Tailscale admin console."},
		WarningCodeNodeKeyCritical:    {Severity: "error", Source: "node_key", Description: "Service node key expires within 3 days; reauthenticate or review expiry before it loses connectivity."},
		WarningCodeNodeKeyExpired:     {Severity: "error", Source: "node_key", Description: "Service node key has expired; review this node in the Tailscale admin console."},
		WarningCodeNodeKeyUnknown:     {Severity: "info", Source: "node_key", Description: "Node key expiry is unavailable; absence of a reported deadline does not prove that expiry is disabled."},
		WarningCodeCredentialCritical: {Severity: "error", Source: "credentials", Description: "Stored credential expires within 3 days; rotate it using the stdin login command. Assumed expiry remains an estimate."},
	} {
		WarningCodeRegistry[code] = meta
	}
}
