package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/monody0007/tslink/internal/inspect"
	"github.com/monody0007/tslink/internal/registry"
	tsruntime "github.com/monody0007/tslink/internal/runtime"
)

// writeRegistryWithIsolatedEntry writes a file share whose directory is gone,
// which the daemon's loader isolates as a per-service issue, followed by web.
// The isolated entry comes first because there registry.Load, the diagnostic
// loader, fills fields the entry omits from web's; only the daemon's loader
// reads the entries as the daemon does. It returns what the daemon's sync
// loads and the fingerprint it writes into runtime.json.
func writeRegistryWithIsolatedEntry(t *testing.T, regPath string) (*registry.Registry, []registry.ServiceIssue, string) {
	t.Helper()
	data, err := json.Marshal(map[string]any{
		"schema_version": registry.CurrentRegistrySchemaVersion,
		"services": []map[string]any{
			{"name": "docs", "type": "file", "path": filepath.Join(t.TempDir(), "gone"), "tags": []string{"tag:tsmain"}},
			{"name": "web", "type": "proxy", "target": "http://127.0.0.1:3000", "tags": []string{"tag:tsmain"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(regPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	reg, issues, err := registry.LoadForRuntime(regPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(reg.Services) != 1 || len(issues) != 1 {
		t.Fatalf("control: services %+v issues %+v, want web valid and docs isolated", reg.Services, issues)
	}
	fingerprint, err := tsruntime.RegistryFingerprint(reg, issues)
	if err != nil {
		t.Fatal(err)
	}
	return reg, issues, fingerprint
}

// The daemon isolates a bad entry and still writes an exact snapshot for the
// registry. status, doctor and invite compare it with a fingerprint of the
// same input, so the snapshot stays authoritative instead of reading as a
// registry_mismatch for as long as the entry is bad.
func TestStatusTreatsTheDaemonsSnapshotAsExactWithAnIsolatedEntry(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	pidPath := filepath.Join(dir, "tslink.pid")
	snapshotPath := filepath.Join(dir, "runtime.json")
	reg, issues, fingerprint := writeRegistryWithIsolatedEntry(t, regPath)
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

func TestDoctorTreatsTheDaemonsSnapshotAsExactWithAnIsolatedEntry(t *testing.T) {
	env := newDoctorTestEnv(t, nil)
	_, _, fingerprint := writeRegistryWithIsolatedEntry(t, env.regPath)
	write := func(fingerprint string) {
		snapshot := tsruntime.NewSnapshot(env.pid, env.startedAt, fingerprint, env.startedAt.Add(time.Second), nil)
		if err := tsruntime.Save(env.snapshotPath, snapshot); err != nil {
			t.Fatal(err)
		}
	}
	write("sha256:another-registry")
	assertDoctorFinding(t, buildDoctorResult(doctorOptions{}), inspect.WarningCodeRuntimeSnapshotStale)

	write(fingerprint)
	result := buildDoctorResult(doctorOptions{})
	for _, finding := range result.Findings {
		if finding.Code == inspect.WarningCodeRuntimeSnapshotStale {
			t.Fatalf("doctor reports %+v for the snapshot the daemon wrote for this registry", finding)
		}
	}
}

// A registry the daemon's loader rejects cannot be the one it applied; doctor
// says so instead of comparing the snapshot with it.
func TestDoctorReportsARegistryTheDaemonCannotLoad(t *testing.T) {
	env := newDoctorTestEnv(t, nil)
	if err := os.WriteFile(env.regPath, []byte("  \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	finding := assertDoctorFinding(t, buildDoctorResult(doctorOptions{}), inspect.WarningCodeRegistryLoadFailed)
	if finding.Area != "registry" {
		t.Fatalf("finding = %+v, want the registry named", finding)
	}
}

func TestInviteTakesNodeIDsFromTheDaemonsSnapshotWithAnIsolatedEntry(t *testing.T) {
	service := registry.Service{Name: "web", Type: registry.TypeProxy, Target: "http://127.0.0.1:3000", Tags: []string{"tag:tsmain"}}
	regPath, pidPath, snapshotPath := configureExactInviteRuntime(t, []registry.Service{service}, map[string]string{"web": "n-web-owned"})
	loaded, err := inviteLoadSnapshotFn(snapshotPath)
	if err != nil {
		t.Fatal(err)
	}
	_, _, fingerprint := writeRegistryWithIsolatedEntry(t, regPath)
	snapshot := *loaded
	snapshot.RegistryFingerprint = fingerprint
	inviteLoadSnapshotFn = func(string) (*tsruntime.Snapshot, error) { return &snapshot, nil }

	targets, err := inviteDeviceTargetsForPaths(regPath, pidPath, snapshotPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || targets[0].NodeID != "n-web-owned" {
		t.Fatalf("targets = %+v, want web's node ID from the exact snapshot", targets)
	}
}
