package registry

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/duration"
	"github.com/anydoor7/tslink/internal/mcpscope"
)

// PermanentDefault is the explicit allow list's historical meaning: a grant it
// creates without a lifetime does not expire. It never touches a grant that
// exists, never applies to a caller that could not create a permanent grant
// with an explicit "never", and is off for every other entry point.

func permanentDefaultStore(t *testing.T) (string, time.Time) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "registry.json")
	for _, name := range []string{"photos", "finance"} {
		if _, err := Add(path, Service{Name: name, Type: TypeProxy, Target: "http://localhost:3000"}); err != nil {
			t.Fatal(err)
		}
	}
	return path, time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
}

func grantFor(t *testing.T, p Person, app string) PersonGrant {
	t.Helper()
	for _, g := range p.Grants {
		if g.App == app {
			return g
		}
	}
	t.Fatalf("no grant for %s in %+v", app, p)
	return PersonGrant{}
}

func TestPermanentDefaultOnlyAppliesToNewGrantsWithoutALifetime(t *testing.T) {
	for _, audience := range []duration.Audience{duration.TailnetMember, duration.Guest} {
		t.Run(string(audience), func(t *testing.T) {
			path, now := permanentDefaultStore(t)
			// No Policy, no AckNever: the allow list is the acknowledgement.
			p, err := ChangePersonWithLifetime(path, "bob", []string{"photos"}, false, PersonLifetimeOptions{Audience: audience, Now: now, PermanentDefault: true})
			if err != nil {
				t.Fatal(err)
			}
			if g := grantFor(t, p, "photos"); g.ExpiresAt != nil || g.Expired {
				t.Fatalf("new grant = %+v, want permanent", g)
			}

			// Control: the same call without the option is the people default.
			p, err = ChangePersonWithLifetime(path, "carol", []string{"photos"}, false, PersonLifetimeOptions{Audience: audience, Now: now})
			if err != nil {
				t.Fatal(err)
			}
			if g := grantFor(t, p, "photos"); g.ExpiresAt == nil || !g.ExpiresAt.Equal(now.Add(24*time.Hour)) {
				t.Fatalf("people default = %+v, want 24h", g)
			}

			// An explicit lifetime wins, with the policy checks it always had.
			seven := "7d"
			p, err = ChangePersonWithLifetime(path, "dave", []string{"photos"}, false, PersonLifetimeOptions{Audience: audience, Now: now, PermanentDefault: true, Value: &seven})
			if err != nil {
				t.Fatal(err)
			}
			if g := grantFor(t, p, "photos"); g.ExpiresAt == nil || !g.ExpiresAt.Equal(now.Add(7*24*time.Hour)) {
				t.Fatalf("explicit 7d = %+v", g)
			}
			never := "never"
			if _, err := ChangePersonWithLifetime(path, "erin", []string{"photos"}, false, PersonLifetimeOptions{Audience: audience, Now: now, PermanentDefault: true, Value: &never}); err == nil {
				t.Fatal("explicit never without AckNever accepted")
			}
		})
	}
}

func TestPermanentDefaultLeavesExistingGrantsAlone(t *testing.T) {
	path, now := permanentDefaultStore(t)
	deadline := now.Add(48 * time.Hour)
	if _, err := ChangePerson(path, "bob", []string{"photos"}, &deadline, true, false); err != nil {
		t.Fatal(err)
	}
	// photos exists with a chosen deadline; finance is new.
	p, err := ChangePersonWithLifetime(path, "bob", []string{"photos", "finance"}, false, PersonLifetimeOptions{Audience: duration.TailnetMember, Now: now, PermanentDefault: true})
	if err != nil {
		t.Fatal(err)
	}
	if g := grantFor(t, p, "photos"); g.ExpiresAt == nil || !g.ExpiresAt.Equal(deadline) {
		t.Fatalf("existing grant moved: %+v, want %v", g, deadline)
	}
	if g := grantFor(t, p, "finance"); g.ExpiresAt != nil {
		t.Fatalf("new grant = %+v, want permanent", g)
	}
	// A permanent grant is not shortened by a re-add either.
	p, err = ChangePersonWithLifetime(path, "bob", []string{"finance"}, false, PersonLifetimeOptions{Audience: duration.TailnetMember, Now: now.Add(time.Hour), PermanentDefault: true})
	if err != nil {
		t.Fatal(err)
	}
	if g := grantFor(t, p, "finance"); g.ExpiresAt != nil {
		t.Fatalf("re-add shortened a permanent grant: %+v", g)
	}
}

// A caller that could not create a permanent grant with an explicit never
// (a reduced role, or any binding that expires) never gets one by omission:
// it keeps the 24h default, which the scope check then accepts or refuses
// exactly as it does for an explicit 24h.
func TestPermanentDefaultNeverOutrunsTheCallersAuthority(t *testing.T) {
	cases := []struct {
		name    string
		session *mcpscope.Session
		tool    string
		want    string // "permanent", "24h" or "denied"
	}{
		{"no session", nil, "", "permanent"},
		{"owner without expiry", &mcpscope.Session{Who: "agent", Scope: mcpscope.Scope{Role: "owner"}}, "add", "permanent"},
		{"owner binding with room", &mcpscope.Session{Who: "agent", Scope: mcpscope.Scope{Role: "owner"}, ExpiresAt: ptrTime(time.Date(2030, 1, 4, 0, 0, 0, 0, time.UTC))}, "add", "24h"},
		{"owner binding ending within 24h", &mcpscope.Session{Who: "agent", Scope: mcpscope.Scope{Role: "owner"}, ExpiresAt: ptrTime(time.Date(2030, 1, 1, 2, 0, 0, 0, time.UTC))}, "add", "denied"},
		{"people-manager max 48h", &mcpscope.Session{Who: "agent", Scope: mcpscope.Scope{Role: "people-manager", Apps: []string{"photos"}, MaxDuration: "48h"}}, "people_grant", "24h"},
		{"people-manager max 2h", &mcpscope.Session{Who: "agent", Scope: mcpscope.Scope{Role: "people-manager", Apps: []string{"photos"}, MaxDuration: "2h"}}, "people_grant", "denied"},
		{"app-operator max 24h", &mcpscope.Session{Who: "agent", Scope: mcpscope.Scope{Role: "app-operator", Apps: []string{"photos"}, MaxDuration: "24h"}}, "people_grant", "24h"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path, now := permanentDefaultStore(t)
			ctx := context.Background()
			if tc.session != nil {
				ctx = mcpscope.WithClock(mcpscope.WithSession(ctx, *tc.session), func() time.Time { return now })
			}
			p, err := ChangePersonWithLifetime(path, "bob", []string{"photos"}, false, PersonLifetimeOptions{Context: ctx, Audience: duration.TailnetMember, Now: now, PermanentDefault: true, Tool: tc.tool})
			switch tc.want {
			case "denied":
				if err == nil {
					t.Fatalf("grant outliving the capability accepted: %+v", p)
				}
				reg, loadErr := Load(path)
				if loadErr != nil || len(reg.People) != 0 {
					t.Fatalf("refused call left people %+v (%v)", reg.People, loadErr)
				}
			case "permanent":
				if err != nil {
					t.Fatal(err)
				}
				if g := grantFor(t, p, "photos"); g.ExpiresAt != nil {
					t.Fatalf("grant = %+v, want permanent", g)
				}
			case "24h":
				if err != nil {
					t.Fatal(err)
				}
				if g := grantFor(t, p, "photos"); g.ExpiresAt == nil || !g.ExpiresAt.Equal(now.Add(24*time.Hour)) {
					t.Fatalf("grant = %+v, want 24h", g)
				}
			}
		})
	}
}

func ptrTime(t time.Time) *time.Time { return &t }

// `tslink add NAME --proxy P` without allow entries: a genuinely new
// installation starts with the gateway on and the app people-scoped (owner and
// admins plus explicit grants); an installation that existed before keeps its
// mode and the app open to the tailnet.
func TestAddWithoutAllowOnANewInstallationIsPeopleScoped(t *testing.T) {
	for _, tc := range []struct {
		name         string
		priorInstall bool
		wantGateway  bool
	}{
		{"new installation", false, true},
		{"prior installation without a registry file", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "registry.json")
			svc := Service{Name: "notes", Type: TypeProxy, Target: "http://localhost:3001"}
			if _, err := AddWithOutcome(path, svc, AddOptions{InitializeGateway: true, PriorInstall: func() (bool, error) { return tc.priorInstall, nil }}); err != nil {
				t.Fatal(err)
			}
			reg, err := Load(path)
			if err != nil {
				t.Fatal(err)
			}
			if GatewayEnabled(reg) != tc.wantGateway {
				t.Fatalf("gateway enabled = %v, want %v", GatewayEnabled(reg), tc.wantGateway)
			}
			got := reg.Services[0]
			if got.PeopleScoped != tc.wantGateway || (got.PortalRoute != nil) != tc.wantGateway || len(got.AllowedUsers) != 0 {
				t.Fatalf("notes = %+v, want people_scoped=%v with a route=%v and no allow entries", got, tc.wantGateway, tc.wantGateway)
			}
		})
	}
}
