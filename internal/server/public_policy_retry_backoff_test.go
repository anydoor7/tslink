package server

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/registry"
	"github.com/monody0007/tslink/internal/tailapi"
	"github.com/monody0007/tslink/internal/testenv"
)

// policyTickRecorder counts lifecycle ticks through the reconcile seam, which
// the ticker calls once per delivered tick before it decides whether to sync,
// and notes the tick each Funnel policy request is made on. Counting delivered
// ticks keeps the schedule independent of timing: a tick the runtime drops
// because a sync overran the interval is not counted either.
type policyTickRecorder struct {
	ticks atomic.Int32
	mu    sync.Mutex
	calls []int32
}

func (r *policyTickRecorder) policyCall() {
	r.mu.Lock()
	r.calls = append(r.calls, r.ticks.Load())
	r.mu.Unlock()
}

func (r *policyTickRecorder) callTicks(last int32) []int32 {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []int32
	for _, tick := range r.calls {
		if tick <= last {
			out = append(out, tick)
		}
	}
	return out
}

// blockPublicServiceOnPolicy makes every Funnel policy preflight fail, as it
// does for good when tailnet HTTPS is disabled.
func blockPublicServiceOnPolicy(s *Server, r *policyTickRecorder) {
	s.SetEnsureFunnelAttrFn(func(context.Context, tailapi.FunnelPolicyRequest) (tailapi.PolicyMutationResult, error) {
		r.policyCall()
		return tailapi.PolicyMutationResult{WriteOutcome: tailapi.PolicyWriteNotAttempted}, errors.New("tailscale API: HTTPS is disabled for this tailnet")
	})
	s.SetCleanupStaleNodesFn(func(context.Context, []tailapi.CleanupTarget) (tailapi.CleanupResult, error) {
		return tailapi.CleanupResult{}, nil
	})
}

// runLifecycleTicks runs the lifecycle ticker until it has delivered last
// ticks. onTick runs inside the reconcile seam on each tick and returns what
// the reconciler reports as changed.
func runLifecycleTicks(t *testing.T, s *Server, r *policyTickRecorder, last int32, onTick func(tick int32) bool) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reached := make(chan struct{})
	var reachedOnce sync.Once
	s.SetLifecycleReconcileFn(func(context.Context, time.Time) (bool, error) {
		tick := r.ticks.Add(1)
		if tick > last {
			reachedOnce.Do(func() { close(reached) })
			cancel()
			return false, nil
		}
		if onTick != nil {
			return onTick(tick), nil
		}
		return false, nil
	})
	oldInterval := lifecycleTickerInterval
	lifecycleTickerInterval = time.Millisecond
	t.Cleanup(func() { lifecycleTickerInterval = oldInterval })
	done := s.startLifecycleTicker(ctx)
	select {
	case <-reached:
	case <-time.After(20 * time.Second):
		t.Errorf("lifecycle ticker delivered only %d of %d ticks", r.ticks.Load(), last)
	}
	cancel()
	<-done
}

func publicPolicyService(target string) registry.Service {
	return registry.Service{Name: "app", Type: registry.TypeProxy, Target: target, Tags: []string{"tag:owner"}, Funnel: true, PublicAck: true}
}

func newPolicyBlockedServer(t *testing.T, r *policyTickRecorder) *Server {
	t.Helper()
	s, err := New("key", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.closeAllNodes)
	blockPublicServiceOnPolicy(s, r)
	if err := s.syncNodes(context.Background()); err != nil {
		t.Fatalf("sync with a policy-blocked public service: %v", err)
	}
	if s.lastSyncFailed.Load() {
		t.Fatal("per-service policy failure was reported as a failed sync; this test needs the per-service path")
	}
	return s
}

// A service blocked for good by the Funnel policy preflight must not cost one
// policy API request per 30 s tick forever. The first retry still runs on the
// next tick, as before; after that each wait doubles: 1, 2, 4, 8, 16 ticks.
func TestPolicyRetryBackoffDoublesTheWaitBetweenRetries(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatal(err)
	}
	writeRegistry(t, []registry.Service{publicPolicyService("http://localhost:3000")})
	r := &policyTickRecorder{}
	s := newPolicyBlockedServer(t, r)

	runLifecycleTicks(t, s, r, 31, nil)

	if got, want := r.callTicks(31), []int32{0, 1, 3, 7, 15, 31}; !slices.Equal(got, want) {
		t.Fatalf("policy requests on ticks %v, want %v (tick 0 is the initial sync)", got, want)
	}
}

// Editing the registry is how a user reacts to a blocked service, so a sync
// against a changed registry starts the schedule over: the next tick retries.
func TestPolicyRetryBackoffRestartsAfterRegistryChange(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatal(err)
	}
	regPath := writeRegistry(t, []registry.Service{publicPolicyService("http://localhost:3000")})
	r := &policyTickRecorder{}
	s := newPolicyBlockedServer(t, r)
	changed, err := json.Marshal(registry.Registry{Services: []registry.Service{publicPolicyService("http://localhost:4000")}})
	if err != nil {
		t.Fatal(err)
	}

	// On tick 9 the reconciler rewrites the registry, as a Funnel expiry
	// downgrade does, and reports the change.
	runLifecycleTicks(t, s, r, 30, func(tick int32) bool {
		if tick != 9 {
			return false
		}
		if err := os.WriteFile(regPath, append(changed, '\n'), 0o600); err != nil {
			t.Errorf("rewrite registry: %v", err)
		}
		return true
	})

	if got, want := r.callTicks(30), []int32{0, 1, 3, 7, 9, 10, 12, 16, 24}; !slices.Equal(got, want) {
		t.Fatalf("policy requests on ticks %v, want %v (registry changed on tick 9)", got, want)
	}
}

// A retry for an unreadable identity record reads only local files, so it
// stays on every tick even while a policy-blocked service is backing off. The
// policy requests below are the visible side of those syncs: each one also
// re-runs the preflight of the blocked public service.
func TestIdentityRecordRetryIsNotSpacedOutByPolicyBackoff(t *testing.T) {
	services := legacyLayoutServices(t)
	writeLegacyLayout(t, services)
	p := instrumentIdentity(t)
	startLegacyLayoutOnce(t, p)
	recordPath := filepath.Join(mustConfigDir(t), "node-identities", "api.json")
	if err := os.WriteFile(recordPath, []byte("{bad"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeRegistry(t, append(append([]registry.Service(nil), services...), publicPolicyService("http://localhost:3000")))

	r := &policyTickRecorder{}
	s := newIdentityProbeServer(t, "", p)
	t.Cleanup(s.closeAllNodes)
	blockPublicServiceOnPolicy(s, r)
	if err := s.syncNodes(context.Background()); err != nil {
		t.Fatalf("sync with an unreadable record and a policy-blocked service: %v", err)
	}
	if s.nodeRunning("api") || s.nodeRunning("app") {
		t.Fatalf("blocked services started: api=%v app=%v", s.nodeRunning("api"), s.nodeRunning("app"))
	}

	runLifecycleTicks(t, s, r, 12, nil)

	var want []int32
	for tick := int32(0); tick <= 12; tick++ {
		want = append(want, tick)
	}
	if got := r.callTicks(12); !slices.Equal(got, want) {
		t.Fatalf("syncs (seen through policy requests) on ticks %v, want every tick %v", got, want)
	}
	if s.nodeRunning("api") {
		t.Fatalf("api started from an unreadable record at %s", recordPath)
	}
}
