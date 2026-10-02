// Package health provides bounded, value-free app probes and durable alerts.
package health

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/anydoor7/tslink/internal/registry"
)

const (
	Healthy  = "healthy"
	Degraded = "degraded"
	Down     = "down"
	Unknown  = "unknown"
)

type State struct {
	State               string     `json:"state"`
	Kind                string     `json:"kind"`
	LastChecked         *time.Time `json:"last_checked,omitempty"`
	LastError           string     `json:"last_error,omitempty"`
	ConsecutiveFailures int        `json:"consecutive_failures"`
}

func Unchecked(kind string) State { return State{State: Unknown, Kind: kind} }

// Probe returns only stable error codes. No URL, path, transport error or body
// is returned, including on malformed configuration or a response read failure.
func Probe(ctx context.Context, svc registry.Service) string {
	if registry.ValidateHealthConfig(svc.Type, svc.Health) != nil {
		return "health_config_invalid"
	}
	if !TargetSafe(svc) {
		return "health_target_invalid"
	}
	c := registry.HealthConfig{}
	if svc.Health != nil {
		c = *svc.Health
	}
	c = c.Effective()
	timeout, _ := c.Durations()
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	switch svc.Type {
	case registry.TypeProxy:
		target, err := url.Parse(svc.Target)
		if err != nil || target.Host == "" || (target.Scheme != "http" && target.Scheme != "https") {
			return "health_target_invalid"
		}
		// ValidateHealthConfig already proved this is a request URI.
		probeURL, _ := url.ParseRequestURI(c.Path)
		in := (&http.Request{Method: http.MethodGet, URL: probeURL, Header: make(http.Header)}).WithContext(ctx)
		out := in.Clone(ctx)
		// Use the exact path/query join that the production reverse proxy uses.
		pr := httputil.ProxyRequest{In: in, Out: out}
		pr.SetURL(target)
		out.URL.User = nil // ReverseProxy does not synthesize Basic Auth.
		transport := &http.Transport{DisableCompression: true}
		defer transport.CloseIdleConnections()
		client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		resp, err := client.Do(out)
		if err != nil {
			if ctx.Err() != nil {
				return "health_timeout"
			}
			return "health_request_failed"
		}
		defer resp.Body.Close()
		if resp.StatusCode < c.StatusMin || resp.StatusCode > c.StatusMax {
			return "health_status_mismatch"
		}
		if c.BodyContains != "" {
			body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
			if err != nil {
				return "health_body_read_failed"
			}
			if !strings.Contains(string(body), c.BodyContains) {
				return "health_body_mismatch"
			}
		}
	case registry.TypeTCP:
		conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", svc.Target)
		if err != nil {
			return "health_tcp_failed"
		}
		_ = conn.Close()
	case registry.TypeFile:
		path := svc.Path
		if svc.File != "" {
			path = filepath.Join(path, svc.File)
		}
		return probeFile(ctx, path, svc.File == "")
	default:
		return "health_target_invalid"
	}
	return ""
}

// TargetSafe shares the registry's outbound refusal boundary. It performs no
// network or filesystem I/O, so the scheduler can reject targets before work.
func TargetSafe(svc registry.Service) bool {
	switch svc.Type {
	case registry.TypeProxy:
		return registry.ValidateProxyTarget(svc.Target) == nil
	case registry.TypeTCP:
		return registry.ValidateTCPTarget(svc.Target) == nil
	case registry.TypeFile:
		return true
	default:
		return false
	}
}

func Result(previous State, kind, errorCode string, now time.Time) State {
	now = now.UTC()
	r := State{State: Healthy, Kind: kind, LastChecked: &now}
	if errorCode != "" {
		r.LastError = errorCode
		r.ConsecutiveFailures = previous.ConsecutiveFailures + 1
		r.State = Degraded
		if r.ConsecutiveFailures >= 3 {
			r.State = Down
		}
	}
	return r
}
