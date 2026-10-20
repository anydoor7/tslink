package cmd

import (
	"context"
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func recipeInputSchema() map[string]any {
	return objectSchema(map[string]any{
		"health":              healthInputSchema(),
		"request_limits":      mcpRequestLimitsInputSchema,
		"recipe_id":           map[string]any{"type": "string", "description": "Recipe ID from recipe_list."},
		"name":                map[string]any{"type": "string", "description": "Optional service name override."},
		"target":              map[string]any{"type": "string", "description": "Optional loopback HTTP(S) target override; use the host port."},
		"allow":               map[string]any{"type": "string", "description": "Comma-separated private HTTP identities."},
		"tags":                map[string]any{"type": "string", "description": "Comma-separated tags."},
		"preserve_host":       map[string]any{"type": "boolean", "description": "Override the recipe Host forwarding default; omitted uses recipe.preserve_host. False explicitly restores upstream Host rewriting."},
		"ephemeral":           map[string]any{"type": "boolean"},
		"funnel":              map[string]any{"type": "boolean", "description": "PUBLIC INTERNET exposure: review app authentication with the owner first."},
		"public_ack":          map[string]any{"type": "boolean", "description": "Required acknowledgement when funnel is true."},
		"funnel_ttl":          lifetimeSchema(true),
		"no_auto_provision":   map[string]any{"type": "boolean"},
		"no_daemon_install":   map[string]any{"type": "boolean"},
		"control_url":         map[string]any{"type": "string"},
		"force_unsafe_public": map[string]any{"type": "boolean", "description": "DANGER: overrides never-public recipe policy, potentially exposing host control, code execution or secrets to everyone. Requires funnel and public_ack."},
	}, "recipe_id")
}

func recipeCatalogSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"preserve_host": map[string]any{"type": "boolean", "description": "Default Host forwarding policy for this recipe."},
		},
	}
}

func recipeOutputSchema() map[string]any {
	return objectSchema(map[string]any{
		"schema_version": map[string]any{"type": "integer"}, "catalog_version": map[string]any{"type": "integer"},
		"recipe":    recipeCatalogSchema(),
		"service":   mcpServiceViewSchema,
		"requested": mcpServiceViewSchema,
		"action":    map[string]any{"type": "string", "enum": []string{templateActionCreate, templateActionCreated, templateActionSkipExisting}},
		"dry_run":   map[string]any{"type": "boolean"}, "applied": map[string]any{"type": "boolean"},
		"warnings": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		"next":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
	}, "schema_version", "catalog_version", "recipe", "service", "requested", "action", "dry_run", "applied", "warnings", "next")
}
func recipeApplyOutputSchema() map[string]any {
	s := recipeOutputSchema()
	s["properties"].(map[string]any)["daemon_installed"] = mcpDaemonInstalledSchema
	return s
}
func init() {
	mcpToolHints["apps_detect"] = mcpHints(true, false, true, false)
	mcpToolHints["recipe_list"] = mcpHints(true, false, true, false)
	mcpToolHints["recipe_plan"] = mcpHints(true, false, true, false)
	mcpToolHints["recipe_apply"] = mcpHints(false, false, true, true)
	mcpToolDefinitions = append(mcpToolDefinitions,
		mcpToolDefinition{Name: "apps_detect", Description: "Discover local listening TCP services using credential-free, bounded loopback HTTP fingerprints. Never follows redirects or probes non-loopback addresses. Returns confidence and already registered services; does not verify authentication or health.", InputSchema: objectSchema(map[string]any{}), OutputSchema: objectSchema(map[string]any{
			"schema_version": map[string]any{"type": "integer"},
			"listeners":      map[string]any{"type": "array", "items": nestedObjectSchema("Loopback host and port.")},
			"matches":        map[string]any{"type": "array", "items": nestedObjectSchema("Recipe, confidence, fingerprint evidence and registered names.")},
			"complete":       map[string]any{"type": "boolean"},
			"warnings":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		}, "schema_version", "listeners", "matches", "complete", "warnings")},
		mcpToolDefinition{Name: "recipe_list", Description: "Return the versioned catalog of app-specific recipes, including IDs, ports, WebSocket requirements, health path defaults for new services, app-side snippets, safety levels and dated official documentation. Use recipe_list to choose an app recipe before recipe_plan; template_list offers fixed multi-service stacks instead. Reads the built-in catalog only, without probing apps or changing the registry.", InputSchema: objectSchema(map[string]any{}), OutputSchema: objectSchema(map[string]any{
			"schema_version": map[string]any{"type": "integer"}, "catalog_version": map[string]any{"type": "integer"},
			"recipes": map[string]any{"type": "array", "items": recipeCatalogSchema()},
		}, "schema_version", "catalog_version", "recipes")},
		mcpToolDefinition{Name: "recipe_plan", Description: "Preview one app-specific recipe with the supplied overrides; return requested and effective service configurations, a create-or-skip action, owner configuration advice and safety warnings. Use recipe_plan before recipe_apply to review without writes; template_plan previews a fixed multi-service stack instead. Does not probe the app or prove its authentication.", InputSchema: recipeInputSchema(), OutputSchema: recipeOutputSchema()},
		mcpToolDefinition{Name: "recipe_apply", Description: "Register one app-specific recipe with the supplied overrides, then ensure the background service unless no_daemon_install is true. Returns the created or preserved service, requested configuration, action and safety advice. Use recipe_apply after reviewing recipe_plan; template_apply registers a fixed multi-service stack instead. Leaves existing names unchanged. Never-public apps refuse Funnel unless the owner explicitly accepts force_unsafe_public and public_ack. Does not configure, install or probe the third-party app.", InputSchema: recipeInputSchema(), OutputSchema: recipeApplyOutputSchema()},
	)
}

func callRecipeMCPTool(ctx context.Context, actions mcpActions, name string, arguments json.RawMessage) (any, *mcp.CallToolResult, error) {
	switch name {
	case "apps_detect", "recipe_list":
		var args struct{}
		if refusal := mcpArgumentsRefusal(name, decodeMCPArguments(arguments, &args)); refusal != nil {
			return nil, refusal, nil
		}
		if name == "apps_detect" {
			data, err := actions.appsDetect(ctx)
			return data, nil, err
		}
		data, err := actions.recipeList()
		return data, nil, err
	default:
		var args recipeRequest
		decodeErr := decodeMCPArguments(arguments, &args)
		if refusal := mcpArgumentsRefusal(name, decodeErr, mcpRequiredArgument{"recipe_id", args.RecipeID}); refusal != nil {
			return nil, refusal, nil
		}
		data, err := actions.recipeApply(ctx, args, name == "recipe_plan")
		return data, nil, err
	}
}
