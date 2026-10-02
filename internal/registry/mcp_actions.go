package registry

import (
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/anydoor7/tslink/internal/mcpscope"
)

// ChangePersonApp changes one grant under the registry lock. In particular it
// never replaces other grants, clears a global tombstone, or changes invites.
// Revoke retains an expired grant to keep the per-person denial authoritative.
// Authorization and deadline checks are repeated inside the writer lock so a
// caller cannot wait out its binding or race a service becoming public.
func ChangePersonApp(path string, session mcpscope.Session, who, app, lifetime string, revoke bool, nowFn func() time.Time) (person Person, err error) {
	login, err := NormalizePerson(who)
	if err != nil {
		return person, err
	}
	tool := "people_grant"
	if revoke {
		tool = "people_revoke"
	}
	err = withLock(path, func() error {
		now := nowFn()
		if err := session.Authorize(tool, []string{app}, now); err != nil {
			return err
		}
		var expiry *time.Time
		if !revoke {
			var err error
			expiry, err = session.GrantDeadline(lifetime, now)
			if err != nil {
				return err
			}
		}
		reg, err := loadForMutation(path)
		if err != nil {
			return err
		}
		svcIndex := -1
		for i, svc := range reg.Services {
			if svc.Name == app {
				if !PeopleServiceSupported(svc) {
					return mcpscope.Denied{}
				}
				svcIndex = i
			}
		}
		if svcIndex < 0 {
			return CodedError{Code: "not_found", Message: "app not found"}
		}
		index := -1
		for i, p := range reg.People {
			if p.Login == login {
				index, person = i, p
				break
			}
		}
		if person.Revoked {
			return mcpscope.Denied{}
		}
		if index < 0 {
			person = Person{Login: login, Grants: []PersonGrant{}}
		}
		grant := PersonGrant{App: app, ExpiresAt: expiry}
		if revoke {
			t := now.UTC()
			grant.ExpiresAt, grant.Expired = &t, true
		}
		found := false
		for i, g := range person.Grants {
			if g.App == app {
				person.Grants[i], found = grant, true
			}
		}
		if !found {
			person.Grants = append(person.Grants, grant)
		}
		sort.Slice(person.Grants, func(i, j int) bool { return person.Grants[i].App < person.Grants[j].App })
		if index < 0 {
			reg.People = append(reg.People, person)
		} else {
			reg.People[index] = person
		}
		sort.Slice(reg.People, func(i, j int) bool { return reg.People[i].Login < reg.People[j].Login })
		reg.Services[svcIndex].PeopleScoped = true
		return save(path, reg)
	})
	return person, err
}

// RequestAppRestart queues gateway-node reconciliation, preserving enrolled
// identity and configuration. It does not restart the third-party app process.
func RequestAppRestart(path string, session mcpscope.Session, app string, nowFn func() time.Time) (generation uint64, err error) {
	err = withLock(path, func() error {
		if err := session.Authorize("app_restart", []string{app}, nowFn()); err != nil {
			return err
		}
		reg, err := loadForMutation(path)
		if err != nil {
			return err
		}
		for i, svc := range reg.Services {
			if svc.Name != app {
				continue
			}
			if svc.Funnel && session.Scope.Role != "owner" {
				return mcpscope.Denied{}
			}
			if svc.RestartGeneration == math.MaxUint64 {
				return fmt.Errorf("restart generation exhausted")
			}
			generation = svc.RestartGeneration + 1
			reg.Services[i].RestartGeneration = generation
			return save(path, reg)
		}
		return CodedError{Code: "not_found", Message: "app not found"}
	})
	return generation, err
}
