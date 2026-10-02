package server

import (
	"io"
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

func TestReReviewLegacyPunctuationOverRealHTTP(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(config.ConfigDirEnv, dir)
	path := filepath.Join(dir, "registry.json")
	svc := registry.Service{Name: "photos", Type: registry.TypeProxy, Target: "http://localhost:3000", AllowedUsers: []string{"o'connor@example.com"}}
	if _, e := registry.Add(path, svc); e != nil {
		t.Fatal(e)
	}
	who := &apitype.WhoIsResponse{UserProfile: &tailcfg.UserProfile{LoginName: "o'connor@example.com"}}
	lc := fakeWhoIsClient(t, who, nil)
	h := peopleMiddleware(path, svc, func() (*LocalClient, error) { return lc, nil }, func() time.Time { return time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC) })(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "PRIVATE-POSITIVE-CONTROL") }))
	httpServer := httptest.NewServer(h)
	defer httpServer.Close()
	request := func() int {
		resp, e := http.Get(httpServer.URL)
		if e != nil {
			t.Fatal(e)
		}
		defer resp.Body.Close()
		data, _ := io.ReadAll(resp.Body)
		t.Logf("status=%d body=%s", resp.StatusCode, data)
		return resp.StatusCode
	}
	if code := request(); code != 200 {
		t.Fatal("positive legacy-only control failed", code)
	}
	// An unrelated person on a different app activates PeopleAccessAt for all apps.
	if _, e := registry.Add(path, registry.Service{Name: "finance", Type: registry.TypeProxy, Target: "http://localhost:3001"}); e != nil {
		t.Fatal(e)
	}
	if _, e := registry.ChangePerson(path, "alice", []string{"finance"}, nil, false, false); e != nil {
		t.Fatal(e)
	}
	if code := request(); code != 200 {
		t.Errorf("valid previously allowed identity locked out by unrelated people grant: %d", code)
	}
}
