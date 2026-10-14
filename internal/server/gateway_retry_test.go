package server

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/registry"
	"github.com/anydoor7/tslink/internal/testwait"
)

// Reviewer probe: a failed gateway start with a pending route sync (a registry
// event arrived during startup) and no routes. Is the portal retry still armed?
func TestGatewayFailedStartPendingSyncKeepsRetry(t *testing.T) {
	for _, pending := range []bool{false, true} {
		name := "control-no-pending"
		if pending {
			name = "pending-sync-during-start"
		}
		t.Run(name, func(t *testing.T) {
			f := newGatewayFixture(t, func(f *gatewayFixture) {
				for _, n := range []string{"photos", "payroll"} {
					if _, err := registry.Remove(f.path, n); err != nil {
						t.Fatal(err)
					}
				}
			})
			f.s.closePortal()
			prior := f.node
			f.node = &gatewayFakeNode{fakeTSNetServer: fakeTSNetServer{localClient: prior.localClient, certDomains: prior.certDomains, status: prior.status, upWait: true}, ports: map[string]net.Listener{}, fail: map[string]bool{}, stall: map[string]chan struct{}{}, cert: prior.cert, stop: make(chan struct{})}
			old := nodeStartupTimeout
			nodeStartupTimeout = 300 * time.Millisecond
			t.Cleanup(func() { nodeStartupTimeout = old })
			f.s.portalRetryPending.Store(false)
			f.s.syncPortal(context.Background(), f.config)
			run := f.s.portalRun
			if pending {
				f.s.syncPortal(context.Background(), f.config) // registry event during startup
			}
			testwait.Recv(t, run.done, "failed start")
			f.s.mu.Lock()
			state := f.s.portalState
			f.s.mu.Unlock()
			t.Logf("%s: portal state=%s error=%s pendingFlagSeen=%v retryArmed=%v", name, state.State, state.Error, pending, f.s.portalRetryPending.Load())
			if state.State != "failed" || state.Error != "portal_start_failed" || !f.s.portalRetryPending.Load() {
				t.Errorf("failed portal start lost its retry")
			}
		})
	}
}

func TestGatewayRouteRetryAvoidsFullSync(t *testing.T) {
	oldInterval := lifecycleTickerInterval
	lifecycleTickerInterval = 20 * time.Millisecond
	t.Cleanup(func() { lifecycleTickerInterval = oldInterval })
	f := newGatewayFixture(t, func(f *gatewayFixture) { f.node.fail[":8444"] = true })
	f.waitRoute(t, "payroll", "failed")
	run := f.s.portalRun
	run.routesMu.Lock()
	first := run.routes["payroll"]
	run.routesMu.Unlock()
	testwait.Recv(t, first.done, "first failed activation")
	before := f.s.syncGeneration.Load()
	f.now.Add(int64(2 * time.Second))
	f.s.lifecycleReconcileFn = func(context.Context, time.Time) (bool, error) { return false, nil }
	ctx, cancel := context.WithCancel(context.Background())
	done := f.s.startLifecycleTicker(ctx)
	defer func() { cancel(); testwait.Recv(t, done, "ticker stopped") }()
	// Observe an actual retry, not just an idle ticker with no work.
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		run.routesMu.Lock()
		next := run.routes["payroll"]
		retried := next != first && next != nil && next.state.State == "failed"
		run.routesMu.Unlock()
		if retried {
			break
		}
		select {
		case <-deadline.C:
			t.Fatal("route retry did not execute")
		case <-time.After(5 * time.Millisecond):
		}
	}
	cancel()
	testwait.Recv(t, done, "retry completed")
	if got := f.s.syncGeneration.Load(); got != before {
		t.Fatalf("route failure forced full syncNodes: generation %d -> %d", before, got)
	}
	if f.s.portalRetryPending.Load() {
		t.Fatal("route failure armed portal restart retry")
	}
}

func TestGatewayRouteSyncPreservesPortalRetry(t *testing.T) {
	f := newGatewayFixture(t, nil)
	f.s.portalRetryPending.Store(true)
	f.s.syncGatewayRoutes(f.s.portalRun)
	if !f.s.portalRetryPending.Load() {
		t.Fatal("route sync cleared portal retry")
	}
}
