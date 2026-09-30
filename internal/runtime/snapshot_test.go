package runtime

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	goruntime "runtime"
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
			NodeID:      "n-runtime-owned",
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
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(snapshot) error = %v", err)
	}
	if strings.Contains(string(raw), `"partial"`) {
		t.Fatalf("authoritative snapshot changed wire shape with partial marker: %s", raw)
	}

	dirInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("Stat(dir) error = %v", err)
	}
	if goruntime.GOOS != "windows" {
		if got := dirInfo.Mode().Perm(); got != 0o700 {
			t.Fatalf("dir mode = %o, want 0700", got)
		}
	} else if !dirInfo.IsDir() {
		t.Fatalf("snapshot parent mode = %v, want directory on Windows", dirInfo.Mode())
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(snapshot) error = %v", err)
	}
	if goruntime.GOOS != "windows" {
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("snapshot mode = %o, want 0600", got)
		}
	} else if !info.Mode().IsRegular() {
		t.Fatalf("snapshot mode = %v, want regular file on Windows", info.Mode())
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.SchemaVersion != SchemaVersion {
		t.Fatalf("SchemaVersion = %d, want %d", got.SchemaVersion, SchemaVersion)
	}
	if got.DaemonPID != want.DaemonPID || !got.DaemonStartedAt.Equal(want.DaemonStartedAt) {
		t.Fatalf("daemon fields = pid %d started %s, want pid %d started %s", got.DaemonPID, got.DaemonStartedAt, want.DaemonPID, want.DaemonStartedAt)
	}
	if got.RegistryFingerprint != want.RegistryFingerprint || !got.UpdatedAt.Equal(want.UpdatedAt) {
		t.Fatalf("snapshot metadata = %+v, want %+v", got, want)
	}
	if got.Partial {
		t.Fatal("Partial = true after saving an authoritative snapshot")
	}
	if len(got.Services) != 1 {
		t.Fatalf("services = %d, want 1", len(got.Services))
	}
	entry := got.Services[0]
	if entry.NodeID != "n-runtime-owned" {
		t.Fatalf("node_id = %q, want exact stable node ID", entry.NodeID)
	}
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

	first, err := RegistryFingerprint(reg, nil)
	if err != nil {
		t.Fatalf("RegistryFingerprint() error = %v", err)
	}
	second, err := RegistryFingerprint(again, nil)
	if err != nil {
		t.Fatalf("RegistryFingerprint() error = %v", err)
	}
	if first != second {
		t.Fatalf("fingerprints differ for identical registries: %q vs %q", first, second)
	}
	if !strings.HasPrefix(first, "sha256:") {
		t.Fatalf("fingerprint = %q, want sha256 prefix", first)
	}

	third, err := RegistryFingerprint(changed, nil)
	if err != nil {
		t.Fatalf("RegistryFingerprint(changed, nil) error = %v", err)
	}
	if third == first {
		t.Fatalf("fingerprint did not change after registry content changed: %q", third)
	}

	nilFingerprint, err := RegistryFingerprint(nil, nil)
	if err != nil {
		t.Fatalf("RegistryFingerprint(nil, nil) error = %v", err)
	}
	emptyFingerprint, err := RegistryFingerprint(&registry.Registry{Services: []registry.Service{}}, nil)
	if err != nil {
		t.Fatalf("RegistryFingerprint(empty, nil) error = %v", err)
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
	assertSnapshotError(t, err, StatusMalformed, inspect.WarningCodeRuntimeSnapshotUnreadable)
}

func TestLoadUnreadableSnapshot(t *testing.T) {
	oldRead := readFile
	readFile = func(path string) ([]byte, error) {
		return nil, os.ErrPermission
	}
	t.Cleanup(func() { readFile = oldRead })

	_, err := Load("/does/not/matter/runtime.json")
	assertSnapshotError(t, err, StatusUnreadable, inspect.WarningCodeRuntimeSnapshotUnreadable)
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

func TestLoadRetriesOnceOnMalformedThenReturnsUnreadable(t *testing.T) {
	oldRead := readFile
	var calls int
	readFile = func(path string) ([]byte, error) {
		calls++
		return []byte("{"), nil
	}
	t.Cleanup(func() { readFile = oldRead })

	_, err := Load("/does/not/matter/runtime.json")
	assertSnapshotError(t, err, StatusMalformed, inspect.WarningCodeRuntimeSnapshotUnreadable)
	if calls != 2 {
		t.Fatalf("read attempts = %d, want 2", calls)
	}
}

func TestClassifyFreshness(t *testing.T) {
	snapshot := testSnapshot()
	expected := ExpectedRuntime{
		DaemonPID:                  snapshot.DaemonPID,
		DaemonStartedAtLowerBound:  snapshot.DaemonStartedAt,
		CurrentRegistryFingerprint: snapshot.RegistryFingerprint,
	}
	if got := Classify(&snapshot, nil, expected); got.Status != StatusExact || !got.Exact || got.Code != "" {
		t.Fatalf("Classify(exact) = %+v", got)
	}

	pidMismatch := expected
	pidMismatch.DaemonPID = 9999
	if got := Classify(&snapshot, nil, pidMismatch); got.Status != StatusPIDMismatch || got.Code != inspect.WarningCodeRuntimeSnapshotStale || got.Exact {
		t.Fatalf("Classify(pid mismatch) = %+v", got)
	}

	registryMismatch := expected
	registryMismatch.CurrentRegistryFingerprint = "sha256:other"
	if got := Classify(&snapshot, nil, registryMismatch); got.Status != StatusRegistryMismatch || got.Code != inspect.WarningCodeRuntimeSnapshotStale || got.Exact {
		t.Fatalf("Classify(registry mismatch) = %+v", got)
	}

	missingErr := &SnapshotError{Status: StatusMissing, Code: inspect.WarningCodeRuntimeSnapshotMissing, Err: os.ErrNotExist}
	if got := Classify(nil, missingErr, expected); got.Status != StatusMissing || got.Code != inspect.WarningCodeRuntimeSnapshotMissing || got.Exact {
		t.Fatalf("Classify(missing) = %+v", got)
	}

	malformedErr := &SnapshotError{Status: StatusMalformed, Code: inspect.WarningCodeRuntimeSnapshotUnreadable, Err: errors.New("bad json")}
	if got := Classify(nil, malformedErr, expected); got.Status != StatusMalformed || got.Code != inspect.WarningCodeRuntimeSnapshotUnreadable || got.Exact {
		t.Fatalf("Classify(malformed) = %+v", got)
	}

	unreadableErr := &SnapshotError{Status: StatusUnreadable, Code: inspect.WarningCodeRuntimeSnapshotUnreadable, Err: os.ErrPermission}
	if got := Classify(nil, unreadableErr, expected); got.Status != StatusUnreadable || got.Code != inspect.WarningCodeRuntimeSnapshotUnreadable || got.Exact {
		t.Fatalf("Classify(unreadable) = %+v", got)
	}
}

func TestClassifyPartialSnapshotIsNeverAuthoritative(t *testing.T) {
	complete := testSnapshot()
	partial := NewPartialSnapshot(
		complete.DaemonPID,
		complete.DaemonStartedAt,
		complete.RegistryFingerprint,
		complete.UpdatedAt,
		[]ServiceState{{Service: registry.Service{Name: "first", Type: registry.TypeFile, Path: "/tmp/first"}}},
	)
	expected := ExpectedRuntime{
		DaemonPID:                  partial.DaemonPID,
		DaemonStartedAtLowerBound:  partial.DaemonStartedAt,
		CurrentRegistryFingerprint: partial.RegistryFingerprint,
	}

	got := Classify(&partial, nil, expected)
	if got.Status != StatusPartial || got.Exact || got.Code != inspect.WarningCodeRuntimeSnapshotStale {
		t.Fatalf("Classify(partial) = %+v, want explicit non-authoritative partial freshness", got)
	}
}

func TestClassifyRejectsMissingExpectedRuntimeIdentity(t *testing.T) {
	snapshot := testSnapshot()
	expected := ExpectedRuntime{
		DaemonPID:                  snapshot.DaemonPID,
		CurrentRegistryFingerprint: snapshot.RegistryFingerprint,
	}

	got := Classify(&snapshot, nil, expected)
	if got.Status != StatusStale || got.Code != inspect.WarningCodeRuntimeSnapshotStale || got.Exact {
		t.Fatalf("Classify(missing identity) = %+v, want stale not exact", got)
	}
}

func TestClassifyRejectsPriorDaemonSnapshotWithReusedPIDAndFingerprint(t *testing.T) {
	snapshot := testSnapshot()
	expected := ExpectedRuntime{
		DaemonPID:                  snapshot.DaemonPID,
		DaemonStartedAtLowerBound:  snapshot.UpdatedAt.Add(time.Second),
		CurrentRegistryFingerprint: snapshot.RegistryFingerprint,
	}

	got := Classify(&snapshot, nil, expected)
	if got.Status != StatusStale || got.Code != inspect.WarningCodeRuntimeSnapshotStale || got.Exact {
		t.Fatalf("Classify(reused pid stale snapshot) = %+v, want stale not exact", got)
	}
}

func TestNewSnapshotTCPPlaceholderRemainsExpected(t *testing.T) {
	startedAt := time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)
	updatedAt := startedAt.Add(time.Minute)
	snapshot := NewSnapshot(1234, startedAt, "sha256:test", updatedAt, []ServiceState{
		{
			Service: registry.Service{
				Name:   "db",
				Type:   registry.TypeTCP,
				Target: "localhost:5432",
				Port:   5432,
			},
		},
	})

	endpoint := snapshot.Services[0].Endpoint
	if endpoint.Display != "db.<tailnet>.ts.net:5432" || endpoint.State != inspect.EndpointStateExpected {
		t.Fatalf("tcp endpoint = %+v, want placeholder expected", endpoint)
	}
}

func TestNewSnapshotTCPRuntimeHostIsExact(t *testing.T) {
	startedAt := time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)
	updatedAt := startedAt.Add(time.Minute)
	snapshot := NewSnapshot(1234, startedAt, "sha256:test", updatedAt, []ServiceState{
		{
			Service: registry.Service{
				Name:   "db",
				Type:   registry.TypeTCP,
				Target: "localhost:5432",
				Port:   5432,
			},
			RuntimeHost: "db.tailnet.ts.net.",
		},
	})

	endpoint := snapshot.Services[0].Endpoint
	if endpoint.Display != "db.tailnet.ts.net:5432" || endpoint.Host != "db.tailnet.ts.net" || endpoint.State != inspect.EndpointStateExact {
		t.Fatalf("tcp endpoint = %+v, want concrete exact runtime host", endpoint)
	}
}

func TestNewSnapshotFunnelExposure(t *testing.T) {
	startedAt := time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)
	updatedAt := startedAt.Add(time.Minute)
	snapshot := NewSnapshot(1234, startedAt, "sha256:test", updatedAt, []ServiceState{
		{
			Service: registry.Service{
				Name:   "web",
				Type:   registry.TypeProxy,
				Target: "http://localhost:3000",
				Funnel: true,
			},
			RuntimeHost: "web.tailnet.ts.net.",
		},
	})

	entry := snapshot.Services[0]
	if entry.Endpoint.Kind != inspect.EndpointKindPublicHTTPS || entry.Endpoint.Display != "https://web.tailnet.ts.net" || entry.Endpoint.State != inspect.EndpointStateExact {
		t.Fatalf("funnel endpoint = %+v, want public https exact runtime host", entry.Endpoint)
	}
	if entry.Exposure.Kind != inspect.ExposurePublicFunnel || !entry.Exposure.Public {
		t.Fatalf("funnel exposure = %+v, want public funnel", entry.Exposure)
	}
	if entry.RuntimeState != ServiceRuntimeRunning || !entry.FunnelRequested || !entry.FunnelActive || entry.FunnelState != FunnelStateActive || entry.Error != nil {
		t.Fatalf("funnel runtime state = %+v, want requested and active", entry)
	}

	failure := NewSnapshot(1234, startedAt, "sha256:test", updatedAt, []ServiceState{{
		Service:      registry.Service{Name: "blocked", Type: registry.TypeProxy, Target: "http://localhost:3001", Funnel: true},
		RuntimeState: ServiceRuntimeFailed,
		FunnelState:  FunnelStateCapabilityMissing,
		Error:        &ServiceError{Code: registry.CodeFunnelCapabilityMissing, Message: "missing capability", Next: []string{"fix policy"}},
	}}).Services[0]
	if failure.RuntimeState != ServiceRuntimeFailed || !failure.FunnelRequested || failure.FunnelActive || failure.FunnelState != FunnelStateCapabilityMissing || failure.Error == nil || len(failure.Error.Next) != 1 {
		t.Fatalf("failed funnel runtime state = %+v", failure)
	}
}

func TestNewSnapshotServicesAreSortedByName(t *testing.T) {
	startedAt := time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)
	updatedAt := startedAt.Add(time.Minute)
	snapshot := NewSnapshot(1234, startedAt, "sha256:test", updatedAt, []ServiceState{
		{Service: registry.Service{Name: "zeta", Type: registry.TypeFile, Path: "/tmp/zeta"}},
		{Service: registry.Service{Name: "alpha", Type: registry.TypeFile, Path: "/tmp/alpha"}},
		{Service: registry.Service{Name: "middle", Type: registry.TypeFile, Path: "/tmp/middle"}},
	})

	got := []string{snapshot.Services[0].Name, snapshot.Services[1].Name, snapshot.Services[2].Name}
	if strings.Join(got, ",") != "alpha,middle,zeta" {
		t.Fatalf("service order = %v, want alpha,middle,zeta", got)
	}
}

func TestNewSnapshotCertDomainRewritesCustomDomainEndpoint(t *testing.T) {
	startedAt := time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)
	updatedAt := startedAt.Add(time.Minute)
	snapshot := NewSnapshot(1234, startedAt, "sha256:test", updatedAt, []ServiceState{
		{
			Service: registry.Service{
				Name:   "web",
				Type:   registry.TypeProxy,
				Target: "http://localhost:3000",
				Domain: "configured.example.com",
			},
			CertDomains: []string{"runtime.example.com"},
		},
	})

	endpoint := snapshot.Services[0].Endpoint
	if endpoint.Display != "https://runtime.example.com" || endpoint.Host != "runtime.example.com" || endpoint.State != inspect.EndpointStateExact {
		t.Fatalf("endpoint = %+v, want runtime cert-domain rewrite", endpoint)
	}
}

func TestSnapshotErrorAccessors(t *testing.T) {
	sentinel := errors.New("read failed")
	err := &SnapshotError{
		Status: StatusUnreadable,
		Code:   inspect.WarningCodeRuntimeSnapshotUnreadable,
		Err:    sentinel,
	}

	if !errors.Is(err, sentinel) {
		t.Fatalf("errors.Is(SnapshotError, sentinel) = false, want true")
	}
	if got := err.Unwrap(); got != sentinel {
		t.Fatalf("Unwrap() = %v, want sentinel", got)
	}
	if got := err.StableCode(); got != inspect.WarningCodeRuntimeSnapshotUnreadable {
		t.Fatalf("StableCode() = %q, want %q", got, inspect.WarningCodeRuntimeSnapshotUnreadable)
	}
	var nilErr *SnapshotError
	if got := nilErr.Unwrap(); got != nil {
		t.Fatalf("nil Unwrap() = %v, want nil", got)
	}
	if got := nilErr.StableCode(); got != "" {
		t.Fatalf("nil StableCode() = %q, want empty", got)
	}
}

func TestRemoveSnapshotFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "runtime.json")
	if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if err := Remove(path); err != nil {
		t.Fatalf("Remove(existing) error = %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("Stat removed snapshot err = %v, want not exist", err)
	}
	if err := Remove(path); err != nil {
		t.Fatalf("Remove(missing) error = %v, want nil", err)
	}

	nonEmptyDir := filepath.Join(dir, "not-a-file")
	if err := os.Mkdir(nonEmptyDir, 0o700); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(nonEmptyDir, "child"), []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile(child) error = %v", err)
	}
	if err := Remove(nonEmptyDir); err == nil {
		t.Fatal("Remove(non-empty dir) error = nil, want error")
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
