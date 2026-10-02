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
	"time"
)

func TestReReviewQueueWaitDoesNotConsumeProbeBudget(t *testing.T) {
	s, dir := healthTestServer(t)
	var requests atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		select {
		case <-time.After(300 * time.Millisecond):
			w.WriteHeader(200)
		case <-r.Context().Done():
		}
	}))
	defer backend.Close()
	svc := registry.Service{Name: "control", Type: registry.TypeProxy, Target: backend.URL, Health: &registry.HealthConfig{Timeout: "500ms"}}
	if code := health.Probe(context.Background(), svc); code != "" {
		t.Fatal("real HTTP control", code)
	}
	requests.Store(0)
	for i := 0; i < 12; i++ {
		v := svc
		v.Name = fmt.Sprintf("app%02d", i)
		s.nodes[v.Name] = &ServiceNode{service: v}
	}
	r := health.NewRecorder(filepath.Join(dir, health.StateFile), health.NotifierConfig{})
	start := time.Now()
	s.healthCycle(context.Background(), r, time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC), health.Probe)
	failed := 0
	for name, state := range s.healthStates {
		if state.Health.State != health.Healthy {
			failed++
			t.Logf("%s: %+v", name, state.Health)
		}
	}
	t.Logf("healthy HTTP control=300ms timeout=500ms services=12 requests=%d failed=%d cycle=%s", requests.Load(), failed, time.Since(start))
	if failed != 0 {
		t.Errorf("worker-queue wait created %d false backend failures", failed)
	}
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
	good := registry.Service{Name: "healthy", Type: registry.TypeProxy, Target: backend.URL, Health: &registry.HealthConfig{Timeout: "100ms"}}
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
