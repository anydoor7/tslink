package server

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/registry"
	"github.com/monody0007/tslink/internal/tailapi"
	"github.com/monody0007/tslink/internal/testenv"
)

func TestSyncNodesPublicIdentityRetryAfterPreflightFailure(t *testing.T) {
	for _, tc := range []struct {
		name                         string
		blockedFirst, identityChange bool
		globalReadOutage             bool
	}{
		{name: "identity-direct-success-control", identityChange: true},
		{name: "target-after-policy-failure-control", blockedFirst: true},
		{name: "identity-after-policy-failure", blockedFirst: true, identityChange: true},
		{name: "identity-after-global-read-failure", blockedFirst: true, identityChange: true, globalReadOutage: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testenv.SetHome(t, t.TempDir())
			if err := config.EnsureDir(); err != nil {
				t.Fatal(err)
			}
			oldSvc := registry.Service{Name: "app", Type: registry.TypeProxy, Target: "http://localhost:3000", Tags: []string{"tag:old"}, Funnel: true, PublicAck: true}
			newSvc := oldSvc
			if tc.identityChange {
				newSvc.Tags = []string{"tag:new"}
			} else {
				newSvc.Target = "http://localhost:4000"
			}
			writeRegistry(t, []registry.Service{newSvc})
			nodesDir, err := config.NodesDir()
			if err != nil {
				t.Fatal(err)
			}
			stateDir := filepath.Join(nodesDir, "app")
			if err := os.MkdirAll(stateDir, 0700); err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(stateDir, "old-enrolled-identity")
			if err := os.WriteFile(marker, []byte("old"), 0600); err != nil {
				t.Fatal(err)
			}
			s, err := New("key", "")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(s.closeAllNodes)
			listener := &fakeListener{}
			s.nodes["app"] = &ServiceNode{service: oldSvc, funnelListenerActive: true, listener: listener, cancel: func() {}}
			cleanupCalls := 0
			s.SetCleanupStaleNodesFn(func(_ context.Context, targets []tailapi.CleanupTarget) (tailapi.CleanupResult, error) {
				cleanupCalls++
				if len(targets) != 1 || targets[0].Tags[0] != "tag:old" {
					t.Fatalf("cleanup lost old identity: %+v", targets)
				}
				return tailapi.CleanupResult{}, nil
			})
			failPolicy := tc.blockedFirst
			policyErr := errors.New("synthetic policy outage")
			s.SetEnsureFunnelAttrFn(func(context.Context, tailapi.FunnelPolicyRequest) (tailapi.PolicyMutationResult, error) {
				if failPolicy {
					if tc.globalReadOutage {
						return tailapi.PolicyMutationResult{WriteOutcome: tailapi.PolicyWriteNotAttempted}, policyErr
					}
					return tailapi.PolicyMutationResult{WriteOutcome: tailapi.PolicyWriteRejected}, policyErr
				}
				return tailapi.PolicyMutationResult{WriteOutcome: tailapi.PolicyWriteNotAttempted}, nil
			})
			s.ensureTagsFn = func(context.Context, []string) error {
				if failPolicy && tc.globalReadOutage {
					return policyErr
				}
				return nil
			}
			oldNew := newTSNetServerFn
			t.Cleanup(func() { newTSNetServerFn = oldNew })
			constructed := false
			hadOldState := false
			reachedConstruction := errors.New("intentional fake Up stop")
			newTSNetServerFn = func(registry.Service, string, string, string) tsnetServer {
				constructed = true
				_, err := os.Stat(marker)
				hadOldState = err == nil
				return &fakeTSNetServer{upErr: reachedConstruction}
			}
			if tc.blockedFirst {
				if err := s.syncNodes(context.Background()); err != nil && !(tc.globalReadOutage && errors.Is(err, policyErr)) {
					t.Fatal(err)
				}
				if !listener.closed.Load() || constructed {
					t.Fatal("failed policy did not close stale public exposure before retry")
				}
				if _, err := os.Stat(marker); err != nil {
					t.Fatalf("failure destroyed identity before successful preflight: %v", err)
				}
			}
			failPolicy = false
			err = s.syncNodes(context.Background())
			if !errors.Is(err, reachedConstruction) || !constructed {
				t.Fatalf("probe failed to reach fake construction: %v", err)
			}
			wantCleanup := 0
			if tc.identityChange {
				wantCleanup = 1
			}
			if hadOldState == tc.identityChange || cleanupCalls != wantCleanup {
				t.Fatalf("retry reuses stale identity: old_state_at_construction=%v cleanup_calls=%d; want old_state=%v cleanup=%d", hadOldState, cleanupCalls, !tc.identityChange, wantCleanup)
			}
		})
	}
}

// A public node withdrawn by a failed preflight is no longer running, so a
// later registry removal alone is not proof that its tailnet node is gone.
// Its state and record stay until an ownership-proven path (tslink remove, the
// lifecycle reconciler) deletes the state; only then is the record pruned.
func TestSyncNodesRemoveAfterFailedPublicPreflightKeepsStateUntilProvenGone(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatal(err)
	}
	oldSvc := registry.Service{Name: "app", Type: registry.TypeProxy, Target: "http://localhost:3000", Tags: []string{"tag:old"}, Funnel: true, PublicAck: true}
	newSvc := oldSvc
	newSvc.Target = "http://localhost:4000"
	writeRegistry(t, []registry.Service{newSvc})
	nodesDir, err := config.NodesDir()
	if err != nil {
		t.Fatal(err)
	}
	stateDir := filepath.Join(nodesDir, oldSvc.Name)
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(stateDir, "old-enrolled-identity")
	if err := os.WriteFile(marker, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := New("key", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.closeAllNodes)
	listener := &fakeListener{}
	s.nodes[oldSvc.Name] = &ServiceNode{service: oldSvc, funnelListenerActive: true, listener: listener, cancel: func() {}}
	s.SetEnsureFunnelAttrFn(func(context.Context, tailapi.FunnelPolicyRequest) (tailapi.PolicyMutationResult, error) {
		return tailapi.PolicyMutationResult{WriteOutcome: tailapi.PolicyWriteRejected}, errors.New("synthetic policy outage")
	})
	if err := s.syncNodes(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !listener.closed.Load() {
		t.Fatal("changed public listener was not closed")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("failed preflight removed state before registry removal: %v", err)
	}
	writeRegistry(t, nil)
	if err := s.syncNodes(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("registry removal without ownership proof deleted node state: %v", err)
	}
	path, err := s.nodeIdentityPath(oldSvc.Name)
	if err != nil {
		t.Fatal(err)
	}
	if _, found, err := readNodeIdentity(path); err != nil || !found {
		t.Fatalf("record dropped while its state remains: found=%v err=%v", found, err)
	}
	// The ownership-proven path removes the state (lifecycle
	// removeStaleNodeState, or tslink remove with no daemon running).
	if err := os.RemoveAll(stateDir); err != nil {
		t.Fatal(err)
	}
	if err := s.syncNodes(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, found, err := readNodeIdentity(path)
	if err != nil || found {
		t.Fatalf("removed service retained identity after verified state deletion: found=%v err=%v", found, err)
	}
}
