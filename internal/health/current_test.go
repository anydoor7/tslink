package health

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/registry"
)

func TestCurrentAtFreshness(t *testing.T) {
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	svc := registry.Service{Type: registry.TypeProxy}
	if h := CurrentAt(State{State: Healthy}, svc, now); h.State != Unknown || h.LastChecked != nil {
		t.Fatalf("unchecked=%+v", h)
	}
	for _, state := range []string{Healthy, Degraded, Down} {
		checked := now.Add(-125 * time.Second)
		h := CurrentAt(State{State: state, LastChecked: &checked}, svc, now)
		if h.State != state {
			t.Fatal("observation expired early")
		}
		h = CurrentAt(h, svc, now.Add(time.Nanosecond))
		if h.State != Unknown || h.LastError != "health_observation_stale" {
			t.Fatalf("stale=%+v", h)
		}
	}
	svc.Health = &registry.HealthConfig{Interval: "10s", Timeout: "1s"}
	checked := now.Add(-22 * time.Second)
	if h := CurrentAt(State{State: Healthy, LastChecked: &checked}, svc, now); h.State != Unknown {
		t.Fatalf("configured interval=%+v", h)
	}
	disabledSvc := registry.Service{Type: registry.TypeTCP, Health: &registry.HealthConfig{Disabled: true}}
	if h := CurrentAt(State{State: Down, LastChecked: &checked}, disabledSvc, now.Add(24*time.Hour)); h.State != Disabled || h.LastError != "" || h.LastChecked != nil {
		t.Fatalf("disabled CurrentAt=%+v, want disabled without stale error", h)
	}
}

func TestProbeSkipsDisabledWithoutDialing(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	var accepted atomic.Int32
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			accepted.Add(1)
			_ = conn.Close()
		}
	}()
	svc := registry.Service{
		Name:   "amber-link",
		Type:   registry.TypeTCP,
		Target: ln.Addr().String(),
		Health: &registry.HealthConfig{Disabled: true},
	}
	if code := Probe(context.Background(), svc); code != "health_probe_disabled" {
		t.Fatalf("Probe(disabled) = %q, want health_probe_disabled", code)
	}
	time.Sleep(20 * time.Millisecond)
	if got := accepted.Load(); got != 0 {
		t.Fatalf("disabled Probe dialed backend %d time(s), want 0", got)
	}
}
