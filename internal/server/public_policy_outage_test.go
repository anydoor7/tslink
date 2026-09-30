package server

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/registry"
	"github.com/monody0007/tslink/internal/tailapi"
	"github.com/monody0007/tslink/internal/testenv"
)

func TestSyncNodesChangedPublicTargetStopsDuringGlobalPolicyReadFailure(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatal(err)
	}
	oldSvc := registry.Service{Name: "app", Type: registry.TypeProxy, Target: "http://localhost:3000", Tags: []string{"tag:owner"}, Funnel: true, PublicAck: true}
	newSvc := oldSvc
	newSvc.Target = "http://localhost:4000"
	writeRegistry(t, []registry.Service{newSvc})
	s, err := New("key", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.closeAllNodes)
	listener := &fakeListener{}
	s.nodes["app"] = &ServiceNode{service: oldSvc, listener: listener, funnelListenerActive: true, cancel: func() {}}
	outage := errors.New("synthetic policy read outage")
	funnelCalls, tagsCalls := 0, 0
	s.SetEnsureFunnelAttrFn(func(context.Context, tailapi.FunnelPolicyRequest) (tailapi.PolicyMutationResult, error) {
		funnelCalls++
		return tailapi.PolicyMutationResult{WriteOutcome: tailapi.PolicyWriteNotAttempted}, outage
	})
	s.ensureTagsFn = func(context.Context, []string) error { tagsCalls++; return outage }
	err = s.syncNodes(context.Background())
	if !errors.Is(err, outage) || funnelCalls != 1 || tagsCalls != 1 {
		t.Fatalf("did not exercise both real preflight gates: error=%v funnel=%d tags=%d", err, funnelCalls, tagsCalls)
	}
	if !listener.closed.Load() {
		t.Fatal("changed old PUBLIC listener still reachable after Funnel policy failure plus tag-policy read failure")
	}
}

func TestSyncNodesWithdrawsRemovedOrExpiredPublicOnGlobalPolicyFailure(t *testing.T) {
	for _, tc := range []struct {
		name    string
		expires bool
	}{
		{name: "removed"},
		{name: "expired", expires: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testenv.SetHome(t, t.TempDir())
			if err := config.EnsureDir(); err != nil {
				t.Fatal(err)
			}
			oldSvc := registry.Service{Name: "app", Type: registry.TypeProxy, Target: "http://localhost:3000", Tags: []string{"tag:owner"}, Funnel: true, PublicAck: true}
			privateSvc := registry.Service{Name: "private", Type: registry.TypeProxy, Target: "http://localhost:4000", Tags: []string{"tag:owner"}}
			services := []registry.Service{privateSvc}
			if tc.expires {
				deadline := time.Now().Add(-time.Minute)
				expired := oldSvc
				expired.FunnelExpiresAt = &deadline
				services = append(services, expired)
			}
			writeRegistry(t, services)
			nodesDir, err := config.NodesDir()
			if err != nil {
				t.Fatal(err)
			}
			stateDir := filepath.Join(nodesDir, oldSvc.Name)
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
			t.Cleanup(s.closeAllNodes)
			publicListener, privateListener := &fakeListener{}, &fakeListener{}
			s.nodes[oldSvc.Name] = &ServiceNode{service: oldSvc, listener: publicListener, funnelListenerActive: true, cancel: func() {}}
			s.nodes[privateSvc.Name] = &ServiceNode{service: privateSvc, listener: privateListener, cancel: func() {}}
			outage := errors.New("synthetic policy read outage")
			s.ensureTagsFn = func(context.Context, []string) error { return outage }
			if err := s.syncNodes(context.Background()); !errors.Is(err, outage) {
				t.Fatalf("sync error = %v, want tag-policy outage", err)
			}
			if !publicListener.closed.Load() || privateListener.closed.Load() {
				t.Fatalf("public closed=%v private closed=%v, want true/false", publicListener.closed.Load(), privateListener.closed.Load())
			}
			_, stateErr := os.Stat(marker)
			if tc.expires && stateErr != nil {
				t.Fatalf("expired service lost state needed for private retry: %v", stateErr)
			}
		})
	}
}
