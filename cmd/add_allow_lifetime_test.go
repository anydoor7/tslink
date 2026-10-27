package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/mcpscope"
	"github.com/anydoor7/tslink/internal/registry"
	"github.com/anydoor7/tslink/internal/tailapi"
)

// `add --allow` is an allow-list write that has always been permanent. The
// people flow it now goes through defaults a new grant to 24h; that default
// belongs to `people add`, not to the older command. These tests pin the
// split: add/share allow recipients are permanent unless the caller limits
// them, people add stays 24h, and a limited MCP binding never mints a
// permanent grant.

const addAllowLogin = "alice@example.com"

var addAllowEntries = []string{"cli-add", "mcp-add", "mcp-share"}

type addAllowHarness struct {
	t       *testing.T
	f       *gatewayInviteFixture
	actions mcpActions
	member  bool
}

func newAddAllowHarness(t *testing.T, member bool) *addAllowHarness {
	t.Helper()
	f := newGatewayInviteFixture(t)
	f.login = addAllowLogin
	if member {
		peopleMembershipFn = func(context.Context, string, string) (tailapi.Membership, error) {
			return tailapi.MembershipMember, nil
		}
	}
	restoreShareSeams(t)
	shareIsRunningFn = func(string) bool { return true }
	oldEnsure, oldReg := ensureDaemonFn, registryPathFn
	t.Cleanup(func() { ensureDaemonFn, registryPathFn = oldEnsure, oldReg })
	ensureDaemonFn = func(context.Context, io.Writer, bool) error { return nil }
	registryPathFn = func() (string, error) { return f.paths.Registry, nil }
	return &addAllowHarness{t: t, f: f, actions: defaultMCPActions(f.paths, io.Discard), member: member}
}

// lifetime holds the caller's explicit lifetime fields by their CLI names:
// for, until, ack-never. Empty means the caller gave none.
func (h *addAllowHarness) call(entry string, lifetime map[string]string) error {
	h.t.Helper()
	switch entry {
	case "cli-add":
		flags := map[string]string{"proxy": "localhost:3000", "allow": addAllowLogin}
		for k, v := range lifetime {
			flags[k] = v
		}
		_, err := runAddCmdOutput(h.t, []string{"photos"}, flags)
		return err
	case "mcp-add", "mcp-share":
		args := map[string]any{"allow": []string{addAllowLogin}}
		if entry == "mcp-add" {
			args["name"], args["type"], args["target"] = "photos", "proxy", "localhost:3000"
		} else {
			args["name"], args["target"], args["ephemeral"] = "photos", "3000", false
		}
		for k, v := range lifetime {
			if k == "ack-never" {
				args["ack_never"] = v == "true"
			} else {
				args[k] = v
			}
		}
		raw, _ := json.Marshal(args)
		tool := strings.TrimPrefix(entry, "mcp-")
		result, err := callMCPTool(context.Background(), h.actions, tool, raw)
		if err != nil {
			return err
		}
		if result.IsError {
			wire, _ := json.Marshal(result)
			return fmt.Errorf("mcp %s failed: %s", tool, wire)
		}
		return nil
	}
	h.t.Fatalf("unknown entry %q", entry)
	return nil
}

func (h *addAllowHarness) grant() (registry.PersonGrant, bool) {
	h.t.Helper()
	person, err := readPerson(h.f.paths.Registry, addAllowLogin)
	if err != nil {
		return registry.PersonGrant{}, false
	}
	for _, g := range person.Grants {
		if g.App == "photos" {
			return g, true
		}
	}
	return registry.PersonGrant{}, false
}

func requirePermanentGrant(t *testing.T, h *addAllowHarness, why string) {
	t.Helper()
	g, ok := h.grant()
	if !ok {
		t.Fatalf("%s: no grant for photos", why)
	}
	if g.ExpiresAt != nil || g.Expired {
		t.Fatalf("%s: grant expires_at=%v expired=%v, want permanent", why, g.ExpiresAt, g.Expired)
	}
	person, _ := readPerson(h.f.paths.Registry, addAllowLogin)
	if !registry.PersonGrantActiveAt(person, "photos", peopleNowFn().Add(10*365*24*time.Hour)) {
		t.Fatalf("%s: grant is not active ten years on", why)
	}
}

func requireGrantExpiry(t *testing.T, h *addAllowHarness, want time.Time, why string) {
	t.Helper()
	g, ok := h.grant()
	if !ok || g.ExpiresAt == nil {
		t.Fatalf("%s: grant=%+v ok=%v, want expiry %v", why, g, ok, want)
	}
	if !g.ExpiresAt.Equal(want) {
		t.Fatalf("%s: expires_at=%v, want %v", why, g.ExpiresAt, want)
	}
}

func TestAddAllowWithoutLifetimeIsPermanent(t *testing.T) {
	for _, entry := range addAllowEntries {
		for _, member := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/member=%v", entry, member), func(t *testing.T) {
				h := newAddAllowHarness(t, member)
				// No --for, no --until, no --ack-never: the allow list is the
				// acknowledgement, as it always was.
				if err := h.call(entry, nil); err != nil {
					t.Fatalf("add with allow and no lifetime failed: %v", err)
				}
				requirePermanentGrant(t, h, "first add")
				if posts, _ := h.f.counts(); member && posts != 0 || !member && posts != 1 {
					t.Fatalf("invitation POSTs = %d (member=%v)", posts, member)
				}
			})
		}
	}
}

func TestAddAllowExplicitLifetimeLimitsTheGrant(t *testing.T) {
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, entry := range addAllowEntries {
		t.Run(entry, func(t *testing.T) {
			h := newAddAllowHarness(t, true)
			if err := h.call(entry, map[string]string{"for": "7d"}); err != nil {
				t.Fatal(err)
			}
			requireGrantExpiry(t, h, now.Add(7*24*time.Hour), "--for 7d")

			h = newAddAllowHarness(t, true)
			if err := h.call(entry, map[string]string{"until": "2030-02-01T00:00:00Z"}); err != nil {
				t.Fatal(err)
			}
			requireGrantExpiry(t, h, time.Date(2030, 2, 1, 0, 0, 0, 0, time.UTC), "--until")

			// An explicit never keeps the acknowledgement it always needed.
			h = newAddAllowHarness(t, true)
			if err := h.call(entry, map[string]string{"for": "never"}); err == nil {
				t.Fatal("explicit never without ack_never accepted")
			}
			if _, ok := h.grant(); ok {
				t.Fatal("refused explicit never left a grant")
			}
			h = newAddAllowHarness(t, true)
			if err := h.call(entry, map[string]string{"for": "never", "ack-never": "true"}); err != nil {
				t.Fatal(err)
			}
			requirePermanentGrant(t, h, "explicit never with ack")
		})
	}
}

func TestAddAllowReAddNeverShortensAnExistingGrant(t *testing.T) {
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, entry := range addAllowEntries {
		t.Run(entry, func(t *testing.T) {
			h := newAddAllowHarness(t, true)
			if err := h.call(entry, nil); err != nil {
				t.Fatal(err)
			}
			if err := h.call(entry, nil); err != nil {
				t.Fatalf("re-add: %v", err)
			}
			requirePermanentGrant(t, h, "re-add of a permanent grant")

			// A finite grant someone chose is neither shortened to a default
			// nor silently made permanent by a re-add without a lifetime.
			h = newAddAllowHarness(t, true)
			if err := h.call(entry, map[string]string{"for": "30d"}); err != nil {
				t.Fatal(err)
			}
			if err := h.call(entry, nil); err != nil {
				t.Fatalf("re-add: %v", err)
			}
			requireGrantExpiry(t, h, now.Add(30*24*time.Hour), "re-add of a 30d grant")

			// An explicit lifetime on the re-add is the owner's decision.
			if err := h.call(entry, map[string]string{"for": "7d"}); err != nil {
				t.Fatal(err)
			}
			requireGrantExpiry(t, h, now.Add(7*24*time.Hour), "re-add with --for 7d")
		})
	}
}

// The permanent default is add/share's, not people add's.
func TestPeopleAddKeepsTheTwentyFourHourDefault(t *testing.T) {
	for _, member := range []bool{true, false} {
		t.Run(fmt.Sprintf("member=%v", member), func(t *testing.T) {
			h := newAddAllowHarness(t, member)
			result, err := changePeople(context.Background(), h.f.paths, peopleArguments{Who: addAllowLogin, Apps: []string{"photos"}}, false)
			if err != nil || !result.Complete {
				t.Fatalf("people add: %+v %v", result, err)
			}
			requireGrantExpiry(t, h, time.Date(2030, 1, 2, 0, 0, 0, 0, time.UTC), "people add default")
		})
	}
}

// A binding that expires (or any non-owner scope) cannot mint a permanent
// grant. Omitted lifetime on add/share then keeps the 24h default that
// existed before, which the scope check accepts or refuses like any other
// explicit 24h grant.
func TestAddAllowUnderALimitedBindingNeverMintsAPermanentGrant(t *testing.T) {
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	owner := mcpscope.Scope{Role: "owner"}
	for _, entry := range []string{"mcp-add", "mcp-share"} {
		t.Run(entry+"/unrestricted owner", func(t *testing.T) {
			h := newAddAllowHarness(t, true)
			h.actions.session = &mcpscope.Session{Who: "agent", Scope: owner}
			if err := h.call(entry, nil); err != nil {
				t.Fatal(err)
			}
			requirePermanentGrant(t, h, "owner binding without expiry")
		})
		t.Run(entry+"/owner binding with room for 24h", func(t *testing.T) {
			h := newAddAllowHarness(t, true)
			expires := now.Add(72 * time.Hour)
			h.actions.session = &mcpscope.Session{Who: "agent", Scope: owner, ExpiresAt: &expires}
			if err := h.call(entry, nil); err != nil {
				t.Fatal(err)
			}
			requireGrantExpiry(t, h, now.Add(24*time.Hour), "expiring binding, omitted lifetime")
		})
		t.Run(entry+"/owner binding ending sooner than 24h", func(t *testing.T) {
			h := newAddAllowHarness(t, true)
			expires := now.Add(2 * time.Hour)
			h.actions.session = &mcpscope.Session{Who: "agent", Scope: owner, ExpiresAt: &expires}
			if err := h.call(entry, nil); err == nil {
				t.Fatal("omitted lifetime outlived the binding and was accepted")
			}
			if g, ok := h.grant(); ok {
				t.Fatalf("refused call left a grant: %+v", g)
			}
		})
		t.Run(entry+"/expiring binding refuses an explicit never", func(t *testing.T) {
			h := newAddAllowHarness(t, true)
			expires := now.Add(72 * time.Hour)
			h.actions.session = &mcpscope.Session{Who: "agent", Scope: owner, ExpiresAt: &expires}
			if err := h.call(entry, map[string]string{"for": "never", "ack-never": "true"}); err == nil {
				t.Fatal("expiring binding minted an explicit permanent grant")
			}
			if g, ok := h.grant(); ok {
				t.Fatalf("refused call left a grant: %+v", g)
			}
		})
	}
}

// The same transition through add/share: the person was a member when the
// owner allowed them (a permanent grant, as the allow list always was) and the
// membership lookup now calls them an outsider, so the next `add --allow` turns
// their access into guest access.

func (h *addAllowHarness) person() registry.Person {
	h.t.Helper()
	p, err := readPerson(h.f.paths.Registry, addAllowLogin)
	if err != nil {
		h.t.Fatal(err)
	}
	return p
}

// requireRefusedUntouched checks that a refused transition named the guard (or
// the guest maximum), sent nothing, and left the person's record as it was.
func (h *addAllowHarness) requireRefusedUntouched(err error, before registry.Person, reason string) {
	h.t.Helper()
	if err == nil {
		h.t.Fatalf("add was accepted; grants now %+v", h.person().Grants)
	}
	if !strings.Contains(err.Error(), reason) {
		h.t.Fatalf("error = %v, want it to mention %q", err, reason)
	}
	if got := h.person(); !reflect.DeepEqual(got, before) {
		h.t.Fatalf("refused add changed the person:\n got %+v\nwant %+v", got, before)
	}
	if posts, _ := h.f.counts(); posts != 0 {
		h.t.Fatalf("refused add sent %d invitation(s)", posts)
	}
}

func TestAddAllowFirstGuestInvitationKeepsAPermanentMemberGrant(t *testing.T) {
	for _, entry := range addAllowEntries {
		t.Run(entry, func(t *testing.T) {
			h := newAddAllowHarness(t, true)
			if err := h.call(entry, nil); err != nil {
				t.Fatal(err)
			}
			requirePermanentGrant(t, h, "member's allow-list grant")
			if registry.PersonIsGuest(h.person()) {
				t.Fatal("the member is already a guest")
			}
			before, _ := h.grant()

			lookupSaysOutsider()
			if err := h.call(entry, nil); err != nil {
				t.Fatalf("re-add for a permanent grant, now an outsider: %v", err)
			}

			requirePermanentGrant(t, h, "grant after the transition")
			if after, _ := h.grant(); !reflect.DeepEqual(after, before) {
				t.Fatalf("grant changed by the transition: %+v, want %+v", after, before)
			}
			if !registry.PersonIsGuest(h.person()) {
				t.Fatal("the re-add did not turn the person into a guest, so the transition was not exercised")
			}
			if posts, _ := h.f.counts(); posts != 1 {
				t.Fatalf("invitation POSTs = %d, want 1", posts)
			}
		})
	}
}

func TestAddAllowFirstGuestInvitationRefusesAnExpiringBindingOverAPermanentGrant(t *testing.T) {
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	owner := mcpscope.Scope{Role: "owner"}
	for _, entry := range []string{"mcp-add", "mcp-share"} {
		for name, ends := range map[string]time.Time{"binding with room for 24h": now.Add(72 * time.Hour), "binding ending sooner than 24h": now.Add(2 * time.Hour)} {
			t.Run(entry+"/"+name, func(t *testing.T) {
				expires := ends
				session := &mcpscope.Session{Who: "agent", Scope: owner, ExpiresAt: &expires}

				// Control: while the person is still a member the same re-add, with
				// the same binding, leaves the permanent grant alone.
				h := newAddAllowHarness(t, true)
				if err := h.call(entry, nil); err != nil {
					t.Fatal(err)
				}
				h.actions.session = session
				if err := h.call(entry, nil); err != nil {
					t.Fatalf("control (still a member): %v", err)
				}
				requirePermanentGrant(t, h, "control grant")

				// The transition: the binding cannot create a permanent grant, so
				// it must not carry one across to guest access.
				h = newAddAllowHarness(t, true)
				if err := h.call(entry, nil); err != nil {
					t.Fatal(err)
				}
				requirePermanentGrant(t, h, "member's allow-list grant")
				before := h.person()
				lookupSaysOutsider()
				h.actions.session = session
				err := h.call(entry, nil)
				h.requireRefusedUntouched(err, before, "first guest invitation")
				requirePermanentGrant(t, h, "grant after the refused transition")
			})
		}
	}
}

func TestAddAllowFirstGuestInvitationStillEnforcesTheGuestMaximum(t *testing.T) {
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, entry := range addAllowEntries {
		t.Run(entry, func(t *testing.T) {
			// An omitted lifetime cannot carry a member's 30d grant across.
			h := newAddAllowHarness(t, true)
			if err := h.call(entry, map[string]string{"for": "30d"}); err != nil {
				t.Fatal(err)
			}
			before := h.person()
			lookupSaysOutsider()
			err := h.call(entry, nil)
			h.requireRefusedUntouched(err, before, "exceeds")
			requireGrantExpiry(t, h, now.Add(30*24*time.Hour), "grant after the refused transition")

			// Nor can an explicit request for more than a guest may have, even for
			// a person whose grant is permanent.
			h = newAddAllowHarness(t, true)
			if err := h.call(entry, nil); err != nil {
				t.Fatal(err)
			}
			before = h.person()
			lookupSaysOutsider()
			err = h.call(entry, map[string]string{"for": "30d"})
			h.requireRefusedUntouched(err, before, "exceeds")
			requirePermanentGrant(t, h, "grant after the refused explicit request")

			// A request within the maximum is the owner's decision and is honored.
			h = newAddAllowHarness(t, true)
			if err := h.call(entry, nil); err != nil {
				t.Fatal(err)
			}
			lookupSaysOutsider()
			if err := h.call(entry, map[string]string{"for": "7d"}); err != nil {
				t.Fatalf("explicit 7d for a new guest: %v", err)
			}
			requireGrantExpiry(t, h, now.Add(7*24*time.Hour), "explicit 7d after the transition")
		})
	}
}
