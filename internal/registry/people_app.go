package registry

import (
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
	change := DurationChange{Service: app, Who: login, ChangedAt: options.Now.UTC()}
	for _, person := range reg.People {
		if person.Login != login {
			continue
		}
		for _, grant := range person.Grants {
			if grant.App == app {
				change.PreviousExpiresAt = grant.ExpiresAt
				change.Regranted = grant.Expired || (grant.ExpiresAt != nil && !options.Now.Before(*grant.ExpiresAt))
			}
		}
	}
	options.PreserveRevoked = true
	person, err := changePersonInRegistry(mutationContext(options.Context), reg, login, []string{app}, nil, options.Value != nil, false, &options)
	if err != nil {
		return DurationChange{}, err
	}
	change.Audience = duration.TailnetMember
	if PersonIsGuest(person) {
		change.Audience = duration.Guest
	}
	for _, grant := range person.Grants {
		if grant.App == app {
			change.ExpiresAt = grant.ExpiresAt
		}
	}
	return change, nil
}
