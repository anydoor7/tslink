package server

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/registry"
	"net/netip"
	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/tailcfg"
)

func TestPortalServiceEnforcementModel(t *testing.T) {
	f := newPortalFixture(t)
	public := registry.Service{Name: "public-site", Type: registry.TypeProxy, Target: "http://127.0.0.1:8004", Funnel: true, PublicAck: true}
	if _, err := registry.Add(f.path, public); err != nil {
		t.Fatal(err)
	}
	reg, _, err := registry.Preflight(f.path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(0, f.now.Load())
	for _, login := range []string{"owner", "admin", "alice", "stranger"} {
		for _, tagged := range []bool{false, true} {
			var tags []string
			if tagged {
				tags = []string{"tag:robot"}
			}
			decision := AppAccessDecisionAt(reg, public, login, tags, now)
			wantVisible := !tagged && (login == "owner" || login == "admin")
			if !decision.Allowed || decision.IdentityEnforced || decision.ExpiresAt != nil || decision.DirectoryVisible != wantVisible {
				t.Fatalf("Funnel login=%s tags=%v decision=%+v", login, tags, decision)
			}
			f.who.Store(&apitype.WhoIsResponse{UserProfile: &tailcfg.UserProfile{LoginName: login}, Node: &tailcfg.Node{Tags: tags}})
			found := false
			for _, app := range f.apps(t) {
				if app.Name == public.Name {
					found = true
					if app.AccessNote != "Anyone who can reach this device can connect; TSLink can't limit it per person." {
						t.Error("Funnel note missing")
					}
				}
			}
			if found != decision.DirectoryVisible {
				t.Error("portal did not use shared Funnel decision")
			}
			_, html := f.request(t, "GET", "/", "home.tailnet.ts.net", "", nil)
			if !wantVisible && strings.Contains(html, public.Name) {
				t.Error("public card leaked to visitor")
			}
		}
	}
	for _, typ := range []string{registry.TypeProxy, registry.TypeFile} {
		svc := registry.Service{Name: "photos", Type: typ}
		decision := AppAccessDecisionAt(reg, svc, "alice", nil, now)
		if !decision.IdentityEnforced || !decision.Allowed || !decision.DirectoryVisible || decision.ExpiresAt == nil {
			t.Fatalf("private type=%s decision=%+v", typ, decision)
		}
	}
	if got := AppAccessDecisionAt(reg, registry.Service{Type: "unsupported"}, "owner", nil, now); got != (AppAccessDecision{}) {
		t.Fatalf("unsupported=%+v", got)
	}
	if _, err := registry.RemovePerson(f.path, "owner"); err != nil {
		t.Fatal(err)
	}
	reg, _, err = registry.Preflight(f.path)
	if err != nil {
		t.Fatal(err)
	}
	decision := AppAccessDecisionAt(reg, public, "owner", nil, now)
	if !decision.Allowed || decision.DirectoryVisible {
		t.Fatalf("revoked owner public semantics=%+v", decision)
	}
}

func TestPortalCancelledContextPreservesCurrentRun(t *testing.T) {
	f := newPortalFixture(t)
	before := f.s.portalRun
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f.s.syncPortal(ctx, nil)
	if f.s.portalRun != before || f.fake.closeCount.Load() != 0 {
		t.Fatal("cancelled generation changed the portal")
	}
}

func TestPortalTerminalHandoffErrorStillClosesNode(t *testing.T) {
	for _, terminal := range []string{"complete", "cancelled"} {
		t.Run(terminal, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("TSLINK_CONFIG_DIR", dir)
			s, err := New("", "")
			if err != nil {
				t.Fatal(err)
			}
			fake := &fakeInteractiveTSNetServer{}
			statuses := &sequenceTSNetStatusClient{statuses: []*ipnstate.Status{
				{BackendState: "NeedsLogin", AuthURL: "https://login.example.invalid/fixture"},
				{BackendState: "Running", TailscaleIPs: []netip.Addr{netip.MustParseAddr("100.64.0.1")}},
			}}
			oldFactory, oldStatus := newTSNetServerFn, tsnetStatusClientFn
			newTSNetServerFn = func(registry.Service, string, string, string) tsnetServer { return fake }
			tsnetStatusClientFn = func(tsnetServer) (tsnetStatusClient, error) { return statuses, nil }
			defer func() { s.closePortal(); newTSNetServerFn = oldFactory; tsnetStatusClientFn = oldStatus }()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			s.portalRoot = ctx
			wantErr := errors.New("fixture terminal cleanup failed")
			path := filepath.Join(dir, "offer")
			s.SetAuthHandoffFunc(func(_ context.Context, event AuthHandoff) error {
				if event.State == "pending" {
					if err := os.WriteFile(path, []byte(event.AuthURL), 0600); err != nil {
						return err
					}
					if terminal == "cancelled" {
						cancel()
					}
					return nil
				}
				if event.State != terminal {
					t.Errorf("terminal state=%s want=%s", event.State, terminal)
				}
				if _, err := os.ReadFile(path); err != nil {
					t.Errorf("pending control missing: %v", err)
				}
				return wantErr
			})
			run := &portalRun{config: registry.PortalConfig{Enabled: true, Hostname: "home", Owner: "owner"}}
			_, err = s.startPortalNode(ctx, run, newTSNetServerFn, time.Now, time.Second)
			if !errors.Is(err, wantErr) {
				t.Fatalf("terminal error=%v want cleanup sentinel", err)
			}
			if terminal == "cancelled" && !errors.Is(err, context.Canceled) {
				t.Fatal("lost cancellation cause")
			}
			if fake.closeCount.Load() != 1 {
				t.Fatalf("failed enrollment closed %d times", fake.closeCount.Load())
			}
		})
	}
}

func TestPortalExpiredFunnelUsesPrivateEnforcement(t *testing.T) {
	f := newPortalFixture(t)
	now := time.Unix(0, f.now.Load())
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "PRIVATE-EXPIRED-FUNNEL") }))
	defer backend.Close()
	svc := registry.Service{Name: "temporary-public", Type: registry.TypeProxy, Target: backend.URL, Funnel: true, PublicAck: true, FunnelExpiresAt: &now}
	if _, err := registry.Add(f.path, svc); err != nil {
		t.Fatal(err)
	}
	proxy, err := NewProxyHandler(backend.URL, NewStaticIdentityResolver(f.fake.localClient))
	if err != nil {
		t.Fatal(err)
	}
	private := httptest.NewServer(peopleMiddleware(f.path, registry.EffectiveServiceAt(svc, now), func() (*LocalClient, error) { return f.fake.localClient, nil }, func() time.Time { return now })(proxy))
	defer private.Close()
	for _, login := range []string{"alice", "owner"} {
		f.who.Store(&apitype.WhoIsResponse{UserProfile: &tailcfg.UserProfile{LoginName: login}})
		resp, err := http.Get(private.URL)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		want := http.StatusForbidden
		if login == "owner" {
			want = http.StatusOK
		}
		if resp.StatusCode != want {
			t.Errorf("expired Funnel private proxy login=%s status=%d body=%s want=%d", login, resp.StatusCode, body, want)
		}
		if login == "owner" && string(body) != "PRIVATE-EXPIRED-FUNNEL" {
			t.Fatal("backend positive control missing")
		}
		for _, app := range f.apps(t) {
			if app.Name == svc.Name && (login != "owner" || app.AccessNote != "") {
				t.Error("expired Funnel directory differs from private enforcement")
			}
		}
	}
}
