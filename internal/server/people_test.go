package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/registry"
	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/tailcfg"
)

func TestPeopleHTTPEnforcementWithoutReload(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(config.ConfigDirEnv, dir)
	path := filepath.Join(dir, "registry.json")
	svc := registry.Service{Name: "photos", Type: registry.TypeProxy, Target: "http://localhost:3000", AllowedUsers: []string{"bob"}}
	if _, err := registry.Add(path, svc); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	login := "bob"
	var client *LocalClient
	// Use the real LocalClient HTTP decoder with an in-memory fake transport.
	setLogin := func(s string) {
		login = s
		client = fakeWhoIsClient(t, &apitype.WhoIsResponse{UserProfile: &tailcfg.UserProfile{LoginName: s}}, nil)
	}
	setLogin(login)
	h := peopleMiddleware(path, svc, func() (*LocalClient, error) { return client, nil }, func() time.Time { return now })(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmtBody := "upstream private photo"
		w.Write([]byte(fmtBody))
	}))
	request := func(want int) {
		t.Helper()
		r := httptest.NewRequest("GET", "https://photos.test/", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("login=%s got %d want %d body=%s", login, w.Code, want, w.Body.String())
		}
		if want == 200 && w.Body.String() != "upstream private photo" {
			t.Fatal("positive control did not reach upstream")
		}
		if want == 403 && w.Body.String() != `{"error":"access denied"}`+"\n" {
			t.Fatalf("wrong refusal %s", w.Body.String())
		}
	}
	request(200)
	deadline := now.Add(time.Hour)
	if _, err := registry.ChangePerson(path, "alice", []string{"photos"}, &deadline, true, false); err != nil {
		t.Fatal(err)
	}
	setLogin("alice")
	request(200)
	setLogin("eve")
	request(403)
	setLogin("bob")
	request(200) // Explicit legacy ACL remains supported.
	setLogin("alice")
	now = deadline
	request(403)
	now = deadline.Add(-2 * time.Hour)
	request(403) // Durable latch defeats rollback.
	reg, err := registry.Load(path)
	if err != nil || !reg.People[0].Grants[0].Expired {
		t.Fatal("request did not persist expiry", err)
	}
	if _, err := registry.ChangePerson(path, "alice", nil, nil, true, true); err != nil {
		t.Fatal(err)
	}
	request(200)
	if _, err := registry.RemovePerson(path, "alice"); err != nil {
		t.Fatal(err)
	}
	request(403)
	// Construct a new handler (restart), with a legacy unrestricted snapshot:
	// the registry still overrides accepted shares and old empty allow lists.
	svc.AllowedUsers = nil
	h = peopleMiddleware(path, svc, func() (*LocalClient, error) { return client, nil }, func() time.Time { return now })(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("upstream private photo")) }))
	request(403)
}

func TestPeopleHTTPWhoIsFailuresAndClockCapture(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(config.ConfigDirEnv, dir)
	path := filepath.Join(dir, "registry.json")
	svc := registry.Service{Name: "photos", Type: registry.TypeProxy, Target: "http://localhost:3000"}
	if _, err := registry.Add(path, svc); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	deadline := now.Add(time.Hour)
	if _, err := registry.ChangePerson(path, "alice", []string{"photos"}, &deadline, true, false); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name      string
		who       *apitype.WhoIsResponse
		err       error
		nilClient bool
		want      int
	}{
		{"positive", &apitype.WhoIsResponse{UserProfile: &tailcfg.UserProfile{LoginName: "alice"}}, nil, false, 200},
		{"nil client", nil, nil, true, 403}, {"whois failure", nil, errors.New("offline"), false, 403}, {"nil response", nil, nil, false, 403}, {"missing profile", &apitype.WhoIsResponse{}, nil, false, 403},
		{"tagged", &apitype.WhoIsResponse{UserProfile: &tailcfg.UserProfile{LoginName: "alice"}, Node: &tailcfg.Node{Tags: []string{"tag:admin"}}}, nil, false, 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lc := fakeWhoIsClient(t, tc.who, tc.err)
			if tc.nilClient {
				lc = nil
			}
			old := serverNowFn
			t.Cleanup(func() { serverNowFn = old })
			serverNowFn = func() time.Time { return now }
			h := peopleMiddleware(path, svc, func() (*LocalClient, error) { return lc, tc.err }, serverNowFn)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("private")) }))
			serverNowFn = func() time.Time { return deadline.Add(time.Hour) }
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
			if w.Code != tc.want {
				t.Fatalf("%d != %d %s", w.Code, tc.want, w.Body.String())
			}
		})
	}
	if err := os.WriteFile(path, []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	h := peopleMiddleware(path, svc, func() (*LocalClient, error) {
		t.Fatal("WhoIs must not be reached on corrupt registry")
		return nil, nil
	}, func() time.Time { return now })(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if w.Code != 403 || !strings.Contains(w.Body.String(), "registry unavailable") {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestPeopleLifecycleTickExpiresDurably(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(config.ConfigDirEnv, dir)
	path := filepath.Join(dir, "registry.json")
	if _, err := registry.Add(path, registry.Service{Name: "photos", Type: registry.TypeProxy, Target: "http://localhost:3000"}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	deadline := now.Add(time.Hour)
	if _, err := registry.ChangePerson(path, "alice", []string{"photos"}, &deadline, true, false); err != nil {
		t.Fatal(err)
	}
	oldClock, oldInterval, oldPath := serverNowFn, lifecycleTickerInterval, registryPathFn
	t.Cleanup(func() { serverNowFn = oldClock; lifecycleTickerInterval = oldInterval; registryPathFn = oldPath })
	serverNowFn = func() time.Time { return deadline }
	lifecycleTickerInterval = time.Millisecond
	registryPathFn = func() (string, error) { return path, nil }
	s := &Server{lifecycleReconcileFn: func(context.Context, time.Time) (bool, error) { return false, nil }}
	ctx, cancel := context.WithCancel(context.Background())
	done := s.startLifecycleTicker(ctx)
	// Captured clock must remain the deadline, despite changing the seam.
	serverNowFn = func() time.Time { return now.Add(-time.Hour) }
	defer func() { cancel(); <-done }()
	timeout := time.After(time.Second)
	for {
		reg, err := registry.Load(path)
		if err != nil {
			t.Fatal(err)
		}
		if reg.People[0].Grants[0].Expired {
			break
		}
		select {
		case <-timeout:
			t.Fatal("tick did not persist expired grant")
		case <-time.After(time.Millisecond):
		}
	}
}

func TestPeopleStartupExpiresBeforeStartingNodes(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(config.ConfigDirEnv, dir)
	path := filepath.Join(dir, "registry.json")
	if _, err := registry.Add(path, registry.Service{Name: "photos", Type: registry.TypeProxy, Target: "http://localhost:3000"}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := registry.ChangePerson(path, "alice", []string{"photos"}, &now, true, false); err != nil {
		t.Fatal(err)
	}
	oldClock, oldBefore := serverNowFn, beforeInitialSyncFn
	t.Cleanup(func() { serverNowFn = oldClock; beforeInitialSyncFn = oldBefore })
	serverNowFn = func() time.Time { return now }
	stop := errors.New("stop before networking")
	beforeInitialSyncFn = func(context.Context) error { return stop }
	s, err := New("fake", "")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Run(context.Background()); !errors.Is(err, stop) {
		t.Fatal(err)
	}
	reg, err := registry.Load(path)
	if err != nil || !reg.People[0].Grants[0].Expired {
		t.Fatal("startup did not latch expiry", err)
	}
}

func TestPeopleMiddlewareFailsClosedDuringExpiryWrite(t *testing.T) {
	for _, phase := range []string{"write failure", "invalid re-read"} {
		t.Run(phase, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv(config.ConfigDirEnv, dir)
			path := filepath.Join(dir, "registry.json")
			svc := registry.Service{Name: "photos", Type: registry.TypeProxy, Target: "http://localhost:3000"}
			if _, err := registry.Add(path, svc); err != nil {
				t.Fatal(err)
			}
			deadline := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
			if _, err := registry.ChangePerson(path, "alice", []string{"photos"}, &deadline, true, false); err != nil {
				t.Fatal(err)
			}
			old := peopleExpiryFn
			t.Cleanup(func() { peopleExpiryFn = old })
			peopleExpiryFn = func(path string, now time.Time) (bool, error) {
				if phase == "write failure" {
					return false, errors.New("disk failure")
				}
				if err := os.WriteFile(path, []byte("corrupt"), 0600); err != nil {
					t.Fatal(err)
				}
				return true, nil
			}
			h := peopleMiddleware(path, svc, func() (*LocalClient, error) { t.Fatal("WhoIs reached before registry refusal"); return nil, nil }, func() time.Time { return deadline })(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }))
			// Capture the persistence boundary as well as the clock at construction.
			peopleExpiryFn = old
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
			want := "cannot persist expiry"
			if phase == "invalid re-read" {
				want = "registry unavailable"
			}
			if w.Code != 403 || !strings.Contains(w.Body.String(), want) {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
}

func TestPeopleAssembledProxyAndFileHandlers(t *testing.T) {
	for _, kind := range []string{registry.TypeProxy, registry.TypeFile} {
		t.Run(kind, func(t *testing.T) {
			who := &apitype.WhoIsResponse{UserProfile: &tailcfg.UserProfile{LoginName: "alice"}}
			fake := &fakeTSNetServer{localClient: fakeWhoIsClient(t, who, nil)}
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("private body")) }))
			defer backend.Close()
			svc := registry.Service{Name: "photos", Type: kind, Target: backend.URL}
			if kind == registry.TypeFile {
				svc.Path = t.TempDir()
				svc.Target = ""
				if err := os.WriteFile(filepath.Join(svc.Path, "photo.txt"), []byte("private body"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			h := startHTTPNode(t, fake, svc)
			path, err := registryPathFn()
			if err != nil {
				t.Fatal(err)
			}
			url := "/"
			if kind == registry.TypeFile {
				url = "/photo.txt"
			}
			request := func(want int) {
				t.Helper()
				w := httptest.NewRecorder()
				r := httptest.NewRequest("GET", url, nil)
				r.RemoteAddr = "100.64.0.1:1234"
				h.ServeHTTP(w, r)
				if w.Code != want {
					t.Fatal(w.Code, w.Body.String())
				}
				if want == 200 && w.Body.String() != "private body" {
					t.Fatal("positive backend control failed")
				}
			}
			request(200) // Legacy unrestricted compatibility.
			if _, err := registry.ChangePerson(path, "alice", []string{"photos"}, nil, false, false); err != nil {
				t.Fatal(err)
			}
			request(200)
			who.UserProfile.LoginName = "eve"
			request(403)
			who.UserProfile.LoginName = "alice"
			request(200)
			if _, err := registry.RemovePerson(path, "alice"); err != nil {
				t.Fatal(err)
			}
			request(403)
		})
	}
}

func TestPeopleMiddlewareKeepsUnrelatedServiceIssuesIsolated(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(config.ConfigDirEnv, dir)
	path := filepath.Join(dir, "registry.json")
	svc := registry.Service{Name: "photos", Type: registry.TypeProxy, Target: "http://localhost:3000"}
	for _, ownIssue := range []bool{false, true} {
		broken := "other"
		if ownIssue {
			broken = "photos"
		}
		body := `{"schema_version":2,"services":[{"name":"` + broken + `","type":"proxy","target":"http://localhost:3000","typo":true}`
		if !ownIssue {
			body += `,{"name":"photos","type":"proxy","target":"http://localhost:3000","people_scoped":true}`
		}
		body += `],"people":[{"login":"alice","grants":[{"app":"photos"}]}]}`
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		client := fakeWhoIsClient(t, &apitype.WhoIsResponse{UserProfile: &tailcfg.UserProfile{LoginName: "alice"}}, nil)
		h := peopleMiddleware(path, svc, func() (*LocalClient, error) { return client, nil }, func() time.Time { return time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC) })(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("private")) }))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
		want := 200
		if ownIssue {
			want = 403
		}
		if w.Code != want {
			t.Fatal("wrong isolated failure boundary", w.Code, w.Body.String())
		}
	}
}
