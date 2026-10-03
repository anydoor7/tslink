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
		mcpToolDefinitions = append(mcpToolDefinitions, mcpToolDefinition{Name: name, Description: "Owner-only. " + name + " browser guest grants for one app. Uses local state only; Funnel availability is reported separately. Tokens are shown only when explicitly requested; no OIDC or tailnet account is required.", InputSchema: objectSchema(properties, required...), OutputSchema: objectSchema(out, outputRequired...)})
	}
}
