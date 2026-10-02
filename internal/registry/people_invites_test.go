package registry

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestPeopleInviteLedgerAndValidation(t *testing.T) {
	path := peopleFixture(t)
	if _, e := ChangePerson(path, "alice", []string{"photos"}, nil, false, false); e != nil {
		t.Fatal(e)
	}
	op := PersonInvite{App: "photos", Hostname: "photos", NodeID: "n1", State: PersonInviteSending}
	if e := SavePersonInvite(path, " ALICE ", op); e != nil {
		t.Fatal(e)
	}
	if _, e := RemovePerson(path, "alice"); e != nil {
		t.Fatal(e)
	}
	if e := SavePersonInvite(path, "alice", op); e == nil || !strings.Contains(e.Error(), "grant removed") {
		t.Fatal("revoked person began new send", e)
	}
	if _, e := ChangePerson(path, "alice", []string{"photos"}, nil, false, false); e == nil || !strings.Contains(e.Error(), "pending invite cleanup") {
		t.Fatal("re-add bypassed unresolved cleanup", e)
	}
	op.ID = "1001"
	op.State = PersonInviteComplete
	if e := SavePersonInvite(path, "alice", op); e != nil {
		t.Fatal("late result lost after local denial", e)
	}
	op.State = PersonInviteRevoked
	if e := SavePersonInvite(path, "alice", op); e != nil {
		t.Fatal(e)
	}
	p, e := ChangePerson(path, "alice", []string{"photos"}, nil, false, false)
	if e != nil || p.Revoked || len(p.Invites) != 1 || p.Invites[0].ID != "1001" {
		t.Fatal("terminal history lost", p, e)
	}
	for _, state := range []string{PersonInvitePending, PersonInviteSending, PersonInviteUnknown, PersonInviteComplete, PersonInviteAccepted, PersonInviteRevoked, PersonInviteCancelled} {
		op.State = state
		if e := SavePersonInvite(path, "alice", op); e != nil {
			t.Fatal(state, e)
		}
	}
	for _, bad := range []PersonInvite{
		{App: "bad/", Hostname: "photos", NodeID: "n", State: PersonInviteUnknown},
		{App: "photos", Hostname: "bad/", NodeID: "n", State: PersonInviteUnknown},
		{App: "photos", Hostname: "photos", State: PersonInviteUnknown},
		{App: "photos", Hostname: "photos", NodeID: "n", ID: "../", State: PersonInviteUnknown},
		{App: "photos", Hostname: "photos", NodeID: "n", State: "bad"},
		{App: "photos", Hostname: "photos", NodeID: "n", State: PersonInviteComplete},
	} {
		if e := SavePersonInvite(path, "alice", bad); e == nil {
			t.Fatal("invalid ledger admitted", bad)
		}
	}
	if e := validatePersonInvites([]PersonInvite{op, op}); e == nil {
		t.Fatal("duplicate operation admitted")
	}
	if e := SavePersonInvite(path, "\u212aelly", op); e == nil {
		t.Fatal("unicode store boundary admitted")
	}
	if e := SavePersonInvite(path, "absent", op); e == nil {
		t.Fatal("missing person admitted")
	}
	if e := os.WriteFile(path, []byte(`{"schema_version":2,"services":[],"people":[{"login":"alice","grants":[],"invites":[{"app":"photos","hostname":"photos","node_id":"n1","state":"bad"}]}]}`), 0600); e != nil {
		t.Fatal(e)
	}
	if _, _, e := Preflight(path); e == nil {
		t.Fatal("corrupt durable state loaded")
	}
	if e := SavePersonInvite(path, "alice", op); e == nil {
		t.Fatal("corrupt durable state mutated")
	}
}

func TestPeopleInviteLockAcrossProcesses(t *testing.T) {
	if path := os.Getenv("GO_TEST_PEOPLE_LOCK_PATH"); path != "" {
		acquired, e := TryPeopleInviteWork(path, func() error { fmt.Println("LOCKED"); _, err := bufio.NewReader(os.Stdin).ReadString('\n'); return err })
		if e != nil || !acquired {
			t.Fatal(acquired, e)
		}
		return
	}
	path := peopleFixture(t)
	if _, e := ChangePerson(path, "alice", []string{"photos"}, nil, false, false); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestPeopleInviteLockAcrossProcesses$", "-test.count=1")
	child.Env = append(os.Environ(), "GO_TEST_PEOPLE_LOCK_PATH="+path)
	out, e := child.StdoutPipe()
	if e != nil {
		t.Fatal(e)
	}
	in, e := child.StdinPipe()
	if e != nil {
		t.Fatal(e)
	}
	if e := child.Start(); e != nil {
		t.Fatal(e)
	}
	defer func() { cancel(); _ = child.Wait() }()
	r := bufio.NewReader(out)
	for {
		line, e := r.ReadString('\n')
		if e != nil {
			t.Fatal("child did not acquire lock", e)
		}
		if line == "LOCKED\n" {
			break
		}
	}
	called := false
	acquired, e := TryPeopleInviteWork(path, func() error { called = true; return nil })
	if e != nil || acquired || called {
		t.Fatal("concurrent process acquired remote-work lock", acquired, e, called)
	}
	if _, e := RemovePerson(path, "alice"); e != nil {
		t.Fatal("operation lock blocked local denial", e)
	}
	if _, e := in.Write([]byte("release\n")); e != nil {
		t.Fatal(e)
	}
	if e := child.Wait(); e != nil {
		t.Fatal(e)
	}
	acquired, e = TryPeopleInviteWork(path, func() error { called = true; return nil })
	if e != nil || !acquired || !called {
		t.Fatal("released lock unavailable", acquired, e)
	}
}

func TestPeopleInviteLockRejectsSpecialStateFiles(t *testing.T) {
	path := peopleFixture(t)
	lock := path + ".people-invites.lock"
	if e := os.Mkdir(lock, 0700); e != nil {
		t.Fatal(e)
	}
	called := false
	acquired, e := TryPeopleInviteWork(path, func() error { called = true; return nil })
	if e == nil || acquired || called {
		t.Fatal("directory accepted as lock", acquired, e)
	}
}
