package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/monody0007/tslink/internal/registry"
	tsruntime "github.com/monody0007/tslink/internal/runtime"
)

// The daemon isolates a bad entry and still writes an exact snapshot for the
// registry. status, doctor and invite compare it with a fingerprint of the
// same input, so the snapshot stays authoritative instead of reading as a
// registry_mismatch for as long as the entry is bad.
func TestStatusTreatsTheDaemonsSnapshotAsExactWithAnIsolatedEntry(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	pidPath := filepath.Join(dir, "tslink.pid")
	snapshotPath := filepath.Join(dir, "runtime.json")
	data, err := json.Marshal(map[string]any{
		"schema_version": registry.CurrentRegistrySchemaVersion,
		"services": []map[string]any{
			{"name": "web", "type": "proxy", "target": "http://127.0.0.1:3000", "tags": []string{"tag:tsmain"}},
			{"name": "docs", "type": "file", "path": filepath.Join(dir, "gone"), "tags": []string{"tag:tsmain"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(regPath, data, 0o600); err != nil {
		t.Fatal(err)
	}

	// What the daemon's sync does: load for runtime, fingerprint the load.
	reg, issues, err := registry.LoadForRuntime(regPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 {
		t.Fatalf("control: issues = %+v, want docs isolated", issues)
	}
	fingerprint, err := tsruntime.RegistryFingerprint(reg, issues)
	if err != nil {
		t.Fatal(err)
	}
	startedAt := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	snapshot := tsruntime.NewSnapshot(4242, startedAt, fingerprint, startedAt.Add(time.Second), []tsruntime.ServiceState{
		{Service: reg.Services[0], RuntimeHost: "web.tailnet.ts.net."},
		{Service: issues[0].Service, RuntimeState: tsruntime.ServiceRuntimeFailed, Error: &tsruntime.ServiceError{Code: registry.CodePathNotFound, Message: "path not found"}},
	})
	if err := tsruntime.Save(snapshotPath, snapshot); err != nil {
		t.Fatal(err)
	}
	withStatusURLSeams(t, true, 4242, startedAt)

	result, err := getStatusURLs(pidPath, regPath, snapshotPath)
	if err != nil {
		t.Fatalf("getStatusURLs: %v", err)
	}
	if !result.RuntimeSnapshot.Exact || result.RuntimeSnapshot.Status != tsruntime.StatusExact {
		t.Fatalf("runtime freshness = %+v, want exact", result.RuntimeSnapshot)
	}
}
