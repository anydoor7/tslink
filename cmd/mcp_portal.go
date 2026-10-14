package cmd

func portalSchema() map[string]any {
	return objectSchema(map[string]any{
		"gateway":  gatewaySchema(),
		"enabled":  map[string]any{"type": "boolean"},
		"hostname": map[string]any{"type": "string"},
		"state":    map[string]any{"type": "string"},
		"url":      map[string]any{"type": "string"},
		"error":    map[string]any{"type": "string"},
	}, "enabled", "state")
}

func init() {
	view := portalSchema()
	mcpToolHints["portal_enable"] = mcpHints(false, false, true, false)
	mcpToolHints["portal_disable"] = mcpHints(false, false, true, false)
	mcpToolDefinitions = append(mcpToolDefinitions,
		mcpToolDefinition{Name: "portal_enable", Description: "Enable an independent Tailnet-only home portal. Requires the owner's Tailscale login; optional admins can open all private apps. HTTP MCP requires the current exact portal owner before any owner/admin settings can change; other callers receive access_request_owner_required. Local CLI/stdio MCP is the bootstrap/recovery path; an unset owner cannot be claimed remotely. Confirm administrative identities with the owner. Grants and service registrations stay intact; the daemon hot-reloads the portal independently. Does not install or restart the daemon, send invites, or change tailnet ACLs. Funnel is explicitly refused. A pending result is saved configuration, not proof of a running URL; check status. Outside-tailnet visitors also need the home node shared through Tailscale.", InputSchema: objectSchema(map[string]any{
			"gateway":       map[string]any{"type": "boolean", "description": "Enable the persistent gateway. Omit to retain current settings; false removes gateway mode only when no routes remain."},
			"gateway_ports": map[string]any{"type": "string", "description": "Gateway port range, initially 8443-8499; omitted retains the range."},
			"gateway_tag":   map[string]any{"type": "string", "description": "Credentialed gateway tag; omitted retains the tag, empty restores the default."},
			"owner":         map[string]any{"type": "string", "minLength": 1},
			"hostname":      map[string]any{"type": "string", "default": "home"},
			"admins":        stringArraySchema(),
			"funnel":        map[string]any{"type": "boolean", "default": false, "description": "Always refused when true."},
		}, "owner"), OutputSchema: view},
		mcpToolDefinition{Name: "portal_disable", Description: "Disable the home portal. App services, grants, retained owner/admin identities and enrolled local node state remain. The running daemon closes the portal and route listeners. No remote device deletion or invites.", InputSchema: objectSchema(map[string]any{}), OutputSchema: view},
	)
}
