package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/anydoor7/tslink/internal/credentials"
	"github.com/anydoor7/tslink/internal/health"
	"github.com/anydoor7/tslink/internal/registry"
	tsruntime "github.com/anydoor7/tslink/internal/runtime"
	"github.com/anydoor7/tslink/internal/testwait"
)

func TestHealthCyclePublishesOtherResultsAndCancelsWithStuckProbe(t *testing.T) {
	s, dir := healthTestServer(t)
	for _, name := range []string{"slow", "fast"} {
		s.nodes[name] = &ServiceNode{service: registry.Service{Name: name, Type: registry.TypeProxy, Target: "http://localhost:1234", Health: &registry.HealthConfig{Timeout: "5s"}}}
	}
	r := health.NewRecorder(filepath.Join(dir, health.StateFile), health.NotifierConfig{})
	started, release, exited := make(chan struct{}), make(chan struct{}), make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.healthCycle(ctx, r, time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC), func(_ context.Context, svc registry.Service) string {
			if svc.Name == "slow" {
				close(started)
				<-release // simulate an uninterruptible filesystem system call
				close(exited)
			} else {
				<-started
			}
			return ""
		})
	}()
	t.Cleanup(func() { cancel(); close(release); <-done; <-exited })
	testwait.Until(t, "completed fast probe published despite slow probe", func() bool {
		s.mu.RLock()
		defer s.mu.RUnlock()
		return s.healthStates["fast"].Health.State == health.Healthy
	})
	cancel()
	testwait.Recv(t, done, "monitor shutdown waits for uninterruptible I/O")
}

func TestBoundedHealthReadRetainsSlotAcrossTimeouts(t *testing.T) {
	slots := newHealthReadPool()
	slots.slots = make(chan struct{}, 1)
	started, release, exited := make(chan struct{}), make(chan struct{}), make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	got, attempted := boundedHealthRead(ctx, slots, "stuck", 50*time.Millisecond, "timeout", func(context.Context) string { close(started); <-release; close(exited); return "late" })
	t.Cleanup(func() { close(release); <-exited })
	<-started
	if got != "timeout" || !attempted {
		t.Fatal(got)
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel2()
	got, attempted = boundedHealthRead(ctx2, slots, "other", 50*time.Millisecond, "timeout", func(context.Context) string { return "unbounded extra worker" })
	if got != "timeout" || attempted || len(slots.slots) != 1 {
		t.Fatal("lost persistent worker bound", got, len(slots.slots))
	}
}

func TestHealthMonitorProgressWhileNotifierRuns(t *testing.T) {
	s, dir := healthTestServer(t)
	s.nodes["app"] = &ServiceNode{service: registry.Service{Name: "app", Type: registry.TypeProxy, Target: "http://localhost:1234"}}
	oldNow, oldProbe, oldInventory, oldInterval := serverNowFn, healthProbeFn, healthCredentialInventoryFn, healthTickInterval
	t.Cleanup(func() {
		serverNowFn, healthProbeFn, healthCredentialInventoryFn, healthTickInterval = oldNow, oldProbe, oldInventory, oldInterval
	})
	var ticks, probes atomic.Int32
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	serverNowFn = func() time.Time { return now.Add(time.Duration(ticks.Add(1)) * time.Minute) }
	healthProbeFn = func(context.Context, registry.Service) string { probes.Add(1); return "health_request_failed" }
	healthCredentialInventoryFn = func(time.Time) credentials.Inventory { return credentials.Inventory{} }
	healthTickInterval = 5 * time.Millisecond
	synctest.Test(t, func(t *testing.T) {
		r := health.NewRecorder(filepath.Join(dir, health.StateFile), health.NotifierConfig{Command: []string{"/private/notifier"}})
		started, exited := make(chan struct{}), make(chan struct{})
		r.Send = func(ctx context.Context, _ health.NotifierConfig, _ health.Event) error {
			close(started)
			<-ctx.Done()
			close(exited)
			return ctx.Err()
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := s.startHealthMonitorWithRecorder(ctx, r)
		defer func() { cancel(); <-done }()
		time.Sleep(30 * time.Millisecond)
		synctest.Wait()
		select {
		case <-started:
		default:
			t.Fatal("monitor never started delivery")
		}
		s.mu.RLock()
		before := s.healthStates["app"].Health.LastChecked
		s.mu.RUnlock()
		generation, count := s.events.currentGeneration(), probes.Load()
		if before == nil {
			t.Fatal("no initial health observation")
		}
		time.Sleep(healthTickInterval)
		synctest.Wait()
		s.mu.RLock()
		after := s.healthStates["app"].Health.LastChecked
		s.mu.RUnlock()
		if after == nil || !after.After(*before) || probes.Load() <= count || s.events.currentGeneration() <= generation {
			t.Fatal("blocked delivery stopped subsequent probe or published observation")
		}
		snapshot, err := tsruntime.Load(filepath.Join(dir, "runtime.json"))
		if err != nil || len(snapshot.Services) != 1 || snapshot.Services[0].Health.LastChecked == nil || !snapshot.Services[0].Health.LastChecked.Equal(*after) {
			t.Fatal("subsequent observation was not published to the runtime snapshot", err)
		}
		select {
		case <-exited:
			t.Fatal("delivery ended before progress assertion")
		default:
		}
		cancel()
		<-done
		<-exited
		reopened := health.NewRecorder(r.Path, r.Config)
		if len(reopened.State.Events) != 1 || reopened.State.Events[0].Delivery != "failed" {
			t.Fatal("cancellation not durably journaled as failure", reopened.State.Events)
		}
	})
}

// Pin the monitor's own join path while Send ignores cancellation. The real
// command cancellation contract is covered by health.TestNotifierCommandCancellation.
func TestHealthMonitorShutdownJoinBound(t *testing.T) {
	s, dir := healthTestServer(t)
	s.nodes["app"] = &ServiceNode{service: registry.Service{Name: "app", Type: registry.TypeProxy, Target: "http://localhost:1234"}}
	oldNow, oldProbe, oldInventory, oldInterval := serverNowFn, healthProbeFn, healthCredentialInventoryFn, healthTickInterval
	t.Cleanup(func() {
		serverNowFn, healthProbeFn, healthCredentialInventoryFn, healthTickInterval = oldNow, oldProbe, oldInventory, oldInterval
	})
	var ticks atomic.Int32
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	serverNowFn = func() time.Time { return now.Add(time.Duration(ticks.Add(1)) * time.Minute) }
	healthProbeFn = func(context.Context, registry.Service) string { return "health_request_failed" }
	healthCredentialInventoryFn = func(time.Time) credentials.Inventory { return credentials.Inventory{} }
	healthTickInterval = 5 * time.Millisecond
	synctest.Test(t, func(t *testing.T) {
		r := health.NewRecorder(filepath.Join(dir, health.StateFile), health.NotifierConfig{Command: []string{"/private/notifier"}})
		started, release, exited := make(chan struct{}), make(chan struct{}), make(chan struct{})
		r.Send = func(context.Context, health.NotifierConfig, health.Event) error {
			close(started)
			<-release
			close(exited)
			return nil
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := s.startHealthMonitorWithRecorder(ctx, r)
		defer func() { cancel(); close(release); <-done }()
		time.Sleep(30 * time.Millisecond)
		synctest.Wait()
		select {
		case <-started:
		default:
			t.Fatal("monitor never started delivery")
		}
		begin := time.Now()
		cancel()
		synctest.Wait()
		select {
		case <-done:
			t.Fatal("monitor returned before joining its blocked notifier")
		default:
		}
		time.Sleep(time.Second)
		synctest.Wait()
		select {
		case <-done:
		default:
			t.Fatal("monitor exceeded its one-second delivery join bound")
		}
		if got := time.Since(begin); got != time.Second {
			t.Fatalf("monitor join = %v, want exactly 1s", got)
		}
		select {
		case <-exited:
			t.Fatal("notifier was released before the join assertion")
		default:
		}
		if len(r.State.Events) != 1 || r.State.Events[0].Delivery != "failed" {
			t.Fatal("cancellation not journaled", r.State.Events)
		}
	})
}

// A probe whose backend sent its headers and then stalled the body times out
// with the status it observed before the deadline. A backend that has not
// answered by the deadline leaves no status. The deadline is the cancellation
// of the bounded read's parent, so neither order depends on timing.
func TestHealthTimeoutKeepsOnlyStatusObservedBeforeDeadline(t *testing.T) {
	for _, tc := range []struct {
		name          string
		headersFirst  bool
		wantStatus    int
		wantAttempted bool
	}{{"headers before deadline", true, 200, true}, {"no headers before deadline", false, 0, true}} {
		t.Run(tc.name, func(t *testing.T) {
			entered, release := make(chan struct{}), make(chan struct{})
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.headersFirst {
					w.Header().Set("Content-Length", "100")
					w.WriteHeader(http.StatusOK)
					w.(http.Flusher).Flush()
				}
				close(entered)
				select {
				case <-release:
				case <-r.Context().Done():
				}
				if !tc.headersFirst {
					w.WriteHeader(http.StatusOK)
				}
			}))
			defer backend.Close()
			defer close(release)
			svc := registry.Service{Name: "app", Type: registry.TypeProxy, Target: backend.URL, Health: &registry.HealthConfig{BodyContains: "ready", Timeout: "30s"}}
			parent, deadline := context.WithCancel(context.Background())
			defer deadline()
			probeCtx, status := health.WithResponseStatus(parent)
			// The read stays blocked past the deadline, as a stalled body read does.
			held := make(chan struct{})
			defer close(held)
			type outcome struct {
				code      string
				attempted bool
			}
			result := make(chan outcome, 1)
			go func() {
				code, attempted := boundedHealthRead(probeCtx, newHealthReadPool(), svc.Name, time.Minute, "health_timeout", func(ctx context.Context) string {
					code := health.Probe(ctx, svc)
					<-held
					return code
				})
				result <- outcome{code, attempted}
			}()
			testwait.Recv(t, entered, "backend entered")
			if tc.headersFirst {
				testwait.Until(t, "status observed", func() bool { return status.Code() == 200 })
			}
			deadline()
			got := testwait.Recv(t, result, "bounded read")
			if got.code != "health_timeout" || got.attempted != tc.wantAttempted {
				t.Fatalf("outcome %+v", got)
			}
			h := health.ResultOf(health.State{}, registry.TypeProxy, health.Outcome{Error: got.code, StatusCode: status.Snapshot()}, time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC))
			if h.LastError != "health_timeout" || h.State != health.Degraded || h.StatusCode != tc.wantStatus {
				t.Fatalf("state %+v, want status %d", h, tc.wantStatus)
			}
		})
	}
}
