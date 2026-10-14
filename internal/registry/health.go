package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/anydoor7/tslink/internal/duration"
	"github.com/anydoor7/tslink/internal/errcode"
)

// CodeHealthOptionUnsupported refuses HTTP health options on file and TCP
// services, which accept only timeout and interval.
const CodeHealthOptionUnsupported = "health_option_unsupported"

// HealthConfig is additive service configuration. Zero values select defaults.
// BodyContains and Path are never included in diagnostic views.
type HealthConfig struct {
	Path         string `json:"path,omitempty"`
	StatusMin    int    `json:"status_min,omitempty"`
	StatusMax    int    `json:"status_max,omitempty"`
	BodyContains string `json:"body_contains,omitempty"`
	Timeout      string `json:"timeout,omitempty"`
	Interval     string `json:"interval,omitempty"`
}

// Effective fills defaults at run time; the registry is never rewritten. With
// no status bound set, any 2xx or 3xx answer is healthy: probes do not follow
// redirects, and a redirect proves the backend answers and routes. A single
// explicit bound keeps its pre-0.2.0 meaning, so the other defaults to 200 or
// 299 as before.
func (h HealthConfig) Effective() HealthConfig {
	if h.Path == "" {
		h.Path = "/"
	}
	if h.StatusMin == 0 && h.StatusMax == 0 {
		h.StatusMin, h.StatusMax = 200, 399
	}
	if h.StatusMin == 0 {
		h.StatusMin = 200
	}
	if h.StatusMax == 0 {
		h.StatusMax = 299
	}
	if h.Timeout == "" {
		h.Timeout = "5s"
	}
	if h.Interval == "" {
		h.Interval = "1m"
	}
	return h
}

func (h HealthConfig) Durations() (timeout, interval time.Duration) {
	h = h.Effective()
	timeout, _ = duration.Parse(h.Timeout)
	interval, _ = duration.Parse(h.Interval)
	return
}

func ValidateHealthConfig(kind string, h *HealthConfig) error {
	if h == nil {
		return nil
	}
	c := h.Effective()
	u, err := url.ParseRequestURI(c.Path)
	if err != nil || !strings.HasPrefix(c.Path, "/") || strings.HasPrefix(c.Path, "//") || strings.Contains(c.Path, "#") || u.Host != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return fmt.Errorf("health path must be an absolute request path without host, query or fragment")
	}
	if kind != TypeProxy && (h.Path != "" || h.StatusMin != 0 || h.StatusMax != 0 || h.BodyContains != "") {
		return fmt.Errorf("HTTP health options require a proxy service")
	}
	if c.StatusMin < 100 || c.StatusMax > 599 || c.StatusMin > c.StatusMax {
		return fmt.Errorf("health status range must be within 100..599")
	}
	if len(c.BodyContains) > 4096 {
		return fmt.Errorf("health body substring must be at most 4096 bytes")
	}
	timeout, err := duration.Parse(c.Timeout)
	if err != nil || timeout < 100*time.Millisecond || timeout > 30*time.Second {
		return fmt.Errorf("health timeout must be between 100ms and 30s")
	}
	interval, err := duration.Parse(c.Interval)
	if err != nil || interval < 10*time.Second || interval > 24*time.Hour || interval < timeout {
		return fmt.Errorf("health interval must be between 10s and 1d and at least the timeout")
	}
	return nil
}

// HealthUpdate names the health fields one change sets. A nil field keeps the
// stored value; an empty or zero value selects the default. Reset discards the
// stored settings before applying the named fields.
type HealthUpdate struct {
	Reset                bool
	Path, BodyContains   *string
	StatusMin, StatusMax *int
	Timeout, Interval    *string
}

func (u HealthUpdate) httpOptions() bool {
	return u.Path != nil || u.BodyContains != nil || u.StatusMin != nil || u.StatusMax != nil
}

// Apply returns the resulting stored settings. A block with no field set is
// stored as absent, so a reset service has no health block at all.
func (u HealthUpdate) Apply(current *HealthConfig) *HealthConfig {
	next := HealthConfig{}
	if current != nil && !u.Reset {
		next = *current
	}
	for _, f := range []struct {
		value *string
		field *string
	}{{u.Path, &next.Path}, {u.BodyContains, &next.BodyContains}, {u.Timeout, &next.Timeout}, {u.Interval, &next.Interval}} {
		if f.value != nil {
			*f.field = *f.value
		}
	}
	if u.StatusMin != nil {
		next.StatusMin = *u.StatusMin
	}
	if u.StatusMax != nil {
		next.StatusMax = *u.StatusMax
	}
	if next == (HealthConfig{}) {
		return nil
	}
	return &next
}

// SetHealthContext changes only one service's health settings in a single
// registry write transaction. Access, tags, people scope, routes and node
// identity are kept as stored. authorize sees the registry that will be saved.
// An unchanged result is not written. changed reports whether it was.
func SetHealthContext(ctx context.Context, path, app string, u HealthUpdate, authorize func(*Registry, Service) error) (svc Service, changed bool, err error) {
	err = withLockContext(mutationContext(ctx), path, func() error {
		reg, err := loadForMutation(path)
		if err != nil {
			return err
		}
		for i := range reg.Services {
			if reg.Services[i].Name != app {
				continue
			}
			current := reg.Services[i]
			if authorize != nil {
				if err := authorize(reg, current); err != nil {
					return err
				}
			}
			if current.Type != TypeProxy && u.httpOptions() {
				return CodedError{Code: CodeHealthOptionUnsupported, Message: fmt.Sprintf("health path, status range and body apply to proxy services; %s services accept only timeout and interval", current.Type)}
			}
			next := u.Apply(current.Health)
			if err := ValidateHealthConfig(current.Type, next); err != nil {
				return CodedError{Code: errcode.UsageError, Message: err.Error()}
			}
			before, _ := json.Marshal(current.Health)
			after, _ := json.Marshal(next)
			svc = current
			svc.Health = next
			if string(before) == string(after) {
				return nil
			}
			if err := ValidateService(svc); err != nil {
				return err
			}
			reg.Services[i] = svc
			changed = true
			return save(path, reg)
		}
		return CodedError{Code: errcode.NotFound, Message: "service not found: " + app}
	})
	return svc, changed, err
}
