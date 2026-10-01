package server

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/lifecycle"
	"github.com/anydoor7/tslink/internal/registry"
	runtimesnapshot "github.com/anydoor7/tslink/internal/runtime"
	"github.com/anydoor7/tslink/internal/tailapi"
	"github.com/anydoor7/tslink/internal/testenv"
	tailscale "tailscale.com/client/tailscale/v2"
)

// Absence from registry.json is not proof that the user removed a service: the
// file may have been lost and recreated, replaced by another copy, or hand
// edited with a typo. A running node whose name is merely absent is stopped;
// its node state, the key that is its tailnet identity, stays. Only the
// proof-gated reconciler deletes a removed service's state, keyed on the
// ownership ledger's retired_at that `tslink remove` writes.

func absentStateTCPService(name string, port int) registry.Service {
	return registry.Service{Name: name, Type: registry.TypeTCP, Target: "127.0.0.1:5432", Port: port, Tags: []string{"tag:tsmain"}}
}

// newDaemonWithEnrolledNodes returns a daemon running a node for each service,
// each with node state on disk and its StableNodeID in the ownership ledger,
// as after a successful enrollment.
func newDaemonWithEnrolledNodes(t *testing.T, running []registry.Service) (s *Server, regPath, ownershipPath string) {
	t.Helper()
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatal(err)
	}
	oldNew := newTSNetServerFn
	newTSNetServerFn = func(registry.Service, string, string, string) tsnetServer { return &fakeTSNetServer{} }
	t.Cleanup(func() { newTSNetServerFn = oldNew })
	regPath = writeRegistry(t, running)
	s, err := New("key", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.closeAllNodes)
	ownershipPath, err = config.NodeOwnershipPath()
	if err != nil {
		t.Fatal(err)
	}
	for _, svc := range running {
		state := absentStateMarker(t, svc.Name)
		if err := os.MkdirAll(filepath.Dir(state), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(state, []byte("node key"), 0o600); err != nil {
			t.Fatal(err)
		}
		s.nodes[svc.Name] = newNode(t, svc)
		if err := runtimesnapshot.RecordOwnedNode(ownershipPath, svc.Name, "n-"+svc.Name, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	return s, regPath, ownershipPath
}

func absentStateMarker(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join(config.NodesDirIn(mustConfigDir(t)), name, "tailscaled.state")
}

func assertNodeStateKept(t *testing.T, names ...string) {
	t.Helper()
	for _, name := range names {
		if _, err := os.Stat(absentStateMarker(t, name)); err != nil {
			t.Fatalf("node state of %q was deleted although nothing proved the service removed: %v", name, err)
		}
	}
}

func runningNames(s *Server) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	names := make([]string, 0, len(s.nodes))
	for name := range s.nodes {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// P1: registry.json is lost while the daemon runs, and the next `tslink add`
// recreates it holding only the new service.
func TestAbsentServiceKeepsStateWhenRegistryIsLostThenAdded(t *testing.T) {
	s, regPath, _ := newDaemonWithEnrolledNodes(t, []registry.Service{absentStateTCPService("a", 5001), absentStateTCPService("b", 5002)})
	if err := os.Remove(regPath); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Add(regPath, absentStateTCPService("c", 5003)); err != nil {
		t.Fatal(err)
	}
	if err := s.syncNodes(context.Background()); err != nil {
		t.Fatalf("syncNodes() error = %v", err)
	}
	if got := runningNames(s); len(got) != 1 || got[0] != "c" {
		t.Fatalf("running = %v, want only c", got)
	}
	assertNodeStateKept(t, "a", "b")
}

// P2: a hand edit mistypes the name (api -> Api), which isolates the typo as a
// per-service issue and leaves "api" absent.
func TestAbsentServiceKeepsStateAfterHandEditTypo(t *testing.T) {
	s, regPath, _ := newDaemonWithEnrolledNodes(t, []registry.Service{absentStateTCPService("api", 5001)})
	raw := `{"schema_version":1,"services":[{"name":"Api","type":"tcp","target":"127.0.0.1:5432","port":5001,"tags":["tag:tsmain"],"created_at":"2026-09-29T00:00:00Z"}]}`
	if err := os.WriteFile(regPath, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.syncNodes(context.Background()); err != nil {
		t.Fatalf("syncNodes() error = %v", err)
	}
	if got := runningNames(s); len(got) != 0 {
		t.Fatalf("running = %v, want none", got)
	}
	assertNodeStateKept(t, "api")
}

// P3: registry.json is replaced by another valid copy (a restored backup, a
// dotfiles sync) that does not list "a".
func TestAbsentServiceKeepsStateWhenRegistryIsReplaced(t *testing.T) {
	s, _, _ := newDaemonWithEnrolledNodes(t, []registry.Service{absentStateTCPService("a", 5001), absentStateTCPService("b", 5002)})
	writeRegistry(t, []registry.Service{absentStateTCPService("b", 5002), absentStateTCPService("x", 5009)})
	if err := s.syncNodes(context.Background()); err != nil {
		t.Fatalf("syncNodes() error = %v", err)
	}
	if got := runningNames(s); len(got) != 2 || got[0] != "b" || got[1] != "x" {
		t.Fatalf("running = %v, want [b x]", got)
	}
	assertNodeStateKept(t, "a", "b")
}

// A public listener whose service is absent is withdrawn before a failed
// global policy preflight returns. Withdrawing the listener closes the public
// exposure; it is not proof of removal either.
func TestAbsentPublicServiceKeepsStateWhenWithdrawnOnGlobalPolicyFailure(t *testing.T) {
	s, _, _ := newDaemonWithEnrolledNodes(t, []registry.Service{absentStateTCPService("private", 5002)})
	public := registry.Service{Name: "app", Type: registry.TypeProxy, Target: "http://localhost:3000", Tags: []string{"tag:owner"}, Funnel: true, PublicAck: true}
	state := absentStateMarker(t, public.Name)
	if err := os.MkdirAll(filepath.Dir(state), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(state, []byte("node key"), 0o600); err != nil {
		t.Fatal(err)
	}
	listener := &fakeListener{}
	s.nodes[public.Name] = &ServiceNode{service: public, listener: listener, funnelListenerActive: true, cancel: func() {}}
	outage := errors.New("synthetic policy read outage")
	s.ensureTagsFn = func(context.Context, []string) error { return outage }
	if err := s.syncNodes(context.Background()); !errors.Is(err, outage) {
		t.Fatalf("syncNodes() error = %v, want the policy outage", err)
	}
	if !listener.closed.Load() {
		t.Fatal("control: the absent public listener was not withdrawn")
	}
	assertNodeStateKept(t, public.Name)
}

// Control: an explicit removal still ends with the state deleted. `tslink
// remove` retires the service's ledger rows before it drops the registry
// entry; the daemon stops the node and keeps its state; the lifecycle
// reconciler then deletes the retired device by exact NodeID and, with the
// remote side resolved and no node holding the directory, the local state.
func TestExplicitlyRemovedServiceStateIsDeletedByTheReconciler(t *testing.T) {
	s, regPath, ownershipPath := newDaemonWithEnrolledNodes(t, []registry.Service{absentStateTCPService("a", 5001), absentStateTCPService("b", 5002)})
	fake := testenv.NewStatefulTailnet(t)
	t.Setenv(tailapi.APIBaseURLEnv, fake.URL())
	t.Setenv("TSLINK_API_KEY", "test-placeholder")
	fake.SetDevices([]tailscale.Device{
		{ID: "id-a", NodeID: "n-a", Hostname: "a", Tags: []string{"tag:tsmain"}},
		{ID: "id-b", NodeID: "n-b", Hostname: "b", Tags: []string{"tag:tsmain"}},
	})
	if err := runtimesnapshot.MarkOwnedNodeIDsRetired(ownershipPath, []string{"n-a"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Remove(regPath, "a"); err != nil {
		t.Fatal(err)
	}
	if err := s.syncNodes(context.Background()); err != nil {
		t.Fatalf("syncNodes() error = %v", err)
	}
	if got := runningNames(s); len(got) != 1 || got[0] != "b" {
		t.Fatalf("running = %v, want only b", got)
	}
	if _, err := lifecycle.Reconcile(context.Background(), lifecycle.Options{
		RegistryPath:        regPath,
		OwnershipPath:       ownershipPath,
		CleanLocalNodeState: true,
		LocalNodeStateInUse: s.HoldsNodeState,
	}); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if _, err := os.Stat(absentStateMarker(t, "a")); !os.IsNotExist(err) {
		t.Fatalf("removed service's node state after a resolved reconcile: stat err = %v, want it deleted", err)
	}
	assertNodeStateKept(t, "b")
	if devices := fake.Devices(); len(devices) != 1 || devices[0].NodeID != "n-b" {
		t.Fatalf("devices = %+v, want only b's", devices)
	}
	ledger, err := runtimesnapshot.LoadOwnership(ownershipPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Nodes) != 1 || ledger.Nodes[0].NodeID != "n-b" {
		t.Fatalf("ledger = %+v, want only b's row", ledger.Nodes)
	}
}

// Every node-state deletion goes through runtime.RemoveServiceNodeState with
// the daemon's configured directory, the one its start and its identity checks
// use. When the environment later resolves another installation's config
// directory, an identity reset must not delete a same-named service there.
func TestIdentityResetDeletesNodeStateInTheDaemonsConfigDir(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatal(err)
	}
	s, err := New("key", "")
	if err != nil {
		t.Fatal(err)
	}
	s.SetCleanupStaleNodesFn(func(context.Context, []tailapi.CleanupTarget) (tailapi.CleanupResult, error) {
		return tailapi.CleanupResult{}, nil
	})
	old := absentStateTCPService("app", 5001)
	daemonState := absentStateMarker(t, old.Name)
	if err := os.MkdirAll(filepath.Dir(daemonState), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(daemonState, []byte("node key"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.prepareNodeIdentity(context.Background(), old); err != nil {
		t.Fatalf("record the old identity: %v", err)
	}
	other := t.TempDir()
	t.Setenv(config.ConfigDirEnv, other)
	otherState := filepath.Join(config.NodesDirIn(other), old.Name, "tailscaled.state")
	if err := os.MkdirAll(filepath.Dir(otherState), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(otherState, []byte("another installation's key"), 0o600); err != nil {
		t.Fatal(err)
	}
	changed := old
	changed.Tags = []string{"tag:changed"}
	if _, err := s.prepareNodeIdentity(context.Background(), changed); err != nil {
		t.Fatalf("identity reset: %v", err)
	}
	if _, err := os.Stat(daemonState); !os.IsNotExist(err) {
		t.Fatalf("daemon's own node state after the reset: stat err = %v, want it deleted", err)
	}
	if _, err := os.Stat(otherState); err != nil {
		t.Fatalf("another config directory's node state was deleted: %v", err)
	}
}

// The credential upgrade that resets Tier 1 nodes deletes in the same place.
func TestCredentialUpgradeDeletesNodeStateInTheDaemonsConfigDir(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatal(err)
	}
	s, err := New("key", "")
	if err != nil {
		t.Fatal(err)
	}
	oldPending, oldClear := credentialUpgradePendingFn, clearCredentialUpgradeFn
	credentialUpgradePendingFn = func() (bool, error) { return true, nil }
	clearCredentialUpgradeFn = func() error { return nil }
	t.Cleanup(func() { credentialUpgradePendingFn, clearCredentialUpgradeFn = oldPending, oldClear })
	svc := absentStateTCPService("app", 5001)
	daemonState := absentStateMarker(t, svc.Name)
	if err := os.MkdirAll(filepath.Dir(daemonState), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(daemonState, []byte("tier 1 key"), 0o600); err != nil {
		t.Fatal(err)
	}
	other := t.TempDir()
	t.Setenv(config.ConfigDirEnv, other)
	otherState := filepath.Join(config.NodesDirIn(other), svc.Name, "tailscaled.state")
	if err := os.MkdirAll(filepath.Dir(otherState), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(otherState, []byte("another installation's key"), 0o600); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	err = s.prepareCredentialUpgradeLocked([]registry.Service{svc})
	s.mu.Unlock()
	if err != nil {
		t.Fatalf("prepareCredentialUpgradeLocked() error = %v", err)
	}
	if _, err := os.Stat(daemonState); !os.IsNotExist(err) {
		t.Fatalf("daemon's own Tier 1 state after the upgrade: stat err = %v, want it deleted", err)
	}
	if _, err := os.Stat(otherState); err != nil {
		t.Fatalf("another config directory's node state was deleted: %v", err)
	}
}
