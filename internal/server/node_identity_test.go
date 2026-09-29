package server

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/registry"
	"github.com/monody0007/tslink/internal/tailapi"
	"github.com/monody0007/tslink/internal/testenv"
)

func identityTestSetup(t *testing.T) (registry.Service, registry.Service, string) {
	t.Helper()
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatal(err)
	}
	old := registry.Service{Name: "app", Type: registry.TypeFile, Path: t.TempDir(), Tags: []string{"tag:old"}}
	newService := old
	newService.Tags = []string{"tag:new"}
	stateDir := filepath.Join(config.NodesDirIn(mustConfigDir(t)), old.Name)
	return old, newService, stateDir
}

func mustConfigDir(t *testing.T) string {
	t.Helper()
	dir, err := config.Dir()
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestNodeIdentityFreshServerRetryAfterPolicyFailure(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatal(err)
	}
	old := registry.Service{Name: "app", Type: registry.TypeProxy, Target: "http://localhost:3000", Tags: []string{"tag:old"}, Funnel: true, PublicAck: true}
	newService := old
	newService.Tags = []string{"tag:new"}
	writeRegistry(t, []registry.Service{newService})
	stateDir := filepath.Join(config.NodesDirIn(mustConfigDir(t)), old.Name)
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(stateDir, "old-state")
	if err := os.WriteFile(marker, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	first, err := New("key", "https://old-control.example")
	if err != nil {
		t.Fatal(err)
	}
	listener := &fakeListener{}
	first.nodes[old.Name] = &ServiceNode{service: old, funnelListenerActive: true, listener: listener, cancel: func() {}}
	first.SetEnsureFunnelAttrFn(func(context.Context, tailapi.FunnelPolicyRequest) (tailapi.PolicyMutationResult, error) {
		return tailapi.PolicyMutationResult{WriteOutcome: tailapi.PolicyWriteRejected}, errors.New("policy unavailable")
	})
	if err := first.syncNodes(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !listener.closed.Load() {
		t.Fatal("failed-policy sync left changed public listener reachable")
	}
	path, err := first.nodeIdentityPath(old.Name)
	if err != nil {
		t.Fatal(err)
	}
	recorded, found, err := readNodeIdentity(path)
	oldIdentity := requestedNodeIdentity(old, "https://old-control.example", identityPreparedBeforeUp)
	if err != nil || !found || !sameStringSet(recorded.Tags, oldIdentity.Tags) || recorded.ControlURL != "https://old-control.example" {
		t.Fatalf("old identity was not durable: %+v found=%v err=%v", recorded, found, err)
	}
	second, err := New("key", "https://new-control.example")
	if err != nil {
		t.Fatal(err)
	}
	second.SetEnsureFunnelAttrFn(func(context.Context, tailapi.FunnelPolicyRequest) (tailapi.PolicyMutationResult, error) {
		return tailapi.PolicyMutationResult{WriteOutcome: tailapi.PolicyWriteNotAttempted}, nil
	})
	var cleanupTags []string
	second.SetCleanupStaleNodesFn(func(_ context.Context, targets []tailapi.CleanupTarget) (tailapi.CleanupResult, error) {
		cleanupTags = append(cleanupTags, targets[0].Tags...)
		return tailapi.CleanupResult{}, nil
	})
	oldNew := newTSNetServerFn
	t.Cleanup(func() { newTSNetServerFn = oldNew })
	constructed := false
	stopAtUp := errors.New("constructed replacement")
	newTSNetServerFn = func(registry.Service, string, string, string) tsnetServer {
		constructed = true
		if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("old state still present at replacement construction: %v", err)
		}
		return &fakeTSNetServer{upErr: stopAtUp}
	}
	if err := second.syncNodes(context.Background()); !errors.Is(err, stopAtUp) {
		t.Fatalf("fresh Server retry error = %v", err)
	}
	if !constructed || !sameStringSet(cleanupTags, oldIdentity.Tags) {
		t.Fatalf("fresh Server lost old identity: constructed=%v cleanup tags=%v", constructed, cleanupTags)
	}
}

func TestNodeIdentityRemovalGateAndRecovery(t *testing.T) {
	for _, tc := range []struct {
		name   string
		remove func(string) error
	}{
		{name: "error", remove: func(string) error { return errors.New("permission denied") }},
		{name: "no-op-success", remove: func(string) error { return nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			old, newService, stateDir := identityTestSetup(t)
			writeRegistry(t, []registry.Service{old})
			oldNew := newTSNetServerFn
			constructs := 0
			newTSNetServerFn = func(registry.Service, string, string, string) tsnetServer {
				constructs++
				return &fakeTSNetServer{}
			}
			t.Cleanup(func() { newTSNetServerFn = oldNew })
			s, err := New("key", "")
			if err != nil {
				t.Fatal(err)
			}
			providerCalls := 0
			s.SetAuthKeyProvider(func(context.Context, registry.Service) (string, error) {
				providerCalls++
				return "key", nil
			})
			s.SetCleanupStaleNodesFn(func(context.Context, []tailapi.CleanupTarget) (tailapi.CleanupResult, error) {
				return tailapi.CleanupResult{}, nil
			})
			t.Cleanup(s.closeAllNodes)
			if err := s.syncNodes(context.Background()); err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(stateDir, "old-state")
			if err := os.WriteFile(marker, []byte("old"), 0o600); err != nil {
				t.Fatal(err)
			}
			writeRegistry(t, []registry.Service{newService})
			oldRemove := removeServiceStateDirFn
			removeServiceStateDirFn = tc.remove
			t.Cleanup(func() { removeServiceStateDirFn = oldRemove })
			if err := s.syncNodes(context.Background()); err == nil {
				t.Fatal("changed identity started despite failed or incomplete state removal")
			}
			if constructs != 1 || providerCalls != 1 || s.nodeRunning(old.Name) {
				t.Fatalf("unsafe replacement after failed reset: constructs=%d provider calls=%d running=%v", constructs, providerCalls, s.nodeRunning(old.Name))
			}
			path, _ := s.nodeIdentityPath(old.Name)
			recorded, found, err := readNodeIdentity(path)
			if err != nil || !found || !sameStringSet(recorded.Tags, old.Tags) {
				t.Fatalf("old identity discarded after failed reset: %+v found=%v err=%v", recorded, found, err)
			}
			removeServiceStateDirFn = oldRemove
			if err := s.syncNodes(context.Background()); err != nil {
				t.Fatalf("retry failed after reset recovered: %v", err)
			}
			if constructs != 2 || providerCalls != 2 {
				t.Fatalf("retry constructs=%d provider calls=%d, want 2 each", constructs, providerCalls)
			}
			if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("retry retained old state: %v", err)
			}
		})
	}
}

func TestNodeIdentityWriteFailureAndPostAdvanceCrash(t *testing.T) {
	old, newService, stateDir := identityTestSetup(t)
	writeRegistry(t, []registry.Service{old})
	oldNew := newTSNetServerFn
	constructs := 0
	newTSNetServerFn = func(registry.Service, string, string, string) tsnetServer {
		constructs++
		return &fakeTSNetServer{}
	}
	t.Cleanup(func() { newTSNetServerFn = oldNew })
	s, err := New("key", "")
	if err != nil {
		t.Fatal(err)
	}
	s.SetCleanupStaleNodesFn(func(context.Context, []tailapi.CleanupTarget) (tailapi.CleanupResult, error) {
		return tailapi.CleanupResult{}, nil
	})
	if err := s.syncNodes(context.Background()); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(stateDir, "old-state")
	if err := os.WriteFile(marker, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeRegistry(t, []registry.Service{newService})
	oldWrite := writeNodeIdentityFn
	writeNodeIdentityFn = func(path string, identity nodeIdentity) error {
		if sameStringSet(identity.Tags, newService.Tags) {
			return errors.New("disk full")
		}
		return oldWrite(path, identity)
	}
	t.Cleanup(func() { writeNodeIdentityFn = oldWrite })
	if err := s.syncNodes(context.Background()); err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("replacement metadata write failure = %v", err)
	}
	if constructs != 1 || s.nodeRunning(old.Name) {
		t.Fatalf("replacement constructed after metadata write failure: constructs=%d", constructs)
	}
	path, _ := s.nodeIdentityPath(old.Name)
	recorded, found, err := readNodeIdentity(path)
	if err != nil || !found || !sameStringSet(recorded.Tags, old.Tags) {
		t.Fatalf("old record not retained after write failure: %+v found=%v err=%v", recorded, found, err)
	}
	writeNodeIdentityFn = oldWrite
	s.closeAllNodes()
	afterReset, err := New("key", "")
	if err != nil {
		t.Fatal(err)
	}
	afterReset.SetCleanupStaleNodesFn(func(context.Context, []tailapi.CleanupTarget) (tailapi.CleanupResult, error) {
		return tailapi.CleanupResult{}, nil
	})
	if err := afterReset.syncNodes(context.Background()); err != nil {
		t.Fatalf("fresh Server retry after reset/write failure = %v", err)
	}
	if constructs != 2 {
		t.Fatalf("retry constructs=%d, want 2", constructs)
	}
	// Simulate new tsnet state created after record advancement, followed by
	// process exit. A new Server must reuse it rather than erase it again.
	newMarker := filepath.Join(stateDir, "new-state")
	if err := os.WriteFile(newMarker, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	afterReset.closeAllNodes()
	fresh, err := New("key", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(fresh.closeAllNodes)
	if err := fresh.syncNodes(context.Background()); err != nil {
		t.Fatalf("fresh Server after new enrollment = %v", err)
	}
	if constructs != 3 {
		t.Fatalf("fresh Server constructs=%d, want 3", constructs)
	}
	if _, err := os.Stat(newMarker); err != nil {
		t.Fatalf("fresh Server erased new identity after record advancement: %v", err)
	}
}

func TestNodeIdentityLegacyAdoptionAndCorruptRecord(t *testing.T) {
	old, _, stateDir := identityTestSetup(t)
	writeRegistry(t, []registry.Service{old})
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(stateDir, "legacy-state")
	if err := os.WriteFile(marker, []byte("legacy"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldNew := newTSNetServerFn
	constructs := 0
	newTSNetServerFn = func(registry.Service, string, string, string) tsnetServer {
		constructs++
		return &fakeTSNetServer{}
	}
	t.Cleanup(func() { newTSNetServerFn = oldNew })
	s, err := New("key", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.syncNodes(context.Background()); err != nil {
		t.Fatal(err)
	}
	path, _ := s.nodeIdentityPath(old.Name)
	recorded, found, err := readNodeIdentity(path)
	if err != nil || !found || recorded.Origin != identityLegacyAdopted {
		t.Fatalf("legacy state not marked unverified: %+v found=%v err=%v", recorded, found, err)
	}
	if _, err := os.Stat(marker); err != nil || constructs != 1 {
		t.Fatalf("legacy state was not preserved: marker=%v constructs=%d", err, constructs)
	}
	s.closeAllNodes()
	if err := os.WriteFile(path, []byte("{bad json"), 0o600); err != nil {
		t.Fatal(err)
	}
	fresh, err := New("key", "")
	if err != nil {
		t.Fatal(err)
	}
	// A corrupt record is a failure of its own service, not of the sync.
	if err := fresh.syncNodes(context.Background()); err != nil {
		t.Fatalf("corrupt record failed the whole sync: %v", err)
	}
	if failure := fresh.serviceFailures[old.Name]; !isNodeIdentityFailure(failure) || !strings.Contains(failure.Error.Message, path) {
		t.Fatal("corrupt record fell through to legacy adoption")
	}
	if constructs != 1 {
		t.Fatalf("corrupt record constructed node: %d", constructs)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "{bad json" {
		t.Fatalf("corrupt record was rewritten: %q err=%v", data, err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("corrupt record removed legacy state: %v", err)
	}
}

func TestNodeIdentityInitialWriteFailureBlocksConstruction(t *testing.T) {
	old, _, _ := identityTestSetup(t)
	writeRegistry(t, []registry.Service{old})
	oldWrite := writeNodeIdentityFn
	writeNodeIdentityFn = func(string, nodeIdentity) error { return errors.New("disk full") }
	t.Cleanup(func() { writeNodeIdentityFn = oldWrite })
	oldNew := newTSNetServerFn
	constructed := false
	newTSNetServerFn = func(registry.Service, string, string, string) tsnetServer {
		constructed = true
		return &fakeTSNetServer{}
	}
	t.Cleanup(func() { newTSNetServerFn = oldNew })
	s, err := New("key", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.syncNodes(context.Background()); err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("initial identity write failure = %v", err)
	}
	if constructed {
		t.Fatal("tsnet constructed without durable requested identity")
	}
	writeNodeIdentityFn = oldWrite
	if err := s.syncNodes(context.Background()); err != nil || !s.nodeRunning(old.Name) {
		t.Fatalf("initial start did not recover after disk write recovered: %v", err)
	}
	path, _ := s.nodeIdentityPath(old.Name)
	recorded, found, err := readNodeIdentity(path)
	if err != nil || !found || recorded.Origin != identityPreparedBeforeUp {
		t.Fatalf("new state did not get a prepared identity: %+v found=%v err=%v", recorded, found, err)
	}
	s.closeAllNodes()
}

func TestNodeIdentityEphemeralResetAndTargetOnlyReuse(t *testing.T) {
	for _, tc := range []struct {
		name        string
		change      func(*registry.Service)
		wantRemoved bool
	}{
		{name: "ephemeral", change: func(svc *registry.Service) { svc.Ephemeral = true }, wantRemoved: true},
		{name: "target-only", change: func(svc *registry.Service) { svc.Path = filepath.Dir(svc.Path) }, wantRemoved: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			old, _, stateDir := identityTestSetup(t)
			writeRegistry(t, []registry.Service{old})
			oldNew := newTSNetServerFn
			constructs := 0
			newTSNetServerFn = func(registry.Service, string, string, string) tsnetServer {
				constructs++
				return &fakeTSNetServer{}
			}
			t.Cleanup(func() { newTSNetServerFn = oldNew })
			s, err := New("key", "")
			if err != nil {
				t.Fatal(err)
			}
			s.SetCleanupStaleNodesFn(func(context.Context, []tailapi.CleanupTarget) (tailapi.CleanupResult, error) {
				return tailapi.CleanupResult{}, nil
			})
			t.Cleanup(s.closeAllNodes)
			if err := s.syncNodes(context.Background()); err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(stateDir, "enrolled-state")
			if err := os.WriteFile(marker, []byte("old"), 0o600); err != nil {
				t.Fatal(err)
			}
			changed := old
			tc.change(&changed)
			writeRegistry(t, []registry.Service{changed})
			if err := s.syncNodes(context.Background()); err != nil {
				t.Fatal(err)
			}
			if constructs != 2 {
				t.Fatalf("constructs=%d, want 2", constructs)
			}
			_, err = os.Stat(marker)
			if tc.wantRemoved && !errors.Is(err, os.ErrNotExist) || !tc.wantRemoved && err != nil {
				t.Fatalf("state after %s change: stat=%v want removed=%v", tc.name, err, tc.wantRemoved)
			}
		})
	}
}

func TestNodeIdentityPublicReplacementWriteFailureClosesListener(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatal(err)
	}
	old := registry.Service{Name: "app", Type: registry.TypeProxy, Target: "http://localhost:3000", Tags: []string{"tag:old"}, Funnel: true, PublicAck: true}
	changed := old
	changed.Tags = []string{"tag:new"}
	writeRegistry(t, []registry.Service{changed})
	s, err := New("key", "")
	if err != nil {
		t.Fatal(err)
	}
	listener := &fakeListener{}
	s.nodes[old.Name] = &ServiceNode{service: old, funnelListenerActive: true, listener: listener, cancel: func() {}}
	path, _ := s.nodeIdentityPath(old.Name)
	if err := writeNodeIdentity(path, requestedNodeIdentity(old, "", identityPreparedBeforeUp)); err != nil {
		t.Fatal(err)
	}
	s.SetEnsureFunnelAttrFn(func(context.Context, tailapi.FunnelPolicyRequest) (tailapi.PolicyMutationResult, error) {
		return tailapi.PolicyMutationResult{WriteOutcome: tailapi.PolicyWriteNotAttempted}, nil
	})
	s.SetCleanupStaleNodesFn(func(context.Context, []tailapi.CleanupTarget) (tailapi.CleanupResult, error) {
		return tailapi.CleanupResult{}, nil
	})
	oldWrite := writeNodeIdentityFn
	changedIdentity := requestedNodeIdentity(changed, "", identityPreparedBeforeUp)
	writeNodeIdentityFn = func(path string, identity nodeIdentity) error {
		if sameStringSet(identity.Tags, changedIdentity.Tags) {
			return errors.New("disk full")
		}
		return oldWrite(path, identity)
	}
	t.Cleanup(func() { writeNodeIdentityFn = oldWrite })
	oldNew := newTSNetServerFn
	constructed := false
	newTSNetServerFn = func(registry.Service, string, string, string) tsnetServer {
		constructed = true
		return &fakeTSNetServer{}
	}
	t.Cleanup(func() { newTSNetServerFn = oldNew })
	if err := s.syncNodes(context.Background()); err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("public replacement write failure = %v", err)
	}
	if !listener.closed.Load() || constructed {
		t.Fatalf("public replacement failure left listener or constructed new node: closed=%v constructed=%v", listener.closed.Load(), constructed)
	}
	recorded, found, err := readNodeIdentity(path)
	if err != nil || !found || !sameStringSet(recorded.Tags, requestedNodeIdentity(old, "", identityPreparedBeforeUp).Tags) {
		t.Fatalf("public replacement lost old durable record: %+v found=%v err=%v", recorded, found, err)
	}
}

func TestNodeIdentityCorruptServiceDoesNotBlockUnrelatedPrivateStart(t *testing.T) {
	old, _, stateDir := identityTestSetup(t)
	other := registry.Service{Name: "other", Type: registry.TypeFile, Path: t.TempDir()}
	writeRegistry(t, []registry.Service{old, other})
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(stateDir, "old-state")
	if err := os.WriteFile(marker, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := New("key", "")
	if err != nil {
		t.Fatal(err)
	}
	path, _ := s.nodeIdentityPath(old.Name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{bad json"), 0o600); err != nil {
		t.Fatal(err)
	}
	s.nodes[old.Name] = &ServiceNode{service: old, cancel: func() {}}
	oldNew := newTSNetServerFn
	var constructed []string
	newTSNetServerFn = func(svc registry.Service, _, _, _ string) tsnetServer {
		constructed = append(constructed, svc.Name)
		return &fakeTSNetServer{}
	}
	t.Cleanup(func() { newTSNetServerFn = oldNew })
	t.Cleanup(s.closeAllNodes)
	// The corrupt record is reported as this service's failure, not as a
	// sync error that would withdraw runtime evidence for every service.
	if err := s.syncNodes(context.Background()); err != nil {
		t.Fatalf("corrupt service failed the whole sync: %v", err)
	}
	if failure := s.serviceFailures[old.Name]; !isNodeIdentityFailure(failure) || !strings.Contains(failure.Error.Message, path) {
		t.Fatalf("corrupt service error = %+v", failure)
	}
	if strings.Join(constructed, ",") != "other" || !s.nodeRunning("other") || !s.nodeRunning(old.Name) {
		t.Fatalf("corrupt service blocked unrelated private start: constructed=%v", constructed)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("corrupt service state was touched: %v", err)
	}
}

func TestNodeIdentityRejectsInvalidStoredControlURL(t *testing.T) {
	old, _, _ := identityTestSetup(t)
	s, err := New("key", "")
	if err != nil {
		t.Fatal(err)
	}
	path, _ := s.nodeIdentityPath(old.Name)
	invalid := requestedNodeIdentity(old, "", identityPreparedBeforeUp)
	invalid.ControlURL = "not-a-url"
	if err := writeNodeIdentity(path, invalid); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readNodeIdentity(path); err == nil {
		t.Fatal("invalid stored control URL was accepted as trusted identity")
	}
}
