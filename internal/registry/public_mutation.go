package registry

import (
	"context"
	"path/filepath"
	"reflect"
	"sync"
)

// GuardedChange classifies a guarded write for the caller's authority rule.
// The registry decides what changed; the guard decides who may change it.
type GuardedChange struct {
	// Public: the write changes a publicly exposed service or its guest links
	// (PublicStateChanged), or settles a tentative public registration.
	Public bool
	// OnlyRevokesGuests: the only public difference is existing guest grants
	// going from active to revoked; no service and no other grant changes.
	OnlyRevokesGuests bool
	// RouteAdded: a service gains a home gateway route it did not have,
	// including a new service registered with one (RoutesAdded).
	RouteAdded bool
}

// Relevant reports whether the guard needs to be consulted at all.
func (c GuardedChange) Relevant() bool { return c.Public || c.RouteAdded }

// PublicMutationGuard decides whether a guarded write may proceed. save calls
// it under the registry lock, with the registry as loaded when the lock was
// taken, the registry about to be written and the classification, only when
// the classification is Relevant. A non-nil error refuses the write before
// anything reaches disk.
type PublicMutationGuard func(before, after *Registry, change GuardedChange) error

type publicMutationGuardKey struct{}

// WithPublicMutationGuard makes every registry write performed with ctx
// subject to guard. The guard is one rule at one point: it sees each write a
// tool performs, whichever registry helper performs it, so a tool cannot reach
// public state or create a gateway route by a path that forgot to ask.
func WithPublicMutationGuard(ctx context.Context, guard PublicMutationGuard) context.Context {
	return context.WithValue(ctx, publicMutationGuardKey{}, guard)
}

type activeGuard struct {
	before *Registry
	guard  PublicMutationGuard
}

// save carries no context, so it finds its transaction's guard by path. Two
// locks make that lookup belong to one transaction: the registry file lock
// across processes, and transactionMutex inside this process, which
// withLockContext and tryWithLock hold for the whole transaction (guard
// installation, every save, removal), so no other goroutine can save on the
// path under this guard or while it is installed.
var (
	activeGuardsMu sync.Mutex
	activeGuards   = map[string]*activeGuard{}
	transactions   sync.Map // guardKey -> *sync.Mutex
)

func transactionMutex(path string) *sync.Mutex {
	mu, _ := transactions.LoadOrStore(guardKey(path), &sync.Mutex{})
	return mu.(*sync.Mutex)
}

func guardKey(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return filepath.Clean(path)
}

func activatePublicMutationGuard(path string, before *Registry, guard PublicMutationGuard) func() {
	key := guardKey(path)
	own := &activeGuard{before: before, guard: guard}
	activeGuardsMu.Lock()
	activeGuards[key] = own
	activeGuardsMu.Unlock()
	return func() {
		activeGuardsMu.Lock()
		if activeGuards[key] == own {
			delete(activeGuards, key)
		}
		activeGuardsMu.Unlock()
	}
}

func currentGuard(path string) (*activeGuard, bool) {
	activeGuardsMu.Lock()
	defer activeGuardsMu.Unlock()
	active, ok := activeGuards[guardKey(path)]
	return active, ok
}

func checkPublicMutationGuard(path string, after *Registry) error {
	active, ok := currentGuard(path)
	if !ok {
		return nil
	}
	change := ClassifyGuardedChange(active.before, after)
	if !change.Relevant() {
		return nil
	}
	return active.guard(active.before, after, change)
}

// checkPublicSettle consults the transaction's guard for a public change
// that writes no registry: settling a tentative public registration keeps an
// exposure its creator would otherwise roll back.
func checkPublicSettle(path string, reg *Registry) error {
	active, ok := currentGuard(path)
	if !ok {
		return nil
	}
	return active.guard(active.before, reg, GuardedChange{Public: true})
}

// ClassifyGuardedChange computes the GuardedChange of a write.
func ClassifyGuardedChange(before, after *Registry) GuardedChange {
	change := GuardedChange{Public: PublicStateChanged(before, after), RouteAdded: RoutesAdded(before, after)}
	change.OnlyRevokesGuests = change.Public && onlyRevokesGuests(before, after)
	return change
}

// RoutesAdded reports whether a service gains a gateway route it did not
// have, or a new service is registered with one.
func RoutesAdded(before, after *Registry) bool {
	if after == nil {
		return false
	}
	had := map[string]bool{}
	if before != nil {
		for _, svc := range before.Services {
			had[svc.Name] = svc.PortalRoute != nil
		}
	}
	for _, svc := range after.Services {
		if svc.PortalRoute != nil && !had[svc.Name] {
			return true
		}
	}
	return false
}

// onlyRevokesGuests: services identical, the same grants in the same order,
// and every differing grant differs only by Revoked going false -> true.
func onlyRevokesGuests(before, after *Registry) bool {
	if before == nil || after == nil || !reflect.DeepEqual(before.Services, after.Services) || len(before.Guests) != len(after.Guests) {
		return false
	}
	revoked := false
	for i := range before.Guests {
		was, is := before.Guests[i], after.Guests[i]
		if reflect.DeepEqual(was, is) {
			continue
		}
		if was.Revoked || !is.Revoked {
			return false
		}
		was.Revoked = true
		if !reflect.DeepEqual(was, is) {
			return false
		}
		revoked = true
	}
	return revoked
}

// PublicStateChanged reports whether a write changes anything about a
// service that is publicly exposed before or after it (see PubliclyExposed):
// the service definition itself, including its exposure fields and request
// limits, or any guest link for it. A change that only reduces exposure, such
// as revoking a link or removing the app, counts too. Services that are
// private on both sides, people, invitations, requests and the portal are not
// public state and never match.
func PublicStateChanged(before, after *Registry) bool {
	if before == nil {
		before = emptyRegistry()
	}
	if after == nil {
		after = emptyRegistry()
	}
	services := func(reg *Registry) map[string]Service {
		out := make(map[string]Service, len(reg.Services))
		for _, svc := range reg.Services {
			out[svc.Name] = svc
		}
		return out
	}
	guests := func(reg *Registry, app string) []GuestGrant {
		var out []GuestGrant
		for _, g := range reg.Guests {
			if g.App == app {
				out = append(out, g)
			}
		}
		return out
	}
	old, next := services(before), services(after)
	names := make(map[string]struct{}, len(old)+len(next))
	for name := range old {
		names[name] = struct{}{}
	}
	for name := range next {
		names[name] = struct{}{}
	}
	for _, g := range before.Guests {
		names[g.App] = struct{}{}
	}
	for _, g := range after.Guests {
		names[g.App] = struct{}{}
	}
	for name := range names {
		was, hadBefore := old[name]
		is, hasAfter := next[name]
		exposed := (hadBefore && was.PubliclyExposed()) || (hasAfter && is.PubliclyExposed())
		if !exposed {
			continue
		}
		if hadBefore != hasAfter || !reflect.DeepEqual(was, is) || !reflect.DeepEqual(guests(before, name), guests(after, name)) {
			return true
		}
	}
	return false
}
