package server

import (
	"context"
	"encoding/json"
	"github.com/anydoor7/tslink/internal/health"
	"github.com/anydoor7/tslink/internal/registry"
	tsruntime "github.com/anydoor7/tslink/internal/runtime"
	"github.com/anydoor7/tslink/internal/testenv/localapitest"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"tailscale.com/ipn/ipnstate"
	"testing"
	"time"
)

func TestReviewReplacementNodeDoesNotInheritOldKeyExpiry(t *testing.T) {
	s, dir := healthTestServer(t)
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	oldDeadline := now.Add(30 * 24 * time.Hour)
	newDeadline := now.Add(2 * 24 * time.Hour)
	reads := 0
	server := func(deadline time.Time) *fakeTSNetServer {
		lc := localapitest.NewClient(localapitest.RoundTripFunc(func(req *http.Request) (*http.Response, error) {
			reads++
			b, _ := json.Marshal(ipnstate.Status{Self: &ipnstate.PeerStatus{KeyExpiry: &deadline}})
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(b)))}, nil
		}))
		return &fakeTSNetServer{localClient: lc}
	}
	svc := registry.Service{Name: "app", Type: registry.TypeProxy, Target: "http://localhost:1234", Tags: []string{"tag:old"}, Health: &registry.HealthConfig{Interval: "1d"}}
	s.nodes["app"] = &ServiceNode{service: svc, tsnetSrv: server(oldDeadline)}
	r := health.NewRecorder(filepath.Join(dir, health.StateFile), health.NotifierConfig{})
	probe := func(context.Context, registry.Service) string { return "" }
	s.healthCycle(context.Background(), r, now, probe)
	oldSvc := svc
	svc.Tags = []string{"tag:new"}
	if !serviceChangedWithFallback(oldSvc, svc, "") {
		t.Fatal("fixture did not change runtime identity")
	}
	if healthIdentity(oldSvc) != healthIdentity(svc) {
		t.Fatal("fixture changed backend identity")
	}
	// This is the new ServiceNode installed by stop/start after an auth identity edit.
	s.nodes["app"] = &ServiceNode{service: svc, tsnetSrv: server(newDeadline)}
	s.healthCycle(context.Background(), r, now.Add(time.Minute), probe)
	snapshot, err := tsruntime.Load(filepath.Join(dir, "runtime.json"))
	if err != nil {
		t.Fatal(err)
	}
	key := snapshot.Services[0].NodeKey
	t.Logf("LocalAPI reads=%d, published expiry=%v warning=%q; new node expires=%v", reads, key.ExpiresAt, key.Warning, newDeadline)
	if reads != 2 || key.ExpiresAt == nil || !key.ExpiresAt.Equal(newDeadline) || key.Warning != "critical_3d" {
		t.Errorf("new node must report its own deadline and warning immediately")
	}
}

func TestNodeExpiryRefreshIndependentOfBackendInterval(t *testing.T) {
	s, dir := healthTestServer(t)
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	deadline := now.Add(30 * 24 * time.Hour)
	reads, probes := 0, 0
	lc := localapitest.NewClient(localapitest.RoundTripFunc(func(req *http.Request) (*http.Response, error) {
		reads++
		b, _ := json.Marshal(ipnstate.Status{Self: &ipnstate.PeerStatus{KeyExpiry: &deadline}})
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(b)))}, nil
	}))
	svc := registry.Service{Name: "app", Type: registry.TypeProxy, Target: "http://localhost:1234", Health: &registry.HealthConfig{Interval: "1d"}}
	s.nodes["app"] = &ServiceNode{service: svc, tsnetSrv: &fakeTSNetServer{localClient: lc}}
	r := health.NewRecorder(filepath.Join(dir, health.StateFile), health.NotifierConfig{})
	probe := func(context.Context, registry.Service) string { probes++; return "health_status_mismatch" }
	s.healthCycle(context.Background(), r, now, probe)
	deadline = now.Add(2 * 24 * time.Hour)
	s.healthCycle(context.Background(), r, now.Add(time.Minute), probe)
	key := s.healthStates["app"].NodeKey
	if reads != 2 || probes != 1 || key.Warning != "critical_3d" || !key.ExpiresAt.Equal(deadline) {
		t.Fatalf("node read coupled to backend: reads=%d probes=%d expiry=%+v", reads, probes, key)
	}
	// A replacement is unknown even before its first poll; app health survives.
	s.nodes["app"] = &ServiceNode{service: svc}
	s.writeRuntimeSnapshotLocked("health-fixture", true)
	snapshot, err := tsruntime.Load(filepath.Join(dir, "runtime.json"))
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Services[0].NodeKey.ExpiresAt != nil || snapshot.Services[0].NodeKey.State != health.Unknown || snapshot.Services[0].Health.ConsecutiveFailures != 1 {
		t.Fatal("replacement inherited expiry or lost backend streak", snapshot.Services[0])
	}
}
