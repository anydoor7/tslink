package registry

import (
	"context"
	"fmt"
	"github.com/anydoor7/tslink/internal/mcpaudit"
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
	return changePerson(mutationContext(options.Context), path, who, apps, nil, options.Value != nil, update, &options)
}

type PersonLifetimeOptions struct {
	Context         context.Context
	PrepareAccess   bool
	PreserveRevoked bool
	Tool            string
	// InheritedGrants receives the legacy grants captured under the write lock.
	// Callers may audit them only after the enclosing transaction succeeds.
	InheritedGrants *[]PersonGrant
	// Authorize runs against pre-mutation state under the registry write lock.
	Authorize func(*Registry, string) error
	Value     *string
	Policy    duration.Policy
	Audience  duration.Audience
	AckNever  bool
	Now       time.Time
	// PermanentDefault makes a grant created without an explicit Value
	// permanent instead of 24h. It is the historical meaning of an explicit
	// allow list (`add --allow`, MCP add/share allow), whose entries never
	// expired; the allow list itself is the acknowledgement that Value "never"
	// would otherwise need as AckNever. It applies only to grants this call
	// creates, never to an existing grant, and only where the caller's
	// authority could create a permanent grant at all: an MCP session that
	// cannot (any reduced role, or a binding with an expiry) gets the 24h
	// default, which the same scope check then accepts or refuses.
	PermanentDefault bool
}

func ChangePersonContext(ctx context.Context, path, who string, apps []string, expiry *time.Time, changeExpiry, update bool) (Person, error) {
	return changePerson(ctx, path, who, apps, expiry, changeExpiry, update, nil)
}

func changePerson(ctx context.Context, path, who string, apps []string, expiry *time.Time, changeExpiry, update bool, options *PersonLifetimeOptions) (result Person, err error) {
	err = withLockContext(ctx, path, func() error {
		reg, err := loadForMutation(path)
		if err != nil {
			return err
		}
		result, err = changePersonInRegistry(ctx, reg, who, apps, expiry, changeExpiry, update, options)
		if err != nil {
			return err
		}
		return save(path, reg)
	})
	return result, err
}

func changePersonInRegistry(ctx context.Context, reg *Registry, who string, apps []string, expiry *time.Time, changeExpiry, update bool, options *PersonLifetimeOptions) (result Person, err error) {
	if !update && len(apps) == 0 {
		return result, CodedError{Code: errcode.UsageError, Message: "apps must include at least one private HTTP or file service"}
	}
	login, err := NormalizePerson(who)
	if err != nil {
		return result, err
	}
	err = func() error {
		before := cloneAccessRegistry(reg)
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
		if options == nil && !update && index >= 0 && !result.Revoked {
			return CodedError{Code: errcode.Conflict, Message: fmt.Sprintf("person already exists: %s; use people update", login)}
		}
		if options != nil && options.PreserveRevoked && result.Revoked {
			return CodedError{Code: errcode.Conflict, Message: "person is revoked; restore access explicitly with people add"}
		}
		if options != nil && options.PrepareAccess && index < 0 {
			result = inheritedLegacyPerson(reg, login)
			if options.InheritedGrants != nil {
				*options.InheritedGrants = append([]PersonGrant(nil), result.Grants...)
			}
		}
		// A permanent default is its own acknowledgement (see PermanentDefault).
		ackNever, permanentDefault := false, false
		if options != nil {
			ackNever = options.AckNever
			if options.Value == nil && options.PermanentDefault && permanentDefaultPermitted(ctx, options.Now) {
				ackNever, permanentDefault = true, true
			}
		}
		// The first device invite turns member-designated access into guest
		// access. Omitted expiry cannot carry permanent/overlong grants across
		// that boundary. Invitation retries with recorded history retain F1's
		// deadline preservation semantics.
		if options != nil && index >= 0 && !result.Revoked && options.Audience == duration.Guest && !PersonIsGuest(result) && options.Value == nil {
			for _, g := range result.Grants {
				selected := apps == nil || (len(apps) == 1 && apps[0] == "all")
				for _, app := range apps {
					selected = selected || app == g.App
				}
				if selected {
					if err := options.Policy.Check(duration.Lifetime{Deadline: g.ExpiresAt, Never: g.ExpiresAt == nil}, duration.Guest, ackNever, options.Now); err != nil {
						return CodedError{Code: errcode.UsageError, Message: "first guest invitation requires --for/--until within guest policy: " + err.Error()}
					}
				}
			}
		}
		var newAppExpiry *time.Time
		needsDefault := index < 0 || result.Revoked
		if options != nil && apps != nil {
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
			if audience == "" {
				audience = duration.TailnetMember
			}
			if PersonIsGuest(result) {
				audience = duration.Guest
			}
			value := "24h"
			if options.Value != nil {
				value = *options.Value
			} else if permanentDefault {
				value = "never"
			}
			l, err := options.Policy.Resolve(value, audience, ackNever, options.Now, time.Local)
			if err != nil {
				return CodedError{Code: errcode.UsageError, Message: err.Error()}
			}
			newAppExpiry = l.Deadline
			if changeExpiry {
				expiry, changeExpiry = l.Deadline, true
			}
		}
		previous := result.Grants
		if !update && (index < 0 || result.Revoked) {
			for _, op := range result.Invites {
				if !PersonInviteTerminal(op) {
					return CodedError{Code: errcode.Conflict, Message: fmt.Sprintf("pending invite cleanup for %s; retry people remove before adding again", login)}
				}
			}
			result = Person{Login: login, Grants: previous, Invites: result.Invites, Guest: result.Guest}
		}
		selected := map[string]bool{}
		if apps != nil {
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
			if !update {
				for _, old := range previous {
					if _, ok := selected[old.App]; !ok {
						selected[old.App] = false
					}
				}
			}
			result.Grants = []PersonGrant{}
			for app := range selected {
				g := PersonGrant{App: app, ExpiresAt: newAppExpiry}
				if update || options != nil {
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
				if apps != nil && !selected[result.Grants[i].App] {
					continue
				}
				result.Grants[i].ExpiresAt = expiry
				result.Grants[i].Expired = false
			}
		}
		if options != nil {
			tool := "people_add"
			if update {
				tool = "people_update"
			}
			if options.Tool != "" {
				tool = options.Tool
			}
			for _, grant := range result.Grants {
				if (changeExpiry && (apps == nil || selected[grant.App])) || !personHasGrant(Person{Grants: previous}, grant.App) {
					if err := authorizeLifetime(ctx, tool, grant.App, duration.Lifetime{Deadline: grant.ExpiresAt, Never: grant.ExpiresAt == nil}, options.Now); err != nil {
						return err
					}
				}
			}
		}
		if options != nil && options.Audience == duration.Guest {
			result.Guest = true
		}
		if options != nil && options.PrepareAccess {
			for i := range reg.Services {
				svc := &reg.Services[i]
				if apps != nil && !selected[svc.Name] {
					continue
				}
				if !personHasGrant(result, svc.Name) {
					continue
				}
				if svc.Funnel || svc.GuestGate {
					return CodedError{Code: "people_service_unsupported", Message: "public and browser-guest apps use their existing access settings"}
				}
				if GatewayEnabled(reg) {
					if err := defaultGatewayRoute(reg, svc); err != nil {
						return err
					}
				} else {
					if svc.PortalOnly {
						return CodedError{Code: "endpoint_unavailable", Message: svc.Name + " is unavailable while its home is off"}
					}
					if !legacyLoginAllowed(svc.AllowedUsers, login) {
						svc.AllowedUsers = append(svc.AllowedUsers, login)
					}
				}
			}
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
		if options != nil && options.PrepareAccess {
			changedApps := map[string]bool{}
			for app, explicit := range selected {
				if explicit {
					changedApps[app] = true
				}
			}
			if apps == nil || update {
				for _, grant := range previous {
					changedApps[grant.App] = true
				}
			}
			if err := preserveAccessExcept(before, reg, login, changedApps, options.Now); err != nil {
				return err
			}
		}
		return nil
	}()
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
	return expirePeople(path, now, withLock, false)
}

// TryExpirePeople fails closed instead of queueing a request behind a writer.
func TryExpirePeople(path string, now time.Time) (bool, error) {
	return expirePeople(path, now, func(path string, fn func() error) error {
		acquired, err := tryWithLock(path, fn)
		if err == nil && !acquired {
			return CodedError{Code: "registry_unavailable", Message: "registry writer busy"}
		}
		return err
	}, true)
}

func expirePeople(path string, now time.Time, lock func(string, func() error) error, bounded bool) (changed bool, err error) {
	// Do not take a writer lock when there is no expired grant. Startup and
	// shutdown must not block behind an unrelated registry writer.
	read := func() (*Registry, error) {
		if !bounded {
			return loadForMutation(path)
		}
		reg, issues, err := PortalPreflight(path)
		if err == nil && len(issues) > 0 {
			err = issues[0]
		}
		return reg, err
	}
	var observed *Registry
	if bounded {
		observed, err = read()
	} else {
		observed, _, err = Preflight(path)
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !latchPeopleExpiry(observed, now) {
		return false, nil
	}
	var expired []mcpaudit.Change
	err = lock(path, func() error {
		reg, err := read()
		if err != nil {
			return err
		}
		for _, person := range reg.People {
			for _, grant := range person.Grants {
				if !grant.Expired && grant.ExpiresAt != nil && !now.Before(*grant.ExpiresAt) {
					expired = append(expired, mcpaudit.Change{Action: "grant_expired", App: grant.App, Subject: person.Login, ExpiresAt: grant.ExpiresAt})
				}
			}
		}
		changed = latchPeopleExpiry(reg, now)
		if changed {
			return save(path, reg)
		}
		return nil
	})
	if err == nil {
		for _, change := range expired {
			if err = recordExpiry(path, now, change); err != nil {
				break
			}
		}
	}
	return changed, err
}

// PeopleAccessAt returns an authoritative decision when people rules apply.
// Callers may use the legacy ACL only when authoritative is false.
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
			// An unscoped browser-guest app with no ACL keeps its open private
			// access when a person is added to an unrelated app. Explicit ACLs,
			// grants (including expired ones), and revocations still use the
			// existing people rules.
			if svc.GuestGate && !svc.PeopleScoped && len(svc.AllowedUsers) == 0 && !p.Revoked && !personHasGrant(p, svc.Name) {
				return false, false
			}
			return PersonGrantActiveAt(p, svc.Name, now), true
		}
	}
	if svc.PeopleScoped && len(svc.AllowedUsers) == 0 {
		return false, true
	}
	return false, false
}

func cloneAccessRegistry(reg *Registry) *Registry {
	copy := *reg
	copy.Services = append([]Service(nil), reg.Services...)
	for i := range copy.Services {
		copy.Services[i].AllowedUsers = append([]string(nil), reg.Services[i].AllowedUsers...)
	}
	copy.People = append([]Person(nil), reg.People...)
	for i := range copy.People {
		copy.People[i].Grants = append([]PersonGrant(nil), reg.People[i].Grants...)
	}
	return &copy
}

func legacyLoginAllowed(allow []string, login string) bool {
	for _, entry := range allow {
		canonical, err := NormalizePerson(entry)
		if err == nil && !strings.HasPrefix(canonical, "tag:") && canonical == login {
			return true
		}
	}
	return false
}

// Inherit only enumerated legacy rights. Never infer a permanent "all" grant
// from an open service, or rebuild an existing person's revoked/expired grant.
func inheritedLegacyPerson(reg *Registry, login string) Person {
	p := Person{Login: login, Grants: []PersonGrant{}}
	for _, svc := range reg.Services {
		if PeopleServiceSupported(svc) && legacyLoginAllowed(svc.AllowedUsers, login) {
			p.Grants = append(p.Grants, PersonGrant{App: svc.Name})
		}
	}
	return p
}

// The finite policy vocabulary (people, explicit allow entries and admins),
// plus unknown human/machine controls, exhausts this evaluator's branches.
// Any access change outside the requested human/app pairs aborts the write.
func preserveAccessExcept(before, after *Registry, login string, apps map[string]bool, now time.Time) error {
	logins := map[string]bool{"tslink-unknown-human": true}
	tags := map[string]bool{"tag:tslink-unknown-machine": true}
	for _, reg := range []*Registry{before, after} {
		for _, p := range reg.People {
			logins[p.Login] = true
		}
		if reg.Portal != nil {
			for _, who := range append([]string{reg.Portal.Owner}, reg.Portal.Admins...) {
				if who != "" {
					logins[who] = true
				}
			}
		}
		for _, svc := range reg.Services {
			for _, entry := range svc.AllowedUsers {
				if strings.HasPrefix(entry, "tag:") {
					tags[entry] = true
				} else if who, err := NormalizePerson(entry); err == nil {
					logins[who] = true
				}
			}
		}
	}
	if login != "" {
		logins[login] = true
	}
	conflicts := []string{}
	for _, old := range before.Services {
		if !PeopleServiceSupported(old) {
			continue
		}
		var next *Service
		for i := range after.Services {
			if after.Services[i].Name == old.Name {
				next = &after.Services[i]
				break
			}
		}
		if next == nil {
			continue
		}
		changed := false
		for who := range logins {
			if who == login && apps[old.Name] {
				continue
			}
			a, _, _ := PrivateAppAccessAt(before, old, who, nil, now)
			b, _, _ := PrivateAppAccessAt(after, *next, who, nil, now)
			// Restoring a tombstone may restore this person's open GuestGate
			// baseline, but must never hide a loss or another person's change.
			if who == login && !a && b && restoresGuestGateBaseline(before, after, old, *next, who) {
				continue
			}
			changed = changed || a != b
		}
		for tag := range tags {
			a, _, _ := PrivateAppAccessAt(before, old, "tslink-unknown-human", []string{tag}, now)
			b, _, _ := PrivateAppAccessAt(after, *next, "tslink-unknown-human", []string{tag}, now)
			changed = changed || a != b
		}
		if changed {
			conflicts = append(conflicts, old.Name)
		}
	}
	if len(conflicts) > 0 {
		sort.Strings(conflicts)
		return CodedError{Code: errcode.Conflict, Message: "access setup would change existing access to " + strings.Join(conflicts, ", ") + "; set the intended access list before retrying"}
	}
	return nil
}

// restoresGuestGateBaseline admits revival into the legacy open baseline only
// when before contains a revoked tombstone for login, not an explicit grant or
// a service becoming open during setup.
func restoresGuestGateBaseline(before, after *Registry, old, next Service, login string) bool {
	if !old.GuestGate || old.PeopleScoped || len(old.AllowedUsers) != 0 ||
		!next.GuestGate || next.PeopleScoped || len(next.AllowedUsers) != 0 {
		return false
	}
	revoked := false
	for _, p := range before.People {
		if p.Login == login {
			revoked = p.Revoked
			break
		}
	}
	if !revoked {
		return false
	}
	for _, p := range after.People {
		if p.Login == login {
			return !p.Revoked && !personHasGrant(p, next.Name)
		}
	}
	return false
}

func PrivateAppAccessAt(reg *Registry, svc Service, login string, tags []string, now time.Time) (bool, *time.Time, bool) {
	allowed, authoritative := PeopleAccessAt(reg, svc, login, tags, now)
	canonical, err := ResolvePersonLoginIn(reg, login)
	if err != nil {
		return allowed, nil, false
	}
	var person *Person
	if len(tags) == 0 {
		for i := range reg.People {
			if reg.People[i].Login == canonical {
				person = &reg.People[i]
				if person.Revoked {
					return false, nil, false
				}
				break
			}
		}
		if reg.Portal != nil {
			for _, admin := range append([]string{reg.Portal.Owner}, reg.Portal.Admins...) {
				if canonical == admin {
					return true, nil, true
				}
			}
		}
	}
	if !authoritative {
		allowed = LegacyAccessAllowed(login, tags, svc.AllowedUsers)
	}
	if allowed && person != nil {
		for _, grant := range person.Grants {
			if grant.App == svc.Name {
				return true, grant.ExpiresAt, false
			}
		}
	}
	return allowed, nil, false
}

func LegacyAccessAllowed(login string, nodeTags []string, allowedUsers []string) bool {
	if !utf8.ValidString(login) {
		return false
	}
	if len(allowedUsers) == 0 {
		return true
	}

	normalizedLogin, loginErr := NormalizePerson(login)
	for _, entry := range allowedUsers {
		entry = strings.TrimSpace(entry)
		// Check tag match: entry is "tag:xxx" and caller's node has that tag
		if strings.HasPrefix(entry, "tag:") {
			for _, t := range nodeTags {
				if t == entry {
					return true
				}
			}
			continue
		}

		// Check email/login match
		canonical, err := NormalizePerson(entry)
		if loginErr == nil && err == nil && normalizedLogin == canonical {
			return true
		}
	}

	return false
}
