package server

import (
	"github.com/anydoor7/tslink/internal/registry"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/tailcfg"
	"testing"
	"time"
)

func TestGuestGatePrivateListenerKeepsUnrelatedPersonAccess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.json")
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	svc := registry.Service{Name: "drop-w2", Type: registry.TypeProxy, Target: "http://localhost:3000", Funnel: true, PublicAck: true, GuestGate: true}
	if _, err := registry.Add(path, svc); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Add(path, registry.Service{Name: "photos", Type: registry.TypeProxy, Target: "http://localhost:3001", AllowedUsers: []string{"a@example.invalid"}}); err != nil {
		t.Fatal(err)
	}
	client := fakeWhoIsClient(t, &apitype.WhoIsResponse{UserProfile: &tailcfg.UserProfile{LoginName: "a@example.invalid"}}, nil)
	handler := peopleMiddleware(path, svc, func() (*LocalClient, error) { return client, nil }, func() time.Time { return now })(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("upstream")) }))
	probe := func(want int) {
		t.Helper()
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("GET", "https://drop-w2.test/", nil))
		if w.Code != want {
			t.Fatalf("private listener status=%d want=%d", w.Code, want)
		}
		if want == 200 && w.Body.String() != "upstream" {
			t.Fatal("not the upstream response")
		}
	}
	probe(200)
	// The trusted store seam isolates the listener regression from the
	// mutation preflight, which independently rejects this on v0.3.0.
	if _, err := registry.ChangePerson(path, "a@example.invalid", []string{"photos"}, nil, false, false); err != nil {
		t.Fatal(err)
	}
	probe(200)
	if _, err := registry.RemovePerson(path, "a@example.invalid"); err != nil {
		t.Fatal(err)
	}
	probe(403)
}
