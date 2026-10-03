package registry

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/anydoor7/tslink/internal/duration"
	"github.com/anydoor7/tslink/internal/errcode"
)

// PeopleRegistrySchemaVersion is deliberately unsupported by older binaries.
// Their strict top-level decoder also refuses people, preventing lossy writes.
const PeopleRegistrySchemaVersion = CurrentRegistrySchemaVersion

type Person struct {
	Login   string         `json:"login"`
	Grants  []PersonGrant  `json:"grants"`
	Revoked bool           `json:"revoked,omitempty"`
	Invites []PersonInvite `json:"invites,omitempty"`
	// Guest is sticky and committed with the first invitation's finite grants,
	// before remote work. Invite history remains the legacy classification.
	Guest bool `json:"guest,omitempty"`
}

func PersonIsGuest(p Person) bool {
	return p.Guest || len(p.Invites) > 0
}

type PersonGrant struct {
	App       string     `json:"app"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	// Expired is a durable latch: observing a deadline cannot be undone by
	// moving the clock back or restarting. An explicit update can renew it.
	Expired bool `json:"expired,omitempty"`
}

func NormalizePerson(login string) (string, error) {
	if !utf8.ValidString(login) {
		return "", CodedError{Code: errcode.UsageError, Message: "person login must be valid UTF-8"}
	}
	// Only outer ASCII whitespace and ASCII case are equivalent. Unicode
	// bytes are preserved, including Kelvin sign and dotted capital I.
	login = canonicalPersonBytes(login)
	if login == "" || strings.IndexFunc(login, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) }) >= 0 {
		return "", CodedError{Code: errcode.UsageError, Message: "person login must be nonempty and contain no controls or internal whitespace"}
	}
	return login, nil
}

func canonicalPersonBytes(login string) string {
	canonical := []byte(strings.Trim(login, " \t\r\n\v\f"))
	for i, c := range canonical {
		if c >= 'A' && c <= 'Z' {
			canonical[i] = c + ('a' - 'A')
		}
	}
	return string(canonical)
}

// Parent schema-2 writers admitted internal Unicode whitespace and C1
// controls. Preserve those already-stored keys without applying the parent's
// Unicode folding. New people input still uses NormalizePerson's strict rule.
func legacyStoredPersonLogin(login string) bool {
	return utf8.ValidString(login) && login != "" && login == canonicalPersonBytes(login) && !strings.HasPrefix(login, "tag:") &&
		!strings.ContainsAny(login, " \t\r\n,\\") && strings.IndexFunc(login, func(r rune) bool { return r < 32 || r == 127 }) < 0
}

func normalizePersonFromRegistry(reg *Registry, who string) (string, error) {
	login, err := NormalizePerson(who)
	if err == nil {
		return login, nil
	}
	candidate := canonicalPersonBytes(who)
	for _, p := range reg.People {
		if p.Login == candidate && legacyStoredPersonLogin(candidate) {
			return candidate, nil
		}
	}
	return "", err
}

// ResolvePersonLoginIn resolves WhoIs against one already-read registry. It
// preserves the same exact legacy-key compatibility as ResolvePersonLogin,
// without another file read or any change to identity semantics.
func ResolvePersonLoginIn(reg *Registry, who string) (string, error) {
	return normalizePersonFromRegistry(reg, who)
}

// ResolvePersonLogin adds only exact matching of existing legacy schema-2
// keys to the new-input grammar. Reads never rewrite or normalize stored data.
func ResolvePersonLogin(path, who string) (string, error) {
	login, inputErr := NormalizePerson(who)
	if inputErr == nil {
		return login, nil
	}
	reg, _, err := Preflight(path)
	if err != nil {
		return "", inputErr
	}
	return normalizePersonFromRegistry(reg, who)
}

// ParsePersonExpiry stores UTC wall time, never a restart-relative duration.
func ParsePersonExpiry(value string, now time.Time) (*time.Time, error) {
	l, err := (duration.Policy{}).Resolve(value, duration.TailnetMember, false, now, time.Local)
	return l.Deadline, err
}

func validatePeople(people []Person) error {
	seen := map[string]bool{}
	for _, p := range people {
		login, err := NormalizePerson(p.Login)
		if err != nil && legacyStoredPersonLogin(p.Login) {
			login, err = p.Login, nil
		}
		if err != nil || login != p.Login || seen[login] {
			return fmt.Errorf("invalid or duplicate person login %q", p.Login)
		}
		seen[login] = true
		if err := validatePersonInvites(p.Invites); err != nil {
			return err
		}
		apps := map[string]bool{}
		for _, g := range p.Grants {
			if err := ValidateName(g.App); err != nil {
				return err
			}
			if apps[g.App] {
				return fmt.Errorf("duplicate grant for %q", g.App)
			}
			apps[g.App] = true
		}
	}
	return nil
}

func PersonGrantActiveAt(p Person, app string, now time.Time) bool {
	if p.Revoked {
		return false
	}
	for _, g := range p.Grants {
		if g.App == app {
			return !g.Expired && (g.ExpiresAt == nil || now.Before(*g.ExpiresAt))
		}
	}
	return false
}

func PeopleServiceSupported(svc Service) bool {
	return (!svc.Funnel || svc.GuestGate) && (svc.Type == TypeProxy || svc.Type == TypeFile)
}

// ChangePerson atomically replaces the named person's grants. nil apps keeps
// the set on update; nil expiry keeps deadlines unless changeExpiry is true.
// all selects the current private HTTP/file services, not future additions.
func ChangePerson(path, who string, apps []string, expiry *time.Time, changeExpiry, update bool) (result Person, err error) {
	return changePerson(context.Background(), path, who, apps, expiry, changeExpiry, update, nil)
}

// ChangePersonWithLifetime preserves the F1 store contract, but resolves user
// lifetimes under the same lock as the grants. Omitted add/new-app expiry is 24h.
func ChangePersonWithLifetime(path, who string, apps []string, update bool, options PersonLifetimeOptions) (Person, error) {
	return changePerson(options.Context, path, who, apps, nil, options.Value != nil, update, &options)
}

type PersonLifetimeOptions struct {
	Context context.Context
	// Authorize runs against pre-mutation state under the registry write lock.
	Authorize func(*Registry, string) error
	Value     *string
	Policy    duration.Policy
	Audience  duration.Audience
	AckNever  bool
	Now       time.Time
}

func ChangePersonContext(ctx context.Context, path, who string, apps []string, expiry *time.Time, changeExpiry, update bool) (Person, error) {
	return changePerson(ctx, path, who, apps, expiry, changeExpiry, update, nil)
}

func changePerson(ctx context.Context, path, who string, apps []string, expiry *time.Time, changeExpiry, update bool, options *PersonLifetimeOptions) (result Person, err error) {
	if !update && len(apps) == 0 {
		return result, CodedError{Code: errcode.UsageError, Message: "apps must include at least one private HTTP or file service"}
	}
	login, err := NormalizePerson(who)
	if err != nil {
		return result, err
	}
	err = withLockContext(ctx, path, func() error {
		reg, err := loadForMutation(path)
		if err != nil {
			return err
		}
		if options != nil && options.Authorize != nil {
			if err := options.Authorize(reg, login); err != nil {
				return err
			}
		}
		index := -1
		for i, p := range reg.People {
			if p.Login == login {
				index = i
				result = p
				break
			}
		}
		if update && (index < 0 || result.Revoked) {
			return CodedError{Code: errcode.NotFound, Message: fmt.Sprintf("person not found: %s", login)}
		}
		if !update && index >= 0 && !result.Revoked {
			return CodedError{Code: errcode.Conflict, Message: fmt.Sprintf("person already exists: %s; use people update", login)}
		}
		// The first device invite turns member-designated access into guest
		// access. Omitted expiry cannot carry permanent/overlong grants across
		// that boundary. Invitation retries with recorded history retain F1's
		// deadline preservation semantics.
		if options != nil && update && options.Audience == duration.Guest && !PersonIsGuest(result) && options.Value == nil {
			for _, g := range result.Grants {
				selected := apps == nil || (len(apps) == 1 && apps[0] == "all")
				for _, app := range apps {
					selected = selected || app == g.App
				}
				if selected {
					if err := options.Policy.Check(duration.Lifetime{Deadline: g.ExpiresAt, Never: g.ExpiresAt == nil}, duration.Guest, false, options.Now); err != nil {
						return CodedError{Code: errcode.UsageError, Message: "first guest invitation requires --for/--until within guest policy: " + err.Error()}
					}
				}
			}
		}
		var newAppExpiry *time.Time
		needsDefault := !update
		if options != nil && update && apps != nil {
			for _, app := range apps {
				if app == "all" {
					for _, svc := range reg.Services {
						if PeopleServiceSupported(svc) && !personHasGrant(result, svc.Name) {
							needsDefault = true
						}
					}
				} else if !personHasGrant(result, app) {
					needsDefault = true
				}
			}
		}
		if options != nil && (options.Value != nil || needsDefault) {
			audience := options.Audience
			if PersonIsGuest(result) {
				audience = duration.Guest
			}
			value := "24h"
			if options.Value != nil {
				value = *options.Value
			}
			l, err := options.Policy.Resolve(value, audience, options.AckNever, options.Now, time.Local)
			if err != nil {
				return CodedError{Code: errcode.UsageError, Message: err.Error()}
			}
			newAppExpiry = l.Deadline
			if changeExpiry || !update {
				expiry, changeExpiry = l.Deadline, true
			}
		}
		previous := result.Grants
		if !update {
			for _, op := range result.Invites {
				if !PersonInviteTerminal(op) {
					return CodedError{Code: errcode.Conflict, Message: fmt.Sprintf("pending invite cleanup for %s; retry people remove before adding again", login)}
				}
			}
			result = Person{Login: login, Grants: []PersonGrant{}, Invites: result.Invites, Guest: result.Guest}
		}
		if apps != nil {
			selected := map[string]bool{}
			all := len(apps) == 1 && apps[0] == "all"
			if all {
				for _, svc := range reg.Services {
					if PeopleServiceSupported(svc) {
						selected[svc.Name] = true
					}
				}
			} else {
				for _, app := range apps {
					selected[app] = true
				}
			}
			if len(selected) == 0 {
				return CodedError{Code: errcode.UsageError, Message: "apps must include at least one private HTTP or file service"}
			}
			for app := range selected {
				found := false
				for _, svc := range reg.Services {
					if svc.Name != app {
						continue
					}
					found = true
					if !PeopleServiceSupported(svc) {
						return CodedError{Code: "people_service_unsupported", Message: fmt.Sprintf("person access refuses service %q: TCP has no HTTP WhoIs enforcement; Funnel is public", app)}
					}
				}
				if !found {
					return CodedError{Code: errcode.NotFound, Message: fmt.Sprintf("service not found: %s", app)}
				}
			}
			result.Grants = []PersonGrant{}
			for app := range selected {
				g := PersonGrant{App: app, ExpiresAt: newAppExpiry}
				if update {
					for _, old := range previous {
						if old.App == app {
							g = old
							break
						}
					}
				}
				result.Grants = append(result.Grants, g)
			}
			sort.Slice(result.Grants, func(i, j int) bool { return result.Grants[i].App < result.Grants[j].App })
		}
		if changeExpiry {
			for i := range result.Grants {
				result.Grants[i].ExpiresAt = expiry
				result.Grants[i].Expired = false
			}
		}
		if options != nil && options.Audience == duration.Guest {
			result.Guest = true
		}
		// Scope is sticky, including after the final grant is removed. An empty
		// people list must never turn a formerly private app back into allow-all.
		for i := range reg.Services {
			for _, g := range result.Grants {
				if g.App == reg.Services[i].Name {
					reg.Services[i].PeopleScoped = true
				}
			}
		}
		if index < 0 {
			reg.People = append(reg.People, result)
		} else {
			reg.People[index] = result
		}
		sort.Slice(reg.People, func(i, j int) bool { return reg.People[i].Login < reg.People[j].Login })
		return save(path, reg)
	})
	return result, err
}

// RemovePerson retains a deny tombstone so a legacy empty allow-list, a tag
// rule or an accepted device share cannot restore this login's HTTP access.
func RemovePerson(path, who string) (removed bool, err error) {
	return RemovePersonAuthorizedContext(context.Background(), path, who, nil)
}

func RemovePersonContext(ctx context.Context, path, who string) (bool, error) {
	return RemovePersonAuthorizedContext(ctx, path, who, nil)
}

func RemovePersonAuthorized(path, who string, authorize func(*Registry, string) error) (bool, error) {
	return RemovePersonAuthorizedContext(context.Background(), path, who, authorize)
}

// RemovePersonAuthorizedContext checks authority and session under the write lock.
func RemovePersonAuthorizedContext(ctx context.Context, path, who string, authorize func(*Registry, string) error) (removed bool, err error) {
	login, err := ResolvePersonLogin(path, who)
	if err != nil {
		return false, err
	}
	err = withLockContext(ctx, path, func() error {
		reg, err := loadForMutation(path)
		if err != nil {
			return err
		}
		if authorize != nil {
			if err := authorize(reg, login); err != nil {
				return err
			}
		}
		found := false
		for i := range reg.People {
			if reg.People[i].Login == login {
				found = true
				removed = !reg.People[i].Revoked
				reg.People[i].Revoked = true
				reg.People[i].Grants = []PersonGrant{}
			}
		}
		if !found {
			reg.People = append(reg.People, Person{Login: login, Revoked: true, Grants: []PersonGrant{}})
		}
		for i := range reg.Services {
			var allow []string
			for _, entry := range reg.Services[i].AllowedUsers {
				if canonical, e := NormalizePerson(entry); e == nil && canonical == login {
					removed = true
					reg.Services[i].PeopleScoped = true
				} else {
					allow = append(allow, entry)
				}
			}
			reg.Services[i].AllowedUsers = allow
		}
		return save(path, reg)
	})
	return removed, err
}

func latchPeopleExpiry(reg *Registry, now time.Time) (changed bool) {
	for i := range reg.People {
		for j := range reg.People[i].Grants {
			g := &reg.People[i].Grants[j]
			if !g.Expired && g.ExpiresAt != nil && !now.Before(*g.ExpiresAt) {
				g.Expired = true
				changed = true
			}
		}
	}
	return changed
}

func removeAppGrants(reg *Registry, app string) {
	for i := range reg.People {
		grants := []PersonGrant{}
		for _, g := range reg.People[i].Grants {
			if g.App != app {
				grants = append(grants, g)
			}
		}
		reg.People[i].Grants = grants
	}
}

// ExpirePeople is shared by startup, ticks and requests. Persist only changes.
func ExpirePeople(path string, now time.Time) (changed bool, err error) {
	// Do not take a writer lock when there is no expired grant. Startup and
	// shutdown must not block behind an unrelated registry writer.
	observed, _, err := Preflight(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !latchPeopleExpiry(observed, now) {
		return false, nil
	}
	err = withLock(path, func() error {
		reg, err := loadForMutation(path)
		if err != nil {
			return err
		}
		changed = latchPeopleExpiry(reg, now)
		if changed {
			return save(path, reg)
		}
		return nil
	})
	return changed, err
}

// PeopleAccessAt returns an authoritative decision for known logins or scoped
// services. Callers may use the legacy ACL only when authoritative is false.
func PeopleAccessAt(reg *Registry, svc Service, login string, tags []string, now time.Time) (allowed, authoritative bool) {
	var err error
	login, err = normalizePersonFromRegistry(reg, login)
	if err != nil {
		// Malformed WhoIs identities cannot participate in authorization.
		return false, true
	}
	for _, p := range reg.People {
		// Tags represent machines, never a person, even with a matching profile.
		if login != "" && p.Login == login && len(tags) == 0 {
			return PersonGrantActiveAt(p, svc.Name, now), true
		}
	}
	if svc.PeopleScoped && len(svc.AllowedUsers) == 0 {
		return false, true
	}
	return false, false
}
