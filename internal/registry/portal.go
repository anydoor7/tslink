package registry

import (
	"fmt"
	"strings"
)

const DefaultPortalHostname = "home"
const CodePortalFunnelRefused = "portal_funnel_refused"

// PortalConfig is independent of Services: it can never enter the Funnel
// service listener, cleanup, health probing or service-state directories.
// Owner and Admins are explicit administrative identities for app access.
type PortalConfig struct {
	Enabled  bool     `json:"enabled"`
	Hostname string   `json:"hostname"`
	Owner    string   `json:"owner"`
	Admins   []string `json:"admins,omitempty"`
	Funnel   bool     `json:"funnel,omitempty"`
}

func portalConflict(name string) error {
	return CodedError{Code: "portal_hostname_conflict", Message: fmt.Sprintf("portal hostname %q is reserved; choose a different portal or app name", name)}
}

func ValidatePortal(p *PortalConfig) error {
	if p == nil {
		return nil
	}
	if p.Funnel {
		return CodedError{Code: CodePortalFunnelRefused, Message: "the portal is Tailnet-only and cannot use Funnel"}
	}
	if err := ValidateName(p.Hostname); err != nil {
		return err
	}
	for _, login := range append([]string{p.Owner}, p.Admins...) {
		normalized, err := NormalizePerson(login)
		if err != nil || normalized != login || strings.HasPrefix(login, "tag:") {
			return CodedError{Code: "portal_identity_invalid", Message: "portal owner and admins must be canonical Tailscale login identities"}
		}
	}
	return nil
}

// SetPortal serializes with all registry writers and preserves app and people
// state. Disabling preserves identities and hostname for a later enable.
func SetPortal(path string, p *PortalConfig) error {
	if err := ValidatePortal(p); err != nil {
		return err
	}
	return withLock(path, func() error {
		reg, err := loadForMutation(path)
		if err != nil {
			return err
		}
		if p != nil {
			for _, svc := range reg.Services {
				if svc.Name == p.Hostname {
					return portalConflict(p.Hostname)
				}
			}
		}
		reg.Portal = p
		return save(path, reg)
	})
}

func DisablePortal(path string) error {
	return withLock(path, func() error {
		reg, err := loadForMutation(path)
		if err != nil {
			return err
		}
		if reg.Portal == nil || !reg.Portal.Enabled {
			return nil
		}
		reg.Portal.Enabled = false
		return save(path, reg)
	})
}
