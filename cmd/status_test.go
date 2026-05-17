package cmd

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
	tsruntime "github.com/monody0007/tslink/internal/runtime"
)

type statusURLsJSONResponse struct {
	OK      bool             `json:"ok"`
	Command string           `json:"command"`
	Data    StatusURLsResult `json:"data"`
}

func addStatusTestService(t *testing.T, regPath string, svc registry.Service) registry.Service {
	t.Helper()
	if _, err := registry.Add(regPath, svc); err != nil {
		t.Fatalf("registry.Add: %v", err)
	}
	reg, err := registry.Load(regPath)
	if err != nil {
		t.Fatalf("registry.Load: %v", err)
	}
	for _, existing := range reg.Services {
		if existing.Name == svc.Name {
			return existing
		}
	}
	t.Fatalf("service %q not found after add", svc.Name)
	return registry.Service{}
}

func statusRegistryFingerprint(t *testing.T, regPath string) string {
	t.Helper()
	reg, err := registry.Load(regPath)
	if err != nil {
		t.Fatalf("registry.Load: %v", err)
	}
	fingerprint, err := tsruntime.RegistryFingerprint(reg)
	if err != nil {
		t.Fatalf("RegistryFingerprint: %v", err)
	}
	return fingerprint
}

func withStatusURLSeams(t *testing.T, running bool, pid int, lowerBound time.Time) {
	t.Helper()
	oldIsRunning := isRunningFn
	oldReadPID := readPIDFn
	oldGetAPIKey := getAPIKeyFn
	oldHasClientSecret := hasClientSecretFn
	oldPIDFileModTime := pidFileModTimeFn
	t.Cleanup(func() {
		isRunningFn = oldIsRunning
		readPIDFn = oldReadPID
		getAPIKeyFn = oldGetAPIKey
		hasClientSecretFn = oldHasClientSecret
		pidFileModTimeFn = oldPIDFileModTime
	})

	isRunningFn = func(string) bool { return running }
	readPIDFn = func(string) (int, error) { return pid, nil }
	getAPIKeyFn = func() (string, error) { return "", nil }
	hasClientSecretFn = func() bool { return false }
	pidFileModTimeFn = func(string) (time.Time, error) { return lowerBound, nil }
}

func findStatusService(t *testing.T, result StatusURLsResult, name string) StatusServiceView {
	t.Helper()
	for _, svc := range result.Services {
		if svc.Name == name {
			return svc
		}
	}
	t.Fatalf("service %q not found in status result", name)
	return StatusServiceView{}
}

func hasStatusWarningCode(warnings []inspect.WarningView, code string) bool {
	for _, warning := range warnings {
		if warning.Code == code {
			return true
		}
	}
	return false
}

func TestStatusURLsExactSnapshotUsesRuntimeEndpoint(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	pidPath := filepath.Join(dir, "tslink.pid")
	snapshotPath := filepath.Join(dir, "runtime.json")
	startedAt := time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)
	svc := addStatusTestService(t, regPath, registry.Service{
		Name:   "web",
		Type:   registry.TypeProxy,
		Target: "http://localhost:3000",
	})
	fingerprint := statusRegistryFingerprint(t, regPath)
	snapshot := tsruntime.NewSnapshot(4242, startedAt, fingerprint, startedAt.Add(time.Second), []tsruntime.ServiceState{
		{Service: svc, RuntimeHost: "web.tailnet.ts.net."},
	})
	if err := tsruntime.Save(snapshotPath, snapshot); err != nil {
		t.Fatalf("runtime.Save: %v", err)
	}
	withStatusURLSeams(t, true, 4242, startedAt)

	result, err := getStatusURLs(pidPath, regPath, snapshotPath)
	if err != nil {
		t.Fatalf("getStatusURLs: %v", err)
	}
	if !result.RuntimeSnapshot.Exact || result.RuntimeSnapshot.Status != tsruntime.StatusExact {
		t.Fatalf("runtime freshness = %+v, want exact", result.RuntimeSnapshot)
	}
	web := findStatusService(t, result, "web")
	if web.Endpoint.Display != "https://web.tailnet.ts.net" || web.Endpoint.State != inspect.EndpointStateExact {
		t.Fatalf("endpoint = %+v, want exact runtime URL", web.Endpoint)
	}
	if web.Exposure.Kind != inspect.ExposureTailnet {
		t.Fatalf("exposure = %+v, want tailnet", web.Exposure)
	}
}

func TestStatusURLsMissingSnapshotFallsBackToExpectedEndpoint(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	pidPath := filepath.Join(dir, "tslink.pid")
	snapshotPath := filepath.Join(dir, "runtime.json")
	startedAt := time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)
	addStatusTestService(t, regPath, registry.Service{
		Name:   "web",
		Type:   registry.TypeProxy,
		Target: "http://localhost:3000",
	})
	withStatusURLSeams(t, true, 4242, startedAt)

	result, err := getStatusURLs(pidPath, regPath, snapshotPath)
	if err != nil {
		t.Fatalf("getStatusURLs: %v", err)
	}
	if result.RuntimeSnapshot.Code != inspect.WarningCodeRuntimeSnapshotMissing {
		t.Fatalf("runtime snapshot = %+v, want missing code", result.RuntimeSnapshot)
	}
	web := findStatusService(t, result, "web")
	if web.Endpoint.Display != "https://web.<tailnet>.ts.net" || web.Endpoint.State != statusEndpointStateMissing {
		t.Fatalf("endpoint = %+v, want expected missing fallback", web.Endpoint)
	}
	if !hasStatusWarningCode(web.Warnings, inspect.WarningCodeRuntimeSnapshotMissing) {
		t.Fatalf("warnings = %+v, want runtime_snapshot_missing", web.Warnings)
	}
}

func TestStatusURLsStaleEvidenceFallsBackToExpectedEndpoint(t *testing.T) {
	cases := []struct {
		name            string
		snapshotPID     int
		snapshotStarted time.Time
		snapshotUpdated time.Time
		fingerprint     func(*testing.T, string) string
		wantStatus      string
	}{
		{
			name:            "registry fingerprint mismatch",
			snapshotPID:     4242,
			snapshotStarted: time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC),
			snapshotUpdated: time.Date(2026, 5, 17, 12, 0, 1, 0, time.UTC),
			fingerprint:     func(*testing.T, string) string { return "sha256:other" },
			wantStatus:      tsruntime.StatusRegistryMismatch,
		},
		{
			name:            "pid mismatch",
			snapshotPID:     9999,
			snapshotStarted: time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC),
			snapshotUpdated: time.Date(2026, 5, 17, 12, 0, 1, 0, time.UTC),
			fingerprint:     statusRegistryFingerprint,
			wantStatus:      tsruntime.StatusPIDMismatch,
		},
		{
			name:            "stale freshness",
			snapshotPID:     4242,
			snapshotStarted: time.Date(2026, 5, 17, 11, 59, 0, 0, time.UTC),
			snapshotUpdated: time.Date(2026, 5, 17, 11, 59, 30, 0, time.UTC),
			fingerprint:     statusRegistryFingerprint,
			wantStatus:      tsruntime.StatusStale,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			regPath := filepath.Join(dir, "registry.json")
			pidPath := filepath.Join(dir, "tslink.pid")
			snapshotPath := filepath.Join(dir, "runtime.json")
			lowerBound := time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)
			svc := addStatusTestService(t, regPath, registry.Service{
				Name:   "web",
				Type:   registry.TypeProxy,
				Target: "http://localhost:3000",
			})
			snapshot := tsruntime.NewSnapshot(tc.snapshotPID, tc.snapshotStarted, tc.fingerprint(t, regPath), tc.snapshotUpdated, []tsruntime.ServiceState{
				{Service: svc, RuntimeHost: "web.tailnet.ts.net"},
			})
			if err := tsruntime.Save(snapshotPath, snapshot); err != nil {
				t.Fatalf("runtime.Save: %v", err)
			}
			withStatusURLSeams(t, true, 4242, lowerBound)

			result, err := getStatusURLs(pidPath, regPath, snapshotPath)
			if err != nil {
				t.Fatalf("getStatusURLs: %v", err)
			}
			if result.RuntimeSnapshot.Status != tc.wantStatus || result.RuntimeSnapshot.Code != inspect.WarningCodeRuntimeSnapshotStale {
				t.Fatalf("runtime snapshot = %+v, want status %s stale code", result.RuntimeSnapshot, tc.wantStatus)
			}
			web := findStatusService(t, result, "web")
			if web.Endpoint.Display != "https://web.<tailnet>.ts.net" || web.Endpoint.State != statusEndpointStateStale {
				t.Fatalf("endpoint = %+v, want expected stale fallback", web.Endpoint)
			}
			if !hasStatusWarningCode(web.Warnings, inspect.WarningCodeRuntimeSnapshotStale) {
				t.Fatalf("warnings = %+v, want runtime_snapshot_stale", web.Warnings)
			}
		})
	}
}

func TestStatusURLsUnreadableSnapshotFallsBackToExpectedEndpoint(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	pidPath := filepath.Join(dir, "tslink.pid")
	snapshotPath := filepath.Join(dir, "runtime.json")
	startedAt := time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)
	addStatusTestService(t, regPath, registry.Service{
		Name:   "web",
		Type:   registry.TypeProxy,
		Target: "http://localhost:3000",
	})
	withStatusURLSeams(t, true, 4242, startedAt)
	oldLoad := runtimeLoadSnapshotFn
	runtimeLoadSnapshotFn = func(string) (*tsruntime.Snapshot, error) {
		return nil, &tsruntime.SnapshotError{
			Status: tsruntime.StatusUnreadable,
			Code:   inspect.WarningCodeRuntimeSnapshotUnreadable,
			Err:    os.ErrPermission,
		}
	}
	t.Cleanup(func() { runtimeLoadSnapshotFn = oldLoad })

	result, err := getStatusURLs(pidPath, regPath, snapshotPath)
	if err != nil {
		t.Fatalf("getStatusURLs: %v", err)
	}
	if result.RuntimeSnapshot.Code != inspect.WarningCodeRuntimeSnapshotUnreadable {
		t.Fatalf("runtime snapshot = %+v, want unreadable code", result.RuntimeSnapshot)
	}
	web := findStatusService(t, result, "web")
	if web.Endpoint.State != statusEndpointStateUnknown {
		t.Fatalf("endpoint = %+v, want unknown state", web.Endpoint)
	}
	if !hasStatusWarningCode(web.Warnings, inspect.WarningCodeRuntimeSnapshotUnreadable) {
		t.Fatalf("warnings = %+v, want runtime_snapshot_unreadable", web.Warnings)
	}
}

func TestStatusURLsTCPRendersRawHostPort(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	pidPath := filepath.Join(dir, "tslink.pid")
	snapshotPath := filepath.Join(dir, "runtime.json")
	startedAt := time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)
	svc := addStatusTestService(t, regPath, registry.Service{
		Name:   "db",
		Type:   registry.TypeTCP,
		Target: "localhost:5432",
		Port:   5432,
	})
	fingerprint := statusRegistryFingerprint(t, regPath)
	snapshot := tsruntime.NewSnapshot(4242, startedAt, fingerprint, startedAt.Add(time.Second), []tsruntime.ServiceState{
		{Service: svc, RuntimeHost: "db.tailnet.ts.net"},
	})
	if err := tsruntime.Save(snapshotPath, snapshot); err != nil {
		t.Fatalf("runtime.Save: %v", err)
	}
	withStatusURLSeams(t, true, 4242, startedAt)

	result, err := getStatusURLs(pidPath, regPath, snapshotPath)
	if err != nil {
		t.Fatalf("getStatusURLs: %v", err)
	}
	db := findStatusService(t, result, "db")
	if db.Endpoint.Display != "db.tailnet.ts.net:5432" || db.Endpoint.State != inspect.EndpointStateExact {
		t.Fatalf("endpoint = %+v, want raw exact TCP host:port", db.Endpoint)
	}
	if strings.Contains(db.Endpoint.Display, "https://") {
		t.Fatalf("TCP endpoint rendered as HTTPS: %+v", db.Endpoint)
	}
}

func TestStatusURLsJSONIncludesSchemaWarningsAndRedactedAllow(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	pidPath := filepath.Join(dir, "tslink.pid")
	snapshotPath := filepath.Join(dir, "runtime.json")
	addStatusTestService(t, regPath, registry.Service{
		Name:         "web",
		Type:         registry.TypeProxy,
		Target:       "http://localhost:3000",
		Tags:         []string{"tag:tsmain"},
		AllowedUsers: []string{"alice@example.com", "bob@example.com"},
	})
	withStatusURLSeams(t, false, 0, time.Time{})

	oldPIDPath := statusPIDPathFn
	oldRegistryPath := statusRegistryPathFn
	oldSnapshotPath := statusRuntimeSnapshotPathFn
	t.Cleanup(func() {
		statusPIDPathFn = oldPIDPath
		statusRegistryPathFn = oldRegistryPath
		statusRuntimeSnapshotPathFn = oldSnapshotPath
		rootCmd.SetArgs(nil)
		_ = rootCmd.PersistentFlags().Set("json", "false")
		_ = statusCmd.Flags().Set("urls", "false")
	})
	statusPIDPathFn = func() (string, error) { return pidPath, nil }
	statusRegistryPathFn = func() (string, error) { return regPath, nil }
	statusRuntimeSnapshotPathFn = func() (string, error) { return snapshotPath, nil }
	rootCmd.SetArgs([]string{"status", "--urls", "--json"})

	raw := captureStdout(t, func() {
		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("root execute: %v", err)
		}
	})
	var resp statusURLsJSONResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("unmarshal status --urls --json response: %v\nraw: %s", err, raw)
	}
	if !resp.OK || resp.Command != "status" {
		t.Fatalf("response envelope = %+v, want ok status", resp)
	}
	if resp.Data.SchemaVersion != inspect.SchemaVersion {
		t.Fatalf("schema_version = %q, want %q", resp.Data.SchemaVersion, inspect.SchemaVersion)
	}
	if resp.Data.RuntimeSnapshot.Code != inspect.WarningCodeRuntimeSnapshotMissing {
		t.Fatalf("runtime snapshot = %+v, want missing code", resp.Data.RuntimeSnapshot)
	}
	web := findStatusService(t, resp.Data, "web")
	if web.Allow.Count != 2 || !web.Allow.Redacted || len(web.Allow.Entries) != 0 {
		t.Fatalf("allow summary = %+v, want redacted count-only summary", web.Allow)
	}
	if !hasStatusWarningCode(web.Warnings, inspect.WarningCodeRuntimeSnapshotMissing) {
		t.Fatalf("warnings = %+v, want runtime_snapshot_missing", web.Warnings)
	}
}

func TestStatusURLsReturnsRegistryLoadError(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	pidPath := filepath.Join(dir, "tslink.pid")
	snapshotPath := filepath.Join(dir, "runtime.json")
	if err := os.WriteFile(regPath, []byte("{bad"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	withStatusURLSeams(t, false, 0, time.Time{})

	_, err := getStatusURLs(pidPath, regPath, snapshotPath)
	if err == nil {
		t.Fatal("getStatusURLs error = nil, want registry load error")
	}
	var syntaxErr *json.SyntaxError
	if !errors.As(err, &syntaxErr) {
		t.Fatalf("getStatusURLs error = %T %[1]v, want JSON syntax error", err)
	}
}
