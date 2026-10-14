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
	"sync"
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
	// StatusCode is the HTTP status the backend answered. It is absent when no
	// response arrived; response bodies and redirect targets are never kept.
	StatusCode int `json:"status_code,omitempty"`
}

func Unchecked(kind string) State { return State{State: Unknown, Kind: kind} }

type canonicalHostKey struct{}
type responseStatusKey struct{}

// ResponseStatus receives the HTTP status of a probe's backend response. Only
// a status observed before the probe's deadline counts: a timed-out read can
// still finish after its caller moved on, and its late status is dropped.
type ResponseStatus struct {
	mu     sync.Mutex
	code   int
	frozen bool
}

// Code returns the status observed so far, or 0 when no response arrived.
func (s *ResponseStatus) Code() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.code
}

// Snapshot returns the observed status and ignores every later observation.
// A caller takes it once the probe has returned or its deadline has passed.
func (s *ResponseStatus) Snapshot() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.frozen = true
	return s.code
}

// observe records code unless the probe context has already ended or the
// caller has taken its snapshot.
func (s *ResponseStatus) observe(ctx context.Context, code int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.frozen && ctx.Err() == nil {
		s.code = code
	}
}

// WithResponseStatus lets a caller learn what the backend answered while Probe
// keeps returning only a stable error code.
func WithResponseStatus(ctx context.Context) (context.Context, *ResponseStatus) {
	s := &ResponseStatus{}
	return context.WithValue(ctx, responseStatusKey{}, s), s
}

// WithCanonicalHost supplies trusted receiving-node metadata for a backend
// probe. Preserve-host services refuse a missing or invalid name. The scheduler
// and doctor obtain it from the node or verified runtime evidence, never callers.
func WithCanonicalHost(ctx context.Context, host string) context.Context {
	return context.WithValue(ctx, canonicalHostKey{}, host)
}

// Probe returns only stable error codes. No URL, path, transport error or body
// is returned, including on malformed configuration or a response read failure.
type Dialer interface {
	DialContext(context.Context, string, string) (net.Conn, error)
}

func Probe(ctx context.Context, svc registry.Service) string { return ProbeWithDialer(ctx, svc, nil) }

// ProbeWithDialer uses the supplied gateway transport for remote probes. A
// remote service without it is refused before DNS or host-network I/O.
func ProbeWithDialer(ctx context.Context, svc registry.Service, dialer Dialer) string {
	if svc.Upstream == "tailnet" && dialer == nil {
		return "remote_upstream_unavailable"
	}
	if registry.ValidateHealthConfig(svc.Type, svc.Health) != nil {
		return "health_config_invalid"
	}
	if !TargetSafe(svc) {
		return "health_target_invalid"
	}
	if svc.RequestLimits != nil && svc.Type == registry.TypeTCP {
		return "health_request_limits_invalid"
	}
	if _, err := registry.ResolveRequestLimits(svc.RequestLimits); err != nil {
		return "health_request_limits_invalid"
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
		if svc.PreserveHost {
			host, _ := ctx.Value(canonicalHostKey{}).(string)
			if svc.PortalRoute != nil {
				name, port, err := net.SplitHostPort(host)
				if err != nil || port == "" || registry.CanonicalProxyHost(nil, name) == "" {
					host = ""
				}
			} else {
				host = registry.CanonicalProxyHost(nil, host)
			}
			if host == "" {
				return "health_canonical_host_unavailable"
			}
			out.Host = host
			out.Header.Set("X-Forwarded-Host", host)
			out.Header.Set("X-Forwarded-Proto", "https")
		}
		// This bodyless GET fits every valid request-body limit. F8's read,
		// header and idle limits bound incoming client data, not this backend's
		// response; health.timeout and the 64 KiB response cap still apply.
		transport := &http.Transport{DisableCompression: true}
		if dialer != nil {
			transport.DialContext = dialer.DialContext
		}
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
		if s, ok := ctx.Value(responseStatusKey{}).(*ResponseStatus); ok {
			s.observe(ctx, resp.StatusCode)
		}
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

// UnexpectedResponse reports a check that reached the backend and got an
// answer the health settings do not accept. The backend is up; the check, or
// the app behind it, needs attention. Every other failure, including a body
// that could not be read after the headers, means the backend is unreachable.
func UnexpectedResponse(code string) bool {
	return code == "health_status_mismatch" || code == "health_body_mismatch"
}

// Outcome is one attempted check: Error is a stable error code, empty on
// success; StatusCode is the HTTP status the backend answered, 0 when no
// response arrived.
type Outcome struct {
	Error      string
	StatusCode int
}

func Result(previous State, kind, errorCode string, now time.Time) State {
	return ResultOf(previous, kind, Outcome{Error: errorCode}, now)
}

// ResultOf derives the next state. Only unreachable checks escalate: three in
// a row are down. An unexpected response is at most degraded, however long it
// lasts. consecutive_failures counts the current run of one failure class, so
// a long mismatch run never makes one connection failure jump to down.
func ResultOf(previous State, kind string, o Outcome, now time.Time) State {
	now = now.UTC()
	r := State{State: Healthy, Kind: kind, LastChecked: &now}
	// A timeout keeps a status whose headers arrived before the deadline, as
	// when a body stalls; ResponseStatus already dropped later ones.
	if o.StatusCode >= 100 && o.StatusCode <= 999 {
		r.StatusCode = o.StatusCode
	}
	if o.Error == "" {
		return r
	}
	r.LastError = o.Error
	r.ConsecutiveFailures = previous.ConsecutiveFailures + 1
	if previous.LastError != "" && UnexpectedResponse(previous.LastError) != UnexpectedResponse(o.Error) {
		r.ConsecutiveFailures = 1
	}
	r.State = Degraded
	if !UnexpectedResponse(o.Error) && r.ConsecutiveFailures >= 3 {
		r.State = Down
	}
	return r
}
