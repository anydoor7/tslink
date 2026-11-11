package registry

import (
	"testing"
	"time"
)

// Removing an app ends its guest links. Before, the grants stayed active in
// the ledger, so re-adding an app with the same name and raising its gate
// made the removed app's links authorize the new app.
func TestRemoveRevokesTheAppsGuestLinksSoARecreatedGateDoesNotReviveThem(t *testing.T) {
	path := guestRegistry(t)
	_, oldToken, err := CreateGuest(path, guestOptions())
	if err != nil {
		t.Fatal(err)
	}
	if _, reason := FindGuestToken(path, "photos", oldToken, guestTestNow); reason != "allowed" {
		t.Fatalf("precondition: old link = %s", reason)
	}
	if removed, err := Remove(path, "photos"); err != nil || !removed {
		t.Fatalf("remove: %v %v", removed, err)
	}
	if _, err := Add(path, Service{Name: "photos", Type: TypeProxy, Target: "http://127.0.0.1:4000"}); err != nil {
		t.Fatal(err)
	}
	later := guestTestNow.Add(time.Minute)
	o := guestOptions()
	o.Now = later
	_, newToken, err := CreateGuest(path, o)
	if err != nil {
		t.Fatal(err)
	}
	if _, reason := FindGuestToken(path, "photos", oldToken, later); reason != "revoked" {
		t.Fatalf("removed app's link on the recreated app = %s, want revoked", reason)
	}
	if _, reason := FindGuestToken(path, "photos", newToken, later); reason != "allowed" {
		t.Fatalf("new link = %s", reason)
	}
	list, err := ListGuests(path, later)
	if err != nil || len(list) != 2 {
		t.Fatalf("history kept: %d %v", len(list), err)
	}
}

// A repeated add that leaves Funnel unspecified keeps the guest gate, so HTTP
// settings such as request limits stay changeable on a gated app; an add that
// explicitly turns Funnel off still meets the sticky refusal.
func TestAddKeepsTheGuestGateWhenFunnelIsUnspecified(t *testing.T) {
	path := guestRegistry(t)
	if _, _, err := CreateGuest(path, guestOptions()); err != nil {
		t.Fatal(err)
	}
	limits := &RequestLimits{MaxBody: "1GiB"}
	svc := Service{Name: "photos", Type: TypeProxy, Target: "http://127.0.0.1:3000", RequestLimits: limits}
	if _, err := AddWithOutcome(path, svc, AddOptions{PreserveUnspecified: true, PreserveFunnelExpiry: true, ExplicitFields: map[string]bool{"funnel": false}, Now: guestTestNow}); err != nil {
		t.Fatalf("limit change on a gated app: %v", err)
	}
	reg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	got := reg.Services[0]
	if !got.GuestGate || !got.Funnel || !got.PublicAck || got.RequestLimits == nil || got.RequestLimits.MaxBody != "1GiB" {
		t.Fatalf("after limit change: gate %v funnel %v limits %+v", got.GuestGate, got.Funnel, got.RequestLimits)
	}
	if _, err := AddWithOutcome(path, Service{Name: "photos", Type: TypeProxy, Target: "http://127.0.0.1:3000"}, AddOptions{PreserveUnspecified: true, ExplicitFields: map[string]bool{"funnel": true}, Now: guestTestNow}); err == nil {
		t.Fatal("explicit funnel-off add removed the gate")
	}
}
