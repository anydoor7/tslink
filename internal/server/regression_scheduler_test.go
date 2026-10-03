package server

import (
	"context"
	"fmt"
	"github.com/anydoor7/tslink/internal/health"
	"github.com/anydoor7/tslink/internal/registry"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func TestReReviewQueueWaitDoesNotConsumeProbeBudget(t *testing.T) {
	t.Run("real_http", func(t *testing.T) {
		s, dir := healthTestServer(t)
		var requests atomic.Int32
		backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.WriteHeader(200) }))
		defer backend.Close()
		svc := registry.Service{Name: "control", Type: registry.TypeProxy, Target: backend.URL, Health: &registry.HealthConfig{Timeout: "5s"}}
		if code := health.Probe(context.Background(), svc); code != "" {
			t.Fatal("real HTTP control", code)
		}
		requests.Store(0)
		for i := 0; i < 12; i++ {
			v := svc
			v.Name = fmt.Sprintf("app%02d", i)
			s.nodes[v.Name] = &ServiceNode{service: v}
		}
		recorder := health.NewRecorder(filepath.Join(dir, health.StateFile), health.NotifierConfig{})
		s.healthCycle(context.Background(), recorder, time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC), health.Probe)
		if requests.Load() != 12 {
			t.Fatalf("real HTTP reads = %d, want 12", requests.Load())
		}
		for name, state := range s.healthStates {
			if state.Health.State != health.Healthy {
				t.Errorf("real HTTP %s: %+v", name, state.Health)
			}
		}
	})
	t.Run("virtual_queue", func(t *testing.T) {
		s, dir := healthTestServer(t)
		for i := 0; i < 12; i++ {
			name := fmt.Sprintf("app%02d", i)
			s.nodes[name] = &ServiceNode{service: registry.Service{Name: name, Type: registry.TypeProxy, Target: "http://127.0.0.1:1", Health: &registry.HealthConfig{Timeout: "500ms"}}}
		}
		recorder := health.NewRecorder(filepath.Join(dir, health.StateFile), health.NotifierConfig{})
		synctest.Test(t, func(t *testing.T) {
			var reads atomic.Int32
			start := time.Now()
			s.healthCycle(context.Background(), recorder, time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC), func(ctx context.Context, _ registry.Service) string {
				if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) != 500*time.Millisecond {
					t.Errorf("installed queued probe budget = %v (present=%v), want exactly 500ms", time.Until(deadline), ok)
					return "health_timeout"
				}
				reads.Add(1)
				select {
				case <-time.After(300 * time.Millisecond):
					return ""
				case <-ctx.Done():
					return "health_timeout"
				}
			})
			if reads.Load() != 12 {
				t.Fatalf("admitted probes = %d, want 12", reads.Load())
			}
			for name, state := range s.healthStates {
				if state.Health.State != health.Healthy {
					t.Errorf("virtual queued %s: %+v", name, state.Health)
				}
			}
			if elapsed := time.Since(start); elapsed != 900*time.Millisecond {
				t.Fatalf("virtual queue duration = %v, want three 300ms batches", elapsed)
			}
		})
	})
}

func TestReReviewStuckPoolDoesNotDisableHealthyApps(t *testing.T) {
	s, dir := healthTestServer(t)
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	r := health.NewRecorder(filepath.Join(dir, health.StateFile), health.NotifierConfig{})
	release := make(chan struct{})
	exited := make(chan struct{}, 4)
	var stuckCalls atomic.Int32
	s.nodes["stuck"] = &ServiceNode{service: registry.Service{Name: "stuck", Type: registry.TypeProxy, Target: "http://localhost:1234", Health: &registry.HealthConfig{Timeout: "100ms"}}}
	for i := 0; i < 4; i++ {
		s.healthCycle(context.Background(), r, now.Add(time.Duration(i)*time.Minute), func(context.Context, registry.Service) string {
			stuckCalls.Add(1)
			<-release
			exited <- struct{}{}
			return ""
		})
	}
	t.Cleanup(func() {
		close(release)
		for i := int32(0); i < stuckCalls.Load(); i++ {
			<-exited
		}
	})
	if stuckCalls.Load() != 1 {
		t.Errorf("one service accumulated concurrent reads: %d", stuckCalls.Load())
	}
	for name := range s.nodes {
		delete(s.nodes, name)
	}
	var hits atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1); w.WriteHeader(200) }))
	defer backend.Close()
	good := registry.Service{Name: "healthy", Type: registry.TypeProxy, Target: backend.URL, Health: &registry.HealthConfig{Timeout: "5s"}}
	if code := health.Probe(context.Background(), good); code != "" || hits.Load() != 1 {
		t.Fatal("real HTTP positive control", code, hits.Load())
	}
	hits.Store(0)
	s.nodes[good.Name] = &ServiceNode{service: good}
	for i := 1; i <= 3; i++ {
		s.healthCycle(context.Background(), r, now.Add(time.Duration(i+4)*time.Minute), health.Probe)
	}
	got := s.healthStates[good.Name].Health
	t.Logf("one removed stuck service accumulated reads=%d healthy endpoint requests=%d health=%+v alertError=%q events=%+v", stuckCalls.Load(), hits.Load(), got, r.Error, r.State.Events)
	if hits.Load() != 3 || got.State != health.Healthy {
		t.Errorf("permanently occupied pool reported an unprobed healthy app down; no monitor saturation diagnostic")
	}
}
