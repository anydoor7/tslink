package server

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/accesslog"
	"github.com/anydoor7/tslink/internal/duration"
	"github.com/anydoor7/tslink/internal/registry"
)

func TestWave2PortalRequestEligibility(t *testing.T) {
	f := newPortalFixture(t)
	f.who.Store(requestMember("alice"))
	requestableApp(t, f, "secret-payroll", true)
	requestableApp(t, f, "guest-app", true)
	_, before := requestForm(t, f)
	if !strings.Contains(before, `<option value="guest-app">`) {
		t.Fatal("positive requestability control", before)
	}
	now := time.Unix(0, f.now.Load())
	if _, _, err := registry.CreateGuest(f.path, registry.CreateGuestOptions{App: "guest-app", Value: "2h", PublicAck: true, Policy: duration.Policy{}, Now: now}); err != nil {
		t.Fatal(err)
	}
	_, after := requestForm(t, f)
	if strings.Contains(after, `<option value="guest-app">`) || !strings.Contains(after, `<option value="secret-payroll">`) {
		t.Fatal("guest request entry or missing private control", after)
	}
	if _, err := registry.SubmitAccessRequest(f.path, "alice", "guest-app", "1h", "", now); err == nil {
		t.Fatal("guest request accepted outside portal")
	}
}

func TestWave2ClosedGuestGateRefusesNewSessions(t *testing.T) {
	f := newGuestFixture(t, "", false, true)
	_ = f.login() // A valid bearer establishes a session before shutdown.
	gate := f.s.nodes["photos"].handlerCloser.(*guestGate)
	if err := gate.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(f.path)
	if err != nil {
		t.Fatal(err)
	}
	// The listener still exists during this real shutdown window; the closed
	// gate must refuse a new request without touching grant state.
	response, _ := f.request("GET", "/guest/"+f.token, "", nil)
	if response.StatusCode != 503 || len(response.Cookies()) != 0 {
		t.Fatalf("closed gate admitted a session: status=%d cookies=%d", response.StatusCode, len(response.Cookies()))
	}
	after, err := os.ReadFile(f.path)
	if err != nil || string(before) != string(after) {
		t.Fatal("closed gate changed grant state", err)
	}
}

func TestWave2GuestPrivateAccessAndAudit(t *testing.T) {
	t.Run("private_people", func(t *testing.T) {
		f := newGuestFixture(t, "", false, false)
		response, _ := f.request("GET", "/", "", nil)
		if response.StatusCode != 204 {
			t.Fatal("private control", response.StatusCode)
		}
		if _, err := registry.RemovePerson(f.path, "alice"); err != nil {
			t.Fatal(err)
		}
		response, _ = f.request("GET", "/", "", nil)
		if response.StatusCode != 403 {
			t.Fatal("revoked private person bypassed people", response.StatusCode)
		}
	})
	t.Run("public_use_revoke", func(t *testing.T) {
		f := newGuestFixture(t, "", false, true)
		cookies := f.login()
		response, _ := f.request("GET", "/", "", cookies)
		if response.StatusCode != 204 {
			t.Fatal(response.StatusCode)
		}
		if _, err := registry.RevokeGuest(f.path, f.grant.ID, accessTestTime); err != nil {
			t.Fatal(err)
		}
		response, _ = f.request("GET", "/", "", cookies)
		if response.StatusCode != 401 {
			t.Fatal(response.StatusCode)
		}
		f.s.stopNodeLocked("photos")
		drainAccess(t, f.store)
		log, err := accesslog.Query(f.dir, accesslog.Filter{App: "photos"})
		if err != nil {
			t.Fatal(err)
		}
		used, revoked := false, false
		for _, e := range log.Events {
			if e.Guest != nil && e.Guest.LinkID == f.grant.ID && e.Guest.Decision == "allowed" {
				used = true
			}
			for _, c := range e.Changes {
				if c.Action == "guest_revoked" && c.ID == f.grant.ID && e.Surface == "cli" {
					revoked = true
				}
			}
		}
		if !used || !revoked {
			t.Fatalf("missing guest audit use=%t revoke=%t", used, revoked)
		}
		data, _ := json.Marshal(log)
		if strings.Contains(string(data), f.token) {
			t.Fatal("bearer in audit")
		}
	})
}
