package server

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/health"
	"github.com/anydoor7/tslink/internal/registry"
	tsruntime "github.com/anydoor7/tslink/internal/runtime"
	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/tailcfg"
)

func TestWave1HealthCanonicalHostAndPeople(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(config.ConfigDirEnv, dir)
	if err := config.EnsureDir(); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	var wantHost atomic.Value
	wantHost.Store("public.tailnet.ts.net")
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Host != wantHost.Load().(string) || r.Header.Get("X-Forwarded-Host") != r.Host || r.URL.Path != "/base/ready" || r.URL.RawQuery != "fixed=value" {
			http.Error(w, "wrong virtual host or health route", 421)
			return
		}
		io.WriteString(w, "ready")
	}))
	defer backend.Close()
	svc := registry.Service{Name: "photos", Type: registry.TypeProxy, Target: backend.URL + "/base?fixed=value", PreserveHost: true, Health: &registry.HealthConfig{Path: "/ready", BodyContains: "ready"}, RequestLimits: &registry.RequestLimits{MaxBody: "1B"}}
	path := filepath.Join(dir, "registry.json")
	if _, err := registry.Add(path, svc); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	oldNow := serverNowFn
	serverNowFn = func() time.Time { return now }
	t.Cleanup(func() { serverNowFn = oldNow })
	expiry := now.Add(time.Hour)
	if _, err := registry.ChangePerson(path, "alice", []string{"photos"}, &expiry, true, false); err != nil {
		t.Fatal(err)
	}
	reg, err := registry.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	svc = reg.Services[0]
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	lc := fakeWhoIsClient(t, &apitype.WhoIsResponse{UserProfile: &tailcfg.UserProfile{LoginName: "alice"}}, nil)
	fake := &preserveHostTSNetServer{ln: ln, fakeTSNetServer: fakeTSNetServer{localClient: lc, dnsName: "magic.tailnet.ts.net", certDomains: []string{"Public.Tailnet.TS.Net.", "admin.tailnet.ts.net"}}}
	old := newTSNetServerFn
	t.Cleanup(func() { newTSNetServerFn = old })
	newTSNetServerFn = func(registry.Service, string, string, string) tsnetServer { return fake }
	s, err := New("fixture", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.startNodeLocked(context.Background(), svc); err != nil {
		t.Fatal(err)
	}
	defer s.stopNodeLocked(svc.Name)
	request := func(want int) {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, "http://"+ln.Addr().String()+"/ready", nil)
		req.Host = "admin.attacker.example"
		resp, err := (&http.Client{Timeout: time.Second}).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil || resp.StatusCode != want || (want == 200 && string(body) != "ready") {
			t.Fatalf("proxy: status=%d body=%q err=%v", resp.StatusCode, body, err)
		}
	}
	request(200)
	r := health.NewRecorder(filepath.Join(dir, health.StateFile), health.NotifierConfig{})
	check := func(wantError string) {
		t.Helper()
		s.healthCycle(context.Background(), r, now, health.Probe)
		observed := s.healthStates[svc.Name].Health
		if observed.LastChecked == nil || !observed.LastChecked.Equal(now) || observed.LastError != wantError {
			t.Fatalf("health does not follow proxy Host: %+v wantError=%q", observed, wantError)
		}
		snapshotPath, err := runtimeSnapshotPathFn()
		if err != nil {
			t.Fatal(err)
		}
		snapshot, err := tsruntime.Load(snapshotPath)
		if err != nil || len(snapshot.Services) != 1 || snapshot.Services[0].Health.LastChecked == nil || !snapshot.Services[0].Health.LastChecked.Equal(now) || snapshot.Services[0].Health.LastError != wantError {
			t.Fatalf("preserve-host health lost in runtime projection: %+v %v", snapshot, err)
		}
	}
	check("")
	// A cert-backed rename must select the new vhost immediately, even before
	// the ordinary interval is due, and discard results for the old authority.
	fake.certDomains = []string{"new.tailnet.ts.net"}
	wantHost.Store("new.tailnet.ts.net")
	now = now.Add(time.Second)
	request(200)
	check("")
	fake.certDomains = []string{"invalid:443"}
	now = now.Add(time.Second)
	before := calls.Load()
	check("health_canonical_host_unavailable")
	if calls.Load() != before {
		t.Fatal("health contacted a backend without a trusted canonical name")
	}
	fake.certDomains = []string{"new.tailnet.ts.net"}
	now = now.Add(time.Second)
	check("")
	if _, err := registry.RemovePerson(path, "alice"); err != nil {
		t.Fatal(err)
	}
	before = calls.Load()
	request(403)
	if calls.Load() != before {
		t.Fatal("revoked recipe-style service reached its backend")
	}
}

func TestWave1HealthIdentityPolicy(t *testing.T) {
	svc := registry.Service{Type: registry.TypeProxy, Target: "http://localhost:8080"}
	base := healthIdentity(svc)
	svc.PeopleScoped, svc.AllowedUsers = true, []string{"alice"}
	if healthIdentity(svc) != base {
		t.Fatal("people edits reset app observations")
	}
	svc.PreserveHost = true
	if healthIdentity(svc) == base {
		t.Fatal("a virtual-host policy change kept the old health identity")
	}
	base = healthIdentity(svc)
	svc.RequestLimits = registry.RecommendedUploadLimits()
	if healthIdentity(svc) == base {
		t.Fatal("a limit policy change kept the old health identity")
	}
}
