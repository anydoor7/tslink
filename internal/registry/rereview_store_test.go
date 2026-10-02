package registry

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReReviewSupportedIdentityShapes(t *testing.T) {
	for _, s := range []string{"octo-cat@github", "bob-builder@passkey", "a.b_c+d@example.com", "o'connor@example.com", "ops!team@example.com", "alice~team@example.com"} {
		actual, e := NormalizePerson(s)
		t.Logf("login=%q normalized=%q err=%v", s, actual, e)
		if e != nil {
			t.Errorf("valid ASCII IdP login rejected: %q", s)
		}
	}
	// The orchestrator accepts every nonempty login without controls/whitespace.
	if _, e := NormalizePerson("tag:server"); e != nil {
		t.Fatal(e)
	}
}
func TestReReviewStoreTransitionSafety(t *testing.T) {
	path := peopleFixture(t)
	deadline := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, e := ChangePerson(path, "alice", []string{"photos"}, &deadline, true, false); e != nil {
		t.Fatal(e)
	}
	_, e := ExpirePeople(path, deadline)
	if e != nil {
		t.Fatal(e)
	}
	op := PersonInvite{App: "photos", Hostname: "photos", NodeID: "n1", State: PersonInviteSending}
	err := SavePersonInvite(path, "alice", op)
	t.Logf("SavePersonInvite sending after durable expiry returned %v", err)
	if err == nil {
		t.Error("exported store starts sending for expired grant despite active-grant contract")
	}
	// Renew first so grant-expiry refusal cannot mask the ambiguity guard.
	if _, e := ChangePerson(path, "alice", nil, nil, true, true); e != nil {
		t.Fatal(e)
	}
	op.State = PersonInviteUnknown
	if e := SavePersonInvite(path, "alice", op); e != nil {
		t.Fatal(e)
	}
	op.State = PersonInvitePending
	err = SavePersonInvite(path, "alice", op)
	t.Logf("SavePersonInvite unknown->pending without reconciliation returned %v", err)
	if err == nil || !strings.Contains(err.Error(), "ambiguous invite send") {
		t.Error("store did not refuse the ambiguous reset for the correct reason", err)
	}
}

// These are the parent's actual bytes, not a reserialized approximation.
func TestReReviewParentRegistryUpgrade(t *testing.T) {
	bytesFromParent, err := os.ReadFile("testdata/parent-955b394-people.json")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "registry.json")
	if err := os.WriteFile(path, bytesFromParent, 0600); err != nil {
		t.Fatal(err)
	}
	reg, issues, err := Preflight(path)
	if err != nil || len(issues) != 0 {
		t.Fatal(reg, issues, err)
	}
	actual, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(actual, bytesFromParent) {
		t.Fatal("upgrade read changed parent bytes", err)
	}
	allowed, authoritative := PeopleAccessAt(reg, reg.Services[0], " O'CONNOR@EXAMPLE.COM ", nil, time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC))
	if !allowed || !authoritative {
		t.Fatal("parent login stopped matching")
	}
	if removed, err := RemovePerson(path, "O'CONNOR@EXAMPLE.COM"); err != nil || !removed {
		t.Fatal(removed, err)
	}
	reg, _, err = Preflight(path)
	if err != nil || !reg.People[0].Revoked {
		t.Fatal(reg, err)
	}
}

func TestPeopleExactIdentityBytes(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{" \tALICE\r\n\v\f", "alice"}, {"o'CONNOR@", "o'connor@"}, {"ops!TEAM@", "ops!team@"}, {"alice~TEAM@", "alice~team@"},
		{"\u212aELLY@", "\u212aelly@"}, {"\u0130RENE@", "\u0130rene@"}, {"张三@例子.公司", "张三@例子.公司"},
		{"É@", "É@"}, {"é@", "é@"}, {"e\u0301@", "e\u0301@"}, {"a/b:c,\\d@", "a/b:c,\\d@"}, {"tag:server", "tag:server"},
	} {
		got, err := NormalizePerson(tc.input)
		if err != nil || got != tc.want {
			t.Fatal(tc, got, err)
		}
	}
	path := peopleFixture(t)
	for _, login := range []string{"É@", "é@", "e\u0301@", "\u212aelly@", "kelly@", "\u0130rene@", "irene@"} {
		if _, err := ChangePerson(path, login, []string{"photos"}, nil, false, false); err != nil {
			t.Fatal(login, err)
		}
	}
	reg, _, err := Preflight(path)
	if err != nil || len(reg.People) != 7 {
		t.Fatal(reg, err)
	}
	for _, bad := range []string{"\u00a0alice", "alice\u00a0", "a\u2003b", "a\x00b", "a\u0080b", "a\x7fb"} {
		if _, err := NormalizePerson(bad); err == nil {
			t.Fatal("invalid login accepted", bad)
		}
	}
}

func TestPeopleInviteStoreClockCASAndHistory(t *testing.T) {
	path := peopleFixture(t)
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	deadline := now.Add(time.Hour)
	if _, err := ChangePerson(path, "alice", []string{"photos"}, &deadline, true, false); err != nil {
		t.Fatal(err)
	}
	op := PersonInvite{App: "photos", Hostname: "photos", NodeID: "n1", State: PersonInviteSending}
	if err := SavePersonInviteWithOptions(path, "alice", op, PersonInviteSaveOptions{Now: deadline}); err == nil {
		t.Fatal("unlatched deadline allowed send")
	}
	if err := SavePersonInviteWithOptions(path, "alice", op, PersonInviteSaveOptions{}); err == nil {
		t.Fatal("zero clock accepted")
	}
	if err := SavePersonInviteWithOptions(path, "alice", op, PersonInviteSaveOptions{Now: now, Expected: &op}); err == nil {
		t.Fatal("missing expected attempt accepted")
	}
	if err := SavePersonInviteWithOptions(path, "alice", op, PersonInviteSaveOptions{Now: now}); err != nil {
		t.Fatal(err)
	}
	prior := op
	op.State = PersonInviteUnknown
	if err := SavePersonInviteWithOptions(path, "alice", op, PersonInviteSaveOptions{Now: now, Expected: &prior}); err != nil {
		t.Fatal(err)
	}
	stale := prior
	if err := SavePersonInviteWithOptions(path, "alice", op, PersonInviteSaveOptions{Now: now, Expected: &stale}); err == nil {
		t.Fatal("stale CAS accepted")
	}
	badSend := op
	badSend.State = PersonInviteSending
	if err := SavePersonInvite(path, "alice", badSend); err == nil {
		t.Fatal("unknown send restarted without reconciliation")
	}
	badHost := op
	badHost.Hostname = "foreign"
	if err := SavePersonInvite(path, "alice", badHost); err == nil {
		t.Fatal("ownership hostname changed")
	}
	prior = op
	op.State = PersonInvitePending
	if err := SavePersonInviteWithOptions(path, "alice", op, PersonInviteSaveOptions{Now: now, Expected: &prior}); err == nil {
		t.Fatal("unknown reset without confirmation accepted")
	}
	if err := SavePersonInviteWithOptions(path, "alice", op, PersonInviteSaveOptions{Now: now, Expected: &prior, ResetConfirmed: true}); err != nil {
		t.Fatal(err)
	}
	op.State, op.ID = PersonInviteComplete, "1001"
	if err := SavePersonInviteWithOptions(path, "alice", op, PersonInviteSaveOptions{Now: now}); err != nil {
		t.Fatal(err)
	}
	bad := op
	bad.ID = "1002"
	if err := SavePersonInvite(path, "alice", bad); err == nil {
		t.Fatal("successful ID overwritten")
	}
	op.State = PersonInviteReplaced
	if err := SavePersonInvite(path, "alice", op); err != nil {
		t.Fatal(err)
	}
	bad = op
	bad.State = PersonInviteRevoked
	if err := SavePersonInvite(path, "alice", bad); err == nil {
		t.Fatal("retired attempt overwritten")
	}
	next := PersonInvite{App: "photos", Hostname: "photos", NodeID: "n1", State: PersonInviteSending, Attempt: 1}
	if err := SavePersonInviteWithOptions(path, "alice", next, PersonInviteSaveOptions{Now: now}); err != nil {
		t.Fatal(err)
	}
	duplicate := next
	duplicate.Attempt = 2
	if err := SavePersonInviteWithOptions(path, "alice", duplicate, PersonInviteSaveOptions{Now: now}); err == nil {
		t.Fatal("two active attempts admitted")
	}
	reg, _, err := Preflight(path)
	if err != nil || len(reg.People[0].Invites) != 2 || reg.People[0].Invites[0] != op {
		t.Fatal(reg, err)
	}
}

func TestReReviewParentUnusualLoginUpgrade(t *testing.T) {
	raw, err := os.ReadFile("testdata/parent-955b394-identities.json")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "registry.json")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	reg, issues, err := Preflight(path)
	if err != nil || len(issues) != 0 || len(reg.People) != 5 {
		t.Fatal(reg, issues, err)
	}
	actual, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(raw, actual) {
		t.Fatal("parent bytes rewritten", err)
	}
	for _, p := range reg.People {
		if allowed, authoritative := PeopleAccessAt(reg, reg.Services[0], p.Login, nil, time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)); !allowed || !authoritative {
			t.Fatal("parent key no longer matches", p)
		}
		if removed, err := RemovePerson(path, p.Login); err != nil || !removed {
			t.Fatal("parent key no longer removable", p, err)
		}
	}
	for _, invalid := range []string{"alice\u00a0team@", "alice\u0080team@"} {
		fresh := peopleFixture(t)
		if _, err := ChangePerson(fresh, "unrelated", []string{"finance"}, nil, false, false); err != nil {
			t.Fatal(err)
		}
		if _, err := ChangePerson(fresh, invalid, []string{"photos"}, nil, false, false); err == nil {
			t.Fatal("new invalid login admitted through legacy compatibility", invalid)
		}
		if _, err := RemovePerson(fresh, invalid); err == nil {
			t.Fatal("legacy compatibility created an unrecorded invalid key", invalid)
		}
		reg, _, err := Preflight(fresh)
		if err != nil {
			t.Fatal(err)
		}
		if ok, authoritative := PeopleAccessAt(reg, reg.Services[0], invalid, nil, time.Now()); ok || !authoritative {
			t.Fatal("unmanaged malformed login passed", ok, authoritative)
		}
	}
}

func TestPeopleMalformedInputWithoutLegacyRegistry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.json")
	if _, err := ResolvePersonLogin(path, "bad login"); err == nil {
		t.Fatal("missing registry admitted malformed input")
	}
	path = peopleFixture(t)
	if err := SavePersonInvite(path, "bad login", PersonInvite{}); err == nil {
		t.Fatal("malformed store identity accepted")
	}
}
