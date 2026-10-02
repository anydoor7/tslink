package cmd

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
			"app": map[string]any{"type": "string"}, "hostname": map[string]any{"type": "string"}, "node_id": map[string]any{"type": "string"}, "id": map[string]any{"type": "string"}, "state": map[string]any{"type": "string"}, "attempt": map[string]any{"type": "integer", "minimum": 0},
		}, "app", "hostname", "node_id", "state")},
	}, "login", "revoked", "grants")
}

func peopleResultSchema() map[string]any {
	return objectSchema(map[string]any{
		"person":             peopleViewSchema(),
		"complete":           map[string]any{"type": "boolean"},
		"message":            map[string]any{"type": "string"},
		"invite_requirement": map[string]any{"type": "string"},
		"invites": map[string]any{"type": "array", "items": objectSchema(map[string]any{
			"app": map[string]any{"type": "string"}, "id": map[string]any{"type": "string"},
			"invite_url": map[string]any{"type": "string"}, "code": map[string]any{"type": "string"},
			"remote_side_effect_plan": mcpRemoteSideEffectPlanSchema,
			"state":                   map[string]any{"type": "string"}, "reconcile_ids": stringArraySchema(),
		}, "app")},
	}, "person", "complete", "message", "invite_requirement", "invites")
}

func init() {
	for _, name := range []string{"people_add", "people_update"} {
		mcpToolHints[name] = mcpHints(false, true, false, true)
		mcpToolDefinitions = append(mcpToolDefinitions, mcpToolDefinition{
			Name:        name,
			Description: "Changes local access to private HTTP and file apps for a person, so confirm the person, apps and expiry with the user before calling. This makes newly scoped apps deny callers without a grant or an explicit legacy allow rule. TCP cannot enforce people; public Funnel is refused. Optional invite creates single-use per-app device invitations bundled in one guide, so confirm before creating them. Requires a user-owned API token only for invite; OAuth cannot create device invites. print_links explicitly reveals bearer links. No elevated exit-node, multi-use or tailnet-role permissions are offered; use the existing invite tools and their owner-configured elevated permission guard for those. An incomplete invitation result retains local grants and reports durable state/errors. Update with invite alone resumes unfinished work; unknown POSTs require explicit owner-verified reconcile_invites after remote listing. Completed operations are reused. There is no exactly-once guarantee. Logins accept all nonempty strings without controls or internal whitespace. Trim outer ASCII whitespace and lowercase ASCII A-Z only; compare exact bytes without Unicode folding or normalization. replace_invites explicitly confirms replacement of a disappeared completed ID, retires its attempt, and preserves all grants/deadlines; requires update with invite and no apps/for changes.",
			InputSchema: objectSchema(map[string]any{
				"who":               map[string]any{"type": "string", "minLength": 1},
				"apps":              map[string]any{"type": "array", "minItems": 1, "items": map[string]any{"type": "string"}, "description": "App names, or [all] for all current private HTTP/file apps. Required on add; omission on update keeps the app set."},
				"for":               map[string]any{"type": "string", "description": "Positive duration, e.g. 1h or 7d, or never. Omit on update to preserve expiry."},
				"invite":            map[string]any{"type": "boolean", "default": false},
				"print_links":       map[string]any{"type": "boolean", "default": false},
				"replace_invites":   map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}, "description": "Update with invite only, no apps/for: owner-confirmed app to recorded old invite ID. Requires remote absence before replacement; preserves grants and deadlines."},
				"reconcile_invites": map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}, "description": "Update --invite only: owner-verified app to invite ID mapping, or none after checking absence. Unknown POSTs cannot be retried blindly."},
			}, "who"), OutputSchema: peopleResultSchema(),
		})
		input := mcpToolDefinitions[len(mcpToolDefinitions)-1].InputSchema
		if name == "people_add" {
			delete(input["properties"].(map[string]any), "reconcile_invites")
			delete(input["properties"].(map[string]any), "replace_invites")
			input["required"] = []string{"who", "apps"}
		}
		if name == "people_update" {
			input["anyOf"] = []any{map[string]any{"required": []string{"apps"}}, map[string]any{"required": []string{"for"}}, map[string]any{"required": []string{"invite"}, "properties": map[string]any{"invite": map[string]any{"const": true}}}}
		}
	}
	mcpToolHints["people_list"] = mcpHints(true, false, true, false)
	mcpToolDefinitions = append(mcpToolDefinitions, mcpToolDefinition{Name: "people_list", Description: "Read people, their app grants, absolute deadlines and revocation tombstones from local files. No credential is needed; sends no invitations and writes nothing.", InputSchema: objectSchema(map[string]any{}), OutputSchema: objectSchema(map[string]any{"people": map[string]any{"type": "array", "items": peopleViewSchema()}}, "people")})
	mcpToolHints["people_remove"] = mcpHints(false, true, true, true)
	mcpToolDefinitions = append(mcpToolDefinitions, mcpToolDefinition{Name: "people_remove", Description: "Revokes a person locally across private HTTP/file services before cleaning up pending device invitations, so confirm with the user before calling. Retains a deny tombstone. Local denial needs no token; remote cleanup needs a user-owned API token and reports deferred or partial work in complete/cleanup. Retry resumes cleanup. Unknown POSTs require explicit owner-verified reconcile_invites app=id or app=none, with remote listing. Accepted network shares may remain. TCP/Funnel are outside person enforcement; in-flight streams finish.", InputSchema: objectSchema(map[string]any{"who": map[string]any{"type": "string", "minLength": 1}, "reconcile_invites": map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}}}, "who"), OutputSchema: objectSchema(map[string]any{"login": map[string]any{"type": "string"}, "removed": map[string]any{"type": "boolean"}, "revoked": map[string]any{"type": "boolean"}, "complete": map[string]any{"type": "boolean"}, "cleanup": peopleResultSchema()["properties"].(map[string]any)["invites"]}, "login", "removed", "revoked", "complete", "cleanup")})
}
