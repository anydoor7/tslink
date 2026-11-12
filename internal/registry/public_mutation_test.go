package registry

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestPubliclyExposed(t *testing.T) {
	deadline := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name string
		svc  Service
		want bool
	}{
		{"private", Service{Name: "a"}, false},
		{"routed private", Service{Name: "a", PortalRoute: &PortalRoute{Port: 8443}}, false},
		{"old deadline without ack", Service{Name: "a", FunnelExpiresAt: &deadline}, false},
		{"open funnel", Service{Name: "a", Funnel: true, PublicAck: true}, true},
		{"lapsed funnel keeps ack", Service{Name: "a", PublicAck: true, FunnelExpiresAt: &deadline}, true},
		{"guest gate", Service{Name: "a", GuestGate: true}, true},
	} {
		if got := tc.svc.PubliclyExposed(); got != tc.want {
			t.Errorf("%s: PubliclyExposed = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestPublicStateChanged(t *testing.T) {
	pub := Service{Name: "pub", GuestGate: true, Target: "http://localhost:1"}
	priv := Service{Name: "priv", Target: "http://localhost:2"}
	grant := GuestGrant{ID: "g1", App: "pub"}
	base := func() *Registry {
		return &Registry{Services: []Service{pub, priv}, Guests: []GuestGrant{grant}}
	}
	for _, tc := range []struct {
		name   string
		mutate func(*Registry)
		want   bool
	}{
		{"nothing", func(*Registry) {}, false},
		{"private target", func(r *Registry) { r.Services[1].Target = "http://localhost:3" }, false},
		{"private removed", func(r *Registry) { r.Services = r.Services[:1] }, false},
		{"new private", func(r *Registry) { r.Services = append(r.Services, Service{Name: "n"}) }, false},
		{"people only", func(r *Registry) { r.People = []Person{{Login: "a@example.com"}} }, false},
		{"public target", func(r *Registry) { r.Services[0].Target = "http://localhost:3" }, true},
		{"public removed", func(r *Registry) { r.Services = r.Services[1:] }, true},
		{"private made public", func(r *Registry) { r.Services[1].Funnel, r.Services[1].PublicAck = true, true }, true},
		{"new public", func(r *Registry) { r.Services = append(r.Services, Service{Name: "n", GuestGate: true}) }, true},
		{"link revoked", func(r *Registry) { r.Guests[0].Revoked = true }, true},
		{"link added", func(r *Registry) { r.Guests = append(r.Guests, GuestGrant{ID: "g2", App: "pub"}) }, true},
	} {
		after := base()
		tc.mutate(after)
		if got := PublicStateChanged(base(), after); got != tc.want {
			t.Errorf("%s: PublicStateChanged = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// The guard runs under the lock against the registry loaded under it, and a
// refusal reaches no disk; a write with no public change never consults it.
func TestPublicMutationGuardRefusesBeforeDisk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.json")
	deadline := time.Now().Add(time.Hour).UTC()
	if _, err := Add(path, Service{Name: "fun", Type: TypeProxy, Target: "http://localhost:1", Funnel: true, PublicAck: true, FunnelExpiresAt: &deadline}); err != nil {
		t.Fatal(err)
	}
	if _, err := Add(path, Service{Name: "priv", Type: TypeProxy, Target: "http://localhost:2"}); err != nil {
		t.Fatal(err)
	}
	refusal := errors.New("refused")
	calls := 0
	ctx := WithPublicMutationGuard(context.Background(), func(before, after *Registry, _ GuardedChange) error {
		calls++
		if len(before.Services) != 2 {
			t.Errorf("guard saw %d services before", len(before.Services))
		}
		return refusal
	})
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := MutateServiceContext(ctx, path, "fun", func(s Service) (Service, error) { s.Target = "http://localhost:9"; return s, nil }); !errors.Is(err, refusal) {
		t.Fatalf("public mutation err = %v", err)
	}
	if after, _ := os.ReadFile(path); !bytes.Equal(before, after) || calls != 1 {
		t.Fatalf("refused write reached disk (calls %d)", calls)
	}
	if _, err := MutateServiceContext(ctx, path, "priv", func(s Service) (Service, error) { s.Target = "http://localhost:8"; return s, nil }); err != nil || calls != 1 {
		t.Fatalf("private mutation err = %v, guard calls %d", err, calls)
	}
	// Without the guard in the context the same write proceeds.
	if _, err := MutateService(path, "fun", func(s Service) (Service, error) { s.Target = "http://localhost:9"; return s, nil }); err != nil {
		t.Fatal(err)
	}
}

// The guard must not depend on file-lock exclusivity alone: with the file
// lock stubbed out, a guarded transaction's guard is never seen by another
// goroutine's save, and a refused write never reaches disk.
func TestPublicMutationGuardHoldsWithoutTheFileLock(t *testing.T) {
	oldLock, oldTry := lockFn, tryLockFn
	lockFn = func(*os.File) error { return nil }
	tryLockFn = func(*os.File) (bool, error) { return true, nil }
	t.Cleanup(func() { lockFn, tryLockFn = oldLock, oldTry })
	path := filepath.Join(t.TempDir(), "registry.json")
	deadline := time.Now().Add(time.Hour).UTC()
	if _, err := Add(path, Service{Name: "fun", Type: TypeProxy, Target: "http://localhost:1", Funnel: true, PublicAck: true, FunnelExpiresAt: &deadline}); err != nil {
		t.Fatal(err)
	}
	refusal := errors.New("refused")
	outsider := WithPublicMutationGuard(context.Background(), func(_, _ *Registry, _ GuardedChange) error { return refusal })
	var localRefused, outsiderSaved, daemonRefused atomic.Int64
	var wg sync.WaitGroup
	stop := time.Now().Add(time.Second)
	for g := 0; g < 4; g++ {
		wg.Add(3)
		go func() {
			defer wg.Done()
			for time.Now().Before(stop) {
				if _, err := MutateServiceContext(outsider, path, "fun", func(s Service) (Service, error) { s.Target = "http://attacker:9"; return s, nil }); err == nil {
					outsiderSaved.Add(1)
				}
			}
		}()
		go func() {
			defer wg.Done()
			for time.Now().Before(stop) {
				if _, err := MutateService(path, "fun", func(s Service) (Service, error) { s.RestartGeneration++; return s, nil }); errors.Is(err, refusal) {
					localRefused.Add(1)
				}
			}
		}()
		go func() {
			defer wg.Done()
			for time.Now().Before(stop) {
				_, err := tryWithLock(path, func() error {
					reg, err := loadForMutation(path)
					if err != nil {
						return err
					}
					reg.Services[0].RestartGeneration++
					return save(path, reg)
				})
				if errors.Is(err, refusal) {
					daemonRefused.Add(1)
				}
			}
		}()
	}
	wg.Wait()
	reg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	activeGuardsMu.Lock()
	left := len(activeGuards)
	activeGuardsMu.Unlock()
	if outsiderSaved.Load() != 0 || localRefused.Load() != 0 || daemonRefused.Load() != 0 || reg.Services[0].Target != "http://localhost:1" || left != 0 {
		t.Fatalf("guard crossed transactions: outsiderSaved=%d localRefused=%d daemonRefused=%d target=%s stale=%d", outsiderSaved.Load(), localRefused.Load(), daemonRefused.Load(), reg.Services[0].Target, left)
	}
}

// The revoke exemption is exact: only existing grants going active ->
// revoked, with no service or other grant change.
func TestClassifyGuardedChange(t *testing.T) {
	pub := Service{Name: "pub", GuestGate: true}
	base := func() *Registry {
		return &Registry{Services: []Service{pub, {Name: "priv"}}, Guests: []GuestGrant{{ID: "g1", App: "pub", PINHash: "pbkdf2$x"}, {ID: "g2", App: "pub"}}}
	}
	for _, tc := range []struct {
		name                   string
		mutate                 func(*Registry)
		public, onlyRevoke, rt bool
	}{
		{"revoke one", func(r *Registry) { r.Guests[0].Revoked = true }, true, true, false},
		{"revoke both", func(r *Registry) { r.Guests[0].Revoked, r.Guests[1].Revoked = true, true }, true, true, false},
		{"revoke and retarget", func(r *Registry) { r.Guests[0].Revoked = true; r.Services[0].Target = "x" }, true, false, false},
		{"revoke and add a grant", func(r *Registry) {
			r.Guests[0].Revoked = true
			r.Guests = append(r.Guests, GuestGrant{ID: "g3", App: "pub"})
		}, true, false, false},
		{"revoke and extend", func(r *Registry) { r.Guests[0].Revoked = true; r.Guests[1].ExpiresAt = time.Now() }, true, false, false},
		{"revoke and clear the PIN of the same grant", func(r *Registry) { r.Guests[0].Revoked = true; r.Guests[0].PINHash = "" }, true, false, false},
		{"revoke and extend the same grant", func(r *Registry) { r.Guests[0].Revoked = true; r.Guests[0].ExpiresAt = time.Now() }, true, false, false},
		{"unrevoke", func(r *Registry) { r.Guests[0].Revoked = true }, true, true, false},
		{"route a private app", func(r *Registry) { r.Services[1].PortalRoute = &PortalRoute{Port: 8443} }, false, false, true},
		{"new routed app", func(r *Registry) {
			r.Services = append(r.Services, Service{Name: "n", PortalRoute: &PortalRoute{Port: 8444}})
		}, false, false, true},
	} {
		after := base()
		tc.mutate(after)
		before := base()
		if tc.name == "unrevoke" {
			before, after = after, base()
			tc.onlyRevoke = false
		}
		got := ClassifyGuardedChange(before, after)
		if got.Public != tc.public || got.OnlyRevokesGuests != tc.onlyRevoke || got.RouteAdded != tc.rt {
			t.Errorf("%s: %+v", tc.name, got)
		}
	}
}

// R2-2 end to end: a guard that admits only revoke-only writes (the owner
// role exemption) refuses a write that revokes a grant and changes another
// field of the same grant, and nothing reaches disk; the plain revoke passes.
func TestRevokeExemptionRefusesABundledGrantChange(t *testing.T) {
	path := guestRegistry(t)
	view, _, err := CreateGuest(path, guestOptions())
	if err != nil {
		t.Fatal(err)
	}
	refusal := errors.New("portal owner required")
	ctx := WithPublicMutationGuard(context.Background(), func(_, _ *Registry, change GuardedChange) error {
		if change.OnlyRevokesGuests {
			return nil
		}
		return refusal
	})
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	err = withLockContext(ctx, path, func() error {
		reg, err := loadForMutation(path)
		if err != nil {
			return err
		}
		for i := range reg.Guests {
			if reg.Guests[i].ID == view.ID {
				reg.Guests[i].Revoked = true
				reg.Guests[i].ExpiresAt = reg.Guests[i].ExpiresAt.Add(24 * time.Hour)
			}
		}
		return save(path, reg)
	})
	if !errors.Is(err, refusal) {
		t.Fatalf("bundled revoke err = %v", err)
	}
	if after, _ := os.ReadFile(path); !bytes.Equal(before, after) {
		t.Fatal("refused bundled revoke reached disk")
	}
	if _, err := RevokeGuestContext(ctx, path, view.ID, guestTestNow); err != nil {
		t.Fatalf("plain revoke under the exemption: %v", err)
	}
}
