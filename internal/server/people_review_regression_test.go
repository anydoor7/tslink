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

func TestReviewPeopleDistinctUnicodeLogins(t *testing.T) {
	for _, tc := range []struct{ grant, ascii, lookalike string }{{"kelly@example.com", " KELLY@EXAMPLE.COM ", "\u212aelly@example.com"}, {"irene@example.com", "IRENE@EXAMPLE.COM", "\u0130rene@example.com"}} {
		t.Run(tc.grant, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv(config.ConfigDirEnv, dir)
			path := filepath.Join(dir, "registry.json")
			svc := registry.Service{Name: "photos", Type: registry.TypeProxy, Target: "http://localhost:3000"}
			if _, err := registry.Add(path, svc); err != nil {
				t.Fatal(err)
			}
			if _, err := registry.ChangePerson(path, tc.grant, []string{"photos"}, nil, false, false); err != nil {
				t.Fatal(err)
			}
			for _, caller := range []struct {
				login string
				want  int
			}{{tc.grant, 200}, {tc.ascii, 200}, {"stranger@example.com", 403}, {tc.lookalike, 403}} {
				lc := fakeWhoIsClient(t, &apitype.WhoIsResponse{UserProfile: &tailcfg.UserProfile{LoginName: caller.login}}, nil)
				h := peopleMiddleware(path, svc, func() (*LocalClient, error) { return lc, nil }, func() time.Time { return time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC) })(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("private body")) }))
				w := httptest.NewRecorder()
				h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
				t.Logf("grant=%q caller=%q status=%d expected=%d", tc.grant, caller.login, w.Code, caller.want)
				if w.Code != caller.want {
					t.Errorf("distinct Unicode identity was authorized: %q got %d expected %d", caller.login, w.Code, caller.want)
				}
			}
		})
	}
}
