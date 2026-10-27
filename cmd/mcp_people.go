package cmd

import (
	"encoding/json"
	"fmt"
	"unicode/utf8"
)

// Reject malformed wire strings before encoding/json can replace their bytes.
func decodePeopleMCPArguments(raw json.RawMessage, target any) error {
	if !utf8.ValidString(string(raw)) {
		return fmt.Errorf("person arguments must be valid UTF-8")
	}
	return decodeMCPArguments(raw, target)
}

func peopleViewSchema() map[string]any {
	grant := objectSchema(map[string]any{
		"app":        map[string]any{"type": "string"},
		"expires_at": map[string]any{"type": "string", "format": "date-time"},
		"expired":    map[string]any{"type": "boolean"},
		"active":     map[string]any{"type": "boolean"},
		"url":        map[string]any{"type": "string"},
	}, "app", "active")
	return objectSchema(map[string]any{
		"login": map[string]any{"type": "string"}, "revoked": map[string]any{"type": "boolean"},
		"grants": map[string]any{"type": "array", "items": grant},
		"invites": map[string]any{"type": "array", "items": objectSchema(map[string]any{
			"target_kind": map[string]any{"type": "string", "enum": []string{"app", "gateway"}}, "app": map[string]any{"type": "string"}, "hostname": map[string]any{"type": "string"}, "node_id": map[string]any{"type": "string"}, "id": map[string]any{"type": "string"}, "state": map[string]any{"type": "string"}, "attempt": map[string]any{"type": "integer", "minimum": 0},
		}, "app", "hostname", "node_id", "state")},
	}, "login", "revoked", "grants")
}

func peopleResultSchema() map[string]any {
	return objectSchema(map[string]any{
		"person":     peopleViewSchema(),
		"access":     map[string]any{"type": "array", "items": objectSchema(map[string]any{"app": map[string]any{"type": "string"}, "access_state": map[string]any{"type": "string"}, "url": map[string]any{"type": "string"}, "message": map[string]any{"type": "string"}}, "app", "access_state", "message")},
		"membership": map[string]any{"type": "string", "enum": []string{"member", "non_member"}},
		"qr_payload": map[string]any{"type": "string"}, "qr_warning": map[string]any{"type": "string"}, "warnings": stringArraySchema(), "guide": stringArraySchema(), "guide_zh": stringArraySchema(),
		"portal":             nestedObjectSchema("Independent home portal configuration and exact runtime address when available."),
		"complete":           map[string]any{"type": "boolean"},
		"message":            map[string]any{"type": "string"},
		"invite_requirement": map[string]any{"type": "string"},
		"invites": map[string]any{"type": "array", "items": objectSchema(map[string]any{
			"app": map[string]any{"type": "string"}, "id": map[string]any{"type": "string"},
			"invite_url": map[string]any{"type": "string"}, "code": map[string]any{"type": "string"},
			"remote_side_effect_plan": mcpRemoteSideEffectPlanSchema,
			"target_kind":             map[string]any{"type": "string", "enum": []string{"app", "gateway"}}, "state": map[string]any{"type": "string"}, "reconcile_ids": stringArraySchema(),
		}, "app")},
	}, "person", "complete", "message", "invite_requirement", "invites")
}

func init() {
	for i := range mcpToolDefinitions {
		d := &mcpToolDefinitions[i]
		if d.Name != "add" && d.Name != "share" {
			continue
		}
		props := d.InputSchema["properties"].(map[string]any)
		props["for"], props["until"] = allowLifetimeSchema(), map[string]any{"type": "string", "description": "Limit human allow recipients to this deadline; conflicts with for."}
		props["ack_never"], props["print_links"] = map[string]any{"type": "boolean"}, map[string]any{"type": "boolean"}
		props["allow"].(map[string]any)["description"] = "Human recipients use the common access action: membership verification, app grants and necessary invitations. A recipient named here keeps access without an expiry unless for or until limits it (people_add defaults to 24h); an explicit never requires ack_never, and a binding that cannot create permanent grants gets the 24h default instead. tag: entries remain machine ACLs. New installations use the gateway; existing installs preserve their mode. Refused for TCP and public Funnel."
		d.OutputSchema["properties"].(map[string]any)["access"] = map[string]any{"type": "array", "items": peopleResultSchema()}
	}

	for _, name := range []string{"people_add", "people_update"} {
		mcpToolHints[name] = mcpHints(false, true, false, true)
		mcpToolDefinitions = append(mcpToolDefinitions, mcpToolDefinition{
			Name: name,
			Description: map[string]string{
				"people_add":    "Grant private HTTP/file apps to a person, adding to existing grants and preserving omitted deadlines. New grants default to 24h; never requires ack_never for members and device-invited guests. Explicit add can restore a revoked person after cleanup. The common access action verifies network membership, assigns gateway routes when enabled and prepares necessary device invitations. Members need no invitation; outsiders accept one gateway invitation or one per app when the gateway is off. API/identity/runtime failures return incomplete with retained local state. Requires owner authority for new people, routes and shares. Confirm apps, recipients and lifetime. TCP/public apps are refused. Bearer links are hidden unless print_links is true. Unknown POST outcomes require reconciliation; retries never blindly resend. Existing explicit legacy rights are preserved; incompatible open legacy policies conflict.",
				"people_update": "Replace an existing person's app set when apps is supplied; omission preserves grants. Only explicit for/until changes existing deadlines. Uses the same membership, gateway routing and invitation completion as people_add. Retrying without lifetime changes resumes transport work. Removed apps remain denied. never requires ack_never; public/browser access stays finite. Requires owner authority for new routes or device sharing. Bearer links stay hidden unless print_links is true; invite is a compatibility field with no effect. Unknown POSTs require owner-verified reconcile_invites; replace_invites binds replacement to the recorded ID and preserves deadlines.",
			}[name],
			InputSchema: objectSchema(map[string]any{
				"who":               map[string]any{"type": "string", "minLength": 1},
				"apps":              map[string]any{"type": "array", "minItems": 1, "items": map[string]any{"type": "string"}, "description": "App names, or [all] for all current private HTTP/file apps. Required on add; omission on update keeps the app set."},
				"for":               lifetimeSchema(false),
				"until":             map[string]any{"type": "string", "description": "Absolute deadline (RFC3339, YYYY-MM-DD, YYYY-MM-DDTHH:MM); conflicts with for."},
				"ack_never":         map[string]any{"type": "boolean", "default": false},
				"invite":            map[string]any{"type": "boolean", "description": "Compatibility field; necessary invitations are automatic."},
				"print_links":       map[string]any{"type": "boolean", "default": false},
				"qr":                map[string]any{"type": "boolean", "default": false, "description": "Return QR payload text only; never image bytes."},
				"qr_invite":         map[string]any{"type": "string", "description": "App invitation QR or @gateway; requires qr and print_links. Treat as a credential."},
				"replace_invites":   map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}, "description": "Update without apps/for: owner-confirmed app or @gateway to recorded old invite ID. Requires remote absence for the same NodeID. A stale gateway invitation can be retired as target_gone when current gateway ownership is proven, including accepted shares; preserves grants and deadlines."},
				"reconcile_invites": map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}, "description": "Update: owner-verified app or @gateway to invite ID mapping, or none after checking absence. Unknown POSTs cannot be retried blindly."},
			}, "who"), OutputSchema: peopleResultSchema(),
		})
		input := mcpToolDefinitions[len(mcpToolDefinitions)-1].InputSchema
		if name == "people_add" {
			delete(input["properties"].(map[string]any), "reconcile_invites")
			delete(input["properties"].(map[string]any), "replace_invites")
			input["required"] = []string{"who", "apps"}
		}

	}
	mcpToolHints["people_list"] = mcpHints(true, false, true, false)
	mcpToolDefinitions = append(mcpToolDefinitions, mcpToolDefinition{Name: "people_list", Description: "Read people, their app grants, absolute deadlines and revocation tombstones from local files. No credential is needed; sends no invitations and writes nothing.", InputSchema: objectSchema(map[string]any{}), OutputSchema: objectSchema(map[string]any{"people": map[string]any{"type": "array", "items": peopleViewSchema()}}, "people")})
	mcpToolHints["people_remove"] = mcpHints(false, true, true, true)
	mcpToolDefinitions = append(mcpToolDefinitions, mcpToolDefinition{Name: "people_remove", Description: "Revokes a person locally across private HTTP/file services before cleaning up pending device invitations, so confirm with the user before calling. Retains a deny tombstone. Local denial needs no token; remote cleanup needs a user-owned API token and reports deferred or partial work in complete/cleanup. Retry resumes cleanup. Unknown POSTs require explicit owner-verified reconcile_invites app=id or app=none, with remote listing. Accepted network shares may remain. TCP/Funnel are outside person enforcement; in-flight streams finish.", InputSchema: objectSchema(map[string]any{"who": map[string]any{"type": "string", "minLength": 1}, "reconcile_invites": map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}}}, "who"), OutputSchema: objectSchema(map[string]any{"login": map[string]any{"type": "string"}, "removed": map[string]any{"type": "boolean"}, "revoked": map[string]any{"type": "boolean"}, "complete": map[string]any{"type": "boolean"}, "cleanup": peopleResultSchema()["properties"].(map[string]any)["invites"]}, "login", "removed", "revoked", "complete", "cleanup")})
}
