package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"html/template"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/health"
	"github.com/anydoor7/tslink/internal/registry"
	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/tailcfg"
)

type portalFixture struct {
	s    *Server
	path string
	addr string
	now  *atomic.Int64
	who  *atomic.Pointer[apitype.WhoIsResponse]
	fake *preserveHostTSNetServer
}

func newPortalFixture(t *testing.T) portalFixture {
	t.Helper()
	dir := t.TempDir()
	t.Setenv(config.ConfigDirEnv, dir)
	if err := config.EnsureDir(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "registry.json")
	for _, svc := range []registry.Service{
		{Name: "photos", Type: registry.TypeProxy, Target: "http://127.0.0.1:8001"},
		{Name: "secret-payroll", Type: registry.TypeProxy, Target: "http://127.0.0.1:8002", AllowedUsers: []string{"legacy", "tag:robot"}},
	} {
		if _, err := registry.Add(path, svc); err != nil {
			t.Fatal(err)
		}
	}
	now := &atomic.Int64{}
	now.Store(time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC).UnixNano())
	expires := time.Unix(0, now.Load()).Add(time.Hour)
	if _, err := registry.ChangePerson(path, "alice", []string{"photos"}, &expires, true, false); err != nil {
		t.Fatal(err)
	}
	portal := &registry.PortalConfig{Enabled: true, Hostname: "home", Owner: "owner", Admins: []string{"admin"}}
	if err := registry.SetPortal(path, portal); err != nil {
		t.Fatal(err)
	}
	who := &atomic.Pointer[apitype.WhoIsResponse]{}
	who.Store(&apitype.WhoIsResponse{UserProfile: &tailcfg.UserProfile{LoginName: "alice"}})
	lc := &LocalClient{OmitAuth: true, Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		b, _ := json.Marshal(who.Load())
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(b)), Header: make(http.Header)}, nil
	})}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	fake := &preserveHostTSNetServer{ln: ln, fakeTSNetServer: fakeTSNetServer{localClient: lc, dnsName: "home.tailnet.ts.net", certDomains: []string{"home.tailnet.ts.net"}}}
	oldFactory, oldClock := newTSNetServerFn, serverNowFn
	newTSNetServerFn = func(registry.Service, string, string, string) tsnetServer { return fake }
	serverNowFn = func() time.Time { return time.Unix(0, now.Load()) }
	s, err := New("fixture", "")
	if err != nil {
		t.Fatal(err)
	}
	s.syncPortal(context.Background(), portal)
	run := s.portalRun
	select {
	case <-run.done:
	case <-time.After(3 * time.Second):
		t.Fatal("portal startup stalled")
	}
	if s.portalState.State != "running" {
		t.Fatalf("portal did not start: %+v", s.portalState)
	}
	reg, _, err := registry.Preflight(path)
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	for _, svc := range reg.Services {
		node := &ServiceNode{service: svc, tsnetSrv: &fakeTSNetServer{certDomains: []string{svc.Name + ".tailnet.ts.net"}}, runtimeHost: svc.Name + ".tailnet.ts.net"}
		s.nodes[svc.Name] = node
		checked := time.Unix(0, now.Load())
		s.healthStates = mapOrInitHealth(s.healthStates)
		s.healthStates[svc.Name] = serviceHealth{Identity: healthProbeIdentity(svc, svc.Name+".tailnet.ts.net"), Node: node, Health: health.State{State: health.Healthy, Kind: svc.Type, LastChecked: &checked}}
	}
	s.mu.Unlock()
	t.Cleanup(func() { s.closePortal(); newTSNetServerFn = oldFactory; serverNowFn = oldClock; ln.Close() })
	return portalFixture{s: s, path: path, addr: ln.Addr().String(), now: now, who: who, fake: fake}
}

func mapOrInitHealth(m map[string]serviceHealth) map[string]serviceHealth {
	if m == nil {
		return map[string]serviceHealth{}
	}
	return m
}

func (f portalFixture) request(t *testing.T, method, path, host, origin string, body io.Reader) (*http.Response, string) {
	t.Helper()
	r, err := http.NewRequest(method, "http://"+f.addr+path, body)
	if err != nil {
		t.Fatal(err)
	}
	r.Host = host
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	resp, err := (&http.Client{Timeout: 3 * time.Second}).Do(r)
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	return resp, string(b)
}

func (f portalFixture) apps(t *testing.T) []PortalApp {
	t.Helper()
	r, body := f.request(t, "GET", "/api/apps", "home.tailnet.ts.net", "", nil)
	if r.StatusCode != 200 {
		t.Fatalf("JSON status=%d body=%s", r.StatusCode, body)
	}
	var wire struct {
		OK            bool       `json:"ok"`
		SchemaVersion int        `json:"schema_version"`
		Data          portalPage `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &wire); err != nil || !wire.OK || wire.SchemaVersion != 1 {
		t.Fatalf("JSON envelope: %s %v", body, err)
	}
	return wire.Data.Apps
}

func TestPortalHandlerFailurePaths(t *testing.T) {
	f := newPortalFixture(t)
	f.s.closePortal()
	h := PortalHandler{RegistryPath: f.path, LocalClient: f.fake.localClient,
		CanonicalHost: func() string { return "home.tailnet.ts.net" }, Now: func() time.Time { return time.Unix(0, f.now.Load()) },
		Apps: func(*registry.Registry, time.Time) map[string]PortalApp { return nil },
	}
	listener := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { h.ServeHTTP(w, r) }))
	defer listener.Close()
	request := func(path string) (int, string) {
		r, _ := http.NewRequest("GET", listener.URL+path, nil)
		r.Host = "home.tailnet.ts.net"
		resp, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		return resp.StatusCode, string(data)
	}
	status, body := request("/api/apps")
	if status != 200 || !strings.Contains(body, `"name":"photos","url":"","health":"unknown"`) || strings.Contains(body, "secret-payroll") {
		t.Fatalf("missing observation fallback=%d %s", status, body)
	}
	h.LocalClient = nil
	if status, _ := request("/"); status != 403 {
		t.Fatal("missing WhoIs client did not fail closed")
	}
	h.LocalClient = f.fake.localClient
	oldTemplate := portalTemplate
	portalTemplate = template.Must(template.New("broken").Parse(`partial{{template "missing" .}}`))
	defer func() { portalTemplate = oldTemplate }()
	if status, body := request("/"); status != 500 || strings.Contains(body, "partial") {
		t.Fatalf("template failure leaked partial HTML=%d %s", status, body)
	}
}

func TestPortalTCPAddressListener(t *testing.T) {
	f := newPortalFixture(t)
	svc := registry.Service{Name: "database", Type: registry.TypeTCP, Target: "localhost:5432", Port: 5432}
	if _, err := registry.Add(f.path, svc); err != nil {
		t.Fatal(err)
	}
	reg, _, err := registry.Preflight(f.path)
	if err != nil {
		t.Fatal(err)
	}
	for _, current := range reg.Services {
		if current.Name == svc.Name {
			svc = current
		}
	}
	f.s.mu.Lock()
	f.s.nodes[svc.Name] = &ServiceNode{service: svc, tsnetSrv: &fakeTSNetServer{}, runtimeHost: "database.tailnet.ts.net"}
	f.s.mu.Unlock()
	f.who.Store(&apitype.WhoIsResponse{UserProfile: &tailcfg.UserProfile{LoginName: "owner"}})
	address := "database.tailnet.ts.net:5432"
	found := false
	for _, app := range f.apps(t) {
		if app.Name == svc.Name {
			found = app.URL == address
		}
	}
	resp, body := f.request(t, "GET", "/", "home.tailnet.ts.net", "", nil)
	if !found || resp.StatusCode != 200 || !strings.Contains(body, "Connect using your app:") || !strings.Contains(body, address) || strings.Contains(body, `href="`+address) || strings.Contains(body, "ZgotmplZ") {
		t.Fatalf("TCP address JSON/HTML found=%t: %s", found, body)
	}
}

func TestPortalLegacyPeopleExpiryListener(t *testing.T) {
	f := newPortalFixture(t)
	reg, _, err := registry.Preflight(f.path)
	if err != nil {
		t.Fatal(err)
	}
	expires := time.Unix(0, f.now.Load()).Add(time.Hour)
	reg.People = append(reg.People, registry.Person{Login: "legacy\u00a0key", Grants: []registry.PersonGrant{{App: "photos", ExpiresAt: &expires}}})
	data, err := json.Marshal(reg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	f.who.Store(&apitype.WhoIsResponse{UserProfile: &tailcfg.UserProfile{LoginName: " LEGACY\u00a0KEY "}})
	apps := f.apps(t)
	if len(apps) != 1 || apps[0].ExpiresAt == nil || !apps[0].ExpiresAt.Equal(expires) || apps[0].ExpiryText != "Access ends January 1, 2030 at 01:00 UTC." {
		t.Fatalf("F1 legacy grant lost its deadline: %+v", apps)
	}
	f.now.Add(int64(time.Hour))
	if apps := f.apps(t); len(apps) != 0 {
		t.Fatalf("F1 legacy grant survived its deadline: %+v", apps)
	}
}

func TestPortalVisitorListener(t *testing.T) {
	f := newPortalFixture(t)
	for _, tc := range []struct {
		name, login string
		tags        []string
		want        []string
	}{
		{"owner", "owner", nil, []string{"photos", "secret-payroll"}},
		{"admin", "admin", nil, []string{"photos", "secret-payroll"}},
		{"person", "alice", nil, []string{"photos"}},
		{"legacy", "legacy", nil, []string{"secret-payroll"}},
		{"tagged", "alice", []string{"tag:robot"}, []string{"secret-payroll"}},
		{"tagged-owner", "owner", []string{"tag:other"}, []string{}},
		{"stranger", "stranger", nil, []string{}},
		{"malformed", "ali\nce", nil, []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f.who.Store(&apitype.WhoIsResponse{UserProfile: &tailcfg.UserProfile{LoginName: tc.login}, Node: &tailcfg.Node{Tags: tc.tags}})
			apps := f.apps(t)
			names := []string{}
			for _, a := range apps {
				names = append(names, a.Name)
				if a.URL != "https://"+a.Name+".tailnet.ts.net" || a.Health != "healthy" {
					t.Fatalf("runtime observation: %+v", a)
				}
			}
			if !slices.Equal(names, tc.want) {
				t.Fatalf("apps=%v want=%v", names, tc.want)
			}
			resp, html := f.request(t, "GET", "/", "home.tailnet.ts.net", "", nil)
			if resp.StatusCode != 200 || resp.Header.Get("Cache-Control") != "private, no-store" || resp.Header.Get("Content-Security-Policy") != portalCSP() {
				t.Fatalf("headers=%v status=%d", resp.Header, resp.StatusCode)
			}
			for _, name := range []string{"photos", "secret-payroll"} {
				visible := false
				for _, n := range tc.want {
					visible = visible || n == name
				}
				if strings.Contains(html, "<h2>"+name+"</h2>") != visible {
					t.Fatalf("HTML and JSON differ for %s", name)
				}
				if !visible && strings.Contains(html, name) {
					t.Fatalf("hidden name or URL leaked: %s", name)
				}
			}
		})
	}
	if f.fake.listenFunnelCalled != 0 {
		t.Fatal("portal used Funnel")
	}
}

func TestPortalDeadlineAndTombstoneListener(t *testing.T) {
	f := newPortalFixture(t)
	original, _ := os.ReadFile(f.path)
	apps := f.apps(t)
	if len(apps) != 1 || apps[0].ExpiresAt == nil || !strings.Contains(apps[0].ExpiryText, "January 1, 2030 at 01:00 UTC") {
		t.Fatalf("expiry=%+v", apps)
	}
	f.now.Add(int64(time.Hour) - 1)
	if len(f.apps(t)) != 1 {
		t.Fatal("grant ended before deadline")
	}
	f.now.Add(1)
	if len(f.apps(t)) != 0 {
		t.Fatal("grant visible at deadline")
	}
	unchanged, _ := os.ReadFile(f.path)
	if !bytes.Equal(original, unchanged) {
		t.Fatal("read-only portal changed registry")
	}
	f.now.Add(-int64(time.Hour))
	if _, err := registry.RemovePerson(f.path, "alice"); err != nil {
		t.Fatal(err)
	}
	if len(f.apps(t)) != 0 {
		t.Fatal("tombstone leaked apps")
	}
	_, html := f.request(t, "GET", "/", "home.tailnet.ts.net", "", nil)
	if !strings.Contains(html, "No apps available yet") || strings.Contains(html, "photos") || strings.Contains(html, "secret-payroll") {
		t.Fatal("empty state leaked apps")
	}
}

func TestPortalSecurityListener(t *testing.T) {
	f := newPortalFixture(t)
	for _, tc := range []struct {
		name, method, path, host, origin string
		want                             int
	}{
		{"wrong-host", "GET", "/", "attacker.example", "", 403},
		{"origin", "GET", "/", "home.tailnet.ts.net", "https://attacker.example", 403},
		{"null-origin", "GET", "/", "home.tailnet.ts.net", "null", 403},
		{"http-origin", "GET", "/", "home.tailnet.ts.net", "http://home.tailnet.ts.net", 403},
		{"empty-origin", "GET", "/", "home.tailnet.ts.net", "", 200},
		{"same-origin", "GET", "/", "home.tailnet.ts.net", "https://home.tailnet.ts.net", 200},
		{"post", "POST", "/access-requests", "home.tailnet.ts.net", "", 403},
		{"reserved", "GET", "/access-requests", "home.tailnet.ts.net", "", 404},
		{"head", "HEAD", "/api/apps", "home.tailnet.ts.net", "", 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, b := f.request(t, tc.method, tc.path, tc.host, tc.origin, nil)
			if r.StatusCode != tc.want {
				t.Fatalf("status=%d body=%s", r.StatusCode, b)
			}
			if r.Header.Get("Cache-Control") != "private, no-store" || r.Header.Get("Content-Security-Policy") == "" {
				t.Fatal("error omitted security headers")
			}
			if tc.method == "HEAD" && b != "" {
				t.Fatal("HEAD emitted body")
			}
		})
	}
	f.who.Store(nil)
	r, _ := f.request(t, "GET", "/", "home.tailnet.ts.net", "", nil)
	if r.StatusCode != 403 {
		t.Fatal("missing identity accepted")
	}
	f.who.Store(&apitype.WhoIsResponse{UserProfile: &tailcfg.UserProfile{LoginName: "alice"}})
	if err := os.WriteFile(f.path, []byte(`{"schema_version":2,"services":[`), 0o600); err != nil {
		t.Fatal(err)
	}
	r, b := f.request(t, "GET", "/", "home.tailnet.ts.net", "", nil)
	if r.StatusCode != 503 || strings.Contains(b, "photos") {
		t.Fatalf("partial registry status=%d body=%s", r.StatusCode, b)
	}
}

func TestPortalGoldenHTML(t *testing.T) {
	for _, tc := range []struct {
		name string
		page portalPage
	}{
		{"apps", portalPage{Apps: []PortalApp{{Name: "Family photos", URL: "https://photos.tailnet.ts.net", Health: "healthy", ExpiryText: "Access ends January 2, 2030 at 12:00 UTC."}, {Name: "Notes", URL: "https://notes.tailnet.ts.net", Health: "degraded", ExpiryText: "No scheduled expiry."}, {Name: "Calendar", URL: "https://calendar.tailnet.ts.net", Health: "down", ExpiryText: "No scheduled expiry."}, {Name: "Documents", Health: "unknown", ExpiryText: "No scheduled expiry."}}}},
		{"empty", portalPage{Apps: []PortalApp{}}},
		{"tcp", portalPage{Apps: []PortalApp{{Name: "Database", URL: "database.tailnet.ts.net:5432", Health: "healthy", ExpiryText: "No scheduled expiry."}}}},
		{"hostile", portalPage{Apps: []PortalApp{{Name: `<script>alert("x")</script>&'`, URL: `https://photos.tailnet.ts.net/?x="<x>&`, Health: "healthy", ExpiryText: `Expires <b>now</b>`}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var b bytes.Buffer
			if err := portalTemplate.Execute(&b, tc.page); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join("testdata", "portal-"+tc.name+".golden.html")
			if os.Getenv("UPDATE_PORTAL_GOLDEN") == "1" {
				if err := os.WriteFile(path, b.Bytes(), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(want, b.Bytes()) {
				t.Fatalf("golden mismatch %s: %v", path, err)
			}
			if strings.Contains(b.String(), "<script>") || strings.Contains(b.String(), "<b>now</b>") {
				t.Fatal("unescaped hostile HTML")
			}
		})
	}
}

func TestPortalIndependentDisable(t *testing.T) {
	f := newPortalFixture(t)
	before := f.s.nodes["photos"]
	f.s.syncPortal(context.Background(), nil)
	if f.s.nodes["photos"] != before || before.tsnetSrv.(*fakeTSNetServer).closed {
		t.Fatal("portal shutdown changed app node")
	}
	if f.fake.closeCount.Load() != 1 {
		t.Fatalf("portal closes=%d", f.fake.closeCount.Load())
	}
	f.s.syncPortal(context.Background(), nil)
	if f.fake.closeCount.Load() != 1 {
		t.Fatal("duplicate portal close")
	}
}

func TestPortalRuntimeHealth(t *testing.T) {
	f := newPortalFixture(t)
	for _, state := range []string{health.Healthy, health.Degraded, health.Down} {
		f.s.mu.Lock()
		h := f.s.healthStates["photos"]
		h.Health.State = state
		f.s.healthStates["photos"] = h
		f.s.mu.Unlock()
		if a := f.apps(t); a[0].Health != state {
			t.Fatalf("health=%s want=%s", a[0].Health, state)
		}
	}
	f.now.Add(int64(3 * time.Minute))
	if a := f.apps(t); a[0].Health != "unknown" {
		t.Fatal("stale health survived")
	}
	f.s.mu.Lock()
	f.s.nodes["photos"].tsnetSrv.(*fakeTSNetServer).certDomains = []string{"invalid/host"}
	f.s.mu.Unlock()
	if a := f.apps(t); a[0].URL != "" {
		t.Fatal("invalid canonical authority exposed")
	}
}

func TestPortalWhoIsFailureListener(t *testing.T) {
	f := newPortalFixture(t)
	// Missing profile is distinguishable from a valid visitor with no apps.
	f.who.Store(&apitype.WhoIsResponse{})
	r, b := f.request(t, "GET", "/api/apps", "home.tailnet.ts.net", "", nil)
	if r.StatusCode != 403 || !strings.Contains(b, "unable to identify caller") {
		t.Fatalf("status=%d body=%s", r.StatusCode, b)
	}
}

func TestPortalCSPMatchesRenderedStyle(t *testing.T) {
	f := newPortalFixture(t)
	resp, html := f.request(t, "GET", "/", "home.tailnet.ts.net", "", nil)
	_, rest, ok := strings.Cut(html, "<style>")
	if !ok {
		t.Fatal("stylesheet missing")
	}
	css, _, ok := strings.Cut(rest, "</style>")
	if !ok {
		t.Fatal("stylesheet is incomplete")
	}
	hash := sha256.Sum256([]byte(css))
	csp := resp.Header.Get("Content-Security-Policy")
	for _, directive := range []string{"default-src 'none'", "style-src 'sha256-" + base64.StdEncoding.EncodeToString(hash[:]) + "'", "base-uri 'none'", "frame-ancestors 'none'", "form-action 'self'"} {
		if !strings.Contains(csp, directive) {
			t.Fatalf("CSP missing %s: %s", directive, csp)
		}
	}
	if strings.Contains(csp, "unsafe-inline") || strings.Contains(csp, "unsafe-eval") {
		t.Fatal("CSP permits arbitrary active content")
	}
}

func TestPortalAdminTombstoneAndExpiryLatch(t *testing.T) {
	f := newPortalFixture(t)
	if _, err := registry.ChangePerson(f.path, "admin", []string{"photos"}, nil, false, false); err != nil {
		t.Fatal(err)
	}
	f.who.Store(&apitype.WhoIsResponse{UserProfile: &tailcfg.UserProfile{LoginName: "admin"}})
	if len(f.apps(t)) != 2 {
		t.Fatal("administrator control cannot see all apps")
	}
	if _, err := registry.RemovePerson(f.path, "admin"); err != nil {
		t.Fatal(err)
	}
	if len(f.apps(t)) != 0 {
		t.Fatal("tombstoned administrator retained access")
	}
	f.who.Store(&apitype.WhoIsResponse{UserProfile: &tailcfg.UserProfile{LoginName: "alice"}})
	if len(f.apps(t)) != 1 {
		t.Fatal("active grant control is missing")
	}
	f.now.Add(int64(time.Hour))
	if _, err := registry.ExpirePeople(f.path, time.Unix(0, f.now.Load())); err != nil {
		t.Fatal(err)
	}
	f.now.Add(-int64(time.Hour))
	if len(f.apps(t)) != 0 {
		t.Fatal("clock rollback resurrected expiry latch")
	}
}
