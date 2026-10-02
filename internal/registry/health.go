package registry

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/anydoor7/tslink/internal/duration"
)

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

func (h HealthConfig) Effective() HealthConfig {
	if h.Path == "" {
		h.Path = "/"
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
