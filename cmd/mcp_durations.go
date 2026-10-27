package cmd

import (
	"encoding/json"
	"fmt"
	"github.com/anydoor7/tslink/internal/duration"
)

func decodeExtendMCPArguments(raw json.RawMessage, args *extendArguments) error {
	if err := decodePeopleMCPArguments(raw, args); err != nil {
		return err
	}
	if args.Who == "" {
		var fields map[string]json.RawMessage
		// The preceding strict decode already proved the JSON shape.
		_ = json.Unmarshal(raw, &fields)
		if _, present := fields["who"]; present {
			return fmt.Errorf("who must be a nonempty login; omit who to select Funnel")
		}
	}
	return nil
}

func lifetimeSchema(public bool) map[string]any {
	description := "Relative lifetime (descending unique units w,d,h,m,s,ms,us,ns) or until <RFC3339|YYYY-MM-DD|YYYY-MM-DDTHH:MM>. Local unless offset given; DST gaps/folds require an offset. Minimum 1h. Presets: " + duration.Suggestions + "."
	if public {
		description += " Default 24h; guest/public maximum defaults to 7d, configurable in durations.public_max. Never refused. Omission on add preserves an existing deadline."
	} else {
		description += " New grants default to 24h; update omission preserves deadlines. Never requires ack_never for members and device-invited guests; finite guest grants retain the configured maximum."
	}
	return map[string]any{"type": "string", "description": description, "examples": []string{"1h", "8h", "24h", "3d", "7d", "90m", "1w", "1d12h", "until 2030-06-01T18:00:00Z"}}
}

// allowLifetimeSchema is the lifetime of add/share human allow recipients.
// Unlike people_add they have no 24h default: an allow list never expired.
func allowLifetimeSchema() map[string]any {
	schema := lifetimeSchema(false)
	description := "Limit human allow recipients to this lifetime (descending unique units w,d,h,m,s,ms,us,ns) or until <RFC3339|YYYY-MM-DD|YYYY-MM-DDTHH:MM>. Local unless offset given; DST gaps/folds require an offset. Minimum 1h. Presets: " + duration.Suggestions + ". Omitted: a recipient this call newly allows has no expiry and an existing grant keeps its deadline; a binding that expires or cannot create permanent grants gets 24h. Explicit never requires ack_never for members and device-invited guests; finite guest grants retain the configured maximum."
	schema["description"] = description
	return schema
}

func init() {
	mcpToolHints["extend"] = mcpHints(false, true, false, false)
	mcpToolDefinitions = append(mcpToolDefinitions, mcpToolDefinition{
		Name: "extend", Description: "Reset an existing person-app grant or acknowledged Funnel deadline using for from the operation time or an absolute until, extending or shortening access. Returns previous and new deadlines, audience, change time and whether expired access was regranted. Use extend to change an existing deadline; use people_grant to create a person-app grant. who selects the person grant for service; omit who for Funnel, which is owner-only. Confirm target and duration with the owner. Expired access requires regrant=true; revoked people cannot be revived. For Funnel, regrant=true re-enables public internet exposure of an expired Funnel. Changes local state only; no invitations, live tailnet calls or access-log dependency.",
		InputSchema: objectSchema(map[string]any{
			"service":   map[string]any{"type": "string", "minLength": 1},
			"who":       map[string]any{"type": "string", "minLength": 1},
			"for":       lifetimeSchema(false),
			"until":     map[string]any{"type": "string", "description": "RFC3339, YYYY-MM-DD or YYYY-MM-DDTHH:MM; local without offset. Mutually exclusive with for."},
			"regrant":   map[string]any{"type": "boolean", "default": false},
			"ack_never": map[string]any{"type": "boolean", "default": false},
		}, "service"),
		OutputSchema: objectSchema(map[string]any{
			"service": map[string]any{"type": "string"}, "who": map[string]any{"type": "string"},
			"audience":            map[string]any{"type": "string", "enum": []string{"tailnet_member", "guest", "public"}},
			"previous_expires_at": map[string]any{"type": []string{"string", "null"}, "format": "date-time"},
			"expires_at":          map[string]any{"type": []string{"string", "null"}, "format": "date-time"},
			"regranted":           map[string]any{"type": "boolean"}, "changed_at": map[string]any{"type": "string", "format": "date-time"},
		}, "service", "audience", "previous_expires_at", "expires_at", "regranted", "changed_at"),
	})
	mcpToolDefinitions[len(mcpToolDefinitions)-1].InputSchema["oneOf"] = []any{map[string]any{"required": []string{"for"}}, map[string]any{"required": []string{"until"}}}
}
