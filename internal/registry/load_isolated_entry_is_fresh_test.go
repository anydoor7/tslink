package registry

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// leakRegistryJSON puts an entry that fails validation (a proxy with no
// target) before a valid one that sets many fields.
const leakRegistryJSON = `{"schema_version":1,"services":[
{"name":"docs","type":"proxy","created_at":"2026-05-17T12:00:00Z"},
{"name":"web","type":"proxy","target":"http://127.0.0.1:8080","tags":["tag:web"],"allowed_users":["alice@example.com"],"ephemeral":true,"control_url":"https://controlplane.tailscale.com","no_auto_provision":true,"created_at":"2026-05-18T12:00:00Z"}
]}`

// TestLoadGivesAnIsolatedEntryOnlyItsOwnFields is the B1 author's open risk
// 1: Load decoded the whole file a second time into the slice the valid
// services already filled, and encoding/json reuses slice elements, so the
// isolated docs entry inherited every field it omits from web.
func TestLoadGivesAnIsolatedEntryOnlyItsOwnFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.json")
	if err := os.WriteFile(path, []byte(leakRegistryJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	reg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]Service{}
	for _, svc := range reg.Services {
		byName[svc.Name] = svc
	}
	docs, web := byName["docs"], byName["web"]
	if docs.Name != "docs" || web.Name != "web" || len(reg.Services) != 2 {
		t.Fatalf("services = %+v, want docs and web", reg.Services)
	}
	if docs.Target != "" || len(docs.Tags) != 0 || len(docs.AllowedUsers) != 0 || docs.Ephemeral || docs.ControlURL != "" || docs.NoAutoProvision {
		t.Fatalf("isolated docs entry carries fields it never wrote: %+v", docs)
	}
	if want := time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC); !docs.CreatedAt.Equal(want) {
		t.Fatalf("docs created_at = %s, want its own", docs.CreatedAt)
	}
	// Control: the valid entry keeps everything it wrote.
	if web.Target != "http://127.0.0.1:8080" || len(web.Tags) != 1 || len(web.AllowedUsers) != 1 || !web.Ephemeral || web.ControlURL == "" || !web.NoAutoProvision {
		t.Fatalf("web lost fields: %+v", web)
	}
	if err := ValidateService(docs); err == nil {
		t.Fatal("docs validates; the fixture has no isolated entry")
	}
}
