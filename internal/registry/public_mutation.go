package registry

import (
	"context"
	"path/filepath"
	"reflect"
	"sync"
)

// PublicMutationGuard decides whether a write may change public state. It is
// called by save, under the registry lock, with the registry as loaded when
// the lock was taken and the registry about to be written, and only when
// PublicStateChanged reports a change. A non-nil error refuses the write
// before anything reaches disk.
type PublicMutationGuard func(before, after *Registry) error

type publicMutationGuardKey struct{}

// WithPublicMutationGuard makes every registry write performed with ctx
// subject to guard. The guard is one rule at one point: it sees each write a
// tool performs, whichever registry helper performs it, so a tool cannot reach
// public state by a path that forgot to ask.
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

func checkPublicMutationGuard(path string, after *Registry) error {
	activeGuardsMu.Lock()
	active, ok := activeGuards[guardKey(path)]
	activeGuardsMu.Unlock()
	if !ok || !PublicStateChanged(active.before, after) {
		return nil
	}
	return active.guard(active.before, after)
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
