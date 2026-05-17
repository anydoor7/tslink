package runtime

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/monody0007/tslink/internal/inspect"
	"github.com/monody0007/tslink/internal/registry"
)

func testSnapshot() Snapshot {
	startedAt := time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)
	updatedAt := time.Date(2026, 5, 17, 12, 1, 0, 0, time.UTC)
	return NewSnapshot(1234, startedAt, "sha256:test", updatedAt, []ServiceState{
		{
			Service: registry.Service{
				Name: "app",
				Type: registry.TypeFile,
				Path: "/tmp/app",
			},
			CertDomains: []string{"app.tailnet.ts.net"},
		},
	})
}

func TestSaveLoadSnapshotAtomicPrivateFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "runtime.json")
	want := testSnapshot()

	if err := Save(path, want); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	dirInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("Stat(dir) error = %v", err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o700 {
		t.Fatalf("dir mode = %o, want 0700", got)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(snapshot) error = %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("snapshot mode = %o, want 0600", got)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.SchemaVersion != SchemaVersion {
		t.Fatalf("SchemaVersion = %q, want %q", got.SchemaVersion, SchemaVersion)
	}
	if got.DaemonPID != want.DaemonPID || !got.DaemonStartedAt.Equal(want.DaemonStartedAt) {
		t.Fatalf("daemon fields = pid %d started %s, want pid %d started %s", got.DaemonPID, got.DaemonStartedAt, want.DaemonPID, want.DaemonStartedAt)
	}
	if got.RegistryFingerprint != want.RegistryFingerprint || !got.UpdatedAt.Equal(want.UpdatedAt) {
		t.Fatalf("snapshot metadata = %+v, want %+v", got, want)
	}
	if len(got.Services) != 1 {
		t.Fatalf("services = %d, want 1", len(got.Services))
	}
	entry := got.Services[0]
	if entry.Endpoint.Kind != inspect.EndpointKindHTTPS || entry.Endpoint.State != inspect.EndpointStateExact {
		t.Fatalf("endpoint = %+v, want https exact", entry.Endpoint)
	}
	if entry.Endpoint.Display != "https://app.tailnet.ts.net" || entry.Endpoint.Host != "app.tailnet.ts.net" {
		t.Fatalf("endpoint = %+v, want cert-domain URL", entry.Endpoint)
	}
	if entry.Exposure.Kind != inspect.ExposureTailnet {
		t.Fatalf("exposure = %+v, want tailnet", entry.Exposure)
	}
	if strings.Join(entry.CertDomains, ",") != "app.tailnet.ts.net" {
		t.Fatalf("cert domains = %v", entry.CertDomains)
	}
}

func TestSaveRenameFailureKeepsPreviousSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.json")
	original := testSnapshot()
	if err := Save(path, original); err != nil {
		t.Fatalf("initial Save() error = %v", err)
	}

	oldRename := renameFile
	renameFile = func(oldpath, newpath string) error {
		return errors.New("rename blocked")
	}
	t.Cleanup(func() { renameFile = oldRename })

	replacement := original
	replacement.DaemonPID = 5678
	if err := Save(path, replacement); err == nil {
		t.Fatal("Save() error = nil, want rename failure")
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.DaemonPID != original.DaemonPID {
		t.Fatalf("DaemonPID = %d, want original %d after failed rename", got.DaemonPID, original.DaemonPID)
	}

	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".tmp") {
			t.Fatalf("temporary file %q was not cleaned up", entry.Name())
		}
	}
}

func TestRegistryFingerprintStableAndChanges(t *testing.T) {
	reg := &registry.Registry{Services: []registry.Service{
		{Name: "app", Type: registry.TypeProxy, Target: "http://localhost:3000"},
	}}
	again := &registry.Registry{Services: []registry.Service{
		{Name: "app", Type: registry.TypeProxy, Target: "http://localhost:3000"},
	}}
	changed := &registry.Registry{Services: []registry.Service{
		{Name: "app", Type: registry.TypeProxy, Target: "http://localhost:4000"},
	}}

	first, err := RegistryFingerprint(reg)
	if err != nil {
		t.Fatalf("RegistryFingerprint() error = %v", err)
	}
	second, err := RegistryFingerprint(again)
	if err != nil {
		t.Fatalf("RegistryFingerprint() error = %v", err)
	}
	if first != second {
		t.Fatalf("fingerprints differ for identical registries: %q vs %q", first, second)
	}
	if !strings.HasPrefix(first, "sha256:") {
		t.Fatalf("fingerprint = %q, want sha256 prefix", first)
	}

	third, err := RegistryFingerprint(changed)
	if err != nil {
		t.Fatalf("RegistryFingerprint(changed) error = %v", err)
	}
	if third == first {
		t.Fatalf("fingerprint did not change after registry content changed: %q", third)
	}

	nilFingerprint, err := RegistryFingerprint(nil)
	if err != nil {
		t.Fatalf("RegistryFingerprint(nil) error = %v", err)
	}
	emptyFingerprint, err := RegistryFingerprint(&registry.Registry{Services: []registry.Service{}})
	if err != nil {
		t.Fatalf("RegistryFingerprint(empty) error = %v", err)
	}
	if nilFingerprint != emptyFingerprint {
		t.Fatalf("nil fingerprint = %q, empty fingerprint = %q; want equal", nilFingerprint, emptyFingerprint)
	}
}

func TestLoadMissingAndMalformedSnapshot(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "missing.json"))
	assertSnapshotError(t, err, StatusMissing, inspect.WarningCodeRuntimeSnapshotMissing)

	path := filepath.Join(t.TempDir(), "runtime.json")
	if err := os.WriteFile(path, []byte("{bad json"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	_, err = Load(path)
	assertSnapshotError(t, err, StatusMalformed, inspect.WarningCodeRuntimeSnapshotStale)
}

func TestLoadRetriesOnceOnMalformedThenSucceeds(t *testing.T) {
	valid, err := json.Marshal(testSnapshot())
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	oldRead := readFile
	var calls int
	readFile = func(path string) ([]byte, error) {
		calls++
		if calls == 1 {
			return []byte("{"), nil
		}
		return valid, nil
	}
	t.Cleanup(func() { readFile = oldRead })

	got, err := Load("/does/not/matter/runtime.json")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if calls != 2 {
		t.Fatalf("read attempts = %d, want 2", calls)
	}
	if got.DaemonPID != testSnapshot().DaemonPID {
		t.Fatalf("DaemonPID = %d, want %d", got.DaemonPID, testSnapshot().DaemonPID)
	}
}

func TestLoadRetriesOnceOnMalformedThenReturnsStale(t *testing.T) {
	oldRead := readFile
	var calls int
	readFile = func(path string) ([]byte, error) {
		calls++
		return []byte("{"), nil
	}
	t.Cleanup(func() { readFile = oldRead })

	_, err := Load("/does/not/matter/runtime.json")
	assertSnapshotError(t, err, StatusMalformed, inspect.WarningCodeRuntimeSnapshotStale)
	if calls != 2 {
		t.Fatalf("read attempts = %d, want 2", calls)
	}
}

func TestClassifyFreshness(t *testing.T) {
	snapshot := testSnapshot()
	if got := Classify(&snapshot, nil, snapshot.DaemonPID, snapshot.RegistryFingerprint); got.Status != StatusExact || !got.Exact || got.Code != "" {
		t.Fatalf("Classify(exact) = %+v", got)
	}

	if got := Classify(&snapshot, nil, 9999, snapshot.RegistryFingerprint); got.Status != StatusPIDMismatch || got.Code != inspect.WarningCodeRuntimeSnapshotStale || got.Exact {
		t.Fatalf("Classify(pid mismatch) = %+v", got)
	}

	if got := Classify(&snapshot, nil, snapshot.DaemonPID, "sha256:other"); got.Status != StatusRegistryMismatch || got.Code != inspect.WarningCodeRuntimeSnapshotStale || got.Exact {
		t.Fatalf("Classify(registry mismatch) = %+v", got)
	}

	missingErr := &SnapshotError{Status: StatusMissing, Code: inspect.WarningCodeRuntimeSnapshotMissing, Err: os.ErrNotExist}
	if got := Classify(nil, missingErr, snapshot.DaemonPID, snapshot.RegistryFingerprint); got.Status != StatusMissing || got.Code != inspect.WarningCodeRuntimeSnapshotMissing || got.Exact {
		t.Fatalf("Classify(missing) = %+v", got)
	}

	malformedErr := &SnapshotError{Status: StatusMalformed, Code: inspect.WarningCodeRuntimeSnapshotStale, Err: errors.New("bad json")}
	if got := Classify(nil, malformedErr, snapshot.DaemonPID, snapshot.RegistryFingerprint); got.Status != StatusMalformed || got.Code != inspect.WarningCodeRuntimeSnapshotStale || got.Exact {
		t.Fatalf("Classify(malformed) = %+v", got)
	}
}

func assertSnapshotError(t *testing.T, err error, status, code string) {
	t.Helper()
	var snapshotErr *SnapshotError
	if !errors.As(err, &snapshotErr) {
		t.Fatalf("error = %T %[1]v, want *SnapshotError", err)
	}
	if snapshotErr.Status != status || snapshotErr.Code != code {
		t.Fatalf("SnapshotError = status %q code %q, want status %q code %q", snapshotErr.Status, snapshotErr.Code, status, code)
	}
}
