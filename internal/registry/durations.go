package registry

import (
	"fmt"
	"time"

	"github.com/anydoor7/tslink/internal/duration"
	"github.com/anydoor7/tslink/internal/errcode"
)

func personHasGrant(p Person, app string) bool {
	for _, g := range p.Grants {
		if g.App == app {
			return true
		}
	}
	return false
}

// Only an unchanged, decided public lifetime is grandfathered. Every new
// public deadline, including reactivation, is checked after preservation.
func checkAddedFunnelLifetime(svc Service, preserved bool, options AddOptions, now time.Time) error {
	if !svc.Funnel || preserved || options.LifetimePolicy == nil {
		return nil
	}
	if err := options.LifetimePolicy.Check(duration.Lifetime{Deadline: svc.FunnelExpiresAt, Never: svc.FunnelExpiresAt == nil}, duration.Public, false, now); err != nil {
		return CodedError{Code: errcode.UsageError, Message: "stored Funnel lifetime: " + err.Error()}
	}
	return nil
}

func parseFunnelRelative(value string) (time.Duration, bool, error) {
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	d, err := duration.ParseRelative(value)
	if err != nil {
		return 0, false, err
	}
	t := now.Add(d)
	err = (duration.Policy{}).Check(duration.Lifetime{Deadline: &t}, duration.Public, false, now)
	return d, false, err
}

type ExtendOptions struct {
	Service  string
	Who      string // Empty selects Funnel; otherwise selects this person's app grant.
	Value    string
	Regrant  bool
	AckNever bool
	Policy   duration.Policy
	Now      time.Time
}

// DurationChange is also the narrow payload for a future access-log event.
// Returned only after a successful atomic save; no logging dependency or I/O.
type DurationChange struct {
	Service           string            `json:"service"`
	Who               string            `json:"who,omitempty"`
	Audience          duration.Audience `json:"audience"`
	PreviousExpiresAt *time.Time        `json:"previous_expires_at"`
	ExpiresAt         *time.Time        `json:"expires_at"`
	Regranted         bool              `json:"regranted"`
	ChangedAt         time.Time         `json:"changed_at"`
}

// ExtendDuration sets a new lifetime from Now (not from the prior deadline).
// Regrant only resets expiry; it never clears a person's revocation tombstone.
func ExtendDuration(path string, options ExtendOptions) (result DurationChange, err error) {
	if err := ValidateName(options.Service); err != nil {
		return result, err
	}
	err = withLock(path, func() error {
		reg, err := loadForMutation(path)
		if err != nil {
			return err
		}
		svcIndex := -1
		for i, svc := range reg.Services {
			if svc.Name == options.Service {
				svcIndex = i
				break
			}
		}
		if svcIndex < 0 {
			return CodedError{Code: errcode.NotFound, Message: "service not found: " + options.Service}
		}
		svc := &reg.Services[svcIndex]
		result = DurationChange{Service: svc.Name, Audience: duration.Public, ChangedAt: options.Now.UTC()}
		var grant *PersonGrant
		expired := false
		if options.Who != "" {
			login, err := normalizePersonFromRegistry(reg, options.Who)
			if err != nil {
				return err
			}
			result.Who, result.Audience = login, duration.TailnetMember
			for i := range reg.People {
				p := &reg.People[i]
				if p.Login != login || p.Revoked {
					continue
				}
				if PersonIsGuest(*p) {
					result.Audience = duration.Guest
				}
				for j := range p.Grants {
					if p.Grants[j].App == svc.Name {
						grant = &p.Grants[j]
						break
					}
				}
			}
			if grant == nil {
				return CodedError{Code: errcode.NotFound, Message: "active person or app grant not found: " + login}
			}
			if !PeopleServiceSupported(*svc) {
				return CodedError{Code: errcode.UsageError, Message: "person grant requires a private HTTP/file service"}
			}
			result.PreviousExpiresAt = grant.ExpiresAt
			expired = grant.Expired || (grant.ExpiresAt != nil && !options.Now.Before(*grant.ExpiresAt))
		} else {
			if !svc.PublicAck || svc.FunnelExpiryUndecided() || (!svc.Funnel && (svc.FunnelExpiresAt == nil || svc.FunnelExpiresAt.After(options.Now))) {
				return CodedError{Code: errcode.UsageError, Message: "service has no active or expired acknowledged Funnel TTL"}
			}
			result.PreviousExpiresAt = svc.FunnelExpiresAt
			expired = svc.FunnelExpiresAt != nil && !options.Now.Before(*svc.FunnelExpiresAt)
		}
		if expired && !options.Regrant {
			return CodedError{Code: errcode.Conflict, Message: "duration already expired; explicit --regrant (MCP regrant) is required"}
		}
		l, err := options.Policy.Resolve(options.Value, result.Audience, options.AckNever, options.Now, time.Local)
		if err != nil {
			return CodedError{Code: errcode.UsageError, Message: err.Error()}
		}
		result.ExpiresAt, result.Regranted = l.Deadline, expired
		if grant != nil {
			grant.ExpiresAt, grant.Expired = l.Deadline, false
		} else {
			svc.FunnelExpiresAt, svc.Funnel = l.Deadline, true
			if err := ValidateService(*svc); err != nil {
				return fmt.Errorf("extend Funnel: %w", err)
			}
		}
		return save(path, reg)
	})
	if err != nil {
		return DurationChange{}, err
	}
	return result, nil
}
