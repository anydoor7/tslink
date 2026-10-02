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
		description += " New grants default to 24h; update omission preserves deadlines. Never requires ack_never and a tailnet-member login; device-invited guests have the configured public maximum."
	}
	return map[string]any{"type": "string", "description": description, "examples": []string{"1h", "8h", "24h", "3d", "7d", "90m", "1w", "1d12h", "until 2030-06-01T18:00:00Z"}}
}

func init() {
	mcpToolHints["extend"] = mcpHints(false, true, false, false)
	mcpToolDefinitions = append(mcpToolDefinitions, mcpToolDefinition{
		Name: "extend", Description: "Set a new person grant or Funnel deadline from the operation time, extending or shortening it. Confirm the target and duration with the owner. who selects a person grant for service; omit for Funnel. Expired grants require explicit regrant; revoked people cannot be revived. Changes local registry only; no invites, live tailnet call or access-log dependency.",
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
