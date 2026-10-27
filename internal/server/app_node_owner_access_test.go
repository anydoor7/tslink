package server

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/registry"
	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/tailcfg"
)

// A people-scoped app is what `tslink add NAME --proxy P` creates when the
// gateway is on. These tests pin who reaches such an app on its own node (the
// app-node listener), without any grant: the portal owner and admins, and
// nobody else. The owner/admin rule is the registry's PrivateAppAccessAt,
// reached through AppAccessDecisionAt from peopleMiddleware.
func TestAppNodeListenerAdmitsPortalOwnerAndAdminsWithoutAGrant(t *testing.T) {
	for _, gatewayOn := range []bool{true, false} {
		name := "gateway on"
		if !gatewayOn {
			name = "gateway off"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv(config.ConfigDirEnv, dir)
			path := filepath.Join(dir, "registry.json")
			// The portal first: assigning a global owner to a registry whose
			// people-scoped app would newly admit them is refused as a conflict.
			portal := &registry.PortalConfig{Enabled: gatewayOn, Hostname: "home", Owner: "owner@example.com", Admins: []string{"admin@example.com"},
				Gateway: &registry.GatewayConfig{Enabled: gatewayOn, Ports: registry.DefaultGatewayPorts()}}
			if err := registry.SetPortal(path, portal); err != nil {
				t.Fatal(err)
			}
			svc := registry.Service{Name: "photos", Type: registry.TypeProxy, Target: "http://localhost:3000", PeopleScoped: true}
			if _, err := registry.Add(path, svc); err != nil {
				t.Fatal(err)
			}
			now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
			var client *LocalClient
			serve := func(login string, tags []string) *httptest.ResponseRecorder {
				t.Helper()
				who := &apitype.WhoIsResponse{UserProfile: &tailcfg.UserProfile{LoginName: login}}
				if tags != nil {
					who.Node = &tailcfg.Node{Tags: tags}
				}
				client = fakeWhoIsClient(t, who, nil)
				h := peopleMiddleware(path, svc, func() (*LocalClient, error) { return client, nil }, func() time.Time { return now })(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Write([]byte("upstream private photo"))
				}))
				w := httptest.NewRecorder()
				h.ServeHTTP(w, httptest.NewRequest("GET", "https://photos.test/", nil))
				return w
			}
			for _, who := range []string{"owner@example.com", "admin@example.com"} {
				if w := serve(who, nil); w.Code != 200 || w.Body.String() != "upstream private photo" {
					t.Fatalf("%s got %d %q, want the upstream", who, w.Code, w.Body.String())
				}
			}
			// Negative controls: the probe discriminates. Anyone else, and a
			// tagged machine carrying the owner's login, is refused.
			if w := serve("eve@example.com", nil); w.Code != 403 {
				t.Fatalf("stranger got %d, want 403", w.Code)
			} else if code, _ := denialEnvelope(t, w.Body.Bytes()); code != "no_grant" {
				t.Fatalf("stranger refusal code = %q, want no_grant", code)
			}
			if w := serve("owner@example.com", []string{"tag:server"}); w.Code != 403 {
				t.Fatalf("tagged node with the owner login got %d, want 403", w.Code)
			}
		})
	}
}
