package accesslog

import "testing"

func TestMCPWhoIdentityAndExactTags(t *testing.T) {
	dir := t.TempDir()
	s := newTestStore(t, dir, Options{}, nil)
	s.Record(Event{Kind: "mcp", MCP: &MCPAudit{Principal: "tag:Agent", Identity: &MCPIdentity{Login: "alice@example.com", Node: "alice-laptop"}, Tool: "status", Result: AuditResult{Status: "ok", Code: "ok"}}})
	closeStore(t, s)
	for _, tc := range []struct {
		who   string
		count int
	}{{"tag:Agent", 1}, {"absent@example.com", 0}, {"ALICE@example.com", 1}, {"alice-laptop", 1}, {"tag:agent", 0}} {
		t.Run(tc.who, func(t *testing.T) {
			r, e := Query(dir, Filter{Who: tc.who})
			if e != nil || r.Summary.Count != tc.count {
				t.Fatalf("count=%d want=%d err=%v", r.Summary.Count, tc.count, e)
			}
		})
	}
}
