package cmd

import (
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/anydoor7/tslink/internal/inspect"
	"github.com/anydoor7/tslink/internal/output"
	"github.com/anydoor7/tslink/internal/registry"
	tsRuntime "github.com/anydoor7/tslink/internal/runtime"
	"github.com/spf13/cobra"
)

func gatewayRouteView(p tsRuntime.PortalState, app string) (tsRuntime.GatewayRouteState, bool) {
	if p.Gateway != nil {
		for _, r := range p.Gateway.Routes {
			if r.App == app {
				return r, true
			}
		}
	}
	return tsRuntime.GatewayRouteState{}, false
}

func applyGatewayStatus(s *StatusResult) {
	for i := range s.Services {
		svc := &s.Services[i]
		route, ok := gatewayRouteView(s.Portal, svc.Name)
		if !ok {
			continue
		}
		svc.PortalURL = route.URL
		if svc.PortalOnly && svc.Error == nil {
			svc.Status = "down"
			if route.State == "running" && route.URL != "" {
				svc.Status = "up"
			}
			if route.State == "failed" {
				svc.Status = "failed"
				svc.Error = &tsRuntime.ServiceError{Code: route.Reason, Message: "Gateway route unavailable"}
			}
		}
	}
}

func applyGatewayServiceView(svc *StatusServiceView, p tsRuntime.PortalState) {
	route, ok := gatewayRouteView(p, svc.Name)
	if !ok {
		return
	}
	svc.PortalURL = route.URL
	if !svc.PortalOnly {
		return
	}
	svc.RuntimeState = route.State
	svc.Endpoint = inspect.EndpointView{Kind: inspect.EndpointKindHTTPS, State: statusEndpointStateMissing}
	if route.State == "running" && route.URL != "" {
		u, err := url.Parse(route.URL)
		if err == nil {
			svc.Endpoint = inspect.EndpointView{Kind: inspect.EndpointKindHTTPS, State: inspect.EndpointStateExact, Display: route.URL, Host: u.Hostname(), Port: route.Port}
		}
	}
	if route.State == "failed" {
		svc.Error = &tsRuntime.ServiceError{Code: route.Reason, Message: "Gateway route unavailable"}
	}
	// Portal-only apps deliberately have no per-app runtime node row.
	var warnings []inspect.WarningView
	for _, w := range svc.Warnings {
		if w.Message != "Runtime snapshot is exact but does not include this registered service." {
			warnings = append(warnings, w)
		}
	}
	svc.Warnings = warnings
}

func diagnoseGateway(result *DoctorResult) {
	g := result.Portal.Gateway
	if g == nil || !g.Enabled {
		return
	}
	if g.IdentityChanged {
		result.addFinding("gateway_node_identity_changed", "", "portal", "Gateway NodeID changed; accepted shares still target the previous device.", nil)
	}
	if (result.Portal.State == "running" || result.Portal.Error == "gateway_ephemeral_refused") && !g.Persistent {
		result.addFinding("gateway_ephemeral_refused", "", "portal", "Gateway must use a persistent node.", nil)
	}
	for _, route := range g.Routes {
		if route.Reason != "" {
			result.addFinding(route.Reason, route.App, "portal", "Gateway route is unavailable.", nil)
		}
		if route.Health == "down" {
			result.addFinding("upstream_unreachable", route.App, "portal", "App upstream is not reachable.", nil)
		}
	}
}

func parseGatewayArguments(args portalArguments, prior ...*registry.GatewayConfig) (*registry.GatewayConfig, error) {
	if !args.Gateway && (args.GatewayPortsSet || args.GatewayTagSet || args.GatewayPorts != "" || args.GatewayTag != "") {
		return nil, registry.CodedError{Code: "gateway_required", Message: "gateway options require --gateway"}
	}
	var g *registry.GatewayConfig
	if len(prior) > 0 && prior[0] != nil {
		copy := *prior[0]
		g = &copy
	}
	if args.GatewaySet && !args.Gateway {
		if args.GatewayPorts != "" || args.GatewayTag != "" {
			return nil, registry.CodedError{Code: "gateway_required", Message: "gateway options require gateway mode"}
		}
		return nil, nil // final registry validation refuses this while routes exist
	}
	if args.Gateway {
		if g == nil {
			g = &registry.GatewayConfig{Ports: registry.DefaultGatewayPorts()}
		}
		g.Enabled = true
	}
	if g == nil || !g.Enabled {
		if args.GatewayPorts != "" || args.GatewayTag != "" || args.GatewayPortsSet || args.GatewayTagSet {
			return nil, registry.CodedError{Code: "gateway_required", Message: "gateway options require gateway mode"}
		}
		return g, nil
	}
	if args.GatewayPortsSet || args.GatewayPorts != "" {
		if args.GatewayPorts == "" {
			g.Ports = registry.DefaultGatewayPorts()
		} else {
			from, to, ok := strings.Cut(args.GatewayPorts, "-")
			var e1, e2 error
			g.Ports.From, e1 = strconv.Atoi(from)
			g.Ports.To, e2 = strconv.Atoi(to)
			if !ok || e1 != nil || e2 != nil {
				return nil, registry.CodedError{Code: "gateway_ports_invalid", Message: "use a range such as 8443-8499"}
			}
		}
	}
	if args.GatewayTagSet || args.GatewayTag != "" {
		g.Tag = args.GatewayTag
	}
	return g, registry.ValidateGatewayConfig(g)
}

func configuredGatewayView(reg *registry.Registry) *tsRuntime.GatewayState {
	if reg == nil || reg.Portal == nil || reg.Portal.Gateway == nil {
		return nil
	}
	g := reg.Portal.Gateway
	view := &tsRuntime.GatewayState{Enabled: g.Enabled, Ports: g.Ports, Routes: []tsRuntime.GatewayRouteState{}}
	for _, svc := range reg.Services {
		if svc.PortalRoute != nil {
			state := "pending"
			if !reg.Portal.Enabled {
				state = "disabled"
			}
			view.Routes = append(view.Routes, tsRuntime.GatewayRouteState{App: svc.Name, Port: svc.PortalRoute.Port, State: state, HostLabel: "this host"})
		}
	}
	sort.Slice(view.Routes, func(i, j int) bool { return view.Routes[i].App < view.Routes[j].App })
	return view
}

func newPortalRouteCmd() *cobra.Command {
	group := &cobra.Command{Use: "route", Short: "Manage app ports on the portal gateway", RunE: runCommandGroup}
	var port int
	var cross bool
	add := &cobra.Command{Use: "add <app>", Short: "Route a private HTTP or file app through the gateway", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
		return runPortalRouteChange(c, args[0], &registry.PortalRoute{Port: port, AllowCrossOrigin: cross})
	}}
	add.Flags().IntVar(&port, "port", 0, "Stable TLS port; default is the lowest available port")
	add.Flags().BoolVar(&cross, "allow-cross-origin", false, "Allow browser requests from other origins to this app")
	remove := &cobra.Command{Use: "remove <app>", Short: "Remove a route while keeping the app's own node", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error { return runPortalRouteChange(c, args[0], nil) }}
	list := &cobra.Command{Use: "list", Short: "List gateway routes and observed URLs", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error { return writePortalRoutes(c, "portal route list") }}
	group.AddCommand(add, remove, list)
	return group
}

func runPortalRouteChange(c *cobra.Command, app string, route *registry.PortalRoute) error {
	paths, err := resolveSharePaths()
	if err != nil {
		return err
	}
	if err := registry.SetPortalRoute(c.Context(), paths.Registry, app, route); err != nil {
		return err
	}
	command := "portal route add"
	if route == nil {
		command = "portal route remove"
	}
	return writePortalRoutes(c, command)
}

func writePortalRoutes(c *cobra.Command, command string) error {
	paths, err := resolveSharePaths()
	if err != nil {
		return err
	}
	reg, err := registry.Load(paths.Registry)
	if err != nil {
		return err
	}
	pid, _ := inviteReadPIDFn(paths.PID)
	view := readPortalView(reg, paths.Registry, inviteIsRunningFn(paths.PID), pid)
	routes := []tsRuntime.GatewayRouteState{}
	if view.Gateway != nil {
		routes = view.Gateway.Routes
	}
	if jsonOutput(c) {
		output.WriteJSON(c.OutOrStdout(), output.NewSuccess(command, struct {
			Portal tsRuntime.PortalState         `json:"portal"`
			Routes []tsRuntime.GatewayRouteState `json:"routes"`
		}{view, routes}))
		return nil
	}
	for _, r := range routes {
		fmt.Fprintf(c.OutOrStdout(), "%s\t%d\t%s\t%s\n", r.App, r.Port, r.State, r.URL)
	}
	return nil
}
