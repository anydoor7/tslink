package credentials

import (
	"os"
	"strings"
	"testing"
)

// When the keyring and the legacy file disagree, migration keeps both and
// must say so: the operator otherwise never learns the file was kept.
func TestMigrateFromLegacyWarnsWhenLegacyFileConflictsWithKeyring(t *testing.T) {
	setup(t)
	if err := SetAPIKey("tskey-api-FAKE-RING"); err != nil {
		t.Fatal(err)
	}
	path := apiKeyPath(t)
	if err := os.WriteFile(path, []byte("tskey-api-FAKE-FILE"), 0o600); err != nil {
		t.Fatal(err)
	}
	logs := captureCredentialLogs(t)
	if MigrateFromLegacy() {
		t.Fatal("conflicting legacy file was migrated")
	}
	out := logs.String()
	if !strings.Contains(out, "level=WARN") || !strings.Contains(out, "legacy API key file") || !strings.Contains(out, path) {
		t.Fatalf("conflicting legacy file kept without a warning naming it:\n%s", out)
	}
	if strings.Contains(out, "FAKE-RING") || strings.Contains(out, "FAKE-FILE") {
		t.Fatalf("warning leaks a credential value:\n%s", out)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "tskey-api-FAKE-FILE" {
		t.Fatalf("conflicting legacy file changed: %v", err)
	}
	if key, err := GetAPIKey(); err != nil || key != "tskey-api-FAKE-RING" {
		t.Fatalf("keyring value changed by a conflicting migration: kept=%v err=%v", key == "tskey-api-FAKE-RING", err)
	}
}
