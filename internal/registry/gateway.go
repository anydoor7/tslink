package registry

import (
	"context"
	"fmt"
	"net/url"
)

type GatewayPorts struct {
	From int `json:"from"`
	To   int `json:"to"`
}

type GatewayConfig struct {
	Enabled bool         `json:"enabled"`
	Ports   GatewayPorts `json:"ports"`
	Tag     string       `json:"tag,omitempty"`
}

type PortalRoute struct {
	Port             int  `json:"port"`
	AllowCrossOrigin bool `json:"allow_cross_origin,omitempty"`
}

func DefaultGatewayPorts() GatewayPorts { return GatewayPorts{From: 8443, To: 8499} }

func gatewayError(code, message string) error { return CodedError{Code: code, Message: message} }

// GatewayBadPort follows https://fetch.spec.whatwg.org/#port-blocking,
// fetched 2026-10-13. 80 and 443 are additionally reserved by TSLink.
func GatewayBadPort(port int) bool {
	switch port {
	case 0, 1, 7, 9, 11, 13, 15, 17, 19, 20, 21, 22, 23, 25, 37, 42, 43,
		53, 69, 77, 79, 87, 95, 101, 102, 103, 104, 109, 110, 111, 113, 115,
		117, 119, 123, 135, 137, 139, 143, 161, 179, 389, 427, 465, 512, 513,
		514, 515, 526, 530, 531, 532, 540, 548, 554, 556, 563, 587, 601, 636,
		989, 990, 993, 995, 1719, 1720, 1723, 2049, 3659, 4045, 4190, 5060,
		5061, 6000, 6566, 6665, 6666, 6667, 6668, 6669, 6679, 6697, 10080, 80, 443:
		return true
	}
	return false
}

func ValidateGatewayConfig(g *GatewayConfig) error {
	if g == nil {
		return nil
	}
	if g.Ports.From < 1 || g.Ports.To > 65535 || g.Ports.From > g.Ports.To {
		return gatewayError("gateway_ports_invalid", "gateway port range must be within 1-65535")
	}
	if g.Tag != "" {
		return ValidateTag(g.Tag)
	}
	return nil
}

func validateGatewayService(svc Service) error {
	if svc.PortalRoute != nil || svc.PortalOnly {
		if svc.GuestGate {
			return gatewayError("portal_route_guest_refused", "guest-gated services cannot be routed")
		}
		if svc.Funnel {
			return gatewayError("portal_route_funnel_refused", "public Funnel services cannot be routed")
		}
		if svc.Type != TypeProxy && svc.Type != TypeFile {
			return gatewayError("portal_route_type_unsupported", "only private HTTP proxy and file services can be routed")
		}
	}
	if svc.Upstream != "" && svc.Upstream != "host" && svc.Upstream != "tailnet" {
		return gatewayError("portal_upstream_invalid", "upstream must be host or tailnet")
	}
	if svc.Upstream == "tailnet" {
		u, err := url.Parse(svc.Target)
		if !svc.PortalOnly || svc.Type != TypeProxy || err != nil || u.Scheme != "https" || u.Hostname() == "" {
			return gatewayError("portal_upstream_invalid", "tailnet upstream requires a portal-only HTTPS proxy")
		}
	}
	return nil
}

func hasGatewayFields(reg *Registry) bool {
	if reg.Portal != nil && reg.Portal.Gateway != nil {
		return true
	}
	for _, s := range reg.Services {
		if s.PortalRoute != nil || s.PortalOnly || s.Upstream != "" {
			return true
		}
	}
	return false
}

// ValidateGateway is called at both the read and the final write boundary.
// Disabled portals keep their routes, but a missing gateway cannot own routes.
func ValidateGateway(reg *Registry) error {
	var g *GatewayConfig
	if reg.Portal != nil {
		g = reg.Portal.Gateway
	}
	if err := ValidateGatewayConfig(g); err != nil {
		return err
	}
	used := map[int]bool{}
	for _, svc := range reg.Services {
		if err := validateGatewayService(svc); err != nil {
			return err
		}
		if svc.PortalOnly && svc.PortalRoute == nil {
			return gatewayError("portal_only_requires_route", "portal-only app requires a route")
		}
		if svc.PortalRoute == nil {
			continue
		}
		if g == nil || !g.Enabled {
			return gatewayError("gateway_required", "routes require portal gateway mode")
		}
		if !svc.PeopleScoped {
			return gatewayError("portal_route_requires_scope", "routed apps must be people-scoped")
		}
		p := svc.PortalRoute.Port
		if GatewayBadPort(p) {
			return gatewayError("portal_route_bad_port", "port is blocked by browsers or reserved")
		}
		if p < g.Ports.From || p > g.Ports.To {
			return gatewayError("portal_route_port_range", "route port is outside the gateway range")
		}
		if used[p] {
			return gatewayError("portal_route_port_conflict", "route port is already assigned")
		}
		used[p] = true
	}
	return nil
}

// AllocateGatewayPort is deterministic; existing assignments are persisted in
// the registry and excluded under the same mutation lock as the write.
func AllocateGatewayPort(reg *Registry, app string, requested int) (int, error) {
	if reg.Portal == nil || reg.Portal.Gateway == nil || !reg.Portal.Gateway.Enabled {
		return 0, gatewayError("gateway_required", "enable portal gateway mode first")
	}
	g := reg.Portal.Gateway
	if err := ValidateGatewayConfig(g); err != nil {
		return 0, err
	}
	used := map[int]bool{}
	for _, svc := range reg.Services {
		if svc.PortalRoute == nil {
			continue
		}
		if svc.Name == app {
			if requested == 0 {
				requested = svc.PortalRoute.Port
			}
		} else {
			used[svc.PortalRoute.Port] = true
		}
	}
	if requested != 0 {
		if GatewayBadPort(requested) {
			return 0, gatewayError("portal_route_bad_port", "port is blocked by browsers or reserved")
		}
		if requested < g.Ports.From || requested > g.Ports.To {
			return 0, gatewayError("portal_route_port_range", "route port is outside the gateway range")
		}
		if used[requested] {
			return 0, gatewayError("portal_route_port_conflict", "route port is already assigned")
		}
		return requested, nil
	}
	for p := g.Ports.From; p <= g.Ports.To; p++ {
		if !GatewayBadPort(p) && !used[p] {
			return p, nil
		}
	}
	return 0, gatewayError("portal_route_ports_exhausted", "no available gateway port")
}

func SetPortalRoute(ctx context.Context, path, app string, route *PortalRoute) error {
	return withLockContext(ctx, path, func() error {
		reg, err := loadForMutation(path)
		if err != nil {
			return err
		}
		for i := range reg.Services {
			svc := &reg.Services[i]
			if svc.Name != app {
				continue
			}
			if route == nil {
				if svc.PortalOnly {
					return gatewayError("portal_only_requires_route", "remove the portal-only app instead")
				}
				svc.PortalRoute = nil
			} else {
				copy := *route
				copy.Port, err = AllocateGatewayPort(reg, app, copy.Port)
				if err != nil {
					return err
				}
				svc.PortalRoute, svc.PeopleScoped = &copy, true
			}
			return save(path, reg)
		}
		return gatewayError("not_found", fmt.Sprintf("app %q not found", app))
	})
}
