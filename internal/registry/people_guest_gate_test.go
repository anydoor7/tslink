package registry

import (
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/duration"
)

func TestGuestGateReAddRevokedPerson(t *testing.T) {
	for _, gateEnabled := range []bool{false, true} {
		for _, guest := range []bool{false, true} {
			t.Run(fmt.Sprintf("gate=%v/guest=%v", gateEnabled, guest), func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "registry.json")
				now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
				const login = "returning"
				gate := Service{Name: "drop-w2", Type: TypeProxy, Target: "http://localhost:3000", Funnel: true, PublicAck: true, GuestGate: true}
				if gateEnabled {
					if _, err := Add(path, gate); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := Add(path, Service{Name: "photos", Type: TypeProxy, Target: "http://localhost:3001", AllowedUsers: []string{login}}); err != nil {
					t.Fatal(err)
				}
				never := "never"
				options := PersonLifetimeOptions{PrepareAccess: true, Value: &never, AckNever: true, Now: now}
				if guest {
					options.Audience = duration.Guest
				}
				add := func() {
					t.Helper()
					if _, err := ChangePersonWithLifetime(path, login, []string{"photos"}, false, options); err != nil {
						t.Fatal(err)
					}
				}
				add()
				if removed, err := RemovePerson(path, login); err != nil || !removed {
					t.Fatalf("remove: removed=%v err=%v", removed, err)
				}
				before, err := Load(path)
				if err != nil {
					t.Fatal(err)
				}
				if !before.People[0].Revoked {
					t.Fatal("missing revoke tombstone")
				}
				if gateEnabled {
					allowed, _, _ := PrivateAppAccessAt(before, before.Services[0], login, nil, now)
					if allowed {
						t.Fatal("revoked person retained gate access")
					}
				}
				add()
				after, err := Load(path)
				if err != nil {
					t.Fatal(err)
				}
				p := after.People[0]
				if p.Revoked || !PersonGrantActiveAt(p, "photos", now) || personHasGrant(p, gate.Name) || PersonIsGuest(p) != guest {
					t.Fatal("re-add did not restore exactly the requested access")
				}
				if gateEnabled {
					if !reflect.DeepEqual(before.Services[0], after.Services[0]) {
						t.Fatal("re-add changed GuestGate configuration")
					}
					for _, who := range []string{login, "unrelated", "unknown"} {
						for _, tags := range [][]string{nil, {"tag:backup"}} {
							allowed, _, _ := PrivateAppAccessAt(after, after.Services[0], who, tags, now)
							if !allowed {
								t.Fatal("open gate baseline not preserved")
							}
						}
					}
				}
			})
		}
	}
}

func TestGuestGateRevivalPreservesOtherAccessChanges(t *testing.T) {
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	past := now.Add(-time.Hour)
	for _, tc := range []struct {
		name         string
		mutate       func(*Registry, *Registry)
		wantConflict bool
	}{
		{name: "revive-open-baseline"},
		{name: "access-loss-revoke", wantConflict: true, mutate: func(b, a *Registry) {
			b.People[0].Revoked, a.People[0].Revoked = false, true
		}},
		{name: "access-loss-explicit-expired-grant", wantConflict: true, mutate: func(b, a *Registry) {
			b.People[0].Revoked = false
			a.People[0].Grants = []PersonGrant{{App: "gate", ExpiresAt: &past}}
		}},
		{name: "expired-grant-removal-is-not-revival", wantConflict: true, mutate: func(b, a *Registry) {
			b.People[0].Revoked = false
			b.People[0].Grants = []PersonGrant{{App: "gate", ExpiresAt: &past}}
		}},
		{name: "explicit-grant-is-not-baseline", wantConflict: true, mutate: func(b, a *Registry) {
			a.People[0].Grants = []PersonGrant{{App: "gate"}}
		}},
		{name: "missing-record-is-not-revival", wantConflict: true, mutate: func(b, a *Registry) {
			a.People = nil
		}},
		{name: "other-person-revival", wantConflict: true, mutate: func(b, a *Registry) {
			b.People = append(b.People, Person{Login: "other", Revoked: true})
			a.People = append(a.People, Person{Login: "other"})
		}},
		{name: "other-person-access-loss", wantConflict: true, mutate: func(b, a *Registry) {
			b.People = append(b.People, Person{Login: "other"})
			a.People = append(a.People, Person{Login: "other", Revoked: true})
		}},
		{name: "ordinary-private-service", wantConflict: true, mutate: func(b, a *Registry) {
			b.Services[0].GuestGate, a.Services[0].GuestGate = false, false
			b.Services[0].Funnel, a.Services[0].Funnel = false, false
			a.Portal = &PortalConfig{Owner: "returning"}
		}},
		{name: "newly-open-scope", wantConflict: true, mutate: func(b, a *Registry) {
			b.Services[0].PeopleScoped = true
		}},
		{name: "newly-open-acl", wantConflict: true, mutate: func(b, a *Registry) {
			b.Services[0].AllowedUsers = []string{"returning", "tag:backup"}
		}},
		{name: "newly-closed-scope", wantConflict: true, mutate: func(b, a *Registry) {
			a.Services[0].PeopleScoped = true
			a.Portal = &PortalConfig{Owner: "returning"}
		}},
		{name: "newly-closed-acl", wantConflict: true, mutate: func(b, a *Registry) {
			a.Services[0].AllowedUsers = []string{"returning"}
			a.Portal = &PortalConfig{Owner: "returning"}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := &Registry{
				Services: []Service{{Name: "gate", Type: TypeProxy, GuestGate: true, Funnel: true}},
				People:   []Person{{Login: "returning", Revoked: true}},
			}
			after := cloneAccessRegistry(before)
			after.People[0].Revoked = false
			if tc.mutate != nil {
				tc.mutate(before, after)
			}
			err := preserveAccessExcept(before, after, "returning", map[string]bool{"photos": true}, now)
			if (err != nil) != tc.wantConflict {
				t.Fatalf("conflict=%v want=%v: %v", err != nil, tc.wantConflict, err)
			}
		})
	}
}

func TestGuestGateExpiredUnrelatedGrantsKeepBaseline(t *testing.T) {
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	past, future := now.Add(-time.Hour), now.Add(time.Hour)
	for _, grant := range []PersonGrant{{App: "photos", ExpiresAt: &past}, {App: "photos", ExpiresAt: &future, Expired: true}} {
		for _, funnelExpired := range []bool{false, true} {
			t.Run(fmt.Sprintf("latched=%v/funnel-expired=%v", grant.Expired, funnelExpired), func(t *testing.T) {
				svc := Service{Name: "gate", Type: TypeProxy, GuestGate: true, Funnel: true}
				if funnelExpired {
					svc.FunnelExpiresAt = &past
				}
				svc = EffectiveServiceAt(svc, now)
				reg := &Registry{People: []Person{{Login: "known", Guest: true, Grants: []PersonGrant{grant}}}}
				known, _, _ := PrivateAppAccessAt(reg, svc, "known", nil, now)
				unknown, _, _ := PrivateAppAccessAt(reg, svc, "unknown", nil, now)
				if !unknown || known != unknown {
					t.Fatal("expired unrelated grants changed the open gate baseline")
				}
			})
		}
	}
}

func TestPeopleGrantPreservesUnscopedGuestGate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.json")
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	gate := Service{Name: "drop-w2", Type: TypeProxy, Target: "http://localhost:3000", Funnel: true, PublicAck: true, GuestGate: true}
	for _, svc := range []Service{gate, {Name: "photos", Type: TypeProxy, Target: "http://localhost:3001", AllowedUsers: []string{"a@example.invalid", "tag:backup"}}, {Name: "finance", Type: TypeProxy, Target: "http://localhost:3002", AllowedUsers: []string{"b@example.invalid", "tag:backup"}}} {
		if _, err := Add(path, svc); err != nil {
			t.Fatal(err)
		}
	}
	before, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	never := "never"
	if _, err = ChangePersonWithLifetime(path, "a@example.invalid", []string{"photos"}, false, PersonLifetimeOptions{PrepareAccess: true, Value: &never, AckNever: true, Now: now}); err != nil {
		t.Fatal(err)
	}
	after, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before.Services[0], after.Services[0]) {
		t.Fatal("unrelated guest gate configuration changed")
	}
	if personHasGrant(after.People[0], "drop-w2") {
		t.Fatal("grant inferred for unrelated guest gate")
	}
	for _, who := range []string{"a@example.invalid", "b@example.invalid", "unknown"} {
		for _, tags := range [][]string{nil, {"tag:backup"}, {"tag:unknown"}} {
			a, _, _ := PrivateAppAccessAt(before, before.Services[0], who, tags, now)
			b, _, _ := PrivateAppAccessAt(after, after.Services[0], who, tags, now)
			if !a || a != b {
				t.Fatal("guest gate legacy access changed")
			}
		}
	}
}

func TestGuestGateLegacyFallbackPreservesExplicitDenials(t *testing.T) {
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	past, future := now.Add(-time.Hour), now.Add(time.Hour)
	for _, tc := range []struct {
		name            string
		scoped, revoked bool
		allow           []string
		grant           *PersonGrant
		want            bool
	}{
		{name: "unrelated-person-open", want: true},
		// Defensive boundary: a nonempty GuestGate ACL is not a normal product
		// configuration and must not override an authoritative people denial.
		{name: "legacy-allow-does-not-override-known-person", allow: []string{"a@example.invalid"}},
		{name: "legacy-deny", allow: []string{"b@example.invalid"}},
		{name: "scoped-missing", scoped: true},
		{name: "revoked", revoked: true},
		{name: "expired", grant: &PersonGrant{App: "drop-w2", ExpiresAt: &past}},
		{name: "latched", grant: &PersonGrant{App: "drop-w2", ExpiresAt: &future, Expired: true}},
		{name: "active", grant: &PersonGrant{App: "drop-w2", ExpiresAt: &future}, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := Service{Name: "drop-w2", Type: TypeProxy, Funnel: true, GuestGate: true, PeopleScoped: tc.scoped, AllowedUsers: tc.allow}
			person := Person{Login: "a@example.invalid", Revoked: tc.revoked, Grants: []PersonGrant{{App: "photos"}}}
			if tc.grant != nil {
				person.Grants = append(person.Grants, *tc.grant)
			}
			reg := &Registry{Services: []Service{svc}, People: []Person{person}}
			got, _, _ := PrivateAppAccessAt(reg, svc, person.Login, nil, now)
			if got != tc.want {
				t.Fatalf("allowed=%v want=%v", got, tc.want)
			}
		})
	}
}
