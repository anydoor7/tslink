package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/mcpscope"
	"github.com/anydoor7/tslink/internal/registry"
	"github.com/anydoor7/tslink/internal/tailapi"
)

// `invite device` used to share the network and nothing else: the person's
// access to the app came from the app's allow list, which never expired. It
// now saves a people grant first. These tests pin that the grant it creates
// carries the allow list's meaning (no expiry unless something limits it), that
// a grant that already exists is never moved, and that a caller who could not
// create a permanent grant does not get one by omission. The command has no
// lifetime flag, so there is no explicit lifetime to test.

const inviteDeviceLogin = "alice@example.com"

var inviteDeviceEntries = []string{"cli", "mcp"}

type inviteDeviceHarness struct {
	t       *testing.T
	f       *gatewayInviteFixture
	actions mcpActions
}

func newInviteDeviceHarness(t *testing.T, member bool) *inviteDeviceHarness {
	t.Helper()
	f := newGatewayInviteFixture(t)
	f.login = inviteDeviceLogin
	f.emailMode = true
	if member {
		peopleMembershipFn = func(context.Context, string, string) (tailapi.Membership, error) {
			return tailapi.MembershipMember, nil
		}
	}
	inviteRegistryPathFn = func() (string, error) { return f.paths.Registry, nil }
	invitePIDPathFn = func() (string, error) { return f.paths.PID, nil }
	inviteRuntimeSnapshotPathFn = func() (string, error) { return f.paths.Snapshot, nil }
	return &inviteDeviceHarness{t: t, f: f, actions: defaultMCPActions(f.paths, io.Discard)}
}

// call runs one device invitation for photos through the CLI command body or
// the MCP tool, which share inviteDeviceCreate.
func (h *inviteDeviceHarness) call(entry string) error {
	h.t.Helper()
	return h.callFor(entry, "photos")
}

func (h *inviteDeviceHarness) callFor(entry, service string) error {
	h.t.Helper()
	switch entry {
	case "cli":
		var out bytes.Buffer
		return inviteDeviceRun(context.Background(), &out, service, inviteDeviceLogin, false, false, false, true)
	case "mcp":
		raw, _ := json.Marshal(map[string]any{"service": service, "email": inviteDeviceLogin})
		result, err := callMCPTool(context.Background(), h.actions, "invite_device", raw)
		if err != nil {
			return err
		}
		if result.IsError {
			wire, _ := json.Marshal(result)
			return fmt.Errorf("mcp invite_device failed: %s", wire)
		}
		return nil
	}
	h.t.Fatalf("unknown entry %q", entry)
	return nil
}

// seed saves a grant the way `people add` does, before the invitation under
// test. The fixture answers a device invitation differently in email mode, so
// the seeding call runs with it off.
func (h *inviteDeviceHarness) seed(args peopleArguments) {
	h.t.Helper()
	h.f.emailMode = false
	defer func() { h.f.emailMode = true }()
	if _, err := changePeople(context.Background(), h.f.paths, args, false); err != nil {
		h.t.Fatalf("seed grant: %v", err)
	}
}

func (h *inviteDeviceHarness) grantOn(app string) (registry.PersonGrant, bool) {
	h.t.Helper()
	person, err := readPerson(h.f.paths.Registry, inviteDeviceLogin)
	if err != nil {
		return registry.PersonGrant{}, false
	}
	for _, g := range person.Grants {
		if g.App == app {
			return g, true
		}
	}
	return registry.PersonGrant{}, false
}

func (h *inviteDeviceHarness) requirePermanent(app, why string) {
	h.t.Helper()
	g, ok := h.grantOn(app)
	if !ok {
		h.t.Fatalf("%s: no grant for %s", why, app)
	}
	if g.ExpiresAt != nil || g.Expired {
		h.t.Fatalf("%s: grant expires_at=%v expired=%v, want permanent", why, g.ExpiresAt, g.Expired)
	}
	person, _ := readPerson(h.f.paths.Registry, inviteDeviceLogin)
	if !registry.PersonGrantActiveAt(person, app, peopleNowFn().Add(10*365*24*time.Hour)) {
		h.t.Fatalf("%s: grant on %s is not active ten years on", why, app)
	}
}

func (h *inviteDeviceHarness) requireExpiry(app string, want time.Time, why string) {
	h.t.Helper()
	g, ok := h.grantOn(app)
	if !ok || g.ExpiresAt == nil {
		h.t.Fatalf("%s: grant=%+v ok=%v, want expiry %v", why, g, ok, want)
	}
	if !g.ExpiresAt.Equal(want) {
		h.t.Fatalf("%s: expires_at=%v, want %v", why, g.ExpiresAt, want)
	}
}

// allowList puts the login on the app's legacy allow list, the way an app
// registered with `add --allow` before people grants existed has it.
func (h *inviteDeviceHarness) allowList(app string) {
	h.t.Helper()
	svc, err := loadPersistedService(h.f.paths.Registry, app)
	if err != nil {
		h.t.Fatal(err)
	}
	svc.AllowedUsers = append(svc.AllowedUsers, inviteDeviceLogin)
	if _, err := registry.Add(h.f.paths.Registry, svc); err != nil {
		h.t.Fatal(err)
	}
}

func TestInviteDeviceNewPersonIsPermanent(t *testing.T) {
	for _, entry := range inviteDeviceEntries {
		for _, member := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/member=%v", entry, member), func(t *testing.T) {
				h := newInviteDeviceHarness(t, member)
				if err := h.call(entry); err != nil {
					t.Fatalf("invite device failed: %v", err)
				}
				h.requirePermanent("photos", "first invite device")
				if posts, _ := h.f.counts(); member && posts != 0 || !member && posts != 1 {
					t.Fatalf("invitation POSTs = %d (member=%v)", posts, member)
				}
				// A repeated invitation changes nothing about the grant.
				if err := h.call(entry); err != nil {
					t.Fatalf("repeat invite device failed: %v", err)
				}
				h.requirePermanent("photos", "repeat invite device")
			})
		}
	}
}

// A second app the person is not yet granted gets the same default; the first
// stays as it was.
func TestInviteDeviceNewAppForAnExistingPersonIsPermanent(t *testing.T) {
	for _, entry := range inviteDeviceEntries {
		t.Run(entry, func(t *testing.T) {
			h := newInviteDeviceHarness(t, true)
			h.seed(peopleArguments{Who: inviteDeviceLogin, Apps: []string{"finance"}, For: inviteDeviceLifetime("7d")})
			if err := h.call(entry); err != nil {
				t.Fatal(err)
			}
			h.requirePermanent("photos", "new app")
			h.requireExpiry("finance", time.Date(2030, 1, 8, 0, 0, 0, 0, time.UTC), "other app")
		})
	}
}

// The person's access before the upgrade came from the allow list. The first
// people record inherits those entries as grants without a deadline, and the
// device invitation must leave them that way.
func TestInviteDeviceKeepsAnAllowListedPersonPermanent(t *testing.T) {
	for _, entry := range inviteDeviceEntries {
		for _, member := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/member=%v", entry, member), func(t *testing.T) {
				h := newInviteDeviceHarness(t, member)
				h.allowList("photos")
				if err := h.call(entry); err != nil {
					t.Fatalf("invite device for an allow-listed person failed: %v", err)
				}
				h.requirePermanent("photos", "allow-listed app after invite device")
				// Repeating it still changes nothing.
				if err := h.call(entry); err != nil {
					t.Fatal(err)
				}
				h.requirePermanent("photos", "allow-listed app after a repeat")
			})
		}
	}
}

// Allow-listed on one app, invited to another: the inherited grant stays and
// the new app gets the default for a new grant.
func TestInviteDeviceForAnotherAppKeepsTheInheritedAllowEntry(t *testing.T) {
	h := newInviteDeviceHarness(t, true)
	h.allowList("photos")
	if err := h.callFor("cli", "finance"); err != nil {
		t.Fatal(err)
	}
	h.requirePermanent("photos", "inherited allow entry")
	h.requirePermanent("finance", "new app")
}

func TestInviteDeviceNeverMovesAnExistingGrant(t *testing.T) {
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, entry := range inviteDeviceEntries {
		for _, member := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/member=%v", entry, member), func(t *testing.T) {
				// A grant someone chose for seven days stays seven days: it is
				// neither shortened to 24h nor made permanent.
				h := newInviteDeviceHarness(t, member)
				h.seed(peopleArguments{Who: inviteDeviceLogin, Apps: []string{"photos"}, For: inviteDeviceLifetime("7d")})
				if err := h.call(entry); err != nil {
					t.Fatalf("invite device over a 7d grant failed: %v", err)
				}
				h.requireExpiry("photos", now.Add(7*24*time.Hour), "7d grant after invite device")

				// So does a 3d one (a guest's finite grants are capped at 7d).
				h = newInviteDeviceHarness(t, member)
				h.seed(peopleArguments{Who: inviteDeviceLogin, Apps: []string{"photos"}, For: inviteDeviceLifetime("3d")})
				if err := h.call(entry); err != nil {
					t.Fatal(err)
				}
				h.requireExpiry("photos", now.Add(3*24*time.Hour), "3d grant after invite device")

				// A 24h grant is not widened either.
				h = newInviteDeviceHarness(t, member)
				h.seed(peopleArguments{Who: inviteDeviceLogin, Apps: []string{"photos"}})
				if err := h.call(entry); err != nil {
					t.Fatal(err)
				}
				h.requireExpiry("photos", now.Add(24*time.Hour), "24h people grant after invite device")
			})
		}
	}
}

// A grant that has already ended stays ended: the invitation shares the
// network, it does not restore access (that takes an explicit lifetime change).
func TestInviteDeviceDoesNotReviveAnEndedGrant(t *testing.T) {
	h := newInviteDeviceHarness(t, true)
	h.seed(peopleArguments{Who: inviteDeviceLogin, Apps: []string{"photos"}, For: inviteDeviceLifetime("2h")})
	end := peopleNowFn().Add(2 * time.Hour)
	later := peopleNowFn
	peopleNowFn = func() time.Time { return end.Add(time.Hour) }
	t.Cleanup(func() { peopleNowFn = later })
	if err := h.call("cli"); err == nil {
		t.Fatal("invite device reported success for a grant that has ended")
	}
	g, ok := h.grantOn("photos")
	if !ok || g.ExpiresAt == nil || !g.ExpiresAt.Equal(end) {
		t.Fatalf("ended grant after invite device = %+v ok=%v, want it unchanged at %v", g, ok, end)
	}
}

// A binding that expires cannot mint a permanent grant by omission: the
// omitted lifetime stays the 24h default, which its expiry accepts or refuses.
// invite_device is an owner-only tool, so a reduced role never reaches the
// grant at all.
func TestInviteDeviceUnderALimitedBindingNeverMintsAPermanentGrant(t *testing.T) {
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	owner := mcpscope.Scope{Role: "owner"}
	t.Run("unrestricted owner", func(t *testing.T) {
		h := newInviteDeviceHarness(t, true)
		h.actions.session = &mcpscope.Session{Who: "agent", Scope: owner}
		if err := h.call("mcp"); err != nil {
			t.Fatal(err)
		}
		h.requirePermanent("photos", "owner binding without expiry")
	})
	t.Run("owner binding with room for 24h", func(t *testing.T) {
		h := newInviteDeviceHarness(t, true)
		expires := now.Add(72 * time.Hour)
		h.actions.session = &mcpscope.Session{Who: "agent", Scope: owner, ExpiresAt: &expires}
		if err := h.call("mcp"); err != nil {
			t.Fatal(err)
		}
		h.requireExpiry("photos", now.Add(24*time.Hour), "expiring binding")
	})
	t.Run("owner binding ending sooner than 24h", func(t *testing.T) {
		h := newInviteDeviceHarness(t, true)
		expires := now.Add(2 * time.Hour)
		h.actions.session = &mcpscope.Session{Who: "agent", Scope: owner, ExpiresAt: &expires}
		if err := h.call("mcp"); err == nil {
			t.Fatal("omitted lifetime outlived the binding and was accepted")
		}
		if g, ok := h.grantOn("photos"); ok {
			t.Fatalf("refused call left a grant: %+v", g)
		}
	})
	t.Run("owner binding with an existing grant keeps it", func(t *testing.T) {
		h := newInviteDeviceHarness(t, true)
		h.seed(peopleArguments{Who: inviteDeviceLogin, Apps: []string{"photos"}, For: inviteDeviceLifetime("7d")})
		expires := now.Add(72 * time.Hour)
		h.actions.session = &mcpscope.Session{Who: "agent", Scope: owner, ExpiresAt: &expires}
		if err := h.call("mcp"); err != nil {
			t.Fatal(err)
		}
		h.requireExpiry("photos", now.Add(7*24*time.Hour), "existing 7d grant under an expiring binding")
	})
	for _, role := range []string{"viewer", "app-operator", "people-manager"} {
		t.Run(role+" cannot invite devices", func(t *testing.T) {
			h := newInviteDeviceHarness(t, true)
			h.actions.session = &mcpscope.Session{Who: "agent", Scope: mcpscope.Scope{Role: role, Apps: []string{"photos"}}}
			if err := h.call("mcp"); err == nil {
				t.Fatal("reduced role invited a device")
			}
			if g, ok := h.grantOn("photos"); ok {
				t.Fatalf("refused call left a grant: %+v", g)
			}
		})
	}
}

func inviteDeviceLifetime(v string) *string { return &v }
