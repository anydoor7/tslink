package server

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/registry"
	"github.com/anydoor7/tslink/internal/tailapi"
	"github.com/anydoor7/tslink/internal/testenv"
)

// A changed public service is withdrawn while the Funnel policy preflight
// fails transiently. The sync itself succeeds (the failure is per service), so
// nothing else would ever run the preflight again: the lifecycle ticker must
// retry it, and once the outage clears the replacement node must be built.
func TestLifecycleTickerRetriesPolicyBlockedPublicServiceAfterOutage(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatal(err)
	}
	oldSvc := registry.Service{Name: "app", Type: registry.TypeProxy, Target: "http://localhost:3000", Tags: []string{"tag:owner"}, Funnel: true, PublicAck: true}
	newSvc := oldSvc
	newSvc.Target = "http://localhost:4000"
	writeRegistry(t, []registry.Service{newSvc})

	var constructs atomic.Int32
	stopAtUp := errors.New("intentional fake Up stop")
	oldNew := newTSNetServerFn
	newTSNetServerFn = func(registry.Service, string, string, string) tsnetServer {
		constructs.Add(1)
		return &fakeTSNetServer{upErr: stopAtUp}
	}
	t.Cleanup(func() { newTSNetServerFn = oldNew })

	s, err := New("key", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.closeAllNodes)
	listener := &fakeListener{}
	s.nodes["app"] = &ServiceNode{service: oldSvc, listener: listener, funnelListenerActive: true, cancel: func() {}}
	var outage atomic.Bool
	outage.Store(true)
	var policyCalls atomic.Int32
	s.SetEnsureFunnelAttrFn(func(context.Context, tailapi.FunnelPolicyRequest) (tailapi.PolicyMutationResult, error) {
		policyCalls.Add(1)
		if outage.Load() {
			return tailapi.PolicyMutationResult{WriteOutcome: tailapi.PolicyWriteNotAttempted}, errors.New("tailscale API 503")
		}
		return tailapi.PolicyMutationResult{WriteOutcome: tailapi.PolicyWriteNotAttempted}, nil
	})
	s.SetCleanupStaleNodesFn(func(context.Context, []tailapi.CleanupTarget) (tailapi.CleanupResult, error) {
		return tailapi.CleanupResult{}, nil
	})
	// The production reconciler reports "nothing changed" on an ordinary tick.
	s.SetLifecycleReconcileFn(func(context.Context, time.Time) (bool, error) { return false, nil })

	if err := s.syncNodes(context.Background()); err != nil {
		t.Fatalf("sync during outage: %v", err)
	}
	if !listener.closed.Load() || s.nodeRunning("app") || constructs.Load() != 0 {
		t.Fatalf("changed public service not withdrawn during outage: closed=%v running=%v constructs=%d", listener.closed.Load(), s.nodeRunning("app"), constructs.Load())
	}
	if s.lastSyncFailed.Load() {
		t.Fatal("per-service policy failure was reported as a failed sync; this test needs the per-service path")
	}

	outage.Store(false)
	oldInterval := lifecycleTickerInterval
	lifecycleTickerInterval = 10 * time.Millisecond
	t.Cleanup(func() { lifecycleTickerInterval = oldInterval })
	ctx, cancel := context.WithCancel(context.Background())
	done := s.startLifecycleTicker(ctx)
	deadline := time.Now().Add(3 * time.Second)
	for constructs.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done
	if constructs.Load() == 0 || policyCalls.Load() < 2 {
		t.Fatalf("lifecycle ticker never retried the blocked public service after the outage: policy calls=%d constructs=%d", policyCalls.Load(), constructs.Load())
	}
}
