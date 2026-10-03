package accesslog

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMutationJournalLegacyQuery(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp-audit.json")
	legacy := `[{"id":"old","time":"2030-01-01T00:00:00Z","who":"alice@example.com","scope":"app-operator","capabilities":{"role":"app-operator","apps":["photos","finance"]},"tool":"app_restart","apps":["photos"],"result":"ok"}]`
	if err := os.WriteFile(path, []byte(legacy), 0600); err != nil {
		t.Fatal(err)
	}
	r, err := Query(dir, Filter{AllowedApps: []string{"photos"}, Who: "ALICE@example.com"})
	if err != nil || len(r.Events) != 1 || r.Summary.Count != 1 {
		t.Fatal("legacy receipt missing", r, err)
	}
	m := r.Events[0].MCP
	if m == nil || m.Principal != "alice@example.com" || m.Role != "app-operator" || m.Identity.Login != "" || len(m.Capabilities.Apps) != 1 || m.Capabilities.Apps[0] != "photos" {
		t.Fatalf("legacy mapping %+v", m)
	}
	if r.Events[0].Surface != "scoped_mcp" || r.Events[0].Kind != "mcp" {
		t.Fatal(r.Events[0])
	}
	b, err := os.ReadFile(path)
	if err != nil || string(b) != legacy {
		t.Fatal("query rewrote historical evidence", err)
	}
}

func TestMutationJournalDeniedQuery(t *testing.T) {
	dir := t.TempDir()
	for _, code := range []string{"denied", "mcp_scope_denied"} {
		t.Run(code, func(t *testing.T) {
			entry := `[{"id":"denial","time":"2030-01-01T00:00:00Z","principal":"alice@example.com","role":"owner","tool":"requests_approve","apps":["photos"],"result":"` + code + `"}]`
			if err := os.WriteFile(filepath.Join(dir, "mcp-audit.json"), []byte(entry), 0o600); err != nil {
				t.Fatal(err)
			}
			r, err := Query(dir, Filter{Decision: "denied"})
			if err != nil || len(r.Events) != 1 || r.Summary.Denied != 1 {
				t.Fatalf("denial query: %+v %v", r, err)
			}
			e := r.Events[0]
			if e.Surface != "mcp" || e.MCP == nil || e.MCP.Result.Status != "denied" || e.MCP.Result.Code != code {
				t.Fatalf("denial mapping: %+v", e)
			}
		})
	}
}
