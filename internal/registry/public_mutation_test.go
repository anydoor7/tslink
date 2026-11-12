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
	ctx := WithPublicMutationGuard(context.Background(), func(before, after *Registry) error {
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
	outsider := WithPublicMutationGuard(context.Background(), func(_, _ *Registry) error { return refusal })
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
