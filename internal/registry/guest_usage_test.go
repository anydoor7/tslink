package registry

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/filelock"
)

func TestGuestCountersBatchAndRetry(t *testing.T) {
	path := guestRegistry(t)
	view, _, err := CreateGuest(path, guestOptions())
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := range 50 {
		if _, reason := CheckGuest(path, "photos", view.ID, guestTestNow.Add(time.Duration(i)*time.Second), true, i == 0); reason != "allowed" {
			t.Fatal(reason)
		}
	}
	after, err := os.ReadFile(path)
	if err != nil || string(before) != string(after) {
		t.Fatal("authorization wrote counters", err)
	}
	memory, err := ShowGuest(path, view.ID, guestTestNow)
	if err != nil || memory.Uses != 50 || memory.Sessions != 1 || memory.LastUsedAt == nil || !memory.LastUsedAt.Equal(guestTestNow.Add(49*time.Second)) {
		t.Fatalf("pending counts: %+v %v", memory, err)
	}
	original := marshalFn
	marshalFn = func(any, string, string) ([]byte, error) { return nil, fmt.Errorf("counter write unavailable") }
	if err := FlushGuestCounters(path); err == nil {
		t.Fatal("failed flush reported success")
	}
	marshalFn = original
	t.Cleanup(func() { marshalFn = original })
	if err := FlushGuestCounters(path); err != nil {
		t.Fatal(err)
	}
	if err := FlushGuestCounters(path); err != nil {
		t.Fatal(err)
	}
	reg, _, err := guestPreflight(path)
	if err != nil || reg.Guests[0].Uses != 50 || reg.Guests[0].Sessions != 1 {
		t.Fatalf("persisted counts: %+v %v", reg, err)
	}
	if _, reason := CheckGuest(path, "photos", view.ID, guestTestNow, true, false); reason != "allowed" {
		t.Fatal(reason)
	}
	revoked, err := RevokeGuest(path, view.ID, guestTestNow)
	if err != nil || !revoked.Revoked || revoked.Uses != 51 {
		t.Fatalf("revoke flush: %+v %v", revoked, err)
	}
	reg, _, err = guestPreflight(path)
	if err != nil || !reg.Guests[0].Revoked || reg.Guests[0].Uses != 51 {
		t.Fatalf("durable revoke counts: %+v %v", reg, err)
	}
}

func TestGuestCounterFlushWriterRecovery(t *testing.T) {
	path := guestRegistry(t)
	view, _, err := CreateGuest(path, guestOptions())
	if err != nil {
		t.Fatal(err)
	}
	if _, reason := CheckGuest(path, "photos", view.ID, guestTestNow, true, false); reason != "allowed" {
		t.Fatal(reason)
	}
	lock, err := os.OpenFile(path+".lock", os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err = filelock.Lock(lock); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	err = FlushGuestCounters(path)
	if err == nil || time.Since(start) > time.Second {
		t.Fatal("busy flush must remain bounded", err)
	}
	if err = filelock.Unlock(lock); err != nil {
		t.Fatal(err)
	}
	if err = FlushGuestCounters(path); err != nil {
		t.Fatal(err)
	}
	reg, _, err := guestPreflight(path)
	if err != nil || reg.Guests[0].Uses != 1 {
		t.Fatal("pending batch lost", err)
	}
}

func TestGuestCountersWithOtherRegistryWrites(t *testing.T) {
	path := guestRegistry(t)
	view, _, err := CreateGuest(path, guestOptions())
	if err != nil {
		t.Fatal(err)
	}
	if _, reason := CheckGuest(path, "photos", view.ID, guestTestNow, true, false); reason != "allowed" {
		t.Fatal(reason)
	}
	if _, err = Add(path, Service{Name: "other", Type: TypeProxy, Target: "http://127.0.0.1:3001"}); err != nil {
		t.Fatal(err)
	}
	reg, _, err := guestPreflight(path)
	if err != nil || reg.Guests[0].Uses != 1 {
		t.Fatal("opportunistic batch missing", err)
	}
}

func TestGuestCounterFlushMissingAndCorruptRegistry(t *testing.T) {
	path := guestRegistry(t)
	view, _, err := CreateGuest(path, guestOptions())
	if err != nil {
		t.Fatal(err)
	}
	if _, reason := CheckGuest(path, "photos", view.ID, guestTestNow, true, false); reason != "allowed" {
		t.Fatal(reason)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err = FlushGuestCounters(path); !os.IsNotExist(err) {
		t.Fatal("missing registry flush", err)
	}
	if _, err = os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("flush recreated missing registry", err)
	}
	if err = os.WriteFile(path, []byte(`{"guests":`), 0600); err != nil {
		t.Fatal(err)
	}
	if err = FlushGuestCounters(path); err == nil {
		t.Fatal("corrupt registry rewritten")
	}
	current, err := os.ReadFile(path)
	if err != nil || string(current) != `{"guests":` {
		t.Fatal("corrupt bytes changed", err)
	}
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err = FlushGuestCounters(path); err != nil {
		t.Fatal(err)
	}
	reg, _, err := Preflight(path)
	if err != nil || reg.Guests[0].Uses != 1 {
		t.Fatal("failed batch lost on recovery", err)
	}
}
