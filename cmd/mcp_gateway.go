package cmd

import (
	"context"

	"github.com/anydoor7/tslink/internal/registry"
	tsRuntime "github.com/anydoor7/tslink/internal/runtime"
)

type portalRouteArguments struct {
	App              string `json:"app"`
	Port             int    `json:"port,omitempty"`
	AllowCrossOrigin bool   `json:"allow_cross_origin,omitempty"`
}

type portalRoutesResult struct {
	Portal tsRuntime.PortalState         `json:"portal"`
	Routes []tsRuntime.GatewayRouteState `json:"routes"`
}

func portalRoutes(paths sharePaths) (portalRoutesResult, error) {
	reg, err := registry.Load(paths.Registry)
	if err != nil {
		return portalRoutesResult{}, err
	}
	pid, _ := inviteReadPIDFn(paths.PID)
	view := readPortalView(reg, paths.Registry, inviteIsRunningFn(paths.PID), pid)
	routes := []tsRuntime.GatewayRouteState{}
	if view.Gateway != nil {
		routes = view.Gateway.Routes
	}
	return portalRoutesResult{Portal: view, Routes: routes}, nil
}

func changePortalRoute(ctx context.Context, paths sharePaths, args portalRouteArguments, remove bool) (portalRoutesResult, error) {
	route := &registry.PortalRoute{Port: args.Port, AllowCrossOrigin: args.AllowCrossOrigin}
	if remove {
		route = nil
	}
	if err := registry.SetPortalRouteAuthorized(ctx, paths.Registry, args.App, route, func(reg *registry.Registry) error { return requireRequestOwnerInRegistry(ctx, reg) }); err != nil {
		return portalRoutesResult{}, err
	}
	return portalRoutes(paths)
}

func gatewayRouteSchema() map[string]any {
	return objectSchema(map[string]any{
		"app": map[string]any{"type": "string"}, "port": map[string]any{"type": "integer"},
		"url": map[string]any{"type": "string"}, "state": map[string]any{"type": "string"},
		"reason": map[string]any{"type": "string"}, "host_label": map[string]any{"type": "string"},
		"health_checked": map[string]any{"type": "string"}, "health": map[string]any{"type": "string"}, "offline_since": map[string]any{"type": "string"},
	}, "app", "port", "state", "host_label")
}

func gatewaySchema() map[string]any {
	return objectSchema(map[string]any{
		"enabled": map[string]any{"type": "boolean"}, "persistent": map[string]any{"type": "boolean"},
		"tagged": map[string]any{"type": "boolean"}, "node_id": map[string]any{"type": "string"},
		"ownership_proven": map[string]any{"type": "boolean"}, "identity_changed": map[string]any{"type": "boolean"},
		"dropped": map[string]any{"type": "integer", "minimum": 0},
		"ports":   objectSchema(map[string]any{"from": map[string]any{"type": "integer"}, "to": map[string]any{"type": "integer"}}, "from", "to"),
		"routes":  map[string]any{"type": "array", "items": gatewayRouteSchema()},
	}, "enabled", "persistent", "tagged", "ownership_proven", "dropped", "ports", "routes")
}

func init() {
	for _, name := range []string{"portal_route_add", "portal_route_remove"} {
		mcpToolHints[name] = mcpHints(false, name == "portal_route_remove", true, false)
	}
	mcpToolHints["portal_route_list"] = mcpHints(true, false, true, false)
	out := objectSchema(map[string]any{"portal": portalSchema(), "routes": map[string]any{"type": "array", "items": gatewayRouteSchema()}}, "portal", "routes")
	mcpToolDefinitions = append(mcpToolDefinitions,
		mcpToolDefinition{Name: "portal_route_add", Description: "Route a private HTTP or file app on a stable gateway port. Makes the app people-scoped. HTTP MCP requires the current portal owner, checked in the write transaction. The same-origin gate defaults on. Does not invite anyone or restart the gateway node.", InputSchema: objectSchema(map[string]any{"app": map[string]any{"type": "string", "minLength": 1}, "port": map[string]any{"type": "integer", "minimum": 1, "maximum": 65535}, "allow_cross_origin": map[string]any{"type": "boolean", "description": "Allow browser calls from other origins; default false."}}, "app"), OutputSchema: out},
		mcpToolDefinition{Name: "portal_route_remove", Description: "Remove an app's gateway route. Refuses portal-only apps; remove the app instead. HTTP MCP requires the current portal owner in the write transaction.", InputSchema: objectSchema(map[string]any{"app": map[string]any{"type": "string", "minLength": 1}}, "app"), OutputSchema: out},
		mcpToolDefinition{Name: "portal_route_list", Description: "List configured gateway routes and exact URLs when current runtime evidence proves them. Read-only; no enrollment or remote API calls.", InputSchema: objectSchema(map[string]any{}), OutputSchema: out},
	)
}
