package server

import (
	"bufio"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/health"
	"github.com/anydoor7/tslink/internal/registry"
	tsruntime "github.com/anydoor7/tslink/internal/runtime"
)

func TestHealthUnattemptedAgingWakesConnectedStream(t *testing.T) {
	for _, path := range []string{"available", "unavailable"} {
		t.Run(path, func(t *testing.T) { testHealthAgingStream(t, path == "unavailable") })
	}
}

func testHealthAgingStream(t *testing.T, unavailablePath bool) {
	t.Helper()
	s, dir := healthTestServer(t)
	s.healthProbePool = newHealthReadPool()
	s.healthNodePool = newHealthReadPool()
	base := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	r := health.NewRecorder(filepath.Join(dir, health.StateFile), health.NotifierConfig{})
	svc := registry.Service{Name: "app", Type: registry.TypeProxy, Target: "http://localhost:1234"}
	node := &ServiceNode{service: svc}
	s.nodes[svc.Name] = node
	h := health.Result(health.Unchecked(svc.Type), svc.Type, "", base)
	s.healthStates = map[string]serviceHealth{svc.Name: {Identity: healthIdentity(svc), Health: h, NodeKey: nodeKeyExpiry(nil, base), Node: node, NodeChecked: base}}
	r.Commit(context.Background(), r.ObserveHealth(svc.Name, healthIdentity(svc), h, base), base)
	release := make(chan struct{})
	defer func() {
		close(release)
		waitHealthFlights(t, s.healthProbePool)
		waitHealthFlights(t, s.healthNodePool)
	}()
	for _, p := range []*healthReadPool{s.healthProbePool, s.healthNodePool} {
		for _, name := range []string{"hold1", "hold2", "hold3", "hold4"} {
			_, attempted := boundedHealthRead(context.Background(), p, name, 10*time.Millisecond, "timeout", func(context.Context) string { <-release; return "late" })
			if !attempted {
				t.Fatal("saturation positive control not admitted")
			}
		}
	}
	noProbe := func(context.Context, registry.Service) string { t.Error("unattempted app actually probed"); return "" }
	s.healthCycle(context.Background(), r, base, noProbe)
	if r.State.MonitorError == "" {
		t.Fatal("saturation was not published before connecting")
	}
	var seconds atomic.Int64
	var builds atomic.Int32
	// This callback isolates transport wakeups. Production cmd.currentHealth and
	// MCP status/list/event aging are covered by the command health regressions.
	builder := func(context.Context) (any, error) {
		builds.Add(1)
		snap, err := tsruntime.Load(filepath.Join(dir, "runtime.json"))
		if err != nil {
			return nil, err
		}
		state := snap.Services[0].Health
		if base.Add(time.Duration(seconds.Load())*time.Second).Sub(*state.LastChecked) > 125*time.Second {
			state.State = health.Unknown
			state.LastError = "health_observation_stale"
		}
		return state, nil
	}
	cp := &MCPControlPlane{AllowedUsers: []string{"alice@example.com"}, Handler: &mcpProbeHandler{}, EventsSnapshot: builder, EventsKeepalive: 5 * time.Second}
	srv := httptest.NewServer(newMCPControlPlaneHandler(cp, mcpEventsAllowedClient(t), s.events))
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer func() { cancel(); srv.Close() }()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+MCPEventsPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	reader := bufio.NewReader(resp.Body)
	first := readMCPEventFrame(t, reader)
	if first.Event != MCPEventSnapshot || !strings.Contains(first.Data, `"state":"healthy"`) {
		t.Fatal("initial healthy wire control", first)
	}
	if unavailablePath {
		originalPath := runtimeSnapshotPathFn
		runtimeSnapshotPathFn = func() (string, error) { return "", errors.New("snapshot path unavailable") }
		t.Cleanup(func() { runtimeSnapshotPathFn = originalPath })
		s.mu.Lock()
		s.writeRuntimeSnapshotLocked(s.lastRegistryFingerprint, s.lastSnapshotComplete)
		s.mu.Unlock()
	}
	generation := s.events.currentGeneration()
	seconds.Store(125)
	s.healthCycle(context.Background(), r, base.Add(125*time.Second), noProbe)
	if s.events.currentGeneration() != generation {
		t.Error("fresh observation at the exact boundary woke the stream")
	}
	seconds.Store(180)
	for i := 3; i <= 5; i++ {
		s.healthCycle(context.Background(), r, base.Add(time.Duration(i)*time.Minute), noProbe)
	}
	if got := s.events.currentGeneration(); got != generation+1 {
		t.Errorf("staleness must wake once, then suppress unchanged cycles: generations=%d want=%d", got, generation+1)
	}
	next := readMCPEventFrame(t, reader)
	streamBuilds := builds.Load()
	direct, err := builder(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if direct.(health.State).State != health.Unknown {
		t.Fatal("aged builder positive control failed", direct)
	}
	t.Logf("old stream initial=healthy, after three unattempted cycles frame=%s, stream builder calls=%d; direct fresh builder state=%s", next.Event, streamBuilds, direct.(health.State).State)
	s.events.publish()
	control := readMCPEventFrame(t, reader)
	if control.Event != MCPEventUpdate || !strings.Contains(control.Data, `"state":"unknown"`) {
		t.Fatal("same-stream explicit wake positive control failed", control)
	}
	t.Log("positive control: explicit hub publication immediately delivers unknown on the same authorized connection")
	if next.Event != MCPEventUpdate || !strings.Contains(next.Data, `"state":"unknown"`) {
		t.Error("connected stream never reprojects aging health; receives state-free keepalive while polled projection is unknown")
	}
}

func waitHealthFlights(t *testing.T, p *healthReadPool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		p.mu.Lock()
		got := len(p.flights)
		p.mu.Unlock()
		if got == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("flights=%d want=0", got)
		}
		time.Sleep(time.Millisecond)
	}
}
