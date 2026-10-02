package registry

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/duration"
)

func TestFinalPublicLifetimeAfterPreservation(t *testing.T) {
	for _, tc := range []struct {
		name                                                             string
		public, legacyNever, expired, invalid, newService, privateResult bool
	}{
		{name: "private_absent_default"},
		{name: "private_stale_metadata", expired: true},
		{name: "public_deadline_preserved", public: true},
		{name: "public_never_preserved", public: true, legacyNever: true},
		{name: "expired_public_uses_resolved_default", public: true, expired: true},
		{name: "private_never_rejected", invalid: true},
		{name: "new_public_never_rejected", invalid: true, newService: true},
		{name: "rearmed_never_rejected", public: true, expired: true, invalid: true},
		{name: "private_result", privateResult: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := testRegistryPath(t)
			now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
			old := now.Add(72 * time.Hour)
			if tc.expired {
				old = now.Add(-time.Hour)
			}
			existing := Service{Name: "preview", Type: TypeProxy, Target: "http://localhost:3000", Funnel: tc.public, PublicAck: tc.public}
			if tc.public && !tc.legacyNever || tc.expired {
				existing.FunnelExpiresAt = &old
			}
			if !tc.newService {
				if _, err := Add(path, existing); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := os.ReadFile(path)
			deadline := now.Add(24 * time.Hour)
			requested := Service{Name: "preview", Type: TypeProxy, Target: "http://localhost:4000", Funnel: !tc.privateResult, PublicAck: !tc.privateResult, FunnelExpiresAt: &deadline}
			if tc.invalid {
				requested.FunnelExpiresAt = nil
			}
			// A tighter current cap must not retroactively shorten public choices.
			policy := duration.Policy{PublicMax: 24 * time.Hour}
			out, err := AddWithOutcome(path, requested, AddOptions{PreserveFunnelExpiry: true, LifetimePolicy: &policy, Now: now})
			if tc.invalid {
				if err == nil || !strings.Contains(err.Error(), "stored Funnel lifetime: never is allowed only") {
					t.Fatal(err)
				}
				after, _ := os.ReadFile(path)
				if string(before) != string(after) {
					t.Fatal("refusal overwrote registry")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			reg, err := Load(path)
			if err != nil {
				t.Fatal(err)
			}
			got := reg.Services[0].FunnelExpiresAt
			if tc.legacyNever {
				if got != nil {
					t.Fatal("legacy never changed")
				}
				return
			}
			want := deadline
			if tc.public && !tc.expired {
				want = old
			}
			if got == nil || !got.Equal(want) {
				t.Fatalf("final deadline=%v want=%v", got, want)
			}
			if out.RearmedExpiredFunnel != (tc.public && tc.expired) {
				t.Fatal(out)
			}
		})
	}
	for _, s := range []Service{{Funnel: true, PublicAck: true, funnelExpiryUndecided: true}, {Funnel: true}, {PublicAck: true}} {
		if s.HasDecidedPublicLifetime() {
			t.Fatal("absent public decision classified as decided", s)
		}
	}
}

func TestGuestClassificationPersistsBeforeInviteAndAfterReAdd(t *testing.T) {
	path, now := durationStore(t)
	value := "3d"
	p, err := ChangePersonWithLifetime(path, "alice", nil, true, PersonLifetimeOptions{Value: &value, Audience: duration.Guest, Now: now})
	if err != nil || !p.Guest || len(p.Invites) != 0 {
		t.Fatalf("first guest grant: %+v %v", p, err)
	}
	reg, _, err := Preflight(path)
	if err != nil || !reg.People[0].Guest {
		t.Fatal("marker lost on restart", err)
	}
	before, _ := os.ReadFile(path)
	for _, v := range []string{"never", "7d1s"} {
		_, err := ChangePersonWithLifetime(path, "alice", nil, true, PersonLifetimeOptions{Value: &v, Audience: duration.TailnetMember, AckNever: true, Now: now})
		if err == nil || !(strings.Contains(err.Error(), "only for tailnet-member") || strings.Contains(err.Error(), "exceeds")) {
			t.Fatal(err)
		}
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("guest refusal wrote registry")
	}
	// A retry after almost all of the deadline elapsed preserves the deadline;
	// its classification cannot depend on whether REST succeeded or left history.
	p, err = ChangePersonWithLifetime(path, "alice", nil, true, PersonLifetimeOptions{Audience: duration.Guest, Now: now.Add(71 * time.Hour)})
	if err != nil || !p.Grants[0].ExpiresAt.Equal(now.Add(72*time.Hour)) {
		t.Fatal(p, err)
	}
	if _, err := RemovePerson(path, "alice"); err != nil {
		t.Fatal(err)
	}
	p, err = ChangePersonWithLifetime(path, "alice", []string{"photos"}, false, PersonLifetimeOptions{Audience: duration.TailnetMember, Now: now})
	if err != nil || !p.Guest || p.Revoked {
		t.Fatal("re-add lost guest marker", p, err)
	}
	if _, err := ExtendDuration(path, ExtendOptions{Service: "photos", Who: "alice", Value: "never", AckNever: true, Now: now}); err == nil || !strings.Contains(err.Error(), "only for tailnet-member") {
		t.Fatal(err)
	}
}

func TestInviteGrantCASPreservesLateOutcomes(t *testing.T) {
	path, now := durationStore(t)
	v := "3d"
	p, err := ChangePersonWithLifetime(path, "alice", nil, true, PersonLifetimeOptions{Value: &v, Audience: duration.Guest, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	g := p.Grants[1] // photos
	op := PersonInvite{App: "photos", Hostname: "photos", NodeID: "n1", State: PersonInvitePending}
	opts := PersonInviteSaveOptions{Now: now, ExpectedGrant: &g}
	if err := SavePersonInviteWithOptions(path, "alice", op, opts); err != nil {
		t.Fatal("unchanged CAS control", err)
	}
	old := op
	op.State = PersonInviteSending
	opts.Expected = &old
	if _, err := ExtendDuration(path, ExtendOptions{Service: "photos", Who: "alice", Value: "4d", Now: now}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	if err := SavePersonInviteWithOptions(path, "alice", op, opts); err == nil || !strings.Contains(err.Error(), "classification or grant changed") {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("CAS failure overwrote registry")
	}
	// Classifications also participate, even if the grant itself is identical.
	reg, _ := Load(path)
	reg.People[0].Guest, reg.People[0].Invites = false, nil
	if err := save(path, reg); err != nil {
		t.Fatal(err)
	}
	op.State = PersonInvitePending
	opts.Expected = nil
	current := reg.People[0].Grants[1]
	opts.ExpectedGrant = &current
	if err := SavePersonInviteWithOptions(path, "alice", op, opts); err == nil || !strings.Contains(err.Error(), "classification or grant changed") {
		t.Fatal(err)
	}
	// Late POST evidence must survive a concurrent removal, as in F1.
	op.State, op.ID = PersonInviteComplete, "100"
	if _, err := RemovePerson(path, "alice"); err != nil {
		t.Fatal(err)
	}
	if err := SavePersonInviteWithOptions(path, "alice", op, opts); err != nil {
		t.Fatal("late outcome lost", err)
	}
}
