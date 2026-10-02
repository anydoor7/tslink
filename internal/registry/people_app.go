package registry

import (
	"sort"
	"time"

	"github.com/anydoor7/tslink/internal/duration"
)

// ChangePersonAppWithLifetime is an additive F1 contract: change exactly one
// app, preserving every other grant, invitation, classification and tombstone.
// Creating an unknown login is an owner action; a revoked login stays revoked.
func ChangePersonAppWithLifetime(path, who, app string, options PersonLifetimeOptions) (result DurationChange, err error) {
	err = withLock(path, func() error {
		reg, err := loadRequestRegistry(path)
		if err != nil {
			return err
		}
		result, err = grantPersonApp(reg, who, app, options)
		if err != nil {
			return err
		}
		return save(path, reg)
	})
	if err != nil {
		return DurationChange{}, err
	}
	return result, nil
}

// The request transaction uses the same F1 operation before its single save.
func grantPersonApp(reg *Registry, who, app string, options PersonLifetimeOptions) (DurationChange, error) {
	login, err := normalizePersonFromRegistry(reg, who)
	if err != nil {
		return DurationChange{}, err
	}
	si := -1
	for i, svc := range reg.Services {
		if svc.Name == app {
			si = i
			break
		}
	}
	if si < 0 || !PeopleServiceSupported(reg.Services[si]) {
		return DurationChange{}, CodedError{Code: "not_found", Message: "private HTTP/file app unavailable"}
	}
	pi := -1
	p := Person{Login: login, Grants: []PersonGrant{}}
	for i, person := range reg.People {
		if person.Login == login {
			pi, p = i, person
			break
		}
	}
	if p.Revoked {
		return DurationChange{}, CodedError{Code: "conflict", Message: "person is revoked; restore access explicitly with people add"}
	}
	audience := duration.TailnetMember
	if PersonIsGuest(p) {
		audience = duration.Guest
	}
	value := "24h"
	if options.Value != nil {
		value = *options.Value
	}
	lifetime, err := options.Policy.Resolve(value, audience, options.AckNever, options.Now, time.Local)
	if err != nil {
		return DurationChange{}, CodedError{Code: "usage_error", Message: err.Error()}
	}
	change := DurationChange{Service: app, Who: login, Audience: audience, ExpiresAt: lifetime.Deadline, ChangedAt: options.Now.UTC()}
	gi := -1
	for i, g := range p.Grants {
		if g.App == app {
			gi, change.PreviousExpiresAt = i, g.ExpiresAt
			change.Regranted = g.Expired || (g.ExpiresAt != nil && !options.Now.Before(*g.ExpiresAt))
			break
		}
	}
	grant := PersonGrant{App: app, ExpiresAt: lifetime.Deadline}
	if gi < 0 {
		p.Grants = append(p.Grants, grant)
	} else {
		p.Grants[gi] = grant
	}
	sort.Slice(p.Grants, func(i, j int) bool { return p.Grants[i].App < p.Grants[j].App })
	if pi < 0 {
		reg.People = append(reg.People, p)
	} else {
		reg.People[pi] = p
	}
	sort.Slice(reg.People, func(i, j int) bool { return reg.People[i].Login < reg.People[j].Login })
	reg.Services[si].PeopleScoped = true
	return change, nil
}
