package registry

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var guestTestNow = time.Date(2030, 7, 10, 12, 0, 0, 0, time.UTC)

func guestRegistry(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "registry.json")
	if _, e := Add(path, Service{Name: "photos", Type: TypeProxy, Target: "http://127.0.0.1:3000"}); e != nil {
		t.Fatal(e)
	}
	return path
}
func guestOptions() CreateGuestOptions {
	return CreateGuestOptions{App: "photos", Value: "2h", Now: guestTestNow, PublicAck: true}
}
func TestGuestGrantPolicyAndPersistence(t *testing.T) {
	path := guestRegistry(t)
	for _, value := range []string{"never", "59m", "8d", ""} {
		o := guestOptions()
		o.Value = value
		if _, _, e := CreateGuest(path, o); e == nil {
			t.Fatal("policy allowed", value)
		}
	}
	o := guestOptions()
	o.Value = "8d"
	o.Policy.PublicMax = 9 * 24 * time.Hour
	o.PIN = "975310"
	o.Label = "Aunt May"
	v, token, e := CreateGuest(path, o)
	if e != nil {
		t.Fatal(e)
	}
	if len(token) != 43 || v.ExpiresAt.Sub(guestTestNow) != 8*24*time.Hour || !v.PINRequired {
		t.Fatal(v, token)
	}
	raw, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(raw), token) || strings.Contains(string(raw), o.PIN) {
		t.Fatal("plaintext secret persisted")
	}
	reg, _, e := Preflight(path)
	if e != nil || len(reg.Guests) != 1 || reg.SchemaVersion != 2 {
		t.Fatal(reg, e)
	}
	if strings.Contains(string(raw), `"token_hash"`) == false {
		t.Fatal("hash missing")
	}
	got, reason := FindGuestToken(path, "photos", token, guestTestNow)
	if reason != "allowed" || got.ID != v.ID {
		t.Fatal(got, reason)
	}
	if _, reason = FindGuestToken(path, "other", token, guestTestNow); reason != "invalid_token" {
		t.Fatal("app binding", reason)
	}
	if _, reason = FindGuestToken(path, "photos", strings.Repeat("x", 43), guestTestNow); reason != "invalid_token" {
		t.Fatal("wrong token", reason)
	}
	for range 5 {
		if _, r := CheckGuestPIN(path, "photos", v.ID, "bad", guestTestNow); r != "bad_pin" {
			t.Fatal(r)
		}
	}
	if _, r := CheckGuestPIN(path, "photos", v.ID, o.PIN, guestTestNow); r != "rate_limited" {
		t.Fatal(r)
	}
	// Re-read after a simulated process restart: persisted PIN lockout remains.
	if _, _, e = Preflight(path); e != nil {
		t.Fatal(e)
	}
	if _, r := CheckGuestPIN(path, "photos", v.ID, o.PIN, guestTestNow.Add(time.Minute)); r != "rate_limited" {
		t.Fatal("restart lockout", r)
	}
	if _, r := CheckGuestPIN(path, "photos", v.ID, o.PIN, guestTestNow.Add(16*time.Minute)); r != "allowed" {
		t.Fatal(r)
	}
	list, e := ListGuests(path, guestTestNow)
	if e != nil || len(list) != 1 {
		t.Fatal(list, e)
	}
	encoded, _ := json.Marshal(list)
	for _, secret := range []string{token, reg.Guests[0].TokenHash, reg.Guests[0].PINHash, reg.Guests[0].Salt} {
		if strings.Contains(string(encoded), secret) {
			t.Fatal("secret in view")
		}
	}
	if _, e = RevokeGuest(path, v.ID, guestTestNow); e != nil {
		t.Fatal(e)
	}
	if _, r := CheckGuest(path, "photos", v.ID, guestTestNow, true, false); r != "revoked" {
		t.Fatal(r)
	}
	if _, e = ShowGuest(path, "missing", guestTestNow); e == nil {
		t.Fatal("missing show allowed")
	}
	if _, e = RevokeGuest(path, "missing", guestTestNow); e == nil {
		t.Fatal("missing revoke allowed")
	}
}
func TestGuestExposureRefusalsAndStickiness(t *testing.T) {
	path := guestRegistry(t)
	o := guestOptions()
	o.PublicAck = false
	if _, _, e := CreateGuest(path, o); e == nil {
		t.Fatal("unacknowledged public enable")
	}
	for _, pin := range []string{"12345", "abcdef", strings.Repeat("1", 65)} {
		o = guestOptions()
		o.PIN = pin
		if _, _, e := CreateGuest(path, o); e == nil {
			t.Fatal("bad PIN allowed")
		}
	}
	o = guestOptions()
	o.Label = "label\nsecret"
	if _, _, e := CreateGuest(path, o); e == nil {
		t.Fatal("control label allowed")
	}
	public := Service{Name: "photos", Type: TypeProxy, Target: "http://127.0.0.1:3000", Funnel: true, PublicAck: true}
	if _, e := Add(path, public); e != nil {
		t.Fatal(e)
	}
	if _, _, e := CreateGuest(path, guestOptions()); e == nil {
		t.Fatal("open Funnel adopted")
	}
	public.Funnel = false
	if _, e := Add(path, public); e != nil {
		t.Fatal(e)
	}
	if _, _, e := CreateGuest(path, guestOptions()); e != nil {
		t.Fatal(e)
	}
	if _, e := Add(path, public); e == nil {
		t.Fatal("gate removed by add")
	}
	reg, _, e := Preflight(path)
	if e != nil {
		t.Fatal(e)
	}
	if !PeopleServiceSupported(reg.Services[0]) {
		t.Fatal("gated proxy cannot support people")
	}
	if !serviceGuestGateValid(reg.Services[0]) {
		t.Fatal("guest service invalid")
	}
	s := reg.Services[0]
	s.Type = TypeTCP
	if ValidateService(s) == nil {
		t.Fatal("TCP gate allowed")
	}
}
func serviceGuestGateValid(s Service) bool {
	return ValidateService(s) == nil && s.GuestGate && s.Funnel && s.PublicAck
}
func TestGuestUnsafeRegistryAndExpiryLatch(t *testing.T) {
	path := guestRegistry(t)
	v, token, e := CreateGuest(path, guestOptions())
	if e != nil {
		t.Fatal(e)
	}
	if _, r := CheckGuest(path, "photos", v.ID, guestTestNow.Add(2*time.Hour), false, false); r != "expired" {
		t.Fatal(r)
	}
	if _, r := FindGuestToken(path, "photos", token, guestTestNow); r != "expired" {
		t.Fatal("rollback", r)
	}
	raw, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Remove(path); e != nil {
		t.Fatal(e)
	}
	if e = os.Mkdir(path, 0700); e != nil {
		t.Fatal(e)
	}
	if _, r := CheckGuest(path, "photos", v.ID, guestTestNow, false, false); r != "unavailable" {
		t.Fatal(r)
	}
	if e = os.Remove(path); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(path, raw, 0600); e != nil {
		t.Fatal(e)
	}
	reg, _, e := Preflight(path)
	if e != nil {
		t.Fatal(e)
	}
	for _, edit := range []func(*GuestGrant){func(g *GuestGrant) { g.Authentication = "oidc" }, func(g *GuestGrant) { g.TokenHash = "bad" }, func(g *GuestGrant) { g.PINHash = "bad" }, func(g *GuestGrant) { g.Salt = "bad" }, func(g *GuestGrant) { g.ExpiresAt = g.CreatedAt }, func(g *GuestGrant) { g.PINAttempts = 6 }} {
		g := reg.Guests[0]
		edit(&g)
		if validateGuests([]GuestGrant{g}) == nil {
			t.Fatal("invalid ledger admitted")
		}
	}
	if validateGuests([]GuestGrant{reg.Guests[0], reg.Guests[0]}) == nil {
		t.Fatal("duplicate ID admitted")
	}
}

func TestGuestSaveFailureAndMissingService(t *testing.T) {
	path := guestRegistry(t)
	v, token, e := CreateGuest(path, guestOptions())
	if e != nil {
		t.Fatal(e)
	}
	before, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	old := marshalFn
	marshalFn = func(any, string, string) ([]byte, error) { return nil, fmt.Errorf("injected disk boundary failure") }
	t.Cleanup(func() { marshalFn = old })
	if _, r := CheckGuest(path, "photos", v.ID, guestTestNow, true, false); r != "unavailable" {
		t.Fatal("save failure allowed", r)
	}
	if _, _, e = CreateGuest(path, guestOptions()); e == nil {
		t.Fatal("failed save created guest")
	}
	after, e := os.ReadFile(path)
	if e != nil || string(after) != string(before) {
		t.Fatal("failure changed ledger", e)
	}
	marshalFn = old
	if _, e = Remove(path, "photos"); e != nil {
		t.Fatal(e)
	}
	if _, r := FindGuestToken(path, "photos", token, guestTestNow); r != "unavailable" {
		t.Fatal("missing service granted", r)
	}
	if _, r := CheckGuest(path, "photos", "missing", guestTestNow, false, false); r != "invalid_token" {
		t.Fatal(r)
	}
	if _, _, e = CreateGuest(path, guestOptions()); e == nil {
		t.Fatal("missing service created grant")
	}
	if _, r := CheckGuestPIN(path, "photos", v.ID, "975310", guestTestNow); r != "unavailable" {
		t.Fatal(r)
	}
	if _, e = ListGuests(filepath.Join(t.TempDir(), "missing"), guestTestNow); e != nil {
		t.Fatal(e)
	}
}

func TestGuestHistoryCannotBecomeOpenFunnel(t *testing.T) {
	path := guestRegistry(t)
	_, _, e := CreateGuest(path, guestOptions())
	if e != nil {
		t.Fatal(e)
	}
	reg, _, e := Preflight(path)
	if e != nil {
		t.Fatal(e)
	}
	reg.Services[0].GuestGate = false
	raw, e := json.Marshal(reg)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(path, raw, 0600); e != nil {
		t.Fatal(e)
	}
	loaded, issues, e := Preflight(path)
	if e != nil || len(issues) != 1 || len(loaded.Services) != 0 {
		t.Fatal("ungated guest-history Funnel admitted", issues, e)
	}
	if e = save(path, reg); e == nil {
		t.Fatal("writer made guest-history app open")
	}
}

func TestGuestLedgerCapacityAndInvalidViews(t *testing.T) {
	path := guestRegistry(t)
	_, _, e := CreateGuest(path, guestOptions())
	if e != nil {
		t.Fatal(e)
	}
	reg, _, e := Preflight(path)
	if e != nil {
		t.Fatal(e)
	}
	base := reg.Guests[0]
	for i := 1; i < 4096; i++ {
		g := base
		g.ID = fmt.Sprintf("guest-%d", i)
		reg.Guests = append(reg.Guests, g)
	}
	if e = save(path, reg); e != nil {
		t.Fatal(e)
	}
	if _, token, e := CreateGuest(path, guestOptions()); e == nil || token != "" {
		t.Fatal("ledger capacity bypass")
	}
	reg.Guests = append(reg.Guests, base)
	if validateGuests(reg.Guests) == nil {
		t.Fatal("oversized ledger accepted")
	}
	reg.Guests = reg.Guests[:1]
	reg.Services[0].GuestGate = false
	raw, e := json.Marshal(reg)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(path, raw, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = ListGuests(path, guestTestNow); e == nil {
		t.Fatal("invalid service view succeeded")
	}
	if _, e = ShowGuest(path, base.ID, guestTestNow); e == nil {
		t.Fatal("invalid show succeeded")
	}
	if _, e = RevokeGuest(path, base.ID, guestTestNow); e == nil {
		t.Fatal("invalid revoke rewrote registry")
	}
}

func TestGuestUnsupportedAppsAndPINSaveFailure(t *testing.T) {
	path := guestRegistry(t)
	if _, e := Add(path, Service{Name: "tcp-app", Type: TypeTCP, Target: "127.0.0.1:3000"}); e != nil {
		t.Fatal(e)
	}
	o := guestOptions()
	o.App = "tcp-app"
	if _, _, e := CreateGuest(path, o); e == nil {
		t.Fatal("TCP guest accepted")
	}
	o = guestOptions()
	o.PIN = "975310"
	v, _, e := CreateGuest(path, o)
	if e != nil {
		t.Fatal(e)
	}
	if _, r := FindGuestToken(path, "photos", "short", guestTestNow); r != "invalid_token" {
		t.Fatal("short token", r)
	}
	before, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	old := marshalFn
	marshalFn = func(any, string, string) ([]byte, error) { return nil, fmt.Errorf("synthetic PIN persistence failure") }
	t.Cleanup(func() { marshalFn = old })
	if _, r := CheckGuestPIN(path, "photos", v.ID, "975310", guestTestNow); r != "unavailable" {
		t.Fatal("PIN save failure authorized", r)
	}
	marshalFn = old
	after, e := os.ReadFile(path)
	if e != nil || string(after) != string(before) {
		t.Fatal("PIN save failure corrupted ledger", e)
	}
	if e = os.WriteFile(path, []byte(`{"guests":`), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = ListGuests(path, guestTestNow); e == nil {
		t.Fatal("malformed view accepted")
	}
	if _, _, e = CreateGuest(path, guestOptions()); e == nil {
		t.Fatal("malformed registry rewritten")
	}
}
