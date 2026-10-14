package server

import (
	"context"
	"net"
	"path/filepath"
	"testing"

	"github.com/anydoor7/tslink/internal/registry"
	"github.com/anydoor7/tslink/internal/testwait"
)

func TestGatewayPersistentMarkerMismatch(t *testing.T) {
	for _, kind := range []string{"node-id", "enrollment-identity"} {
		t.Run(kind, func(t *testing.T) {
			f := newGatewayFixture(t, nil)
			f.s.mu.Lock()
			persistent := f.s.portalState.Gateway.Persistent
			f.s.mu.Unlock()
			if !persistent {
				t.Fatal("persistent positive control failed")
			}
			f.s.closePortal()
			prior := f.node
			f.node = &gatewayFakeNode{fakeTSNetServer: fakeTSNetServer{localClient: prior.localClient, certDomains: prior.certDomains, status: prior.status}, ports: map[string]net.Listener{}, fail: map[string]bool{}, stall: map[string]chan struct{}{}, cert: prior.cert, stop: make(chan struct{})}
			dir := filepath.Join(filepath.Dir(f.path), "portal-nodes", "home")
			marker, err := readGatewayEnrollment(dir)
			if err != nil {
				t.Fatal(err)
			}
			if kind == "node-id" {
				marker.NodeID = "different-enrolled-node"
				if err := writeGatewayEnrollment(dir, marker); err != nil {
					t.Fatal(err)
				}
			} else {
				// Change the marker through the existing node factory seam, after its
				// initial reuse check and before runtime proves the observed enrollment.
				factory := newTSNetServerFn
				newTSNetServerFn = func(svc registry.Service, stateDir, key, control string) tsnetServer {
					marker.ControlURL = "https://other-control.invalid"
					if err := writeGatewayEnrollment(stateDir, marker); err != nil {
						t.Error(err)
					}
					return factory(svc, stateDir, key, control)
				}
			}
			f.s.syncPortal(context.Background(), f.config)
			testwait.Recv(t, f.s.portalRun.done, "mismatched gateway refused")
			f.s.mu.Lock()
			state := f.s.portalState
			f.s.mu.Unlock()
			if state.State != "failed" || state.Error != "gateway_ephemeral_refused" || state.Gateway == nil || state.Gateway.Persistent {
				t.Fatalf("mismatched enrollment served: %+v", state)
			}
			f.node.mu.Lock()
			count := len(f.node.ports)
			f.node.mu.Unlock()
			if count != 0 {
				t.Fatal("unproven persistent gateway opened listeners", count)
			}
		})
	}
}
