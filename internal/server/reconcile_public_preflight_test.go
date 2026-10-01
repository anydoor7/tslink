package server

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/registry"
	tsruntime "github.com/anydoor7/tslink/internal/runtime"
	"github.com/anydoor7/tslink/internal/tailapi"
	"github.com/anydoor7/tslink/internal/testenv"
)

func TestSyncNodesFunnelPreflightFailureClosesOnlyChangedPublicNode(t *testing.T) {
	for _, tc := range []struct {
		name       string
		oldPublic  bool
		newTarget  string
		wantClosed bool
	}{
		{name: "unchanged public", oldPublic: true, newTarget: "http://localhost:3000"},
		{name: "changed public target", oldPublic: true, newTarget: "http://localhost:4000", wantClosed: true},
		{name: "private to public", oldPublic: false, newTarget: "http://localhost:3000"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testenv.SetHome(t, t.TempDir())
			if err := config.EnsureDir(); err != nil {
				t.Fatal(err)
			}
			// Registry shape `tslink share --funnel` writes: tag:tslink-funnel is
			// derived at node construction and never stored in registry tags.
			oldSvc := registry.Service{Name: "app", Type: registry.TypeProxy, Target: "http://localhost:3000", Tags: []string{"tag:tsmain"}}
			if tc.oldPublic {
				oldSvc.Funnel = true
				oldSvc.PublicAck = true
			}
			newSvc := oldSvc
			newSvc.Target = tc.newTarget
			newSvc.Funnel = true
			newSvc.PublicAck = true
			writeRegistry(t, []registry.Service{newSvc})

			nodesDir, err := config.NodesDir()
			if err != nil {
				t.Fatal(err)
			}
			stateDir := filepath.Join(nodesDir, oldSvc.Name)
			if err := os.MkdirAll(stateDir, 0o700); err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(stateDir, "keep-state")
			if err := os.WriteFile(marker, []byte("keep"), 0o600); err != nil {
				t.Fatal(err)
			}

			s, err := New("key", "")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(s.closeAllNodes)
			listener := &fakeListener{}
			s.nodes[oldSvc.Name] = &ServiceNode{service: oldSvc, funnelListenerActive: tc.oldPublic, listener: listener, cancel: func() {}}
			oldNew := newTSNetServerFn
			newTSNetServerFn = func(registry.Service, string, string, string) tsnetServer {
				t.Fatal("policy failure started a replacement node")
				return nil
			}
			t.Cleanup(func() { newTSNetServerFn = oldNew })
			s.SetEnsureFunnelAttrFn(func(context.Context, tailapi.FunnelPolicyRequest) (tailapi.PolicyMutationResult, error) {
				if listener.closed.Load() {
					t.Fatal("old node closed before policy preflight")
				}
				return tailapi.PolicyMutationResult{WriteOutcome: tailapi.PolicyWriteRejected}, errors.New("synthetic policy outage")
			})

			if err := s.syncNodes(context.Background()); err != nil {
				t.Fatalf("syncNodes: %v", err)
			}
			if got := listener.closed.Load(); got != tc.wantClosed {
				t.Errorf("old listener closed = %v, want %v", got, tc.wantClosed)
			}
			if _, err := os.Stat(marker); err != nil {
				t.Fatalf("local node state was removed: %v", err)
			}
			if failure := s.serviceFailures[oldSvc.Name]; failure.Error == nil || failure.Error.Provision == nil || failure.Error.Provision.Reason != registry.ProvisionReasonEnsureFailed {
				t.Fatalf("policy failure not retained: %+v", failure)
			}
			snapshotPath, err := config.RuntimeSnapshotPath()
			if err != nil {
				t.Fatal(err)
			}
			snapshot, err := tsruntime.Load(snapshotPath)
			if err != nil {
				t.Fatal(err)
			}
			if len(snapshot.Services) != 1 || snapshot.Services[0].Error == nil || snapshot.Services[0].Error.Provision == nil {
				t.Fatalf("runtime snapshot lacks policy failure: %+v", snapshot.Services)
			}
		})
	}
}
