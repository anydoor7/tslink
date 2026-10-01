package server

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/registry"
	"github.com/anydoor7/tslink/internal/tailapi"
	"github.com/anydoor7/tslink/internal/testenv"
)

// publicPair runs an unchanged public node ("stay") and a public node whose
// registry target changed ("moved") and returns their listeners. Every
// withdrawal path must close exactly the changed one: the unchanged node is
// the control that shows the path was reached without over-closing.
func publicPair(t *testing.T) (*Server, *fakeListener, *fakeListener) {
	t.Helper()
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatal(err)
	}
	stay := registry.Service{Name: "stay", Type: registry.TypeProxy, Target: "http://localhost:3000", Tags: []string{"tag:owner"}, Funnel: true, PublicAck: true}
	movedOld := registry.Service{Name: "moved", Type: registry.TypeProxy, Target: "http://localhost:3001", Tags: []string{"tag:owner"}, Funnel: true, PublicAck: true}
	movedNew := movedOld
	movedNew.Target = "http://localhost:4001"
	writeRegistry(t, []registry.Service{stay, movedNew})
	s, err := New("key", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.closeAllNodes)
	stayListener, movedListener := &fakeListener{}, &fakeListener{}
	s.nodes["stay"] = &ServiceNode{service: stay, listener: stayListener, funnelListenerActive: true, cancel: func() {}}
	s.nodes["moved"] = &ServiceNode{service: movedOld, listener: movedListener, funnelListenerActive: true, cancel: func() {}}
	s.SetEnsureFunnelAttrFn(funnelPolicySatisfied)
	s.SetCleanupStaleNodesFn(func(context.Context, []tailapi.CleanupTarget) (tailapi.CleanupResult, error) {
		return tailapi.CleanupResult{}, nil
	})
	oldNew := newTSNetServerFn
	newTSNetServerFn = func(registry.Service, string, string, string) tsnetServer {
		return &fakeTSNetServer{upErr: errors.New("intentional fake Up stop")}
	}
	t.Cleanup(func() { newTSNetServerFn = oldNew })
	return s, stayListener, movedListener
}

// TR1d: a global tag-policy failure returns before the normal stop phase and
// withdraws divergent public listeners; an unchanged public service keeps
// serving through the outage.
func TestGlobalTagPolicyFailureKeepsUnchangedPublicListener(t *testing.T) {
	s, stay, moved := publicPair(t)
	outage := errors.New("tailscale API 503")
	s.SetEnsureFunnelAttrFn(func(context.Context, tailapi.FunnelPolicyRequest) (tailapi.PolicyMutationResult, error) {
		return tailapi.PolicyMutationResult{WriteOutcome: tailapi.PolicyWriteNotAttempted}, outage
	})
	s.ensureTagsFn = func(context.Context, []string) error { return outage }
	if err := s.syncNodes(context.Background()); !errors.Is(err, outage) || !strings.Contains(err.Error(), "ensure ACL tags before restart") {
		t.Fatalf("sync error = %v, want the global tag-policy failure", err)
	}
	if !moved.closed.Load() {
		t.Fatal("changed public listener survived the global policy failure (path not reached)")
	}
	if stay.closed.Load() || !s.nodeRunning("stay") {
		t.Fatalf("unchanged public listener withdrawn by a transient policy outage: closed=%v running=%v", stay.closed.Load(), s.nodeRunning("stay"))
	}
}

// TR1e: a credential-mode transition that cannot be applied returns before
// the stop phase; it must still withdraw a changed public listener.
func TestCredentialUpgradeErrorClosesChangedPublicListener(t *testing.T) {
	s, stay, moved := publicPair(t)
	s.SetCredentialed(true)
	oldPending := credentialUpgradePendingFn
	credentialUpgradePendingFn = func() (bool, error) { return true, nil }
	t.Cleanup(func() { credentialUpgradePendingFn = oldPending })
	err := s.syncNodes(context.Background())
	if err == nil || !strings.Contains(err.Error(), "apply credential-mode transition") {
		t.Fatalf("sync error = %v, want the credential-mode transition error", err)
	}
	if !moved.closed.Load() || s.nodeRunning("moved") {
		t.Fatalf("changed public listener still reachable after credential-upgrade error: closed=%v running=%v", moved.closed.Load(), s.nodeRunning("moved"))
	}
	if stay.closed.Load() || !s.nodeRunning("stay") {
		t.Fatalf("unchanged public listener withdrawn by credential-upgrade error: closed=%v", stay.closed.Load())
	}
}

// TR1j: a node identity record that cannot be read for a running public node
// whose registry entry changed must close that listener; it must not keep the
// old public surface up while its replacement is blocked.
func TestNodeIdentityErrorClosesChangedPublicListener(t *testing.T) {
	s, stay, moved := publicPair(t)
	dir := filepath.Join(mustConfigDir(t), "node-identities")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"stay", "moved"} {
		if err := os.WriteFile(filepath.Join(dir, name+".json"), []byte("{bad"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.syncNodes(context.Background()); err != nil {
		t.Fatalf("sync with unreadable identity records: %v", err)
	}
	if !moved.closed.Load() || s.nodeRunning("moved") {
		t.Fatalf("changed public listener still reachable after identity error: closed=%v running=%v", moved.closed.Load(), s.nodeRunning("moved"))
	}
	if stay.closed.Load() || !s.nodeRunning("stay") {
		t.Fatalf("unchanged public listener withdrawn by identity error: closed=%v", stay.closed.Load())
	}
	for _, name := range []string{"stay", "moved"} {
		if failure := s.serviceFailures[name]; !isNodeIdentityFailure(failure) {
			t.Fatalf("%s identity failure not recorded: %+v", name, failure)
		}
	}
}
