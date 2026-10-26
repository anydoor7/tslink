package server

import (
	"time"

	"github.com/anydoor7/tslink/internal/registry"
)

// AppAccessDecision separates listener enforcement from directory disclosure.
// Raw TCP and Funnel have no TSLink per-identity gate; their cards are an
// owner/admin inventory and do not assert anything about visitor reachability.
type AppAccessDecision struct {
	Allowed          bool
	IdentityEnforced bool
	DirectoryVisible bool
	ExpiresAt        *time.Time
}

// AppAccessAt exposes the listener decision from the same model as the portal.
func AppAccessAt(reg *registry.Registry, svc registry.Service, login string, tags []string, now time.Time) (bool, *time.Time) {
	decision := AppAccessDecisionAt(reg, svc, login, tags, now)
	return decision.Allowed, decision.ExpiresAt
}

func AppAccessDecisionAt(reg *registry.Registry, svc registry.Service, login string, tags []string, now time.Time) AppAccessDecision {
	svc = registry.EffectiveServiceAt(svc, now)
	enforced := (!svc.Funnel || svc.GuestGate) && (svc.Type == registry.TypeProxy || svc.Type == registry.TypeFile)
	if !enforced && svc.Type != registry.TypeTCP && !svc.Funnel {
		return AppAccessDecision{}
	}
	allowed, expiry, administrator := privateAppAccessAt(reg, svc, login, tags, now)
	if !enforced {
		return AppAccessDecision{Allowed: true, DirectoryVisible: administrator}
	}
	if svc.Funnel {
		// Guest authentication belongs to the public listener. Tailnet requests
		// still use people rules; public apps remain an owner/admin inventory.
		return AppAccessDecision{Allowed: allowed, IdentityEnforced: true, DirectoryVisible: administrator, ExpiresAt: expiry}
	}
	return AppAccessDecision{Allowed: allowed, IdentityEnforced: true, DirectoryVisible: allowed, ExpiresAt: expiry}
}

// Tagged machines never inherit a human role. Tombstones precede roles and
// legacy allow lists without claiming to restrict raw TCP or public traffic.
func privateAppAccessAt(reg *registry.Registry, svc registry.Service, login string, tags []string, now time.Time) (bool, *time.Time, bool) {
	return registry.PrivateAppAccessAt(reg, svc, login, tags, now)
}
