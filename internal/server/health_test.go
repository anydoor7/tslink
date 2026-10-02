package server

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/credentials"
	"github.com/anydoor7/tslink/internal/health"
	"github.com/anydoor7/tslink/internal/registry"
	tsruntime "github.com/anydoor7/tslink/internal/runtime"
	"github.com/anydoor7/tslink/internal/testenv/localapitest"
	"tailscale.com/ipn/ipnstate"
)

func healthTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv(config.ConfigDirEnv, dir)
	s, err := New("", "")
	if err != nil {
		t.Fatal(err)
	}
	oldPath := runtimeSnapshotPathFn
	runtimeSnapshotPathFn = func() (string, error) { return filepath.Join(dir, "runtime.json"), nil }
	t.Cleanup(func() { runtimeSnapshotPathFn = oldPath })
	s.lastRegistryFingerprint = "health-fixture"
	s.lastSnapshotComplete = true
	return s, dir
}

func TestHealthCycleBusinessProbeExpiryPersistenceAndInterval(t *testing.T) {
	s, dir := healthTestServer(t)
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	expiry := now.Add(14 * 24 * time.Hour)
	var localRequests atomic.Int32
	lc := localapitest.NewClient(localapitest.RoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/localapi/v0/status" || req.URL.RawQuery != "peers=false" {
			t.Errorf("unexpected LocalAPI request %s", req.URL)
		}
		localRequests.Add(1)
		b, _ := json.Marshal(ipnstate.Status{Self: &ipnstate.PeerStatus{KeyExpiry: &expiry}})
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(b)))}, nil
	}))
	svc := registry.Service{Name: "app", Type: registry.TypeProxy, Target: "http://localhost:1234"}
	s.nodes[svc.Name] = &ServiceNode{service: svc, tsnetSrv: &fakeTSNetServer{localClient: lc}}
	r := health.NewRecorder(filepath.Join(dir, health.StateFile), health.NotifierConfig{})
	var probes atomic.Int32
	probe := func(context.Context, registry.Service) string { probes.Add(1); return "health_status_mismatch" }
	s.healthCycle(context.Background(), r, now, probe)
	s.healthCycle(context.Background(), r, now.Add(10*time.Second), probe)
	if probes.Load() != 1 || localRequests.Load() != 1 {
		t.Fatalf("hammered app probes=%d local=%d", probes.Load(), localRequests.Load())
	}
	s.healthCycle(context.Background(), r, now.Add(time.Minute), probe)
	s.healthCycle(context.Background(), r, now.Add(2*time.Minute), probe)
	snapshot, err := tsruntime.Load(filepath.Join(dir, "runtime.json"))
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Services[0].Health.State != health.Down || snapshot.Services[0].Health.ConsecutiveFailures != 3 || snapshot.Services[0].NodeKey.Warning != "warning_14d" || len(snapshot.Alerts.Events) != 2 {
		t.Fatalf("%+v", snapshot)
	}
	r = health.NewRecorder(r.Path, r.Config)
	s.healthCycle(context.Background(), r, now.Add(3*time.Minute), probe)
	if len(r.State.Events) != 2 {
		t.Fatalf("duplicate alert after restart: %+v", r.State.Events)
	}
	s.healthCycle(context.Background(), r, now.Add(4*time.Minute), func(context.Context, registry.Service) string { return "" })
	if len(r.State.Events) != 3 || r.State.Events[2].Kind != "app_recovered" {
		t.Fatal(r.State.Events)
	}
}

func TestHealthMonitorCapturesClockAndFunctionsBeforeSpawning(t *testing.T) {
	s, dir := healthTestServer(t)
	s.nodes["app"] = &ServiceNode{service: registry.Service{Name: "app", Type: registry.TypeProxy, Target: "http://localhost:1234"}}
	now := time.Date(2030, 6, 1, 0, 0, 0, 0, time.UTC)
	oldNow, oldProbe, oldInventory, oldInterval := serverNowFn, healthProbeFn, healthCredentialInventoryFn, healthTickInterval
	t.Cleanup(func() {
		serverNowFn = oldNow
		healthProbeFn = oldProbe
		healthCredentialInventoryFn = oldInventory
		healthTickInterval = oldInterval
	})
	serverNowFn = func() time.Time { return now }
	healthProbeFn = func(context.Context, registry.Service) string { return "" }
	healthCredentialInventoryFn = func(time.Time) credentials.Inventory { return credentials.Inventory{} }
	healthTickInterval = 5 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := s.startHealthMonitor(ctx)
	t.Cleanup(func() { cancel(); <-done })
	serverNowFn = func() time.Time { panic("uncaptured clock") }
	healthProbeFn = func(context.Context, registry.Service) string { panic("uncaptured probe") }
	healthCredentialInventoryFn = func(time.Time) credentials.Inventory { panic("uncaptured credentials") }
	deadline := time.After(3 * time.Second)
	for {
		snapshot, err := tsruntime.Load(filepath.Join(dir, "runtime.json"))
		if err == nil && len(snapshot.Services) > 0 && snapshot.Services[0].Health.LastChecked != nil {
			if !snapshot.Services[0].Health.LastChecked.Equal(now) {
				t.Fatal("wrong clock", snapshot.Services[0].Health)
			}
			break
		}
		select {
		case <-deadline:
			t.Fatal("no monitor observation")
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func TestHealthCycleIgnoresWithdrawnNodeAndKeepsPartialSnapshot(t *testing.T) {
	s, dir := healthTestServer(t)
	s.lastSnapshotComplete = false
	svc := registry.Service{Name: "app", Type: registry.TypeProxy, Target: "http://localhost:1234"}
	s.nodes["app"] = &ServiceNode{service: svc}
	r := health.NewRecorder(filepath.Join(dir, health.StateFile), health.NotifierConfig{})
	s.healthCycle(context.Background(), r, time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC), func(context.Context, registry.Service) string {
		s.mu.Lock()
		s.nodes["app"] = &ServiceNode{service: svc}
		s.mu.Unlock()
		return ""
	})
	snapshot, err := tsruntime.Load(filepath.Join(dir, "runtime.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.Partial || snapshot.Services[0].Health.State != health.Unknown || len(r.State.Events) != 0 {
		t.Fatalf("stale results were applied: %+v", snapshot)
	}
}

func TestNodeKeyExpiryNeverInventsDeadline(t *testing.T) {
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, st := range []*ipnstate.Status{nil, {}, {Self: &ipnstate.PeerStatus{}}} {
		e := nodeKeyExpiry(st, now)
		if e.State != health.Unknown || e.ExpiresAt != nil || e.DaysLeft != nil {
			t.Fatal(e)
		}
	}
}

func TestHealthIdentityTracksBackendAndKeepsSharingEdits(t *testing.T) {
	svc := registry.Service{Name: "app", Type: registry.TypeProxy, Target: "http://localhost:1234"}
	id := healthIdentity(svc)
	svc.AllowedUsers = []string{"friend@example.com"}
	svc.Tags = []string{"tag:app"}
	if got := healthIdentity(svc); got != id {
		t.Fatal("sharing edit reset app health")
	}
	svc.Health = &registry.HealthConfig{Path: "/ready"}
	if got := healthIdentity(svc); got == id {
		t.Fatal("probe edit kept previous app health")
	}
}

func TestHealthTransitionsArriveOnEventsStream(t *testing.T) {
	s, dir := healthTestServer(t)
	s.nodes["app"] = &ServiceNode{service: registry.Service{Name: "app", Type: registry.TypeProxy, Target: "http://localhost:1234"}}
	s.mu.Lock()
	s.writeRuntimeSnapshotLocked("health-fixture", true)
	s.mu.Unlock()
	srv := mcpEventsTestServer(t, s.events, func(context.Context) (any, error) { return tsruntime.Load(filepath.Join(dir, "runtime.json")) })
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
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
	if first.Event != MCPEventSnapshot {
		t.Fatal(first)
	}
	r := health.NewRecorder(filepath.Join(dir, health.StateFile), health.NotifierConfig{})
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		s.healthCycle(ctx, r, now.Add(time.Duration(i)*time.Minute), func(context.Context, registry.Service) string { return "health_status_mismatch" })
		frame := readMCPEventFrame(t, reader)
		// Node expiry and app checks publish independently as they finish.
		for !strings.Contains(frame.Data, `"consecutive_failures":`+fmt.Sprint(i+1)) {
			frame = readMCPEventFrame(t, reader)
		}
		if frame.Event != MCPEventUpdate || !strings.Contains(frame.Data, `"consecutive_failures":`+fmt.Sprint(i+1)) {
			t.Fatal(frame)
		}
		if i == 2 && !strings.Contains(frame.Data, `"kind":"app_down"`) {
			t.Fatal(frame)
		}
	}
	s.healthCycle(ctx, r, now.Add(3*time.Minute), func(context.Context, registry.Service) string { return "" })
	frame := readMCPEventFrame(t, reader)
	for !strings.Contains(frame.Data, `"kind":"app_recovered"`) {
		frame = readMCPEventFrame(t, reader)
	}
	if frame.Event != MCPEventUpdate || !strings.Contains(frame.Data, `"kind":"app_recovered"`) {
		t.Fatal(frame)
	}
}

func TestHealthMonitorCredentialExpiryAndInvalidNotifier(t *testing.T) {
	s, dir := healthTestServer(t)
	if err := os.WriteFile(filepath.Join(dir, health.ConfigFile), []byte(`{"webhook":"%secret"}`), 0600); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2030, 6, 1, 0, 0, 0, 0, time.UTC)
	expires := now.Add(3 * 24 * time.Hour)
	oldNow, oldInventory, oldInterval := serverNowFn, healthCredentialInventoryFn, healthTickInterval
	t.Cleanup(func() {
		serverNowFn = oldNow
		healthCredentialInventoryFn = oldInventory
		healthTickInterval = oldInterval
	})
	serverNowFn = func() time.Time { return now }
	healthTickInterval = 5 * time.Millisecond
	healthCredentialInventoryFn = func(time.Time) credentials.Inventory {
		return credentials.Inventory{APIKey: credentials.SlotView{Slot: credentials.SlotAPIKey, Present: true, Metadata: &credentials.SlotMetadata{ExpiresAt: &expires, ExpiresAtSource: credentials.ExpirySourceAssumedMax}}}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := s.startHealthMonitor(ctx)
	t.Cleanup(func() { cancel(); <-done })
	deadline := time.After(3 * time.Second)
	for {
		s.mu.RLock()
		alerts := s.alerts
		s.mu.RUnlock()
		if len(alerts.Events) > 0 {
			if alerts.Error != "alert_webhook_invalid" || alerts.Notifier != "none" || alerts.Events[0].Expiry.Source != credentials.ExpirySourceAssumedMax || alerts.Events[0].Expiry.Warning != "critical_3d" {
				t.Fatalf("%+v", alerts)
			}
			break
		}
		select {
		case <-deadline:
			t.Fatal("credential warning missing")
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func TestHealthCycleFailedNodesAndCancellation(t *testing.T) {
	s, dir := healthTestServer(t)
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	svc := registry.Service{Name: "failed", Type: registry.TypeProxy, Target: "http://localhost:1234", Health: &registry.HealthConfig{Interval: "10s"}}
	s.serviceFailures[svc.Name] = tsruntime.ServiceState{Service: svc, RuntimeState: tsruntime.ServiceRuntimeFailed}
	r := health.NewRecorder(filepath.Join(dir, health.StateFile), health.NotifierConfig{})
	probed := false
	s.healthCycle(context.Background(), r, now, func(context.Context, registry.Service) string { probed = true; return "" })
	if got := r.Previous(svc.Name, healthIdentity(svc)); !probed || got.State != health.Healthy {
		t.Fatal(got)
	}
	s.nodes["running"] = &ServiceNode{service: registry.Service{Name: "running", Type: registry.TypeTCP, Target: "localhost:1234"}}
	s.healthCycle(context.Background(), r, now.Add(10*time.Second), func(context.Context, registry.Service) string {
		s.mu.Lock()
		delete(s.serviceFailures, svc.Name)
		s.mu.Unlock()
		return ""
	})
	if got := r.Previous(svc.Name, healthIdentity(svc)); got.LastChecked == nil || !got.LastChecked.Equal(now) {
		t.Fatal("withdrawn failure result published", got)
	}
	// A canceled batch joins workers and never publishes their partial results.
	previous := s.healthStates["running"]
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	s.nodes["running"] = &ServiceNode{service: registry.Service{Name: "running", Type: registry.TypeTCP, Target: "localhost:1234"}}
	canceledProbe := false
	s.healthCycle(ctx, r, now.Add(2*time.Minute), func(context.Context, registry.Service) string { canceledProbe = true; cancel(); return "" })
	if !canceledProbe {
		t.Fatal("cancellation fixture did not start a due probe")
	}
	if got := s.healthStates["running"]; got.Health.LastChecked == nil || !got.Health.LastChecked.Equal(*previous.Health.LastChecked) {
		t.Fatal("canceled batch published health")
	}
}

func TestHealthInventoryReportsUnreadableCredential(t *testing.T) {
	_, dir := healthTestServer(t)
	if err := os.Mkdir(filepath.Join(dir, config.APIKeyFileName), 0700); err != nil {
		t.Fatal(err)
	}
	if inventory := healthCredentialInventoryFn(time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)); inventory.MetadataError == nil {
		t.Fatal("unreadable credential has no metadata error")
	}
	if err := os.Remove(filepath.Join(dir, config.APIKeyFileName)); err != nil {
		t.Fatal(err)
	}
	if inventory := healthCredentialInventoryFn(time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)); inventory.MetadataError != nil {
		t.Fatal(inventory.MetadataError)
	}
}
