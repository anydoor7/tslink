package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/registry"
	"github.com/anydoor7/tslink/internal/testwait"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/tailcfg"
)

// Like real tsnet, this factory ignores a new auth key when enrollment state
// already exists. The test observes the persisted node rather than the desired
// Ephemeral flag supplied by the caller.
func TestGatewayCredentialedUpgradeAndPersistentRestart(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(config.ConfigDirEnv, dir)
	t.Setenv("TSLINK_DISABLE_KEYRING", "1")
	s, err := New("fixture", "")
	if err != nil {
		t.Fatal(err)
	}
	s.credentialed = true
	var keys []registry.Service
	s.authKeyProvider = func(_ context.Context, svc registry.Service) (string, error) {
		keys = append(keys, svc)
		return "fixture", nil
	}
	type enrollment struct {
		NodeID    string
		Ephemeral bool
	}
	cert, _ := gatewayTLSCertificate(t)
	var nodes []*gatewayFakeNode
	var observed []enrollment
	ignored := 0
	old := newTSNetServerFn
	newTSNetServerFn = func(svc registry.Service, stateDir, key, _ string) tsnetServer {
		state := enrollment{}
		path := filepath.Join(stateDir, "fixture-enrollment.json")
		data, err := os.ReadFile(path)
		if err == nil {
			if err := json.Unmarshal(data, &state); err != nil {
				t.Error(err)
			}
			ignored++
		} else if errors.Is(err, os.ErrNotExist) {
			state = enrollment{NodeID: fmt.Sprintf("node-%d", len(nodes)+1), Ephemeral: svc.Ephemeral}
			data, _ = json.Marshal(state)
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Error(err)
			}
		} else {
			t.Error(err)
		}
		observed = append(observed, state)
		n := &gatewayFakeNode{fakeTSNetServer: fakeTSNetServer{localClient: fakeWhoIsClient(t, whoIsUser("owner", "owner"), nil), certDomains: []string{"home.tailnet.ts.net"}, status: &ipnstate.Status{Self: &ipnstate.PeerStatus{ID: tailcfg.StableNodeID(state.NodeID), DNSName: "home.tailnet.ts.net."}}}, ports: map[string]net.Listener{}, fail: map[string]bool{}, stall: map[string]chan struct{}{}, cert: cert, stop: make(chan struct{})}
		nodes = append(nodes, n)
		return n
	}
	t.Cleanup(func() { s.closePortal(); newTSNetServerFn = old })
	cfg := &registry.PortalConfig{Enabled: true, Hostname: "home", Owner: "owner"}
	path := filepath.Join(dir, "registry.json")
	if err := registry.SetPortal(path, cfg); err != nil {
		t.Fatal(err)
	}
	s.syncPortal(context.Background(), cfg)
	testwait.Recv(t, s.portalRun.done, "ephemeral portal")
	if s.portalState.State != "running" || !observed[0].Ephemeral {
		t.Fatalf("control ephemeral portal: %+v %+v", s.portalState, observed)
	}
	cfg = &registry.PortalConfig{Enabled: true, Hostname: "home", Owner: "owner", Gateway: &registry.GatewayConfig{Enabled: true, Ports: registry.DefaultGatewayPorts()}}
	if err := registry.SetPortal(path, cfg); err != nil {
		t.Fatal(err)
	}
	s.syncPortal(context.Background(), cfg)
	testwait.Recv(t, s.portalRun.done, "persistent upgrade")
	if s.portalState.State != "running" || observed[1].Ephemeral || observed[1].NodeID == observed[0].NodeID || ignored != 0 {
		t.Fatalf("old enrolled state ignored persistent key: state=%+v enrollments=%+v ignored=%d", s.portalState, observed, ignored)
	}
	if keys[1].Ephemeral || s.portalState.Gateway == nil || !s.portalState.Gateway.Persistent {
		t.Fatal("persistent enrollment not proven")
	}
	nodeID := s.portalState.Gateway.NodeID
	marker, err := readGatewayEnrollment(filepath.Join(dir, "portal-nodes", "home"))
	if err != nil || marker.NodeID != nodeID {
		t.Fatalf("marker: %+v %v", marker, err)
	}
	s.closePortal()
	s.syncPortal(context.Background(), cfg)
	testwait.Recv(t, s.portalRun.done, "restart with marker")
	if ignored != 1 || observed[2].Ephemeral || s.portalState.Gateway.NodeID != nodeID || !s.portalState.Gateway.Persistent {
		t.Fatalf("persistent restart re-enrolled: ignored=%d observed=%+v state=%+v", ignored, observed, s.portalState.Gateway)
	}
}

func TestGatewayEnrollmentResetValidationAndRecovery(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"", "..", "../outside", "home/child"} {
		if err := resetGatewayNodeState(dir, name); err == nil {
			t.Fatalf("unsafe name accepted: %q", name)
		}
	}
	if err := resetGatewayNodeState("", "home"); err == nil {
		t.Fatal("empty config accepted")
	}
	stateDir := filepath.Join(dir, "portal-nodes", "home")
	if err := os.MkdirAll(stateDir, 0700); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"tailscaled.state": "old enrollment", "ownership.json": `{"schema_version":1,"nodes":[]}`} {
		if err := os.WriteFile(filepath.Join(stateDir, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Rename(stateDir, stateDir+".reenroll"); err != nil {
		t.Fatal(err)
	} // crash after retirement
	if err := resetGatewayNodeState(dir, "home"); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(stateDir, "ownership.json")); err != nil || string(data) != `{"schema_version":1,"nodes":[]}` {
		t.Fatalf("ownership history lost: %s %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "tailscaled.state")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old enrollment survived: %v", err)
	}
	if _, err := os.Stat(stateDir + ".reenroll"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("retirement not completed: %v", err)
	}
	identity := gatewayEnrollmentIdentity(true, []string{"tag:door"}, "")
	identity.NodeID = "node"
	if err := writeGatewayEnrollment(stateDir, identity); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareGatewayEnrollment(dir, "home", identity); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, gatewayEnrollmentFile), []byte(`{"node_id":"node","unknown":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareGatewayEnrollment(dir, "home", identity); err == nil {
		t.Fatal("malformed marker accepted")
	}
}

func TestGatewayFailedRouteRetriesWithBackoff(t *testing.T) {
	f := newGatewayFixture(t, func(f *gatewayFixture) { f.node.fail[":8444"] = true })
	failed := f.waitRoute(t, "payroll", "failed")
	if failed.Reason != "route_listener_failed" || !f.s.gatewayRouteRetryPending.Load() {
		t.Fatal("route failure did not schedule retry")
	}
	f.s.portalRun.routesMu.Lock()
	old := f.s.portalRun.routes["payroll"]
	f.s.portalRun.routesMu.Unlock()
	testwait.Recv(t, old.done, "failed activation finished")
	f.node.mu.Lock()
	f.node.fail[":8444"] = false
	f.node.mu.Unlock()
	f.s.syncPortal(context.Background(), f.config)
	if f.waitRoute(t, "payroll", "failed").Reason != "route_listener_failed" {
		t.Fatal("backoff was skipped")
	}
	f.now.Add(int64(2 * time.Second))
	f.s.syncPortal(context.Background(), f.config)
	f.waitRoute(t, "payroll", "running")
	f.who.Store(whoIsUser("owner", "laptop"))
	code, body, _ := gatewayGet(t, f.client(t), "8444", "/", nil)
	if code != 200 || body != "payroll" {
		t.Fatalf("retry did not restore traffic: %d %s", code, body)
	}
}

func TestGatewayRouteRetryTick(t *testing.T) {
	oldInterval := lifecycleTickerInterval
	lifecycleTickerInterval = 20 * time.Millisecond
	t.Cleanup(func() { lifecycleTickerInterval = oldInterval })
	f := newGatewayFixture(t, func(f *gatewayFixture) { f.node.fail[":8444"] = true })
	f.waitRoute(t, "payroll", "failed")
	f.node.mu.Lock()
	f.node.fail[":8444"] = false
	f.node.mu.Unlock()
	f.now.Add(int64(2 * time.Second))
	f.s.lifecycleReconcileFn = func(context.Context, time.Time) (bool, error) { return false, nil }
	ctx, cancel := context.WithCancel(context.Background())
	done := f.s.startLifecycleTicker(ctx)
	defer func() { cancel(); testwait.Recv(t, done, "retry ticker stop") }()
	f.waitRoute(t, "payroll", "running")
}

func TestGatewayReconfigureDuringPendingActivation(t *testing.T) {
	started := make(chan struct{}, 4)
	stalled := make(chan struct{})
	f := newGatewayFixture(t, func(f *gatewayFixture) {
		f.node.stall[":8444"] = stalled
		f.node.entered = map[string]chan struct{}{":8444": started}
	})
	testwait.Recv(t, started, "first activation entered")
	if err := registry.SetPortalRoute(context.Background(), f.path, "payroll", &registry.PortalRoute{Port: 8444, AllowCrossOrigin: true}); err != nil {
		t.Fatal(err)
	}
	f.s.syncPortal(context.Background(), f.config)
	select {
	case <-started:
		t.Fatal("replacement entered ListenTLS before old activation completed")
	case <-time.After(100 * time.Millisecond):
	}
	close(stalled)
	f.waitRoute(t, "payroll", "running")
	testwait.Recv(t, started, "replacement activation entered")
	f.who.Store(whoIsUser("owner", "laptop"))
	if code, body, _ := gatewayGet(t, f.client(t), "8444", "/", nil); code != 200 || body != "payroll" {
		t.Fatalf("replacement route: %d %s", code, body)
	}
	if f.factories.Load() != 1 || f.node.closeCount.Load() != 0 {
		t.Fatal("route reconfigure restarted shared node")
	}
}

func TestGatewayPendingSyncDrainedAtStartupCompletion(t *testing.T) {
	f := newGatewayFixture(t, nil)
	f.waitRoute(t, "photos", "running")
	run := f.s.portalRun
	// Hold the startup completion barrier open after the initial route scan. This
	// deterministically models the registry event in the narrow pre-done window.
	run.done = make(chan struct{})
	if _, err := registry.Add(f.path, registry.Service{Name: "later", Type: registry.TypeProxy, Target: "http://localhost:9", PortalOnly: true}); err != nil {
		t.Fatal(err)
	}
	f.s.syncPortal(context.Background(), f.config)
	f.s.finishPortalStart(context.Background(), run)
	f.waitRoute(t, "later", "running")
	if run.pending {
		t.Fatal("startup refresh remained pending")
	}
}

func TestGatewayTagRefusedWithoutCredentials(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(config.ConfigDirEnv, dir)
	t.Setenv("TSLINK_DISABLE_KEYRING", "1")
	s, err := New("", "")
	if err != nil {
		t.Fatal(err)
	}
	s.authKeyProvider = func(context.Context, registry.Service) (string, error) { return "", nil }
	run := &portalRun{config: registry.PortalConfig{Enabled: true, Hostname: "home", Owner: "owner", Gateway: &registry.GatewayConfig{Enabled: true, Tag: "tag:door", Ports: registry.DefaultGatewayPorts()}}}
	called := false
	_, err = s.startPortalNode(context.Background(), run, func(registry.Service, string, string, string) tsnetServer { called = true; return &fakeTSNetServer{} }, time.Now, time.Second)
	if err == nil || !strings.Contains(err.Error(), "gateway_tag_requires_credentials") || called {
		t.Fatalf("ignored tag on untagged path: err=%v factory=%v", err, called)
	}
}
