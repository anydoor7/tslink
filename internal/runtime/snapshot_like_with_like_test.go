package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/monody0007/tslink/internal/inspect"
	"github.com/monody0007/tslink/internal/registry"
)

// writeRegistryWithIsolatedEntry writes a registry whose first service is a
// file share with a missing directory: the daemon's loader isolates it as a
// per-service issue and keeps the service after it, so the fingerprint input
// has to put the isolated entry back in its place.
func writeRegistryWithIsolatedEntry(t *testing.T, missingDir string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "registry.json")
	data, err := json.Marshal(map[string]any{
		"schema_version": registry.CurrentRegistrySchemaVersion,
		"services": []map[string]any{
			{"name": "docs", "type": "file", "path": missingDir, "tags": []string{"tag:tsmain"}},
			{"name": "web", "type": "proxy", "target": "http://127.0.0.1:3000", "tags": []string{"tag:tsmain"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// The daemon fingerprints what its loader returned, the valid services and the
// isolated ones; the CLI fingerprints registry.json through the same function
// over the same input, so a snapshot the daemon wrote for this registry is
// exact even though one entry is isolated.
func TestSnapshotForARegistryWithAnIsolatedEntryClassifiesExact(t *testing.T) {
	path := writeRegistryWithIsolatedEntry(t, filepath.Join(t.TempDir(), "gone"))
	reg, issues, err := registry.LoadForRuntime(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(reg.Services) != 1 || len(issues) != 1 || issues[0].Name != "docs" {
		t.Fatalf("control: loader returned services %+v issues %+v, want web valid and docs isolated", reg.Services, issues)
	}
	daemonFingerprint, err := RegistryFingerprint(reg, issues)
	if err != nil {
		t.Fatal(err)
	}
	cliFingerprint, err := CurrentRegistryFingerprint(path)
	if err != nil {
		t.Fatal(err)
	}
	if cliFingerprint != daemonFingerprint {
		t.Fatalf("CLI fingerprint %s, daemon fingerprint %s; want one fingerprint of one input", cliFingerprint, daemonFingerprint)
	}
	snapshot := testSnapshot()
	snapshot.RegistryFingerprint = daemonFingerprint
	got := Classify(&snapshot, nil, ExpectedRuntime{
		DaemonPID:                  snapshot.DaemonPID,
		DaemonStartedAtLowerBound:  snapshot.DaemonStartedAt,
		CurrentRegistryFingerprint: cliFingerprint,
	})
	if got.Status != StatusExact || !got.Exact {
		t.Fatalf("Classify() = %+v, want exact", got)
	}

	// The isolated entry is part of the input: editing it changes the
	// fingerprint, so a snapshot for the earlier edit is not exact.
	edited := writeRegistryWithIsolatedEntry(t, filepath.Join(t.TempDir(), "still-gone"))
	editedFingerprint, err := CurrentRegistryFingerprint(edited)
	if err != nil {
		t.Fatal(err)
	}
	if editedFingerprint == daemonFingerprint {
		t.Fatal("editing the isolated entry left the fingerprint unchanged")
	}
}

// Without isolated entries the input is the valid services alone, the input
// every earlier build fingerprinted, so a running daemon's snapshot stays
// exact across the upgrade.
func TestRegistryFingerprintWithoutIssuesKeepsTheEarlierForm(t *testing.T) {
	reg := &registry.Registry{SchemaVersion: registry.CurrentRegistrySchemaVersion, Services: []registry.Service{
		{Name: "web", Type: registry.TypeProxy, Target: "http://127.0.0.1:3000"},
	}}
	got, err := RegistryFingerprint(reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	earlier, err := json.Marshal(registry.Registry{Services: reg.Services})
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(earlier)
	want := "sha256:" + hex.EncodeToString(sum[:])
	if got != want {
		t.Fatalf("RegistryFingerprint() = %s, want %s", got, want)
	}
}

func writeSnapshotJSON(t *testing.T, edit func(map[string]any)) string {
	t.Helper()
	data, err := json.Marshal(testSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	edit(fields)
	data, err = json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "runtime.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// runtime.json has its own integer schema version. Version 1 is read with
// fields this build does not know, and so is the string earlier builds wrote.
func TestLoadReadsRuntimeSnapshotVersionOne(t *testing.T) {
	for name, edit := range map[string]func(map[string]any){
		"integer 1": func(fields map[string]any) { fields["schema_version"] = 1 },
		"integer 1 with fields added later": func(fields map[string]any) {
			fields["schema_version"] = 1
			fields["added_later"] = map[string]any{"any": "shape"}
		},
		"string written before the integer version": func(fields map[string]any) { fields["schema_version"] = "vnext.1" },
	} {
		t.Run(name, func(t *testing.T) {
			got, err := Load(writeSnapshotJSON(t, edit))
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if got.SchemaVersion != SchemaVersion || got.DaemonPID != testSnapshot().DaemonPID {
				t.Fatalf("Load() = version %d pid %d", got.SchemaVersion, got.DaemonPID)
			}
		})
	}
}

// A snapshot this build cannot read is unreadable, not stale: stale means a
// readable snapshot for another daemon or registry. An incompatible version is
// read once, since reading it again cannot change the answer.
func TestLoadReportsAnIncompatibleSnapshotAsUnreadable(t *testing.T) {
	for name, edit := range map[string]func(map[string]any){
		"integer 2":                 func(fields map[string]any) { fields["schema_version"] = 2 },
		"another string":            func(fields map[string]any) { fields["schema_version"] = "vnext.2" },
		"missing schema_version":    func(fields map[string]any) { delete(fields, "schema_version") },
		"schema_version not scalar": func(fields map[string]any) { fields["schema_version"] = []int{1} },
	} {
		t.Run(name, func(t *testing.T) {
			path := writeSnapshotJSON(t, edit)
			oldRead := readFile
			calls := 0
			readFile = func(path string) ([]byte, error) {
				calls++
				return os.ReadFile(path)
			}
			t.Cleanup(func() { readFile = oldRead })
			_, err := Load(path)
			assertSnapshotError(t, err, StatusUnreadable, inspect.WarningCodeRuntimeSnapshotUnreadable)
			if calls != 1 {
				t.Fatalf("read attempts = %d, want 1", calls)
			}
		})
	}
}

func TestSaveWritesTheIntegerSchemaVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.json")
	if err := Save(path, testSnapshot()); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var header struct {
		SchemaVersion json.RawMessage `json:"schema_version"`
	}
	if err := json.Unmarshal(raw, &header); err != nil {
		t.Fatal(err)
	}
	if string(header.SchemaVersion) != "1" {
		t.Fatalf("schema_version = %s, want 1", header.SchemaVersion)
	}
}
