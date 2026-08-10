package cmd

import (
	"bytes"
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

func TestPollableStatusZeroCredentialTransitionsFromNeedsLoginToAuthenticated(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	pidPath := filepath.Join(dir, "tslink.pid")
	snapshotPath := filepath.Join(dir, "runtime.json")
	handoffPath := filepath.Join(dir, "auth-handoff.json")
	startedAt := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	svc := addStatusTestService(t, regPath, registry.Service{
		Name:   "web",
		Type:   registry.TypeProxy,
		Target: "http://localhost:3000",
		Tags:   []string{"tag:tsmain"},
	})
	withStatusURLSeams(t, true, 4242, startedAt)

	oldLoadHandoff := statusLoadAuthHandoffFn
	statusLoadAuthHandoffFn = func(string) (authHandoffRecord, error) {
		return authHandoffRecord{
			SchemaVersion: authHandoffSchemaVersion,
			Status:        authStatusNeedsLogin,
			Service:       "web",
			AuthURL:       "https://login.tailscale.com/a/status-auth",
			ExpiresAt:     startedAt.Add(authHandoffConservativeLifetime),
			Poll:          "tslink status --json",
			DaemonPID:     4242,
		}, nil
	}
	t.Cleanup(func() { statusLoadAuthHandoffFn = oldLoadHandoff })

	needsLogin, err := getPollableStatus(pidPath, regPath, snapshotPath, handoffPath)
	if err != nil {
		t.Fatalf("getPollableStatus(needs_login): %v", err)
	}
	if needsLogin.Authenticated || needsLogin.AuthStatus != authStatusNeedsLogin {
		t.Fatalf("auth state = authenticated:%v status:%q, want needs_login", needsLogin.Authenticated, needsLogin.AuthStatus)
	}
	if needsLogin.AuthURL != "https://login.tailscale.com/a/status-auth" || needsLogin.ExpiresAt == nil {
		t.Fatalf("auth handoff = url:%q expiry:%v, want pollable URL and expiry", needsLogin.AuthURL, needsLogin.ExpiresAt)
	}
	if len(needsLogin.Services) != 1 || needsLogin.Services[0].Name != "web" || needsLogin.Services[0].Status != authStatusNeedsLogin {
		t.Fatalf("services = %+v, want web needs_login", needsLogin.Services)
	}

	fingerprint := statusRegistryFingerprint(t, regPath)
	snapshot := tsruntime.NewSnapshot(4242, startedAt, fingerprint, startedAt.Add(time.Second), []tsruntime.ServiceState{
		{Service: svc, RuntimeHost: "web.tailnet.ts.net"},
	})
	if err := tsruntime.Save(snapshotPath, snapshot); err != nil {
		t.Fatalf("runtime.Save: %v", err)
	}
	authenticated, err := getPollableStatus(pidPath, regPath, snapshotPath, handoffPath)
	if err != nil {
		t.Fatalf("getPollableStatus(authenticated): %v", err)
	}
	if !authenticated.Authenticated || authenticated.AuthStatus != authStatusAuthenticated {
		t.Fatalf("auth state = authenticated:%v status:%q, want authenticated", authenticated.Authenticated, authenticated.AuthStatus)
	}
	if authenticated.AuthURL != "" || authenticated.ExpiresAt != nil {
		t.Fatalf("completed auth leaked stale handoff: url:%q expiry:%v", authenticated.AuthURL, authenticated.ExpiresAt)
	}
	if len(authenticated.Services) != 1 || authenticated.Services[0].Status != "up" {
		t.Fatalf("services = %+v, want web up", authenticated.Services)
	}
}

func TestPollableStatusShowsEarlierServicesWhileNextNeedsLogin(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	pidPath := filepath.Join(dir, "tslink.pid")
	snapshotPath := filepath.Join(dir, "runtime.json")
	handoffPath := filepath.Join(dir, "auth-handoff.json")
	startedAt := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	first := addStatusTestService(t, regPath, registry.Service{
		Name: "first", Type: registry.TypeFile, Path: t.TempDir(),
	})
	addStatusTestService(t, regPath, registry.Service{
		Name: "second", Type: registry.TypeFile, Path: t.TempDir(),
	})
	withStatusURLSeams(t, true, 4242, startedAt)

	fingerprint := statusRegistryFingerprint(t, regPath)
	snapshot := tsruntime.NewSnapshot(4242, startedAt, fingerprint, startedAt.Add(time.Second), []tsruntime.ServiceState{
		{Service: first, RuntimeHost: "first.tailnet.ts.net"},
	})
	if err := tsruntime.Save(snapshotPath, snapshot); err != nil {
		t.Fatalf("runtime.Save: %v", err)
	}

	oldLoadHandoff := statusLoadAuthHandoffFn
	statusLoadAuthHandoffFn = func(string) (authHandoffRecord, error) {
		return authHandoffRecord{
			SchemaVersion: authHandoffSchemaVersion,
			Status:        authStatusNeedsLogin,
			Service:       "second",
			AuthURL:       "https://login.tailscale.com/a/second-auth",
			ExpiresAt:     startedAt.Add(authHandoffConservativeLifetime),
			Poll:          "tslink status --json",
			DaemonPID:     4242,
		}, nil
	}
	t.Cleanup(func() { statusLoadAuthHandoffFn = oldLoadHandoff })

	result, err := getPollableStatus(pidPath, regPath, snapshotPath, handoffPath)
	if err != nil {
		t.Fatalf("getPollableStatus: %v", err)
	}
	if result.Authenticated || result.AuthStatus != authStatusNeedsLogin || result.AuthURL == "" {
		t.Fatalf("auth state = authenticated:%v status:%q url:%q, want second service handoff", result.Authenticated, result.AuthStatus, result.AuthURL)
	}
	want := map[string]string{"first": "up", "second": authStatusNeedsLogin}
	for _, service := range result.Services {
		if service.Status != want[service.Name] {
			t.Fatalf("service %q status = %q, want %q (all=%+v)", service.Name, service.Status, want[service.Name], result.Services)
		}
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
	if web.Endpoint.Display != "" || web.Endpoint.Host != "" || web.Endpoint.State != statusEndpointStateMissing {
		t.Fatalf("endpoint = %+v, want missing state without placeholder", web.Endpoint)
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
			if web.Endpoint.Display != "" || web.Endpoint.Host != "" || web.Endpoint.State != statusEndpointStateStale {
				t.Fatalf("endpoint = %+v, want stale state without placeholder", web.Endpoint)
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

func TestFormatStatusURLsHumanOutput(t *testing.T) {
	var buf bytes.Buffer
	formatStatusURLs(StatusURLsResult{
		DaemonRunning: true,
		DaemonPID:     4242,
		Authenticated: true,
		ServiceCount:  2,
		RuntimeSnapshot: StatusRuntimeSnapshotResult{
			Status: tsruntime.StatusStale,
			Code:   inspect.WarningCodeRuntimeSnapshotStale,
		},
		Services: []StatusServiceView{
			{
				Name: "web",
				Type: registry.TypeProxy,
				Endpoint: inspect.EndpointView{
					Display: "https://web.tailnet.ts.net",
					State:   inspect.EndpointStateExact,
				},
				Exposure: inspect.ExposureView{Kind: inspect.ExposureTailnetAllow},
				Allow:    inspect.SummaryView{Mode: "restricted", Count: 2, Redacted: true},
				Tags:     inspect.SummaryView{Mode: "configured", Entries: []string{"tag:web"}},
				Backend:  inspect.BackendView{Display: "http://localhost:3000"},
				Warnings: []inspect.WarningView{{Code: inspect.WarningCodeRuntimeSnapshotStale}},
			},
			{
				Name:     "docs",
				Type:     registry.TypeFile,
				Endpoint: inspect.EndpointView{},
				Exposure: inspect.ExposureView{},
				Allow:    inspect.SummaryView{Count: 0},
				Tags:     inspect.SummaryView{Mode: "configured"},
				Backend:  inspect.BackendView{},
			},
		},
	}, &buf)

	raw := buf.String()
	for _, want := range []string{
		"tslink: running (pid 4242)",
		"tailnet: authenticated",
		"services: 2 registered",
		"runtime snapshot: stale (runtime_snapshot_stale)",
		"NAME",
		"web",
		"restricted(2 redacted)",
		"configured:tag:web",
		inspect.WarningCodeRuntimeSnapshotStale,
		"docs",
	} {
		if !strings.Contains(raw, want) {
			t.Fatalf("formatStatusURLs output missing %q:\n%s", want, raw)
		}
	}
}

func TestFormatStatusURLsNoServices(t *testing.T) {
	var buf bytes.Buffer
	formatStatusURLs(StatusURLsResult{
		RuntimeSnapshot: StatusRuntimeSnapshotResult{Status: tsruntime.StatusMissing},
	}, &buf)

	raw := buf.String()
	if !strings.Contains(raw, "runtime snapshot: missing") || !strings.Contains(raw, "service urls: none") {
		t.Fatalf("formatStatusURLs no-services output = %q, want runtime snapshot and none marker", raw)
	}
}

func TestStatusSummaryLabelAndWarningCodes(t *testing.T) {
	summaryCases := []struct {
		name string
		in   inspect.SummaryView
		want string
	}{
		{name: "redacted without mode", in: inspect.SummaryView{Count: 2, Redacted: true}, want: "2 redacted"},
		{name: "redacted with mode", in: inspect.SummaryView{Mode: "restricted", Count: 2, Redacted: true}, want: "restricted(2 redacted)"},
		{name: "entries without mode", in: inspect.SummaryView{Entries: []string{"a", "b"}}, want: "configured:a,b"},
		{name: "entries with mode", in: inspect.SummaryView{Mode: "tags", Entries: []string{"tag:a"}}, want: "tags:tag:a"},
		{name: "mode only", in: inspect.SummaryView{Mode: "all_tailnet"}, want: "all_tailnet"},
		{name: "count only", in: inspect.SummaryView{Count: 3}, want: "3"},
	}
	for _, tc := range summaryCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := summaryLabel(tc.in); got != tc.want {
				t.Fatalf("summaryLabel(%+v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}

	if got := warningCodes(nil); got != "-" {
		t.Fatalf("warningCodes(nil) = %q, want -", got)
	}
	warnings := []inspect.WarningView{
		{Code: inspect.WarningCodeRuntimeSnapshotMissing},
		{Code: inspect.WarningCodeTCPHTTPACLNotApplicable},
	}
	if got := warningCodes(warnings); got != inspect.WarningCodeRuntimeSnapshotMissing+","+inspect.WarningCodeTCPHTTPACLNotApplicable {
		t.Fatalf("warningCodes() = %q, want joined codes", got)
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

func TestFilterStatusURLsResultByName(t *testing.T) {
	input := StatusURLsResult{
		ServiceCount: 2,
		Services: []StatusServiceView{
			{Name: "web", Type: registry.TypeProxy},
			{Name: "db", Type: registry.TypeTCP},
		},
	}
	got, err := filterStatusURLsResult(input, "db")
	if err != nil {
		t.Fatalf("filterStatusURLsResult: %v", err)
	}
	if got.ServiceCount != 1 || len(got.Services) != 1 || got.Services[0].Name != "db" {
		t.Fatalf("filtered result = %+v, want db only", got)
	}
	if _, err := filterStatusURLsResult(input, "missing"); err == nil {
		t.Fatal("missing name error = nil")
	}
	if _, err := filterStatusURLsResult(input, "BAD_NAME"); err == nil {
		t.Fatal("invalid name error = nil")
	}
}
