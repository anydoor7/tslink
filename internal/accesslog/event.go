// Package accesslog provides local, bounded access history. Its writer accepts
// typed metadata only; it has no field for request headers, bodies or URLs.
package accesslog

import (
	"fmt"
	"net/netip"
	"net/url"
	"strings"
	"time"
	"unicode"
)

const SchemaVersion = 1

// Writer is the shared seam for HTTP/TCP, MCP audit and guest decisions.
// Record must return immediately; false means the event was dropped.
type Writer interface{ Record(Event) bool }

type Identity struct {
	Login  string   `json:"login"`
	Node   string   `json:"node"`
	Tags   []string `json:"tags"`
	Remote string   `json:"remote,omitempty"`
}
type Grant struct {
	Kind  string `json:"kind"` // person or legacy_allow
	Entry string `json:"entry"`
}
type Event struct {
	SchemaVersion int       `json:"schema_version"`
	Time          time.Time `json:"time"`
	Kind          string    `json:"kind"` // http, tcp_open, tcp_close, mcp, guest
	App           string    `json:"app"`
	Identity      Identity  `json:"identity"`
	Method        string    `json:"method,omitempty"`
	Path          string    `json:"path,omitempty"`
	Status        int       `json:"status"`
	BytesIn       int64     `json:"bytes_in"`
	BytesOut      int64     `json:"bytes_out"`
	DurationMS    float64   `json:"duration_ms"`
	Decision      string    `json:"decision"`
	Reason        string    `json:"reason,omitempty"`
	Grant         *Grant    `json:"grant,omitempty"`
	Connection    string    `json:"connection,omitempty"`
}

type Options struct {
	Enabled       *bool `json:"enabled,omitempty"`
	RecordPath    *bool `json:"record_path,omitempty"`
	RetentionDays int   `json:"retention_days,omitempty"`
	MaxBytes      int64 `json:"max_bytes,omitempty"`
	QueueSize     int   `json:"queue_size,omitempty"`
}

func (o Options) Validate() error {
	if o.RetentionDays < 0 || o.RetentionDays > 3650 {
		return fmt.Errorf("access_log.retention_days must be 1..3650 (0 uses default)")
	}
	if o.MaxBytes != 0 && (o.MaxBytes < 65536 || o.MaxBytes > 1<<30) {
		return fmt.Errorf("access_log.max_bytes must be 65536..1073741824")
	}
	if o.QueueSize < 0 || o.QueueSize > 65536 {
		return fmt.Errorf("access_log.queue_size must be 1..65536 (0 uses default)")
	}
	return nil
}
func (o Options) Defaults() Options {
	if o.RetentionDays == 0 {
		o.RetentionDays = 30
	}
	if o.MaxBytes == 0 {
		o.MaxBytes = 64 << 20
	}
	if o.QueueSize == 0 {
		o.QueueSize = 1024
	}
	return o
}
func (o Options) IsEnabled() bool { return o.Enabled == nil || *o.Enabled }
func (o Options) PathsEnabled(service *bool) bool {
	return (o.RecordPath == nil || *o.RecordPath) && (service == nil || *service)
}

// CoarseRemote retains a /24 IPv4 or /48 IPv6 prefix, never a port.
func CoarseRemote(remote string) string {
	if prefix, err := netip.ParsePrefix(remote); err == nil {
		remote = prefix.Addr().String()
	}
	addr, err := netip.ParseAddrPort(remote)
	var ip netip.Addr
	if err == nil {
		ip = addr.Addr()
	} else {
		ip, _ = netip.ParseAddr(remote)
	}
	if !ip.IsValid() {
		return ""
	}
	ip = ip.Unmap()
	bits := 48
	if ip.Is4() {
		bits = 24
	}
	return netip.PrefixFrom(ip, bits).Masked().String()
}
func safeText(s string, max int) string {
	if len(s) > max {
		return "[redacted]"
	}
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	if strings.Contains(s, "://") || strings.Contains(s, "tskey-") || strings.Contains(s, "Bearer ") {
		return "[redacted]"
	}
	return s
}

// SafePath accepts only a path, strips query/fragment even for non-HTTP writers,
// and removes known bearer routes and credential-shaped path segments.
func SafePath(p string) string {
	if len(p) > 8192 {
		return "/[redacted]"
	}
	p = strings.SplitN(strings.SplitN(p, "?", 2)[0], "#", 2)[0]
	if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") {
		return ""
	}
	parts := strings.Split(p, "/")
	redactRest := false
	for i, part := range parts {
		decoded, _ := url.PathUnescape(part)
		if redactRest || len(part) >= 32 || strings.Contains(decoded, "tskey-") || strings.Contains(decoded, "://") || strings.Contains(decoded, "Bearer ") {
			parts[i] = "[redacted]"
		}
		switch strings.ToLower(decoded) {
		case "guest", "invite", "token", "auth", "link":
			redactRest = true
		}
	}
	return safeText(strings.Join(parts, "/"), 2048)
}
func sanitize(e Event) Event {
	e.SchemaVersion = SchemaVersion
	e.Time = e.Time.UTC()
	switch e.Kind {
	case "http", "tcp_open", "tcp_close", "mcp", "guest":
	default:
		e.Kind = "unknown"
	}
	e.App = safeText(e.App, 63)
	e.Identity.Login = safeText(e.Identity.Login, 256)
	e.Identity.Node = safeText(e.Identity.Node, 256)
	tags := []string{}
	for _, tag := range e.Identity.Tags {
		if len(tags) == 32 {
			break
		}
		tags = append(tags, safeText(tag, 256))
	}
	e.Identity.Tags = tags
	if e.Identity.Login != "" || e.Identity.Node != "" || len(tags) > 0 {
		e.Identity.Remote = ""
	} else {
		e.Identity.Remote = CoarseRemote(e.Identity.Remote)
	}
	e.Method = safeText(e.Method, 32)
	e.Path = SafePath(e.Path)
	if e.Decision != "denied" {
		e.Decision = "allowed"
		e.Reason = ""
	}
	switch e.Reason {
	case "", "acl", "people", "expired", "limits", "preserve_host_unavailable", "backend_unavailable":
	default:
		e.Reason = "unavailable"
	}
	if e.Grant != nil {
		e.Grant = &Grant{Kind: safeText(e.Grant.Kind, 32), Entry: safeText(e.Grant.Entry, 256)}
	}
	e.Connection = safeText(e.Connection, 64)
	return e
}
