package cmd

import (
	"context"

	"github.com/anydoor7/tslink/internal/mcpscope"
	"github.com/anydoor7/tslink/internal/registry"
	tsRuntime "github.com/anydoor7/tslink/internal/runtime"
	"github.com/anydoor7/tslink/internal/server"
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
		mcpToolDefinition{Name: "portal_route_list", Description: "Read the home portal and its configured gateway routes, returning app names, stable ports, route states and exact URLs only when current runtime evidence proves them. Use portal_route_list to inspect existing routes; portal_route_add and portal_route_remove change routing. Reads local configuration and runtime evidence only; no writes, enrollment or remote API calls.", InputSchema: objectSchema(map[string]any{}), OutputSchema: out},
	)
}

// authorizePortalServiceMutation is the early, explicit form of the portal
// owner rule for the call sites that act before they write (a route removal,
// a node retirement, a URL wait). It covers a routed app (the gateway rule)
// and a publicly exposed one (the public rule). add also preserves existing
// routes when replacing a service, so callers authorize the effective
// service, not just explicit route flags in the incoming request. The
// authority for public state is the guard in save; this check only answers
// earlier.
// mcpPublicOwnerNote ends the description of every tool the public rule
// covers, so a model reads the rule where it chooses the tool.
const mcpPublicOwnerNote = " Over HTTP MCP, a change to a public app (open Funnel, a lapsed Funnel that keeps its public acknowledgement, or guest links), including one that makes an app public or removes it, needs the current portal owner when one is configured or a home gateway is on, checked in the write transaction (access_request_owner_required)."

func authorizePortalServiceMutation(ctx context.Context, reg *registry.Registry, services ...registry.Service) error {
	for _, svc := range services {
		if svc.PortalRoute != nil {
			if err := requireRequestOwnerInRegistry(ctx, reg); err != nil {
				return err
			}
		}
		if svc.PubliclyExposed() {
			if err := requirePortalOwnerForPublicChange(ctx, reg); err != nil {
				return err
			}
		}
	}
	return nil
}

// portalOwnerGovernsPublicChanges reports whether a portal owner is the
// authority for public changes. It is configured once an owner is recorded
// (disabling the portal keeps it; only the current owner can change it
// remotely and nothing remote clears it), and on a home
// gateway before its first owner is bootstrapped, where the gateway rule is
// already fail-closed. A legacy install with no portal owner and no gateway has
// no higher authority than the MCP owner role, which keeps governing.
func portalOwnerGovernsPublicChanges(reg *registry.Registry) bool {
	return reg != nil && reg.Portal != nil && (reg.Portal.Owner != "" || registry.GatewayEnabled(reg))
}

// requirePortalOwnerForPublicChange is the public rule: over remote HTTP MCP,
// a change to a publicly exposed app, or one that makes an app public, needs
// the current, untagged, unrevoked portal owner of reg, the registry as
// loaded under the write lock. Local CLI and stdio MCP callers have no remote
// caller in their context and are unaffected.
func requirePortalOwnerForPublicChange(ctx context.Context, reg *registry.Registry) error {
	if _, remote := server.MCPCallerFromContext(ctx); !remote || !portalOwnerGovernsPublicChanges(reg) {
		return nil
	}
	if err := requireRequestOwnerInRegistry(ctx, reg); err != nil {
		return publicChangeOwnerRequired()
	}
	return nil
}

func publicChangeOwnerRequired() error {
	return registry.CodedError{
		Code:    "access_request_owner_required",
		Message: "changes to a public app (Funnel, guest links, or a recorded public acknowledgement) over remote MCP require the current portal owner; nothing was changed",
		Next:    []string{"Run the change as the portal owner, or with the local tslink CLI or stdio MCP on the gateway host"},
	}
}

// withPublicMutationGuard installs the public rule as the registry write
// guard for a remote MCP call: every write the tool performs is checked under
// the lock, against the registry loaded under that lock, before it reaches
// disk, whichever registry helper performs it.
func withPublicMutationGuard(ctx context.Context) context.Context {
	if _, remote := server.MCPCallerFromContext(ctx); !remote {
		return ctx
	}
	caller := ctx
	return registry.WithPublicMutationGuard(ctx, func(before, _ *registry.Registry, change registry.GuardedChange) error {
		// The gateway rule, on every door that registers a route (add,
		// template_apply, share, people grants that route an app): route
		// creation needs the portal owner, fail-closed while none is set.
		if change.RouteAdded {
			if err := requireRequestOwnerInRegistry(caller, before); err != nil {
				return registry.CodedError{Code: "access_request_owner_required", Message: "creating a home gateway route over remote MCP requires the current portal owner; nothing was changed", Next: []string{"Run the change as the portal owner, or with the local tslink CLI or stdio MCP on the gateway host"}}
			}
		}
		if !change.Public {
			return nil
		}
		// Pulling a link only reduces exposure, and a leaked link must be
		// pullable by the operator on call: an MCP owner-role caller may
		// revoke existing guest grants when that is the whole public change.
		if change.OnlyRevokesGuests && mcpOwnerRole(caller) {
			return nil
		}
		return requirePortalOwnerForPublicChange(caller, before)
	})
}

func mcpOwnerRole(ctx context.Context) bool {
	session, ok := mcpscope.FromContext(ctx)
	return ok && session.Scope.Role == "owner"
}
