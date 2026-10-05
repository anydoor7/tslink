package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/registry"
	"github.com/anydoor7/tslink/internal/testwait"
	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/tailcfg"
)

// Ported independent probes. All files are temporary and listeners are loopback-only.

func TestF5ReviewLifecycleControls(t *testing.T) {
	for _, action := range []string{"disable", "replace"} {
		t.Run(action, func(t *testing.T) {
			f := newPortalFixture(t)
			before := f.s.nodes["photos"]
			var nextAddr string
			if action == "disable" {
				if err := registry.DisablePortal(f.path); err != nil {
					t.Fatal(err)
				}
			} else {
				ln, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				defer ln.Close()
				nextAddr = ln.Addr().String()
				node := &preserveHostTSNetServer{ln: ln, fakeTSNetServer: fakeTSNetServer{dnsName: "family.tailnet.ts.net", certDomains: []string{"family.tailnet.ts.net"}, localClient: f.fake.localClient}}
				newTSNetServerFn = func(registry.Service, string, string, string) tsnetServer { return node }
				if err := registry.SetPortal(f.path, &registry.PortalConfig{Enabled: true, Hostname: "family", Owner: "owner"}); err != nil {
					t.Fatal(err)
				}
			}
			if err := f.s.syncNodes(context.Background()); err != nil {
				t.Fatal(err)
			}
			if f.fake.closeCount.Load() != 1 || f.s.nodes["photos"] != before {
				t.Fatal("old portal not closed exactly once or app replaced")
			}
			if conn, err := net.DialTimeout("tcp", f.addr, time.Second); err == nil {
				conn.Close()
				t.Fatal("old listener still accepts")
			}
			if action == "replace" {
				<-f.s.portalRun.done
				f.addr = nextAddr
				resp, body := f.request(t, "GET", "/api/apps", "family.tailnet.ts.net", "", nil)
				if resp.StatusCode != 200 || !strings.Contains(body, `"name":"photos"`) {
					t.Fatalf("replacement not usable: %d %s", resp.StatusCode, body)
				}
			}
		})
	}
}

func TestF5ReviewPortalOnlyRunRestart(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(config.ConfigDirEnv, dir)
	if err := config.EnsureDir(); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetPortal(filepath.Join(dir, "registry.json"), &registry.PortalConfig{Enabled: true, Hostname: "home", Owner: "owner"}); err != nil {
		t.Fatal(err)
	}
	oldFactory := newTSNetServerFn
	defer func() { newTSNetServerFn = oldFactory }()
	for round := 0; round < 2; round++ {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		fake := &preserveHostTSNetServer{ln: ln, fakeTSNetServer: fakeTSNetServer{dnsName: "home.tailnet.ts.net", certDomains: []string{"home.tailnet.ts.net"}, localClient: fakeWhoIsClient(t, &apitype.WhoIsResponse{UserProfile: &tailcfg.UserProfile{LoginName: "owner"}}, nil)}}
		newTSNetServerFn = func(registry.Service, string, string, string) tsnetServer { return fake }
		s, err := New("fixture", "")
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		ready := make(chan struct{})
		s.SetReadyFunc(func() error { close(ready); return nil })
		done := make(chan error, 1)
		go func() { done <- s.Run(ctx) }()
		select {
		case <-ready:
		case <-time.After(testwait.Budget(t)):
			cancel()
			<-done
			t.Fatal("Run did not become ready")
		}
		s.mu.Lock()
		run := s.portalRun
		s.mu.Unlock()
		select {
		case <-run.done:
		case <-time.After(testwait.Budget(t)):
			cancel()
			<-done
			t.Fatal("portal did not start")
		}
		f := portalFixture{addr: ln.Addr().String()}
		resp, body := f.request(t, "GET", "/", "home.tailnet.ts.net", "", nil)
		if resp.StatusCode != 200 || !strings.Contains(body, "No apps available yet") {
			cancel()
			<-done
			t.Fatalf("portal-only empty response=%d %s", resp.StatusCode, body)
		}
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(testwait.Budget(t)):
			t.Fatal("Run failed to join shutdown")
		}
		if fake.closeCount.Load() != 1 {
			t.Fatalf("round %d close count=%d", round, fake.closeCount.Load())
		}
		if conn, err := net.DialTimeout("tcp", ln.Addr().String(), time.Second); err == nil {
			conn.Close()
			t.Fatal("shutdown listener accepts")
		}
		t.Logf("round=%d empty portal served, Run cancellation joined, listener closed", round)
	}
}

func TestF5ReviewPendingPortalLifecycle(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(config.ConfigDirEnv, dir)
	if err := config.EnsureDir(); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetPortal(filepath.Join(dir, "registry.json"), &registry.PortalConfig{Enabled: true, Hostname: "home", Owner: "owner"}); err != nil {
		t.Fatal(err)
	}
	oldFactory, oldStatus := newTSNetServerFn, tsnetStatusClientFn
	defer func() { newTSNetServerFn = oldFactory; tsnetStatusClientFn = oldStatus }()
	fake := &fakeInteractiveTSNetServer{}
	status := &gatedInteractiveStatusClient{ready: make(chan struct{})}
	newTSNetServerFn = func(registry.Service, string, string, string) tsnetServer { return fake }
	tsnetStatusClientFn = func(tsnetServer) (tsnetStatusClient, error) { return status, nil }
	s, err := New("", "")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	emitted, ready := make(chan struct{}), make(chan struct{})
	path := filepath.Join(dir, "auth-handoff.json")
	terminal := make(chan string, 1)
	s.SetAuthHandoffFunc(func(_ context.Context, handoff AuthHandoff) error {
		b, err := json.Marshal(handoff)
		if err != nil {
			return err
		}
		var event map[string]string
		if err := json.Unmarshal(b, &event); err != nil {
			return err
		}
		if state := event["State"]; state == "complete" || state == "cancelled" {
			terminal <- state
			return os.Remove(path)
		}
		if err := os.WriteFile(path, b, 0600); err != nil {
			return err
		}
		close(emitted)
		return nil
	})
	s.SetReadyFunc(func() error {
		// Wait for a real pending enrollment file before reporting ready.
		select {
		case <-emitted:
		case <-ctx.Done():
			return ctx.Err()
		}
		if _, err := os.ReadFile(path); err != nil {
			return fmt.Errorf("handoff positive control: %w", err)
		}
		close(ready)
		return nil
	})
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	select {
	case <-ready:
	case err := <-done:
		cancel()
		t.Fatalf("Run returned before ready: %v", err)
	case <-time.After(testwait.Budget(t)):
		cancel()
		<-done
		t.Fatal("handoff not emitted")
	}
	s.mu.Lock()
	state := s.portalState
	s.mu.Unlock()
	_, statErr := os.Stat(path)
	t.Logf("after daemon readiness: portal=%+v handoff_exists=%t (record positively read before cleanup)", state, statErr == nil)
	if state.State == "starting" && os.IsNotExist(statErr) {
		t.Error("daemon readiness erased the only pending portal login handoff")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(testwait.Budget(t)):
		t.Fatal("pending enrollment failed to join on Run cancellation")
	}
	if fake.closeCount.Load() != 1 {
		t.Fatalf("pending portal node closed %d times", fake.closeCount.Load())
	}
	select {
	case state := <-terminal:
		if state != "cancelled" {
			t.Fatalf("terminal state=%s", state)
		}
	default:
		t.Error("cancelled portal enrollment did not terminate its handoff")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("cancelled handoff remains: %v", err)
	}
}

func TestF5ReviewReconcileOnAppFailure(t *testing.T) {
	for _, action := range []string{"disable", "replace"} {
		t.Run(action, func(t *testing.T) {
			f := newPortalFixture(t)
			before := f.s.nodes["photos"]
			var nextAddr string
			var calls atomic.Int64
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				http.Error(w, "fixture auth service unavailable", http.StatusServiceUnavailable)
			}))
			defer api.Close()
			f.s.authKeyProvider = func(ctx context.Context, svc registry.Service) (string, error) {
				if svc.Name != "broken" {
					return "fixture", nil
				}
				req, _ := http.NewRequestWithContext(ctx, "POST", api.URL+"/api/v2/tailnet/-/keys", nil)
				res, err := http.DefaultClient.Do(req)
				if err != nil {
					return "", err
				}
				defer res.Body.Close()
				return "", fmt.Errorf("fixture key mint failed: HTTP %d", res.StatusCode)
			}
			if _, err := registry.Add(f.path, registry.Service{Name: "broken", Type: registry.TypeProxy, Target: "http://127.0.0.1:8003"}); err != nil {
				t.Fatal(err)
			}
			if action == "disable" {
				if err := registry.DisablePortal(f.path); err != nil {
					t.Fatal(err)
				}
			} else {
				ln, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				defer ln.Close()
				nextAddr = ln.Addr().String()
				node := &preserveHostTSNetServer{ln: ln, fakeTSNetServer: fakeTSNetServer{dnsName: "family.tailnet.ts.net", certDomains: []string{"family.tailnet.ts.net"}, localClient: f.fake.localClient}}
				newTSNetServerFn = func(registry.Service, string, string, string) tsnetServer { return node }
				if err := registry.SetPortal(f.path, &registry.PortalConfig{Enabled: true, Hostname: "family", Owner: "owner"}); err != nil {
					t.Fatal(err)
				}
			}
			err := f.s.syncNodes(context.Background())
			if err == nil || !strings.Contains(err.Error(), "HTTP 503") || calls.Load() != 1 {
				t.Fatalf("fixture did not exercise auth failure: err=%v calls=%d", err, calls.Load())
			}
			if f.s.nodes["photos"] != before {
				t.Fatal("unchanged app node was replaced")
			}
			if f.fake.closeCount.Load() != 1 {
				t.Errorf("old portal listener was not closed exactly once after %s when unrelated app key mint failed", action)
			}
			if conn, err := net.DialTimeout("tcp", f.addr, time.Second); err == nil {
				conn.Close()
				t.Error("old listener still accepts after reconciliation")
			}
			if action == "replace" {
				f.s.mu.Lock()
				run := f.s.portalRun
				f.s.mu.Unlock()
				if run == nil || run.config.Hostname != "family" {
					t.Error("replacement worker missing")
				} else {
					<-run.done
					f.addr = nextAddr
					resp, body := f.request(t, "GET", "/api/apps", "family.tailnet.ts.net", "", nil)
					if resp.StatusCode != 200 || !strings.Contains(body, `"name":"photos"`) {
						t.Errorf("replacement unavailable: %d %s", resp.StatusCode, body)
					}
				}
			}

		})
	}
}

type reviewTCPNode struct {
	fakeTSNetServer
	listener net.Listener
}

func (n *reviewTCPNode) Listen(string, string) (net.Listener, error) { return n.listener, nil }

func TestF5ReviewTCPMatchesPortal(t *testing.T) {
	f := newPortalFixture(t)
	backend, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	backendDone := make(chan struct{})
	go func() {
		defer close(backendDone)
		conn, err := backend.Accept()
		if err == nil {
			defer conn.Close()
			_, _ = io.WriteString(conn, "PRIVATE-DB-HELLO\n")
		}
	}()
	t.Cleanup(func() { backend.Close(); <-backendDone })
	svc := registry.Service{Name: "database", Type: registry.TypeTCP, Target: backend.Addr().String(), Port: 5432}
	if _, err := registry.Add(f.path, svc); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	node := &reviewTCPNode{listener: ln, fakeTSNetServer: fakeTSNetServer{dnsName: "database.tailnet.ts.net"}}
	newTSNetServerFn = func(registry.Service, string, string, string) tsnetServer { return node }
	if err := f.s.startNodeLocked(context.Background(), svc); err != nil {
		t.Fatal(err)
	}
	defer f.s.stopNodeLocked(svc.Name)
	conn, err := net.DialTimeout("tcp", ln.Addr().String(), testwait.Budget(t))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(testwait.Budget(t)))
	buf := make([]byte, len("PRIVATE-DB-HELLO\n"))
	if _, err := io.ReadFull(conn, buf); err != nil || string(buf) != "PRIVATE-DB-HELLO\n" {
		t.Fatalf("real TCP backend not served: %q %v", buf, err)
	}
	reg, _, err := registry.Preflight(f.path)
	if err != nil {
		t.Fatal(err)
	}
	for _, login := range []string{"alice", "stranger", "owner", "admin"} {
		// TCP rejects allowed_users at registry admission and has no identity enforcement.
		allowed, expiry := AppAccessAt(reg, svc, login, nil, time.Unix(0, f.now.Load()))
		if !allowed || expiry != nil {
			t.Errorf("TCP enforcement decision for %s=%t expiry=%v, real backend connected", login, allowed, expiry)
		}
		f.who.Store(&apitype.WhoIsResponse{UserProfile: &tailcfg.UserProfile{LoginName: login}})
		want := login == "owner" || login == "admin"
		visible := false
		for _, app := range f.apps(t) {
			if app.Name == svc.Name {
				visible = app.URL == "database.tailnet.ts.net:5432"
			}
		}
		if visible != want {
			t.Errorf("TCP directory for %s visible=%t want=%t", login, visible, want)
		}
		_, html := f.request(t, "GET", "/", "home.tailnet.ts.net", "", nil)
		if want && !strings.Contains(html, "Anyone who can reach this device can connect; TSLink can&#39;t limit it per person.") {
			t.Error("owner/admin TCP card missing enforcement note")
		}
		if !want && strings.Contains(html, "database") {
			t.Error("TCP card disclosed to visitor")
		}
	}
}

func TestF5ReviewBrowserArtifacts(t *testing.T) {
	f := newPortalFixture(t)
	f.who.Store(&apitype.WhoIsResponse{UserProfile: &tailcfg.UserProfile{LoginName: "owner"}})
	svc := registry.Service{Name: "tcp-address", Type: registry.TypeTCP, Target: "127.0.0.1:8000", Port: 5432}
	if _, err := registry.Add(f.path, svc); err != nil {
		t.Fatal(err)
	}
	f.s.mu.Lock()
	f.s.nodes[svc.Name] = &ServiceNode{service: svc, runtimeHost: "database.tailnet.ts.net", tsnetSrv: &fakeTSNetServer{dnsName: "database.tailnet.ts.net"}}
	f.s.mu.Unlock()
	for _, name := range []string{"ordinary", "empty", "long-url"} {
		if name == "empty" {
			f.who.Store(&apitype.WhoIsResponse{UserProfile: &tailcfg.UserProfile{LoginName: "stranger"}})
		}
		if name == "long-url" {
			f.who.Store(&apitype.WhoIsResponse{UserProfile: &tailcfg.UserProfile{LoginName: "owner"}})
			host := strings.Repeat("a", 63) + ".tail123456.ts.net"
			f.s.mu.Lock()
			f.s.nodes["photos"].tsnetSrv.(*fakeTSNetServer).certDomains = []string{host}
			f.s.nodes[svc.Name].runtimeHost = host
			f.s.nodes[svc.Name].tsnetSrv.(*fakeTSNetServer).dnsName = host
			f.s.mu.Unlock()
		}
		resp, body := f.request(t, "GET", "/", "home.tailnet.ts.net", "", nil)
		if resp.StatusCode != 200 {
			t.Fatalf("HTML status=%d", resp.StatusCode)
		}
		if name == "long-url" && (!strings.Contains(body, "https://"+strings.Repeat("a", 63)) || !strings.Contains(body, strings.Repeat("a", 63)+".tail123456.ts.net:5432")) {
			t.Fatal("long HTTPS/TCP positive controls missing")
		}
		if out := os.Getenv("F5_PORTAL_BROWSER_OUT"); out != "" {
			if err := os.WriteFile(filepath.Join(out, "browser-"+name+".html"), []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			data, err := json.MarshalIndent(resp.Header, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(out, "browser-"+name+"-headers.json"), data, 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
}
