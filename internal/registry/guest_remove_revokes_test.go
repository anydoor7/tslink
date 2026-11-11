package registry

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/mcpaudit"
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

// The sticky gate: add on a gated app is refused, whatever it changes, and
// the refusal names the in-place commands.
func TestAddOnAGuestGatedAppIsRefusedAndNamesLimitsSet(t *testing.T) {
	path := guestRegistry(t)
	if _, _, err := CreateGuest(path, guestOptions()); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	svc := Service{Name: "photos", Type: TypeProxy, Target: "http://127.0.0.1:3000", RequestLimits: &RequestLimits{MaxBody: "1GiB"}}
	_, err := AddWithOutcome(path, svc, AddOptions{PreserveUnspecified: true, PreserveFunnelExpiry: true, Now: guestTestNow})
	var coded CodedError
	if !errors.As(err, &coded) || coded.Code != "conflict" || !strings.Contains(strings.Join(coded.Next, "\n"), "tslink limits set photos --max-request-body <value>") {
		t.Fatalf("add on a gated app: %v (next %q)", err, coded.Next)
	}
	if after, _ := os.ReadFile(path); !bytes.Equal(before, after) {
		t.Fatal("refused add changed the registry")
	}
}

// writeOldRegistry rewrites the registry as a release before remove-revokes
// left it: the removed app's grants still unrevoked.
func writeOldRegistry(t *testing.T, path string, unrevoke string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	for _, g := range doc["guests"].([]any) {
		if g.(map[string]any)["app"] == unrevoke {
			g.(map[string]any)["revoked"] = false
		}
	}
	raw, _ = json.Marshal(doc)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func guestReceipts(t *testing.T, path string) int {
	t.Helper()
	entries, err := mcpaudit.Journal{Path: filepath.Join(filepath.Dir(path), "mcp-audit.json")}.Read()
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range entries {
		if e.Tool == "guest_revoked" {
			n++
		}
	}
	return n
}

// M1: grants left unrevoked by an earlier release (or a hand edit) for a
// removed app are revoked when a new app of that name raises its gate, in
// the same write, with a receipt each.
func TestGateRaiseRevokesOrphanedGrantsFromAnOlderRegistry(t *testing.T) {
	path := guestRegistry(t)
	_, oldToken, err := CreateGuest(path, guestOptions())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Remove(path, "photos"); err != nil {
		t.Fatal(err)
	}
	writeOldRegistry(t, path, "photos")
	if _, err := Add(path, Service{Name: "photos", Type: TypeProxy, Target: "http://127.0.0.1:4000"}); err != nil {
		t.Fatal(err)
	}
	receipts := guestReceipts(t, path)
	o := guestOptions()
	if _, _, err := CreateGuest(path, o); err != nil {
		t.Fatal(err)
	}
	if _, reason := FindGuestToken(path, "photos", oldToken, guestTestNow); reason != "revoked" {
		t.Fatalf("orphaned link after the gate rose = %s, want revoked", reason)
	}
	if got := guestReceipts(t, path); got != receipts+1 {
		t.Fatalf("receipts %d -> %d, want one for the orphan", receipts, got)
	}
}

// m1: remove writes one guest_revoked receipt per link it revokes, and none
// for a link already revoked.
func TestRemoveWritesAReceiptPerRevokedLink(t *testing.T) {
	path := guestRegistry(t)
	first, _, err := CreateGuest(path, guestOptions())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := CreateGuest(path, guestOptions()); err != nil {
		t.Fatal(err)
	}
	if _, err := RevokeGuest(path, first.ID, guestTestNow); err != nil {
		t.Fatal(err)
	}
	if got := guestReceipts(t, path); got != 1 {
		t.Fatalf("receipts after one revoke = %d", got)
	}
	if _, err := Remove(path, "photos"); err != nil {
		t.Fatal(err)
	}
	if got := guestReceipts(t, path); got != 2 {
		t.Fatalf("receipts after remove = %d, want 2", got)
	}
}

// M1: the tentative-share rollback revokes the name's grants like remove.
func TestRemoveIfUnchangedRevokesTheNamesGuestLinks(t *testing.T) {
	path := guestRegistry(t)
	if _, _, err := CreateGuest(path, guestOptions()); err != nil {
		t.Fatal(err)
	}
	if _, err := Remove(path, "photos"); err != nil {
		t.Fatal(err)
	}
	writeOldRegistry(t, path, "photos")
	svc := Service{Name: "photos", Type: TypeProxy, Target: "http://127.0.0.1:4000"}
	if made, err := AddTentative(path, svc); err != nil || !made {
		t.Fatalf("AddTentative: %v %v", made, err)
	}
	reg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if removed, err := RemoveIfUnchanged(path, reg.Services[0]); err != nil || !removed {
		t.Fatalf("RemoveIfUnchanged: %v %v", removed, err)
	}
	reg, err = Load(path)
	if err != nil || len(reg.Guests) != 1 || !reg.Guests[0].Revoked {
		t.Fatalf("guests after rollback: %+v %v", reg.Guests, err)
	}
}

// R2-1: a link an older release revived (the name re-added and re-gated
// there) predates the current service, so it never authorizes and lists as
// revoked, even though the gate was already up when this release arrived.
func TestAGrantOlderThanItsServiceNeverAuthorizes(t *testing.T) {
	path := guestRegistry(t)
	_, oldToken, err := CreateGuest(path, guestOptions())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Remove(path, "photos"); err != nil {
		t.Fatal(err)
	}
	writeOldRegistry(t, path, "photos")
	if _, err := Add(path, Service{Name: "photos", Type: TypeProxy, Target: "http://127.0.0.1:4000"}); err != nil {
		t.Fatal(err)
	}
	// Re-gate the way the old release did: a raw gate, without the sweep.
	raw, _ := os.ReadFile(path)
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	svc := doc["services"].([]any)[0].(map[string]any)
	reg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	created := reg.Services[0].CreatedAt
	old := reg.Guests[0]
	old.CreatedAt = created.Add(-time.Hour)
	old.ExpiresAt = guestTestNow.Add(time.Hour)
	expires := guestTestNow.Add(2 * time.Hour)
	svc["guest_gate"], svc["funnel"], svc["public_ack"], svc["funnel_expires_at"] = true, true, true, expires
	g := doc["guests"].([]any)[0].(map[string]any)
	g["created_at"], g["expires_at"] = old.CreatedAt, old.ExpiresAt
	raw, _ = json.Marshal(doc)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, reason := FindGuestToken(path, "photos", oldToken, guestTestNow); reason != "revoked" {
		t.Fatalf("revived old link = %s, want revoked", reason)
	}
	list, err := ListGuests(path, guestTestNow)
	if err != nil || len(list) != 1 || !list[0].Revoked {
		t.Fatalf("guest list = %+v %v", list, err)
	}
	grants, err := ReadGuestGrants(path, "photos", guestTestNow)
	if err != nil || len(grants) != 1 || !grants[0].Revoked {
		t.Fatalf("monitor grants = %+v %v", grants, err)
	}
}
