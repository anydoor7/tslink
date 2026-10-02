package registry

import (
	"math"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/mcpscope"
)

func TestScopedPeoplePreserveOtherAppsAndOwnerTombstone(t *testing.T) {
	path := peopleFixture(t)
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	s := mcpscope.Session{Who: "agent", Scope: mcpscope.Scope{Role: "people-manager", Apps: []string{"photos"}, MaxDuration: "2h"}}
	d := now.Add(24 * time.Hour)
	if _, err := ChangePerson(path, "alice", []string{"finance"}, &d, true, false); err != nil {
		t.Fatal(err)
	}
	reg, _ := Load(path)
	original := reg.People[0].Grants[0]
	p, err := ChangePersonApp(path, s, "alice", "photos", "1h", false, clock)
	if err != nil || !PersonGrantActiveAt(p, "photos", now) || !PersonGrantActiveAt(p, "finance", now) {
		t.Fatal(p, err)
	}
	for _, g := range p.Grants {
		if g.App == "finance" && !reflect.DeepEqual(g, original) {
			t.Fatal("finance changed", g)
		}
	}
	p, err = ChangePersonApp(path, s, "alice", "photos", "", true, clock)
	if err != nil || PersonGrantActiveAt(p, "photos", now) || !PersonGrantActiveAt(p, "finance", now) || p.Revoked {
		t.Fatal(p, err)
	}
	reg, _ = Load(path)
	for _, svc := range reg.Services {
		if svc.Name == "photos" {
			allowed, authoritative := PeopleAccessAt(reg, svc, "alice", nil, now)
			if allowed || !authoritative || !svc.PeopleScoped {
				t.Fatal("revocation did not deny", allowed, authoritative)
			}
		}
	}
	if _, err := ChangePersonApp(path, s, "bob", "photos", "1h", false, clock); err != nil {
		t.Fatal(err)
	}
	if _, err := RemovePerson(path, "alice"); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	if _, err := ChangePersonApp(path, s, "alice", "photos", "1h", false, clock); err == nil {
		t.Fatal("agent resurrected tombstone")
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("refusal wrote registry")
	}
}

func TestScopedRegistryDenialsAndExpiryInsideLock(t *testing.T) {
	path := peopleFixture(t)
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	s := mcpscope.Session{Who: "agent", Scope: mcpscope.Scope{Role: "people-manager", Apps: []string{"photos", "db", "pub", "missing"}, MaxDuration: "2h"}}
	for _, tc := range []struct {
		app, forValue string
		session       mcpscope.Session
		who           string
	}{
		{"finance", "1h", s, "alice"}, {"db", "1h", s, "alice"}, {"pub", "1h", s, "alice"}, {"missing", "1h", s, "alice"},
		{"photos", "never", s, "alice"}, {"photos", "3h", s, "alice"}, {"photos", "1h", s, "bad user"},
		{"photos", "1h", mcpscope.Session{Scope: mcpscope.Scope{Role: "viewer", Apps: []string{"photos"}}}, "alice"},
	} {
		before, _ := os.ReadFile(path)
		if _, err := ChangePersonApp(path, tc.session, tc.who, tc.app, tc.forValue, false, func() time.Time { return now }); err == nil {
			t.Fatal("accepted denial", tc)
		}
		after, _ := os.ReadFile(path)
		if string(before) != string(after) {
			t.Fatal("denial mutated file")
		}
	}
	exp := now.Add(30 * time.Minute)
	s.ExpiresAt = &exp
	if _, err := ChangePersonApp(path, s, "alice", "photos", "1h", false, func() time.Time { return now }); err == nil {
		t.Fatal("grant outlived binding")
	}
	if _, err := ChangePersonApp(path, s, "alice", "photos", "10m", false, func() time.Time { return exp }); err == nil {
		t.Fatal("writer clock did not recheck binding")
	}
	if _, err := ChangePersonApp(path, s, "alice", "photos", "10m", false, func() time.Time { return now }); err != nil {
		t.Fatal("positive expiry control", err)
	}
}

func TestScopedRestartOnlyChangesSelectedGeneration(t *testing.T) {
	path := peopleFixture(t)
	clock := func() time.Time { return time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC) }
	s := mcpscope.Session{Who: "operator", Scope: mcpscope.Scope{Role: "app-operator", Apps: []string{"photos", "pub", "missing"}, MaxDuration: "1h"}}
	before, _ := Load(path)
	generation, err := RequestAppRestart(path, s, "photos", clock)
	if err != nil || generation != 1 {
		t.Fatal(generation, err)
	}
	after, _ := Load(path)
	for i := range before.Services {
		if before.Services[i].Name == "photos" {
			before.Services[i].RestartGeneration = 1
		}
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("restart touched unrelated state")
	}
	for _, app := range []string{"finance", "pub", "missing"} {
		if _, err := RequestAppRestart(path, s, app, clock); err == nil {
			t.Fatal("restart accepted", app)
		}
	}
	s.Scope.Role = "people-manager"
	if _, err := RequestAppRestart(path, s, "photos", clock); err == nil {
		t.Fatal("manager restarted app")
	}
	s.Scope = mcpscope.Scope{Role: "owner"}
	if _, err := RequestAppRestart(path, s, "pub", clock); err != nil {
		t.Fatal("owner control", err)
	}
	reg, _ := Load(path)
	reg.Services[0].RestartGeneration = math.MaxUint64
	if err := save(path, reg); err != nil {
		t.Fatal(err)
	}
	if _, err := RequestAppRestart(path, s, reg.Services[0].Name, clock); err == nil {
		t.Fatal("generation wrapped")
	}
}

func TestScopedRegistryIOFailures(t *testing.T) {
	path := peopleFixture(t)
	clock := func() time.Time { return time.Now() }
	s := mcpscope.Session{Scope: mcpscope.Scope{Role: "owner"}}
	if err := os.WriteFile(path, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ChangePersonApp(path, s, "alice", "photos", "1h", false, clock); err == nil {
		t.Fatal("corrupt file mutated")
	}
	if _, err := RequestAppRestart(path, s, "photos", clock); err == nil {
		t.Fatal("corrupt file restarted")
	}
}
