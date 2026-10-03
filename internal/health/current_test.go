package health

import (
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
}
