package server

import (
	"time"

	"github.com/anydoor7/tslink/internal/registry"
)

// AppAccessAt is the shared decision used by private app listeners and the
// portal. Tagged machines never inherit a person's or administrator's identity.
// Tombstones override administrative roles as well as legacy allow lists.
func AppAccessAt(reg *registry.Registry, svc registry.Service, login string, tags []string, now time.Time) (bool, *time.Time) {
	allowed, authoritative := registry.PeopleAccessAt(reg, svc, login, tags, now)
	canonical, err := registry.ResolvePersonLoginIn(reg, login)
	if err != nil {
		return allowed, nil
	}
	var person *registry.Person
	if len(tags) == 0 {
		for i := range reg.People {
			if reg.People[i].Login == canonical {
				person = &reg.People[i]
				if person.Revoked {
					return false, nil
				}
				break
			}
		}
		if reg.Portal != nil {
			for _, admin := range append([]string{reg.Portal.Owner}, reg.Portal.Admins...) {
				if canonical == admin {
					return true, nil
				}
			}
		}
	}
	if !authoritative {
		allowed = isAllowed(login, tags, svc.AllowedUsers)
	}
	if allowed && person != nil {
		for _, grant := range person.Grants {
			if grant.App == svc.Name {
				return true, grant.ExpiresAt
			}
		}
	}
	return allowed, nil
}
