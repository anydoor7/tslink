package mcpaudit

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func auditFixture() Entry {
	return Entry{ID: "call-1", Time: time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC), Principal: "agent", Role: "app-operator", Tool: "people_grant", Apps: []string{"photos"}, Result: "ok"}
}

func TestJournalDurableAndConcurrent(t *testing.T) {
	j := Journal{Path: filepath.Join(t.TempDir(), "audit.json")}
	if v, err := j.Read(); err != nil || len(v) != 0 {
		t.Fatal(v, err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := j.Record(context.Background(), auditFixture()); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	v, err := (Journal{Path: j.Path}).Read()
	if err != nil || len(v) != 24 || v[0].Principal != "agent" || !v[0].Time.Equal(auditFixture().Time) {
		t.Fatal(v, err)
	}
	info, _ := os.Stat(j.Path)
	if info.Mode().Perm()&0077 != 0 && os.PathSeparator != '\\' {
		t.Fatal("journal is not private")
	}
}

func TestJournalBounds(t *testing.T) {
	j := Journal{Path: filepath.Join(t.TempDir(), "audit.json")}
	seed := make([]Entry, 1024)
	for i := range seed {
		seed[i] = auditFixture()
		seed[i].ID = "old"
	}
	data, _ := json.Marshal(seed)
	if err := os.WriteFile(j.Path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := j.Record(context.Background(), auditFixture()); err != nil {
		t.Fatal(err)
	}
	v, err := j.Read()
	if err != nil || len(v) != 1024 || v[len(v)-1].ID != "call-1" {
		t.Fatal(len(v), err)
	}
	for i := range seed {
		seed[i].Principal = strings.Repeat("a", 900)
	}
	data, _ = json.Marshal(seed[:900])
	if len(data) > 1024*1024 {
		t.Fatal("invalid byte-bound control")
	}
	if err := os.WriteFile(j.Path, data, 0600); err != nil {
		t.Fatal(err)
	}
	e := auditFixture()
	e.Principal = strings.Repeat("x", 15000)
	for i := 0; i < 20; i++ {
		if err := j.Record(context.Background(), e); err != nil {
			t.Fatal(err)
		}
	}
	info, _ := os.Stat(j.Path)
	if info.Size() > 1024*1024 {
		t.Fatal("journal exceeded byte bound")
	}
	v, err = j.Read()
	if err != nil || len(v) >= 920 || v[len(v)-1].Principal != e.Principal {
		t.Fatal(len(v), err)
	}
}

func TestJournalFailurePathsPreserveState(t *testing.T) {
	for _, value := range []string{"", "[", "null", "null trailing", strings.Repeat("x", 1024*1024+1)} {
		t.Run("partial-or-oversized", func(t *testing.T) {
			j := Journal{Path: filepath.Join(t.TempDir(), "audit.json")}
			if err := os.WriteFile(j.Path, []byte(value), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := j.Read(); err == nil {
				t.Fatal("accepted corrupt state")
			}
			if err := j.Record(context.Background(), auditFixture()); err == nil {
				t.Fatal("replaced corrupt state")
			}
			got, _ := os.ReadFile(j.Path)
			if string(got) != value {
				t.Fatal("corrupt state changed")
			}
		})
	}
	j := Journal{Path: filepath.Join(t.TempDir(), "audit.json")}
	if err := os.Mkdir(j.Path, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := j.Read(); err == nil {
		t.Fatal("read directory")
	}
	if err := j.Record(context.Background(), auditFixture()); err == nil {
		t.Fatal("replaced directory")
	}
	j = Journal{Path: filepath.Join(t.TempDir(), "parent", "audit.json")}
	if err := os.WriteFile(filepath.Dir(j.Path), []byte("file"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := j.Record(context.Background(), auditFixture()); err == nil {
		t.Fatal("created under file")
	}
	if _, err := j.Read(); err == nil {
		t.Fatal("read under file")
	}
	j = Journal{Path: filepath.Join(t.TempDir(), "audit.json")}
	e := auditFixture()
	e.Principal = strings.Repeat("x", 20000)
	if err := j.Record(context.Background(), e); err == nil {
		t.Fatal("accepted oversized entry")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := j.Record(ctx, auditFixture()); err != context.Canceled {
		t.Fatal(err)
	}
	seed := make([]Entry, 1025)
	data, _ := json.Marshal(seed)
	if err := os.WriteFile(j.Path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := j.Read(); err == nil {
		t.Fatal("accepted oversized entry count")
	}
}

func TestReview2LegacyAuditAttributionSurvivesAppend(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal.json")
	prior := `[{"id":"old","time":"2030-01-01T00:00:00Z","who":"alice@example.com","scope":"app-operator","capabilities":{"role":"app-operator","apps":["photos"]},"tool":"app_restart","apps":["photos"],"result":"ok"}]`
	if err := os.WriteFile(path, []byte(prior), 0600); err != nil {
		t.Fatal(err)
	}
	j := Journal{Path: path}
	entries, err := j.Read()
	if err != nil || len(entries) != 1 || entries[0].Principal != "alice@example.com" || entries[0].Role != "app-operator" || entries[0].Identity.Login != "" || entries[0].Identity.Node != "" {
		t.Fatalf("legacy attribution on read: %+v %v", entries, err)
	}
	if err := j.Record(context.Background(), Entry{Kind: "mcp", ID: "new", Principal: "bob@example.com", Role: "owner", Tool: "app_restart", Apps: []string{"photos"}, Result: "ok"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("rewritten history: %s", data)
	if !strings.Contains(string(data), "alice@example.com") {
		t.Fatal("appending new receipt erased the old authenticated principal")
	}
}

func TestLegacyAuditDecodePrefersCurrentAttribution(t *testing.T) {
	for _, tc := range []struct{ data, principal, role string }{
		{`{"who":"legacy","scope":"app-operator"}`, "legacy", "app-operator"},
		{`{"principal":"current","role":"owner","who":"legacy","scope":"app-operator"}`, "current", "owner"},
		{`{"principal":"current","scope":"app-operator"}`, "current", "app-operator"},
		{`{"who":"legacy","role":"owner"}`, "legacy", "owner"},
	} {
		var entry Entry
		if err := json.Unmarshal([]byte(tc.data), &entry); err != nil || entry.Principal != tc.principal || entry.Role != tc.role || entry.Identity.Login != "" || entry.Identity.Node != "" {
			t.Fatalf("decode %s: %+v %v", tc.data, entry, err)
		}
	}
	var entry Entry
	if err := json.Unmarshal([]byte(`{"time":"invalid"}`), &entry); err == nil {
		t.Fatal("invalid current entry accepted")
	}
}
