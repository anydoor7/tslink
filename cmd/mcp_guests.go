package cmd

// Guest browser grants remain owner-only when reduced scopes are enabled.
var mcpOwnerOnlyGuestTools = map[string]bool{"guest_create": true, "guest_list": true, "guest_show": true, "guest_revoke": true}

func init() {
	for _, name := range []string{"guest_create", "guest_list", "guest_show", "guest_revoke"} {
		read := name == "guest_list" || name == "guest_show"
		mcpToolHints[name] = mcpHints(read, name == "guest_revoke", name != "guest_create", false)
		properties := map[string]any{}
		required := []string{}
		out := map[string]any{"grant": map[string]any{"type": "object"}}
		outputRequired := []string{"grant"}
		if name == "guest_list" {
			out = map[string]any{"grants": map[string]any{"type": "array", "items": map[string]any{"type": "object"}}}
			outputRequired = []string{"grants"}
		}
		if name == "guest_show" || name == "guest_revoke" {
			properties["id"] = map[string]any{"type": "string", "minLength": 1}
			required = []string{"id"}
		}
		if name == "guest_create" {
			properties = map[string]any{"app": map[string]any{"type": "string", "minLength": 1}, "for": lifetimeSchema(true), "label": map[string]any{"type": "string", "maxLength": 200}, "pin": map[string]any{"type": "string", "description": "Optional secret 6..64 digit PIN; never log it."}, "public": map[string]any{"type": "boolean", "default": false}, "print_link": map[string]any{"type": "boolean", "default": false, "description": "Explicitly request the bearer link once; default output is masked."}}
			required = []string{"app", "for"}
			out["link"] = map[string]any{"type": []string{"string", "null"}}
			out["message"] = map[string]any{"type": "string"}
			out["edge_state"] = map[string]any{"type": "string"}
			outputRequired = []string{"grant", "link", "message", "edge_state"}
		}
		mcpToolDefinitions = append(mcpToolDefinitions, mcpToolDefinition{Name: name, Description: map[string]string{
			"guest_create": "Owner-only: create a time-limited browser guest grant for one proxy app and configure its guest gate and Funnel exposure locally. Returns grant metadata, a recipient message and configured edge state; the bearer link is hidden unless print_link is true and is only available at creation. Use guest_create for a new link; guest_show cannot recover an existing token. Requires public acknowledgement when first enabling the guest gate. No OIDC or tailnet account is required for the visitor. Makes no remote API calls; check status separately for live Funnel availability.",
			"guest_list":   "Owner-only: read all browser guest grants from local state, including expiry, revocation status and use counters. Returns a grants array without bearer tokens or secret hashes. Use guest_list to find grant IDs across apps; use guest_show to inspect one known ID. Read-only, with no remote API calls; does not verify live Funnel availability. Visitors need no OIDC or tailnet account.",
			"guest_show":   "Owner-only: look up one browser guest grant by ID and return its metadata, expiry, revocation status and use counters; unknown IDs return not_found. Use guest_show for one known grant; use guest_list to discover IDs. Read-only local state, with no remote API calls or bearer token recovery; create a new link with guest_create if needed. Does not verify live Funnel availability. Visitors need no OIDC or tailnet account.",
			"guest_revoke": "Owner-only: permanently mark one browser guest grant revoked in local state and return its grant metadata; unknown IDs return not_found. Use guest_revoke to deny further use of one link; guest_show only inspects it, and guest_create issues a new grant. Revocation cannot be undone with these tools and does not remove the app or disable its Funnel configuration. Returns no bearer tokens. Makes no remote API calls and does not verify live Funnel availability. Visitors need no OIDC or tailnet account.",
		}[name], InputSchema: objectSchema(properties, required...), OutputSchema: objectSchema(out, outputRequired...)})
	}
}
