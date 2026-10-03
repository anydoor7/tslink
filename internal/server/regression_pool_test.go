package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/anydoor7/tslink/internal/health"
	"github.com/anydoor7/tslink/internal/registry"
	"github.com/anydoor7/tslink/internal/testenv/localapitest"
	"tailscale.com/client/local"
	"tailscale.com/ipn/ipnstate"
)

func monitorError(t *testing.T, r *health.Recorder) string {
	t.Helper()
	var view struct {
		MonitorError string `json:"monitor_error"`
	}
	b, err := json.Marshal(r.View())
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &view); err != nil {
		t.Fatal(err)
	}
	return view.MonitorError
}

func TestReReviewSaturationPreservesUnattemptedHealthAndRecovers(t *testing.T) {
	s, dir := healthTestServer(t)
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	r := health.NewRecorder(filepath.Join(dir, health.StateFile), health.NotifierConfig{})
	var calls atomic.Int32
	release := make(chan struct{})
	var workers sync.WaitGroup
	var once sync.Once
	finish := func() { once.Do(func() { close(release) }); workers.Wait() }
	t.Cleanup(finish)
	for i := 0; i < 4; i++ {
		name := fmt.Sprint("stuck", i)
		s.nodes[name] = &ServiceNode{service: registry.Service{Name: name, Type: registry.TypeProxy, Target: "http://localhost:1234", Health: &registry.HealthConfig{Timeout: "100ms"}}}
	}
	s.healthCycle(context.Background(), r, now, func(context.Context, registry.Service) string {
		workers.Add(1)
		defer workers.Done()
		calls.Add(1)
		<-release
		return ""
	})
	if calls.Load() != 4 {
		t.Fatal("stuck-read control", calls.Load())
	}
	if got := monitorError(t, r); got != "health_monitor_saturated" {
		t.Errorf("no monitor warning: %q", got)
	}
	var hits atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { hits.Add(1); w.WriteHeader(200) }))
	defer backend.Close()
	svc := registry.Service{Name: "healthy", Type: registry.TypeProxy, Target: backend.URL, Health: &registry.HealthConfig{Timeout: "5s"}}
	if got := health.Probe(context.Background(), svc); got != "" || hits.Load() != 1 {
		t.Fatal("HTTP control", got, hits.Load())
	}
	hits.Store(0)
	s.nodes[svc.Name] = &ServiceNode{service: svc}
	previous := health.Result(health.Unchecked(svc.Type), svc.Type, "health_status_mismatch", now)
	r.ObserveHealth(svc.Name, healthIdentity(svc), previous, now)
	r.Commit(context.Background(), nil, now)
	s.healthStates[svc.Name] = serviceHealth{Identity: healthIdentity(svc), Health: previous}
	for i := 1; i <= 3; i++ {
		s.healthCycle(context.Background(), r, now.Add(time.Duration(i)*time.Minute), health.Probe)
	}
	if got := r.Previous(svc.Name, healthIdentity(svc)); got.ConsecutiveFailures != 1 || !got.LastChecked.Equal(now) {
		t.Errorf("unattempted changed streak/time: %+v", got)
	}
	if hits.Load() != 0 {
		t.Error("saturated pool started another read")
	}
	saturated := 0
	for _, e := range r.State.Events {
		if e.Kind == "monitor_saturated" {
			saturated++
		}
		if e.Kind == "app_down" && e.Service == svc.Name {
			t.Error("unattempted app down")
		}
	}
	if saturated != 1 {
		t.Errorf("saturation events=%d", saturated)
	}
	// Drop the blocked services; release and join the actual retained calls.
	for name := range s.nodes {
		if name != svc.Name {
			delete(s.nodes, name)
		}
	}
	finish()
	s.healthCycle(context.Background(), r, now.Add(4*time.Minute), health.Probe)
	if monitorError(t, r) != "" || hits.Load() != 1 || s.healthStates[svc.Name].Health.State != health.Healthy {
		t.Errorf("did not recover: %+v", r.View())
	}
	recovered := 0
	for _, e := range r.State.Events {
		if e.Kind == "monitor_recovered" {
			recovered++
		}
	}
	if recovered != 1 {
		t.Errorf("recovery events=%d", recovered)
	}
	if got := health.NewRecorder(r.Path, r.Config); monitorError(t, got) != "" || len(got.State.Events) != len(r.State.Events) {
		t.Error("monitor transitions not durable")
	}
}

// A context-ignoring LocalAPI transport still runs the real SDK decoder when
// released. Four distinct services fill the pool; one cannot occupy it twice.
func TestReReviewLocalAPIPoolGuardSaturationAndRecovery(t *testing.T) {
	s, dir := healthTestServer(t)
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	expires := now.Add(30*24*time.Hour + time.Hour)
	body, _ := json.Marshal(ipnstate.Status{Self: &ipnstate.PeerStatus{KeyExpiry: &expires}})
	r := health.NewRecorder(filepath.Join(dir, health.StateFile), health.NotifierConfig{})
	release := make(chan struct{})
	var workers sync.WaitGroup
	var once sync.Once
	finish := func() { once.Do(func() { close(release) }); workers.Wait() }
	t.Cleanup(finish)
	var reads atomic.Int32
	for i := 0; i < 4; i++ {
		svc := registry.Service{Name: fmt.Sprint("node", i), Type: registry.TypeProxy, Target: "http://localhost:1234", Health: &registry.HealthConfig{Interval: "1d"}}
		lc := localapitest.NewClient(localapitest.RoundTripFunc(func(*http.Request) (*http.Response, error) {
			workers.Add(1)
			defer workers.Done()
			reads.Add(1)
			<-release
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(body)))}, nil
		}))
		node := &ServiceNode{service: svc, tsnetSrv: &fakeTSNetServer{localClient: lc}}
		s.nodes[svc.Name] = node
	}
	probe := func(context.Context, registry.Service) string { return "" }
	s.healthCycle(context.Background(), r, now, probe)
	if reads.Load() != 4 || monitorError(t, r) != "health_monitor_saturated" {
		t.Errorf("LocalAPI pool control reads=%d monitor=%q", reads.Load(), monitorError(t, r))
	}
	for i := 1; i <= 3; i++ {
		s.healthCycle(context.Background(), r, now.Add(time.Duration(i)*time.Minute), probe)
	}
	if reads.Load() != 4 {
		t.Errorf("LocalAPI duplicated stuck reads: %d", reads.Load())
	}
	for name, observed := range s.healthStates {
		if !observed.NodeChecked.Equal(now) {
			t.Errorf("unattempted expiry advanced poll time for %s: %s", name, observed.NodeChecked)
		}
	}
	finish()
	deadline := time.Now().Add(5 * time.Second)
	for reads.Load() < 8 && time.Now().Before(deadline) {
		s.healthCycle(context.Background(), r, now.Add(4*time.Minute), probe)
		time.Sleep(time.Millisecond)
	}
	if reads.Load() != 8 || monitorError(t, r) != "" {
		t.Errorf("LocalAPI recovery reads=%d monitor=%q", reads.Load(), monitorError(t, r))
	}
	for name, observed := range s.healthStates {
		if observed.NodeKey.ExpiresAt == nil || !observed.NodeKey.ExpiresAt.Equal(expires) {
			t.Errorf("missing decoded recovery for %s", name)
		}
	}
}

func TestReReviewSingleLocalAPIReadSurvivesCyclesWithoutDuplication(t *testing.T) {
	s, dir := healthTestServer(t)
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	r := health.NewRecorder(filepath.Join(dir, health.StateFile), health.NotifierConfig{})
	release := make(chan struct{})
	var reads atomic.Int32
	var workers sync.WaitGroup
	var once sync.Once
	finish := func() { once.Do(func() { close(release) }); workers.Wait() }
	t.Cleanup(finish)
	lc := localapitest.NewClient(localapitest.RoundTripFunc(func(*http.Request) (*http.Response, error) {
		workers.Add(1)
		defer workers.Done()
		reads.Add(1)
		<-release
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
	}))
	svc := registry.Service{Name: "node", Type: registry.TypeProxy, Target: "http://localhost:1234"}
	s.nodes[svc.Name] = &ServiceNode{service: svc, tsnetSrv: &fakeTSNetServer{localClient: lc}}
	for i := 0; i < 4; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		s.healthCycle(ctx, r, now.Add(time.Duration(i)*time.Minute), func(context.Context, registry.Service) string { return "" })
		cancel()
	}
	if reads.Load() != 1 {
		t.Errorf("single service duplicated LocalAPI reads: %d", reads.Load())
	}
}

func TestReReviewLocalAPIQueueGetsFullIOBudget(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, dir := healthTestServer(t)
		now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
		expires := now.Add(20 * 24 * time.Hour)
		body, _ := json.Marshal(ipnstate.Status{Self: &ipnstate.PeerStatus{KeyExpiry: &expires}})
		var reads atomic.Int32
		newClient := func() *local.Client {
			return localapitest.NewClient(localapitest.RoundTripFunc(func(req *http.Request) (*http.Response, error) {
				reads.Add(1)
				if deadline, ok := req.Context().Deadline(); !ok || time.Until(deadline) != 5*time.Second {
					t.Errorf("installed LocalAPI I/O budget = %v (present=%v), want exactly 5s", time.Until(deadline), ok)
				}
				select {
				case <-time.After(3 * time.Second):
					return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(body)))}, nil
				case <-req.Context().Done():
					return nil, req.Context().Err()
				}
			}))
		}
		controlCtx, cancelControl := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancelControl()
		st, err := newClient().StatusWithoutPeers(controlCtx)
		if err != nil || st.Self == nil || st.Self.KeyExpiry == nil {
			t.Fatal("real decoder positive control", st, err)
		}
		reads.Store(0)
		for i := 0; i < 6; i++ {
			svc := registry.Service{Name: fmt.Sprint("node", i), Type: registry.TypeProxy, Target: "http://localhost:1234"}
			s.nodes[svc.Name] = &ServiceNode{service: svc, tsnetSrv: &fakeTSNetServer{localClient: newClient()}}
		}
		r := health.NewRecorder(filepath.Join(dir, health.StateFile), health.NotifierConfig{})
		s.healthCycle(context.Background(), r, now, func(context.Context, registry.Service) string { return "" })
		if reads.Load() != 6 {
			t.Error("missing admitted LocalAPI reads", reads.Load())
		}
		for name, observed := range s.healthStates {
			if observed.NodeKey.ExpiresAt == nil || !observed.NodeKey.ExpiresAt.Equal(expires) {
				t.Errorf("queued healthy LocalAPI lost deadline: %s %+v", name, observed.NodeKey)
			}
		}
	})
}
