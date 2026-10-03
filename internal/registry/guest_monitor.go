package registry

import (
	"fmt"
	"github.com/anydoor7/tslink/internal/mcpaudit"
	"time"
)

// ReadGuestGrants gives a gate one shared authorization snapshot for all live
// flights. Overdue grants latch expiry in one writer transaction; normal ticks
// read and decode the registry only once, regardless of the number of flights.
func ReadGuestGrants(path, app string, now time.Time) ([]GuestGrant, error) {
	reg, issues, err := readGuestState(path)
	if err != nil {
		return nil, err
	}
	if len(issues) > 0 {
		return nil, issues[0]
	}
	grants := []GuestGrant{}
	overdue := false
	for _, svc := range reg.Services {
		if svc.Name == app && svc.GuestGate && svc.Funnel && svc.PublicAck && !FunnelExpiredAt(svc, now) {
			for _, grant := range reg.Guests {
				if grant.App == app {
					grants = append(grants, grant)
					overdue = overdue || (!grant.Revoked && !grant.Expired && !now.Before(grant.ExpiresAt))
				}
			}
		}
	}
	if overdue {
		var expired []mcpaudit.Change
		acquired, err := tryWithLock(path, func() error {
			current, err := loadForMutation(path)
			if err != nil {
				return err
			}
			changed := false
			for i := range current.Guests {
				grant := &current.Guests[i]
				if grant.App == app && !grant.Revoked && !grant.Expired && !now.Before(grant.ExpiresAt) {
					expired = append(expired, mcpaudit.Change{Action: "guest_expired", App: grant.App, ID: grant.ID, ExpiresAt: &grant.ExpiresAt})
					grant.Expired = true
					changed = true
				}
			}
			if changed {
				return save(path, current)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		if !acquired {
			return nil, fmt.Errorf("guest expiry: registry writer busy")
		}
		for _, change := range expired {
			if err := recordExpiry(path, now, change); err != nil {
				return nil, err
			}
		}
	}
	return grants, nil
}
