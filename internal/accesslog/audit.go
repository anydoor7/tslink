package accesslog

import (
	"github.com/anydoor7/tslink/internal/mcpaudit"
	"strings"
)

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
	switch e.Surface {
	case "", "cli", "mcp", "scoped_mcp", "lifecycle", "guest":
	default:
		e.Surface = "unknown"
	}
	changes := make([]mcpaudit.Change, len(e.Changes))
	for i, c := range e.Changes {
		c.Action, c.App, c.ID, c.Subject = auditCode(c.Action), auditCode(c.App), auditCode(c.ID), safeText(c.Subject, 512)
		changes[i] = c
	}
	e.Changes = changes

	if e.Kind != "mcp" && e.Kind != "lifecycle" {
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

// Each intent and completion stays separate; equal IDs correlate the pair.
func fromReceipt(r mcpaudit.Entry) Event {
	// Development journals predate Kind. They contain MCP receipts only.
	if r.Kind == "" {
		r.Kind = "mcp"
	}
	status := "error"
	switch r.Result {
	case "ok", "started":
		status = "ok"
	case "denied", "mcp_scope_denied":
		status = "denied"
	}
	surface := r.Surface
	if surface == "" {
		surface = "mcp"
		if r.Role != "owner" {
			surface = "scoped_mcp"
		}
	}
	e := Event{Time: r.Time, Kind: r.Kind, Surface: surface, Changes: r.Changes,
		Identity: Identity{Login: r.Identity.Login, Node: r.Identity.Node}, MCP: &MCPAudit{
			ID: r.ID, Identity: &MCPIdentity{Login: r.Identity.Login, Node: r.Identity.Node},
			Principal: r.Principal, Role: r.Role, Capabilities: MCPCapabilities{
				Role: r.Capabilities.Role, Apps: r.Capabilities.Apps, Inventory: r.Capabilities.Inventory, MaxDuration: r.Capabilities.MaxDuration},
			ScopeExpiresAt: r.ScopeExpiresAt, Tool: r.Tool, Apps: r.Apps, Phase: r.Phase, Result: AuditResult{Status: status, Code: r.Result}}}
	if len(r.Apps) == 1 {
		e.App = r.Apps[0]
	}
	return sanitize(e)
}
