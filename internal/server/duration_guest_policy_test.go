package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/duration"
	"github.com/anydoor7/tslink/internal/registry"
	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/tailcfg"
)

func TestFirstGuestGrantEnforcedBeforeInviteHistoryOnHTTP(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(config.ConfigDirEnv, dir)
	path := filepath.Join(dir, "registry.json")
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	var hits atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		io.WriteString(w, "private photos")
	}))
	defer backend.Close()
	svc := registry.Service{Name: "photos", Type: registry.TypeProxy, Target: backend.URL}
	if _, err := registry.Add(path, svc); err != nil {
		t.Fatal(err)
	}
	value := "3d"
	if _, err := registry.ChangePersonWithLifetime(path, "alice", []string{"photos"}, false, registry.PersonLifetimeOptions{Value: &value, Audience: duration.Guest, Now: now}); err != nil {
		t.Fatal(err)
	}
	// No invite record exists: this is exactly the first-invitation window.
	_, err := registry.ExtendDuration(path, registry.ExtendOptions{Service: "photos", Who: "alice", Value: "never", AckNever: true, Now: now})
	if err == nil || !strings.Contains(err.Error(), "only for tailnet-member") {
		t.Errorf("guest permanent extension accepted: %v", err)
	}
	lc := fakeWhoIsClient(t, &apitype.WhoIsResponse{UserProfile: &tailcfg.UserProfile{LoginName: "alice"}}, nil)
	target, _ := url.Parse(backend.URL)
	for _, tc := range []struct {
		name   string
		at     time.Time
		status int
		body   string
	}{
		{"finite_active_control", now.Add(time.Hour), 200, "private photos"},
		{"denied_after_8_days", now.Add(8 * 24 * time.Hour), 403, ""},
		{"restart_clock_rollback_stays_denied", now.Add(time.Hour), 403, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := peopleMiddleware(path, svc, func() (*LocalClient, error) { return lc, nil }, func() time.Time { return tc.at })(httputil.NewSingleHostReverseProxy(target))
			listener := httptest.NewServer(h)
			defer listener.Close()
			before := hits.Load()
			resp, err := http.Get(listener.URL)
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			if err != nil {
				t.Fatal(err)
			}
			wantHits := before
			if tc.status == 200 {
				wantHits++
			}
			if resp.StatusCode != tc.status || hits.Load() != wantHits || tc.body != "" && string(body) != tc.body {
				t.Fatalf("status=%d body=%q backend_hits=%d want=%d", resp.StatusCode, body, hits.Load(), wantHits)
			}
		})
	}
}
