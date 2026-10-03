package health

import (
	"github.com/anydoor7/tslink/internal/registry"
	"time"
)

// CurrentAt shares the F2 freshness projection across status and portal.
func CurrentAt(h State, svc registry.Service, now time.Time) State {
	cfg := registry.HealthConfig{}
	if svc.Health != nil {
		cfg = *svc.Health
	}
	timeout, interval := cfg.Durations()
	if h.LastChecked == nil {
		return Unchecked(svc.Type)
	}
	if now.Sub(*h.LastChecked) > 2*interval+timeout {
		h.State = Unknown
		h.LastError = "health_observation_stale"
	}
	return h
}
