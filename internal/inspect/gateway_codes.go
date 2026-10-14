package inspect

func init() {
	for code, meta := range map[string]WarningCodeMeta{
		"gateway_node_identity_changed":    {Severity: "warning", Source: "portal.gateway", Description: "Gateway identity changed; accepted shares still target the previous device."},
		"gateway_ephemeral_refused":        {Severity: "error", Source: "portal.gateway", Description: "A persistent gateway enrollment could not be proven."},
		"gateway_tag_requires_credentials": {Severity: "error", Source: "portal.gateway", Description: "Gateway tags require credentialed enrollment."},
		"gateway_hostname_unavailable":     {Severity: "warning", Source: "portal.gateway", Description: "The gateway has no verified DNS name."},
		"portal_route_bad_port":            {Severity: "error", Source: "portal.gateway", Description: "The route port is reserved or blocked by browsers."},
		"portal_route_port_conflict":       {Severity: "error", Source: "portal.gateway", Description: "The route port is assigned more than once."},
		"portal_route_port_range":          {Severity: "error", Source: "portal.gateway", Description: "The route port is outside the configured range."},
		"route_listener_failed":            {Severity: "warning", Source: "portal.gateway", Description: "The route listener failed and will be retried with backoff."},
		"upstream_down":                    {Severity: "warning", Source: "portal.gateway", Description: "The app upstream is unavailable."},
		"upstream_unreachable":             {Severity: "warning", Source: "portal.gateway", Description: "The app upstream failed its health checks."},
		"remote_upstream_requires_tag":     {Severity: "error", Source: "portal.gateway", Description: "The observed gateway node identity must be tagged for remote apps."},
		"remote_upstream_outside_tailnet":  {Severity: "error", Source: "portal.gateway", Description: "The remote hostname is outside the gateway tailnet suffix."},
		"remote_upstream_not_peer":         {Severity: "error", Source: "portal.gateway", Description: "The remote hostname must identify a peer visible to the gateway node."},
		"remote_upstream_unavailable":      {Severity: "warning", Source: "portal.gateway", Description: "The gateway cannot prove its remote dial context."},
		"target_probe_skipped_remote":      {Severity: "info", Source: "target_probe", Description: "Remote upstream probes run only through the gateway node; see portal.gateway route health."},
	} {
		WarningCodeRegistry[code] = meta
	}
}
