package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/credentials"
	"github.com/anydoor7/tslink/internal/health"
	"github.com/anydoor7/tslink/internal/registry"
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
	deadline := time.After(time.Second)
	for {
		s.mu.RLock()
		state := s.healthStates["fast"].Health
		s.mu.RUnlock()
		if state.State == health.Healthy {
			break
		}
		select {
		case <-deadline:
			t.Fatal("completed fast probe hidden by slow probe")
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("monitor shutdown waits for uninterruptible I/O")
	}
}

func TestBoundedHealthReadRetainsSlotAcrossTimeouts(t *testing.T) {
	slots := make(chan struct{}, 1)
	started, release, exited := make(chan struct{}), make(chan struct{}), make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	got := boundedHealthRead(ctx, slots, "timeout", func(context.Context) string { close(started); <-release; close(exited); return "late" })
	t.Cleanup(func() { close(release); <-exited })
	<-started
	if got != "timeout" {
		t.Fatal(got)
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel2()
	got = boundedHealthRead(ctx2, slots, "timeout", func(context.Context) string { return "unbounded extra worker" })
	if got != "timeout" || len(slots) != 1 {
		t.Fatal("lost persistent worker bound", got, len(slots))
	}
}

func TestHealthMonitorProgressWhileNotifierRuns(t *testing.T) {
	if os.Getenv("TSLINK_HEALTH_MONITOR_NOTIFIER_CHILD") == "1" {
		if os.WriteFile(os.Getenv("TSLINK_HEALTH_MONITOR_NOTIFIER_READY"), []byte("ready"), 0600) != nil {
			os.Exit(2)
		}
		time.Sleep(30 * time.Second)
		os.Exit(0)
	}
	s, dir := healthTestServer(t)
	t.Setenv("TSLINK_HEALTH_MONITOR_NOTIFIER_CHILD", "1")
	ready := filepath.Join(dir, "notifier-ready")
	t.Setenv("TSLINK_HEALTH_MONITOR_NOTIFIER_READY", ready)
	config, _ := json.Marshal(health.NotifierConfig{Command: []string{os.Args[0], "-test.run=^TestHealthMonitorProgressWhileNotifierRuns$"}})
	if err := os.WriteFile(filepath.Join(dir, health.ConfigFile), config, 0600); err != nil {
		t.Fatal(err)
	}
	s.nodes["app"] = &ServiceNode{service: registry.Service{Name: "app", Type: registry.TypeProxy, Target: "http://localhost:1234"}}
	oldNow, oldProbe, oldInventory, oldInterval := serverNowFn, healthProbeFn, healthCredentialInventoryFn, healthTickInterval
	t.Cleanup(func() {
		serverNowFn, healthProbeFn, healthCredentialInventoryFn, healthTickInterval = oldNow, oldProbe, oldInventory, oldInterval
	})
	var ticks, probes atomic.Int32
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	serverNowFn = func() time.Time { return now.Add(time.Duration(ticks.Add(1)) * time.Minute) }
	healthProbeFn = func(context.Context, registry.Service) string { probes.Add(1); return "health_status_mismatch" }
	healthCredentialInventoryFn = func(time.Time) credentials.Inventory { return credentials.Inventory{} }
	healthTickInterval = 5 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := s.startHealthMonitor(ctx)
	t.Cleanup(func() { cancel(); <-done })
	deadline := time.After(3 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil && probes.Load() >= 6 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("slow delivery stopped health observations", probes.Load())
		case <-time.After(5 * time.Millisecond):
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("notifier blocked shutdown")
	}
	r := health.NewRecorder(filepath.Join(dir, health.StateFile), health.NotifierConfig{})
	if len(r.State.Events) != 1 || r.State.Events[0].Delivery != "failed" {
		t.Fatal("cancellation not journaled as failure", r.State.Events)
	}
}
