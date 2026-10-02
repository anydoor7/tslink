package registry

import (
	"errors"
	"fmt"
	"github.com/anydoor7/tslink/internal/atomicfile"
	"os"
	"testing"
	"time"
)

func TestGuestCounterPublishFailure(t *testing.T) {
	path := guestRegistry(t)
	v, _, e := CreateGuest(path, guestOptions())
	if e != nil {
		t.Fatal(e)
	}
	_, reason := CheckGuest(path, "photos", v.ID, guestTestNow, true, true)
	if reason != "allowed" {
		t.Fatal(reason)
	}
	restore := atomicfile.SetDirectorySyncForTest(func(string) error { return fmt.Errorf("injected directory fsync failure") })
	defer restore()
	e = FlushGuestCounters(path)
	if e == nil || !atomicfile.IsPublished(e) || !atomicfile.IsPublished(GuestCounterError(path)) {
		t.Fatal("failure injection did not execute")
	}
	reg, _, e := Preflight(path)
	if e != nil {
		t.Fatal(e)
	}
	onDisk := reg.Guests[0].Uses
	pending := snapshotGuestUsage(path)[v.ID].uses
	t.Logf("after post-rename error: disk_uses=%d pending_uses=%d", onDisk, pending)
	if onDisk != 1 {
		t.Fatal("publication control: injected failure did not occur after rename")
	}
	if pending != 0 {
		t.Error("published batch remained pending")
	}
	restore()
	if e = FlushGuestCounters(path); e != nil {
		t.Fatal(e)
	}
	if !atomicfile.IsPublished(GuestCounterError(path)) {
		t.Error("no-op flush cleared unconfirmed durability")
	}
	reg, _, e = Preflight(path)
	if e != nil {
		t.Fatal(e)
	}
	t.Logf("after retry: disk_uses=%d disk_sessions=%d actual_authorizations=1", reg.Guests[0].Uses, reg.Guests[0].Sessions)
	if reg.Guests[0].Uses != 1 || reg.Guests[0].Sessions != 1 {
		t.Error("retry applied an already published batch twice")
	}
	if _, e = RevokeGuest(path, v.ID, guestTestNow); e != nil || GuestCounterError(path) != nil {
		t.Fatal("durable publication did not clear status", e)
	}
}

func TestGuestCounterPublishedRevokeAndInterleavedUsage(t *testing.T) {
	path := guestRegistry(t)
	v, _, err := CreateGuest(path, guestOptions())
	if err != nil {
		t.Fatal(err)
	}
	if _, reason := CheckGuest(path, "photos", v.ID, guestTestNow, true, true); reason != "allowed" {
		t.Fatal(reason)
	}
	var notified bool
	unsubscribe := WatchGuestCommits(path, func(grants []GuestGrant) { notified = grants[0].Revoked })
	defer unsubscribe()
	cause := errors.New("post-publication failure")
	restore := atomicfile.SetDirectorySyncForTest(func(string) error {
		addGuestUsage(path, v.ID, guestTestNow.Add(time.Second), true, true)
		return cause
	})
	defer restore()
	_, err = RevokeGuest(path, v.ID, guestTestNow)
	if !errors.Is(err, cause) || !atomicfile.IsPublished(err) || !notified {
		t.Fatal("published revoke not notified", err, notified)
	}
	pending := snapshotGuestUsage(path)[v.ID]
	if pending.uses != 1 || pending.sessions != 1 || pending.last == nil || !pending.last.Equal(guestTestNow.Add(time.Second)) {
		t.Fatal("interleaved usage lost", pending)
	}
	restore()
	if err := FlushGuestCounters(path); err != nil {
		t.Fatal(err)
	}
	reg, _, err := Preflight(path)
	if err != nil || reg.Guests[0].Uses != 2 || reg.Guests[0].Sessions != 2 || !reg.Guests[0].Revoked {
		t.Fatal("published counters reapplied", err)
	}
	if GuestCounterError(path) != nil {
		t.Fatal("durable retry did not clear status")
	}
}
func TestGuestCounterInterleavedObservation(t *testing.T) {
	path := guestRegistry(t)
	v, _, e := CreateGuest(path, guestOptions())
	if e != nil {
		t.Fatal(e)
	}
	if _, reason := CheckGuest(path, "photos", v.ID, guestTestNow, true, false); reason != "allowed" {
		t.Fatal(reason)
	}
	old := marshalFn
	defer func() { marshalFn = old }()
	once := false
	marshalFn = func(vv any, p, i string) ([]byte, error) {
		if !once {
			once = true
			addGuestUsage(path, v.ID, guestTestNow.Add(time.Second), true, true)
		}
		return old(vv, p, i)
	}
	if e = FlushGuestCounters(path); e != nil {
		t.Fatal(e)
	}
	reg, _, e := Preflight(path)
	if e != nil || reg.Guests[0].Uses != 1 {
		t.Fatal("published batch control", e)
	}
	pending := snapshotGuestUsage(path)[v.ID]
	if pending.uses != 1 || pending.sessions != 1 || pending.last == nil || !pending.last.Equal(guestTestNow.Add(time.Second)) {
		t.Fatal("concurrent observation lost")
	}
	marshalFn = old
	if e = FlushGuestCounters(path); e != nil {
		t.Fatal(e)
	}
	reg, _, e = Preflight(path)
	if e != nil || reg.Guests[0].Uses != 2 || reg.Guests[0].Sessions != 1 {
		t.Fatal("interleaved batch total", e)
	}
	t.Log("first publication 1 use; observation during marshal retained; second publication 2 uses/1 session")
}
func TestGuestCounterPrePublishFailure(t *testing.T) {
	path := guestRegistry(t)
	v, _, e := CreateGuest(path, guestOptions())
	if e != nil {
		t.Fatal(e)
	}
	if _, r := CheckGuest(path, "photos", v.ID, guestTestNow, true, false); r != "allowed" {
		t.Fatal(r)
	}
	before, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	old := marshalFn
	defer func() { marshalFn = old }()
	marshalFn = func(any, string, string) ([]byte, error) { return nil, fmt.Errorf("injected before publication") }
	if FlushGuestCounters(path) == nil {
		t.Fatal("failure injection")
	}
	after, e := os.ReadFile(path)
	if e != nil || string(after) != string(before) {
		t.Fatal("pre-publish bytes changed")
	}
	marshalFn = old
	if e = FlushGuestCounters(path); e != nil {
		t.Fatal(e)
	}
	reg, _, e := Preflight(path)
	if e != nil || reg.Guests[0].Uses != 1 {
		t.Fatal("pre-publish retry", e)
	}
	t.Log("pre-publication failure retains batch and retries exactly once")
}
