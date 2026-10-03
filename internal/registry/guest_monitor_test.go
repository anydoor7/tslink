package registry

import (
	"os"
	"testing"
	"time"
)

func TestReadGuestGrantsSharedSnapshot(t *testing.T) {
	path := guestRegistry(t)
	v, _, err := CreateGuest(path, guestOptions())
	if err != nil {
		t.Fatal(err)
	}
	o := guestOptions()
	o.Value = "4h"
	long, _, err := CreateGuest(path, o)
	if err != nil {
		t.Fatal(err)
	}
	grants, err := ReadGuestGrants(path, "photos", guestTestNow)
	if err != nil || len(grants) != 2 {
		t.Fatal("allowed snapshot", len(grants), err)
	}
	grants, err = ReadGuestGrants(path, "other", guestTestNow)
	if err != nil || len(grants) != 0 {
		t.Fatal("app mismatch", err)
	}
	grants, err = ReadGuestGrants(path, "photos", v.ExpiresAt)
	if err != nil || len(grants) != 2 {
		t.Fatal("expiry snapshot", err)
	}
	reg, _, err := Preflight(path)
	if err != nil || !reg.Guests[0].Expired || reg.Guests[1].Expired {
		t.Fatal("individual expiry latch", err)
	}
	if _, reason := CheckGuest(path, "photos", v.ID, guestTestNow, false, false); reason != "expired" {
		t.Fatal("rollback lost latch", reason)
	}
	if _, reason := CheckGuest(path, "photos", long.ID, guestTestNow, false, false); reason != "allowed" {
		t.Fatal("long grant ended", reason)
	}
	if _, err := RevokeGuest(path, long.ID, guestTestNow); err != nil {
		t.Fatal(err)
	}
	grants, err = ReadGuestGrants(path, "photos", guestTestNow)
	if err != nil || !grants[1].Revoked {
		t.Fatal("revoke missing from snapshot", err)
	}
	if err := os.WriteFile(path, []byte(`{"guests":`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadGuestGrants(path, "photos", guestTestNow); err == nil {
		t.Fatal("corrupt registry accepted")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadGuestGrants(path, "photos", guestTestNow.Add(time.Second)); !os.IsNotExist(err) {
		t.Fatal("missing registry accepted", err)
	}
}
