package accesslog

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestAuditRoundTrip(t *testing.T) {
	for _, raw := range []string{
		`{"kind":"mcp","mcp":{"id":"audit-1","principal":"alice@example.com","scope":"people-manager","capabilities":{"role":"people-manager","apps":["photos","docs"],"max_duration":"2h"},"scope_expires_at":"2030-07-11T12:00:00Z","tool":"people_grant","apps":["photos","docs"],"result":{"status":"ok","code":"ok"}}}`,
		`{"kind":"mcp","mcp":{"id":"audit-2","principal":"tag:agent","scope":"app-operator","capabilities":{"role":"app-operator","apps":["photos","docs"]},"tool":"remove","apps":["photos","docs"],"result":{"status":"denied","code":"mcp_scope_denied"}},"decision":"denied"}`,
		`{"kind":"mcp","mcp":{"id":"audit-3","principal":"local-user:operator","scope":"owner","capabilities":{"role":"owner"},"tool":"remove","apps":["photos","docs"],"result":{"status":"error","code":"not_found"}}}`,
		`{"kind":"mcp","mcp":{"identity":{"login":"","node":"agent-node"},"principal":"tag:agent","role":"app-operator","phase":"completion","id":"audit-4","capabilities":{"role":"app-operator","apps":["photos","docs"]},"tool":"remove","apps":["photos","docs"],"result":{"status":"ok","code":"ok"}}}`,
		`{"kind":"mcp","mcp":{"identity":{"login":"alice@example.com","node":"alice-node"},"principal":"alice@example.com","role":"people-manager","phase":"intent","id":"audit-5","capabilities":{"role":"people-manager","apps":["photos","docs"],"max_duration":"2h"},"scope_expires_at":"2030-07-11T12:00:00Z","tool":"people_grant","apps":["photos","docs"],"result":{"status":"denied","code":"denied"}}}`,
		`{"kind":"guest","app":"photos","guest":{"link_id":"link-1","app":"photos","decision":"allowed","reason":"active"}}`,
		`{"kind":"guest","app":"docs","guest":{"link_id":"link-2","app":"docs","decision":"denied","reason":"revoked"}}`,
	} {
		var e Event
		if err := json.Unmarshal([]byte(raw), &e); err != nil {
			t.Fatal(err)
		}
		dir := t.TempDir()
		s := newTestStore(t, dir, Options{}, func() time.Time { return testTime })
		if !s.Record(e) {
			t.Fatal("drop")
		}
		closeStore(t, s)
		result, err := Query(dir, Filter{})
		if err != nil || len(result.Events) != 1 {
			t.Fatalf("query %+v %v", result, err)
		}
		encoded, err := json.Marshal(result.Events[0])
		if err != nil {
			t.Fatal(err)
		}
		var want, got map[string]any
		json.Unmarshal([]byte(raw), &want)
		json.Unmarshal(encoded, &got)
		key := "mcp"
		if e.Kind == "guest" {
			key = "guest"
		}
		if !reflect.DeepEqual(got[key], want[key]) {
			t.Errorf("audit fields lost: got %s want %s", encoded, raw)
		}
	}
}

func TestAuditContractPrivacyAndOwnership(t *testing.T) {
	apps := []string{"photos", "docs", "photos"}
	m := &MCPAudit{Identity: &MCPIdentity{Login: "alice@example.com", Node: "agent-node"}, Role: "viewer", ID: "intent", Principal: "local-user:operator", Scope: "viewer", Capabilities: MCPCapabilities{Role: "viewer", Apps: apps, Inventory: true}, Tool: "access_log", Apps: apps, Phase: "started", Result: AuditResult{Status: "ok", Code: "started"}}
	dir := t.TempDir()
	s := newTestStore(t, dir, Options{}, func() time.Time { return testTime })
	if !s.Record(Event{Kind: "mcp", MCP: m, Guest: &GuestDecision{LinkID: "unrelated"}}) {
		t.Fatal("drop")
	}
	m.Apps[0] = "mutated"
	m.Capabilities.Role = "mutated"
	m.Result.Code = "mutated"
	m.Identity.Node = "mutated"
	closeStore(t, s)
	result, err := Query(dir, Filter{App: "photos", Who: "local-user:operator"})
	if err != nil || len(result.Events) != 1 {
		t.Fatal(result, err)
	}
	e := result.Events[0]
	if e.MCP.Identity.Node != "agent-node" || e.MCP.Role != "viewer" || e.MCP.Apps[0] != "photos" || e.MCP.Capabilities.Role != "viewer" || e.MCP.Result.Code != "started" || e.MCP.Phase != "started" || e.Guest != nil || len(result.Summary.Apps) != 2 || result.Summary.People[0].Key != "local-user:operator" {
		t.Fatal(result)
	}
	for _, count := range result.Summary.Apps {
		if count.Count != 1 {
			t.Fatal("duplicate app counted twice", count)
		}
	}
	// Unknown token input has no corresponding typed field and cannot survive.
	var guest Event
	if err := json.Unmarshal([]byte(`{"kind":"guest","token":"synthetic_token_sentinel","guest":{"link_id":"link-1","app":"photos","decision":"allowed","reason":"active","token":"synthetic_token_sentinel"}}`), &guest); err != nil {
		t.Fatal(err)
	}
	sanitized := sanitize(guest)
	raw, err := json.Marshal(sanitized)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "synthetic_token_sentinel") {
		t.Fatal("token entered typed contract")
	}
	invalid := sanitize(Event{Kind: "mcp", MCP: &MCPAudit{ID: strings.Repeat("x", 257), Tool: "remove?token=secret", Result: AuditResult{Status: "unknown", Code: "raw error secret"}, Phase: "unknown"}})
	if invalid.MCP.ID != "[redacted]" || invalid.MCP.Tool != "[redacted]" || invalid.MCP.Result.Status != "error" || invalid.MCP.Result.Code != "invalid_audit_result" || invalid.MCP.Phase != "" || invalid.Decision != "denied" {
		t.Fatal(invalid)
	}
	http := sanitize(Event{Kind: "http", MCP: m, Guest: &GuestDecision{}, Status: 403, Decision: "allowed", Reason: "arbitrary"})
	if http.MCP != nil || http.Guest != nil || http.Status != 403 || http.Decision != "allowed" || http.Reason != "" {
		t.Fatal("HTTP fields changed", http)
	}
	denied := sanitize(Event{Kind: "guest", Guest: &GuestDecision{LinkID: "link-2", App: "photos", Decision: "invalid", Reason: "revoked"}})
	if denied.Guest.Decision != "denied" || denied.Reason != "revoked" {
		t.Fatal(denied)
	}
	if auditCode("") != "" || auditCode("tskey-test") != "[redacted]" {
		t.Fatal("identifier sanitizer")
	}
}
