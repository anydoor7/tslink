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

// MCPCapabilities mirrors F6's mcpscope.Scope without importing its authority.
type MCPCapabilities struct {
	Role        string   `json:"role"`
	Apps        []string `json:"apps,omitempty"`
	Inventory   bool     `json:"inventory,omitempty"`
	MaxDuration string   `json:"max_duration,omitempty"`
}
type AuditResult struct {
	Status string `json:"status"` // ok, denied, error
	Code   string `json:"code"`   // stable code, never a raw error message
}
type MCPIdentity struct {
	Login string `json:"login"`
	Node  string `json:"node"`
}
type MCPAudit struct {
	Identity       *MCPIdentity    `json:"identity,omitempty"`
	Role           string          `json:"role,omitempty"`
	ID             string          `json:"id"`
	Principal      string          `json:"principal"` // F6 Principal (legacy Who): login, tag or local OS user
	Scope          string          `json:"scope,omitempty"`
	Capabilities   MCPCapabilities `json:"capabilities"`
	ScopeExpiresAt *time.Time      `json:"scope_expires_at,omitempty"`
	Tool           string          `json:"tool"`
	Apps           []string        `json:"apps"`
	Result         AuditResult     `json:"result"`
	Phase          string          `json:"phase,omitempty"` // intent/completion; started supports the older F6 journal
}

// GuestDecision contains the non-secret ledger ID, never the bearer token.
// Reason must be a stable code rather than request data or a raw error.
type GuestDecision struct {
	LinkID   string `json:"link_id"`
	App      string `json:"app"`
	Decision string `json:"decision"`
	Reason   string `json:"reason"`
}
type Event struct {
	SchemaVersion int            `json:"schema_version"`
	Time          time.Time      `json:"time"`
	Kind          string         `json:"kind"` // http, tcp_open, tcp_close, mcp, guest
	App           string         `json:"app"`
	Identity      Identity       `json:"identity"`
	Method        string         `json:"method,omitempty"`
	Path          string         `json:"path,omitempty"`
	Status        int            `json:"status"`
	BytesIn       int64          `json:"bytes_in"`
	BytesOut      int64          `json:"bytes_out"`
	DurationMS    float64        `json:"duration_ms"`
	Decision      string         `json:"decision"`
	Reason        string         `json:"reason,omitempty"`
	Grant         *Grant         `json:"grant,omitempty"`
	Connection    string         `json:"connection,omitempty"`
	MCP           *MCPAudit      `json:"mcp,omitempty"`
	Guest         *GuestDecision `json:"guest,omitempty"`
	PathMode      string         `json:"-"` // selected per-service policy; not HTTP metadata
}

type Options struct {
	Enabled       *bool  `json:"enabled,omitempty"`
	RecordPath    *bool  `json:"record_path,omitempty"`
	PathMode      string `json:"path_mode,omitempty"`
	RetentionDays int    `json:"retention_days,omitempty"`
	MaxBytes      int64  `json:"max_bytes,omitempty"`
	QueueSize     int    `json:"queue_size,omitempty"`
}

func (o Options) Validate() error {
	if err := ValidatePathMode(o.PathMode); err != nil {
		return err
	}
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
	return o.ModeFor("", service) != "off"
}

func ValidatePathMode(mode string) error {
	switch mode {
	case "", "prefix", "full", "off":
		return nil
	}
	return fmt.Errorf("access log path mode must be prefix, full or off")
}

// Legacy true inherits the safe default; false remains a hard opt-out.
// A service may opt into full unless global recording is explicitly off.
func (o Options) ModeFor(mode string, legacy *bool) string {
	if o.PathMode == "off" || o.RecordPath != nil && !*o.RecordPath || legacy != nil && !*legacy {
		return "off"
	}
	if mode == "" {
		mode = o.PathMode
	}
	if mode == "full" || mode == "off" {
		return mode
	}
	return "prefix"
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

// SafePath is the full-mode sanitizer. Decode before splitting so escaped
// separators have the same conservative meaning as literal separators. Nested
// escaping is bounded; malformed or deeply escaped input is redacted entirely.
func SafePath(p string) string {
	return PathForMode(p, "full")
}
func PathForMode(p, mode string) string {
	if mode == "off" {
		return ""
	}
	if len(p) > 8192 {
		return "/[redacted]"
	}
	p = strings.SplitN(strings.SplitN(p, "?", 2)[0], "#", 2)[0]
	if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") {
		return ""
	}
	for i := 0; strings.Contains(p, "%"); i++ {
		if i == 8 {
			return "/[redacted]"
		}
		decoded, err := url.PathUnescape(p)
		if err != nil {
			return "/[redacted]"
		}
		p = decoded
	}
	p = strings.ReplaceAll(p, "\\", "/")
	p = strings.SplitN(strings.SplitN(p, "?", 2)[0], "#", 2)[0]
	parts := strings.FieldsFunc(p, func(r rune) bool { return r == '/' })
	if len(parts) == 0 {
		return "/"
	}
	if mode != "full" {
		parts = parts[:1]
	}
	redactRest := false
	for i, part := range parts {
		if redactRest || tokenSegment(part) || safeText(part, 256) == "[redacted]" {
			parts[i] = "[redacted]"
		}
		switch strings.ToLower(part) {
		case "guest", "guests", "invite", "invites", "token", "auth", "link", "links", "g":
			redactRest = true
		}
	}
	return safeText("/"+strings.Join(parts, "/"), 2048)
}
func tokenSegment(s string) bool {
	if len(s) >= 32 {
		return true
	}
	var lower, upper, digit bool
	for _, r := range s {
		lower = lower || r >= 'a' && r <= 'z'
		upper = upper || r >= 'A' && r <= 'Z'
		digit = digit || r >= '0' && r <= '9'
	}
	return len(s) >= 12 && digit && (lower || upper) || len(s) >= 20 && lower && upper
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
	e = sanitizeAudit(e)
	return e
}
