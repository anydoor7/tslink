package accesslog

import "strings"

// Audit identifiers/codes are bounded typed metadata, never free-form errors.
func auditCode(s string) string {
	if s == "" {
		return ""
	}
	if len(s) > 256 {
		return "[redacted]"
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("_-.:@", r)) {
			return "[redacted]"
		}
	}
	return safeText(s, 256)
}
func auditApps(apps []string) []string {
	out := make([]string, len(apps))
	for i, app := range apps {
		out[i] = auditCode(app)
	}
	return out
}
func sanitizeAudit(e Event) Event {
	if e.Kind != "mcp" {
		e.MCP = nil
	}
	if e.Kind != "guest" {
		e.Guest = nil
	}
	if e.MCP != nil {
		m := *e.MCP
		m.ID = auditCode(m.ID)
		m.Principal = safeText(m.Principal, 512)
		m.Scope = auditCode(m.Scope)
		m.Role = auditCode(m.Role)
		if m.Identity != nil {
			id := *m.Identity
			id.Login = safeText(id.Login, 512)
			id.Node = safeText(id.Node, 256)
			m.Identity = &id
		}
		m.Tool = auditCode(m.Tool)
		m.Apps = auditApps(m.Apps)
		m.Capabilities.Apps = auditApps(m.Capabilities.Apps)
		m.Capabilities.Role = auditCode(m.Capabilities.Role)
		m.Capabilities.MaxDuration = auditCode(m.Capabilities.MaxDuration)
		if m.ScopeExpiresAt != nil {
			at := m.ScopeExpiresAt.UTC()
			m.ScopeExpiresAt = &at
		}
		m.Result.Code = auditCode(m.Result.Code)
		switch m.Result.Status {
		case "ok", "denied", "error":
		default:
			m.Result.Status = "error"
			m.Result.Code = "invalid_audit_result"
		}
		if m.Phase != "started" && m.Phase != "intent" && m.Phase != "completion" {
			m.Phase = ""
		}
		if m.Result.Status != "ok" {
			e.Decision = "denied"
		}
		e.MCP = &m
	}
	if e.Guest != nil {
		g := *e.Guest
		g.LinkID = auditCode(g.LinkID)
		g.App = auditCode(g.App)
		g.Reason = auditCode(g.Reason)
		if g.Decision != "allowed" {
			g.Decision = "denied"
		}
		e.App = g.App
		e.Decision = g.Decision
		e.Reason = g.Reason
		e.Guest = &g
	}
	return e
}
