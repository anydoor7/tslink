package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/credentials"
	"github.com/anydoor7/tslink/internal/output"
	"github.com/anydoor7/tslink/internal/registry"
	"github.com/anydoor7/tslink/internal/tailapi"
	"github.com/anydoor7/tslink/internal/testenv"
)

// captureStdout captures output written to os.Stdout during fn execution.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	var readErr error
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		defer r.Close()
		_, readErr = io.Copy(&buf, r)
	}()
	os.Stdout = w
	defer func() {
		os.Stdout = old
		w.Close()
		<-drained
	}()
	fn()
	w.Close()
	<-drained
	if readErr != nil {
		t.Fatal(readErr)
	}
	return buf.String()
}

// parseResult parses a JSON line into an output.Result with raw Data.
func parseResult(t *testing.T, s string) output.Result {
	t.Helper()
	var r output.Result
	if err := json.Unmarshal([]byte(s), &r); err != nil {
		t.Fatalf("failed to parse JSON result: %v\nraw: %s", err, s)
	}
	return r
}

// dataMap extracts the Data field of a Result as map[string]any.
func dataMap(t *testing.T, s string) map[string]any {
	t.Helper()
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(s), &raw); err != nil {
		t.Fatalf("failed to parse JSON: %v\nraw: %s", err, s)
	}
	var data map[string]any
	if err := json.Unmarshal(raw["data"], &data); err != nil {
		t.Fatalf("failed to parse data field: %v\nraw data: %s", err, string(raw["data"]))
	}
	return data
}

func TestWasJSONRequestedReadsPersistentFlag(t *testing.T) {
	setRootJSONFlag(t, false)
	if WasJSONRequested() {
		t.Fatal("WasJSONRequested() = true, want false")
	}

	setRootJSONFlag(t, true)
	if !WasJSONRequested() {
		t.Fatal("WasJSONRequested() = false, want true")
	}
}

// --- Status JSON ---

func TestStatusJSON(t *testing.T) {
	oldIsRunning, oldReadPID, oldGetKey := isRunningFn, readPIDFn, getAPIKeyFn
	oldCS := hasClientSecretFn
	t.Cleanup(func() {
		isRunningFn, readPIDFn, getAPIKeyFn = oldIsRunning, oldReadPID, oldGetKey
		hasClientSecretFn = oldCS
	})

	isRunningFn = func(string) bool { return true }
	readPIDFn = func(string) (int, error) { return 42, nil }
	getAPIKeyFn = func() (string, error) { return "tskey-api-<test-only-xxx>", nil }
	hasClientSecretFn = func() bool { return false }

	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	_, _ = registry.Add(regPath, registry.Service{Name: "a", Type: registry.TypeProxy, Target: "http://localhost:3000"})

	r, err := getStatus(context.Background(), filepath.Join(dir, "pid"), regPath)
	if err != nil {
		t.Fatalf("getStatus(context.Background(), ) error = %v", err)
	}

	got := captureStdout(t, func() {
		output.Success("status", r)
	})

	res := parseResult(t, got)
	if !res.OK {
		t.Error("expected ok=true")
	}
	if res.Command != "status" {
		t.Errorf("expected command=status, got %s", res.Command)
	}
	if res.Code != 0 {
		t.Errorf("expected code=0, got %d", res.Code)
	}

	data := dataMap(t, got)
	if data["daemon_running"] != true {
		t.Errorf("expected daemon_running=true, got %v", data["daemon_running"])
	}
	if data["daemon_pid"] != float64(42) {
		t.Errorf("expected daemon_pid=42, got %v", data["daemon_pid"])
	}
	// A stored credential without an authorized node (A3-5).
	if data["credential_stored"] != true || data["authenticated"] != false {
		t.Errorf("expected credential_stored=true authenticated=false, got %v and %v", data["credential_stored"], data["authenticated"])
	}
	if data["service_count"] != float64(1) {
		t.Errorf("expected service_count=1, got %v", data["service_count"])
	}
}

// --- List JSON ---

func TestListJSON_Empty(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	// Create empty registry
	os.WriteFile(regPath, []byte(`{"services":[]}`), 0o600)

	oldRegPath := registryPathFn
	t.Cleanup(func() { registryPathFn = oldRegPath })
	registryPathFn = func() (string, error) { return regPath, nil }

	reg, _ := registry.Load(regPath)
	result := ListResult{Services: reg.Services, Count: len(reg.Services)}

	got := captureStdout(t, func() {
		output.Success("list", result)
	})

	res := parseResult(t, got)
	if !res.OK {
		t.Error("expected ok=true")
	}

	data := dataMap(t, got)
	if data["count"] != float64(0) {
		t.Errorf("expected count=0, got %v", data["count"])
	}
}

func TestListJSON_WithServices(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	docsDir := t.TempDir()
	_, _ = registry.Add(regPath, registry.Service{Name: "web", Type: registry.TypeProxy, Target: "http://localhost:3000"})
	_, _ = registry.Add(regPath, registry.Service{Name: "docs", Type: registry.TypeFile, Path: docsDir})

	reg, _ := registry.Load(regPath)
	result := ListResult{Services: reg.Services, Count: len(reg.Services)}

	got := captureStdout(t, func() {
		output.Success("list", result)
	})

	data := dataMap(t, got)
	if data["count"] != float64(2) {
		t.Errorf("expected count=2, got %v", data["count"])
	}
	services, ok := data["services"].([]any)
	if !ok || len(services) != 2 {
		t.Fatalf("expected 2 services, got %v", data["services"])
	}
	first := services[0].(map[string]any)
	if first["name"] != "web" {
		t.Errorf("expected first service name=web, got %v", first["name"])
	}
}

// --- Stop JSON ---

func TestStopJSON_NotRunning(t *testing.T) {
	oldIsRunning, oldRemove := isRunningFn, removePIDFn
	t.Cleanup(func() { isRunningFn = oldIsRunning; removePIDFn = oldRemove })

	isRunningFn = func(string) bool { return false }
	removePIDFn = func(string) {}

	var buf bytes.Buffer
	got := captureStdout(t, func() {
		if err := stopService("/fake/pid", true, &buf); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	res := parseResult(t, got)
	if !res.OK {
		t.Error("expected ok=true")
	}
	if res.Command != "stop" {
		t.Errorf("expected command=stop, got %s", res.Command)
	}

	data := dataMap(t, got)
	if data["was_running"] != false {
		t.Errorf("expected was_running=false, got %v", data["was_running"])
	}
	if data["stopped"] != false {
		t.Errorf("expected stopped=false, got %v", data["stopped"])
	}
}

func TestStopJSON_Running(t *testing.T) {
	oldIsRunning, oldStop, oldRemove := isRunningFn, stopDaemonFn, removePIDFn
	t.Cleanup(func() {
		isRunningFn, stopDaemonFn, removePIDFn = oldIsRunning, oldStop, oldRemove
	})

	isRunningFn = func(string) bool { return true }
	stopDaemonFn = func(string) error { return nil }
	removePIDFn = func(string) {}

	var buf bytes.Buffer
	got := captureStdout(t, func() {
		if err := stopService("/fake/pid", true, &buf); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	data := dataMap(t, got)
	if data["was_running"] != true {
		t.Errorf("expected was_running=true, got %v", data["was_running"])
	}
	if data["stopped"] != true {
		t.Errorf("expected stopped=true, got %v", data["stopped"])
	}
}

// --- Logout JSON ---

func TestLogoutJSON_NotLoggedIn(t *testing.T) {
	dir := t.TempDir()
	oldInspect := inspectStoredCredentialsFn
	oldDelete := deleteStoredCredentialsFn
	t.Cleanup(func() {
		inspectStoredCredentialsFn = oldInspect
		deleteStoredCredentialsFn = oldDelete
	})

	inspectStoredCredentialsFn = func() (credentials.StoredCredentialStatus, error) {
		return credentials.StoredCredentialStatus{}, nil
	}
	deleteStoredCredentialsFn = func() error {
		t.Fatal("deleteStoredCredentialsFn must not run for a proved empty logout")
		return nil
	}

	// isRunningFn needs to return false so logout doesn't error
	oldIsRunning := isRunningFn
	t.Cleanup(func() { isRunningFn = oldIsRunning })
	isRunningFn = func(string) bool { return false }

	var buf bytes.Buffer
	got := captureStdout(t, func() {
		err := logoutUser(
			filepath.Join(dir, "pid"),
			filepath.Join(dir, "authkey"),
			filepath.Join(dir, "nodes"),
			dir,
			true,
			&buf,
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	res := parseResult(t, got)
	if !res.OK {
		t.Error("expected ok=true")
	}
	if res.Command != "logout" {
		t.Errorf("expected command=logout, got %s", res.Command)
	}

	data := dataMap(t, got)
	if data["was_logged_in"] != false {
		t.Errorf("expected was_logged_in=false, got %v", data["was_logged_in"])
	}
}

func TestLogoutJSON_LoggedIn(t *testing.T) {
	dir := t.TempDir()
	authKeyPath := filepath.Join(dir, "authkey")
	nodesDir := filepath.Join(dir, "nodes")
	os.WriteFile(authKeyPath, []byte("key"), 0o600)
	os.MkdirAll(nodesDir, 0o700)

	oldInspect := inspectStoredCredentialsFn
	oldDelete := deleteStoredCredentialsFn
	oldIsRunning := isRunningFn
	t.Cleanup(func() {
		inspectStoredCredentialsFn = oldInspect
		deleteStoredCredentialsFn = oldDelete
		isRunningFn = oldIsRunning
	})

	apiKey := "tskey-api-<test-only-xxx>"
	clientSecret := ""
	inspectStoredCredentialsFn = func() (credentials.StoredCredentialStatus, error) {
		return logoutCredentialStatus(apiKey != "", clientSecret != ""), nil
	}
	deleteStoredCredentialsFn = func() error {
		apiKey = ""
		clientSecret = ""
		return nil
	}
	isRunningFn = func(string) bool { return false }

	var buf bytes.Buffer
	got := captureStdout(t, func() {
		err := logoutUser(
			filepath.Join(dir, "pid"),
			authKeyPath,
			nodesDir,
			dir,
			true,
			&buf,
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	data := dataMap(t, got)
	if data["was_logged_in"] != true {
		t.Errorf("expected was_logged_in=true, got %v", data["was_logged_in"])
	}
}

func TestLogoutJSONCredentialResidualRiskReturnsNoSuccessEnvelope(t *testing.T) {
	dir := t.TempDir()
	oldInspect := inspectStoredCredentialsFn
	oldDelete := deleteStoredCredentialsFn
	oldIsRunning := isRunningFn
	t.Cleanup(func() {
		inspectStoredCredentialsFn = oldInspect
		deleteStoredCredentialsFn = oldDelete
		isRunningFn = oldIsRunning
	})
	inspectStoredCredentialsFn = func() (credentials.StoredCredentialStatus, error) {
		return credentials.StoredCredentialStatus{}, os.ErrPermission
	}
	deleteStoredCredentialsFn = func() error { return os.ErrPermission }
	isRunningFn = func(string) bool { return false }

	var buf bytes.Buffer
	got := captureStdout(t, func() {
		err := logoutUser(
			filepath.Join(dir, "pid"),
			filepath.Join(dir, "authkey"),
			filepath.Join(dir, "nodes"),
			dir,
			true,
			&buf,
		)
		if err == nil {
			t.Fatal("logoutUser() error = nil, want residual credential risk")
		}
		if output.ExitCode(err) == output.ExitSuccess {
			t.Fatalf("ExitCode() = success for residual credential risk: %v", err)
		}
	})
	if got != "" {
		t.Fatalf("logoutUser emitted success JSON despite residual credential risk: %s", got)
	}
}

// --- Remove JSON ---

func TestRemoveJSON_Success(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	_, _ = registry.Add(regPath, registry.Service{Name: "web", Type: registry.TypeProxy, Target: "http://localhost:3000"})

	oldDelete := deleteDevicesFn
	t.Cleanup(func() { deleteDevicesFn = oldDelete })
	deleteDevicesFn = func(ctx context.Context, target tailapi.CleanupTarget) (tailapi.CleanupResult, error) {
		return tailapi.CleanupResult{Deleted: []string{target.Hostname}}, nil
	}

	var out, errOut bytes.Buffer
	got := captureStdout(t, func() {
		if err := removeService(regPath, testOwnershipPath(regPath), "web", &out, &errOut, true); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	res := parseResult(t, got)
	if !res.OK {
		t.Error("expected ok=true")
	}
	if res.Command != "remove" {
		t.Errorf("expected command=remove, got %s", res.Command)
	}

	data := dataMap(t, got)
	if data["name"] != "web" {
		t.Errorf("expected name=web, got %v", data["name"])
	}
	if data["removed"] != true {
		t.Errorf("expected removed=true, got %v", data["removed"])
	}
	if data["device_cleaned"] != true {
		t.Errorf("expected device_cleaned=true, got %v", data["device_cleaned"])
	}
}

func TestRemoveJSON_NotFound(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	os.WriteFile(regPath, []byte(`{"services":[]}`), 0o600)

	var out, errOut bytes.Buffer
	got := captureStdout(t, func() {
		if err := removeService(regPath, testOwnershipPath(regPath), "nonexistent", &out, &errOut, true); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	res := parseResult(t, got)
	if !res.OK {
		t.Error("expected ok=true")
	}
	data := dataMap(t, got)
	if data["name"] != "nonexistent" {
		t.Errorf("expected name=nonexistent, got %v", data["name"])
	}
	if data["removed"] != false {
		t.Errorf("expected removed=false, got %v", data["removed"])
	}
}

func TestRemoveJSON_DeviceCleanupSkipped(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	_, _ = registry.Add(regPath, registry.Service{
		Name:   "web",
		Type:   registry.TypeProxy,
		Target: "http://localhost:3000",
		Tags:   []string{"tag:tsmain"},
	})

	oldDelete := deleteDevicesFn
	t.Cleanup(func() { deleteDevicesFn = oldDelete })
	deleteDevicesFn = func(ctx context.Context, target tailapi.CleanupTarget) (tailapi.CleanupResult, error) {
		return tailapi.CleanupResult{
			Matched:    []string{target.Hostname},
			Protected:  []string{target.Hostname},
			Skipped:    true,
			SkipReason: "ownership could not be proven",
		}, nil
	}

	var out, errOut bytes.Buffer
	got := captureStdout(t, func() {
		if err := removeService(regPath, testOwnershipPath(regPath), "web", &out, &errOut, true); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	data := dataMap(t, got)
	if data["device_cleanup_skipped"] != true {
		t.Errorf("expected device_cleanup_skipped=true, got %v", data["device_cleanup_skipped"])
	}
	if data["device_skip_reason"] != "ownership could not be proven" {
		t.Errorf("expected device_skip_reason, got %v", data["device_skip_reason"])
	}
}

func TestRemoveJSON_NoAPIClientCleanupSkipped(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	_, _ = registry.Add(regPath, registry.Service{
		Name:   "web",
		Type:   registry.TypeProxy,
		Target: "http://localhost:3000",
		Tags:   []string{"tag:tsmain"},
	})

	oldDelete := deleteDevicesFn
	t.Cleanup(func() { deleteDevicesFn = oldDelete })
	deleteDevicesFn = func(ctx context.Context, target tailapi.CleanupTarget) (tailapi.CleanupResult, error) {
		return tailapi.CleanupResult{
			Skipped:    true,
			SkipReason: tailapi.ErrNoAPIClient.Error(),
		}, nil
	}

	var out, errOut bytes.Buffer
	got := captureStdout(t, func() {
		if err := removeService(regPath, testOwnershipPath(regPath), "web", &out, &errOut, true); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	data := dataMap(t, got)
	if data["device_cleanup_skipped"] != true {
		t.Errorf("expected device_cleanup_skipped=true, got %v", data["device_cleanup_skipped"])
	}
	if data["device_skip_reason"] != tailapi.ErrNoAPIClient.Error() {
		t.Errorf("expected no-client skip reason, got %v", data["device_skip_reason"])
	}
}

// --- Add JSON ---

func TestAddJSON(t *testing.T) {
	resetRootJSONFlag(t)
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")

	oldRegPath := registryPathFn
	oldEnsureDir := ensureDirFn
	t.Cleanup(func() { registryPathFn = oldRegPath; ensureDirFn = oldEnsureDir })
	registryPathFn = func() (string, error) { return regPath, nil }
	ensureDirFn = func() error { return nil }

	addCmd, _, err := rootCmd.Find([]string{"add"})
	if err != nil {
		t.Fatalf("find add command: %v", err)
	}

	// First add: created=true
	resetCommandLocalFlags(t, addCmd)
	rootCmd.SetArgs([]string{"add", "jsonapp", "--proxy", "localhost:3000", "--json"})
	got := captureStdout(t, func() {
		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	res := parseResult(t, got)
	if !res.OK {
		t.Error("expected ok=true")
	}
	if res.Command != "add" {
		t.Errorf("expected command=add, got %s", res.Command)
	}

	data := dataMap(t, got)
	if data["created"] != true {
		t.Errorf("expected created=true, got %v", data["created"])
	}
	if data["name"] != "jsonapp" {
		t.Errorf("expected name=jsonapp, got %v", data["name"])
	}

	// Second add: created=false (already exists)
	resetCommandLocalFlags(t, addCmd)
	rootCmd.SetArgs([]string{"add", "jsonapp", "--proxy", "localhost:3000", "--json"})
	got2 := captureStdout(t, func() {
		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	data2 := dataMap(t, got2)
	if data2["created"] != false {
		t.Errorf("expected created=false on re-add, got %v", data2["created"])
	}
}

// --- Config JSON ---

func TestConfigSetJSON(t *testing.T) {
	testenv.SetHome(t, t.TempDir())

	var buf bytes.Buffer
	got := captureStdout(t, func() {
		if err := configSet("control-url", "https://example.com", &buf, true); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	res := parseResult(t, got)
	if !res.OK {
		t.Error("expected ok=true")
	}
	if res.Command != "config set" {
		t.Errorf("expected command='config set', got %s", res.Command)
	}

	data := dataMap(t, got)
	if data["key"] != "control-url" {
		t.Errorf("expected key=control-url, got %v", data["key"])
	}
	if data["value"] != "https://example.com" {
		t.Errorf("expected value=https://example.com, got %v", data["value"])
	}
	if data["cleared"] != false {
		t.Errorf("expected cleared=false, got %v", data["cleared"])
	}
}

func TestConfigGetJSON(t *testing.T) {
	testenv.SetHome(t, t.TempDir())

	var buf bytes.Buffer
	got := captureStdout(t, func() {
		if err := configGet("control-url", &buf, true); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	res := parseResult(t, got)
	if !res.OK {
		t.Error("expected ok=true")
	}
	if res.Command != "config get" {
		t.Errorf("expected command='config get', got %s", res.Command)
	}

	data := dataMap(t, got)
	if data["key"] != "control-url" {
		t.Errorf("expected key=control-url, got %v", data["key"])
	}
	if data["is_set"] != false {
		t.Errorf("expected is_set=false for unset value, got %v", data["is_set"])
	}
}

func TestConfigListJSON(t *testing.T) {
	testenv.SetHome(t, t.TempDir())

	var buf bytes.Buffer
	got := captureStdout(t, func() {
		if err := configList(&buf, true); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	res := parseResult(t, got)
	if !res.OK {
		t.Error("expected ok=true")
	}
	if res.Command != "config list" {
		t.Errorf("expected command='config list', got %s", res.Command)
	}

	data := dataMap(t, got)
	items, ok := data["items"].([]any)
	if !ok || len(items) == 0 {
		t.Fatalf("expected items array, got %v", data["items"])
	}
	first := items[0].(map[string]any)
	if first["key"] != "control-url" {
		t.Errorf("expected first item key=control-url, got %v", first["key"])
	}
}

// --- Tags JSON ---

func TestTagsListJSON(t *testing.T) {
	setTagsMocks(t)
	mockDefaults()
	mockRegistryWithServices([]registry.Service{
		{Name: "myapp", Tags: []string{"tag:tsmain"}},
		{Name: "dashboard", Tags: []string{"tag:tsmain", "tag:shared"}},
	})

	var buf bytes.Buffer
	got := captureStdout(t, func() {
		if err := tagsListRun(&buf, true); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	res := parseResult(t, got)
	if !res.OK {
		t.Error("expected ok=true")
	}
	if res.Command != "tags list" {
		t.Errorf("expected command='tags list', got %s", res.Command)
	}

	data := dataMap(t, got)
	services, ok := data["services"].([]any)
	if !ok || len(services) != 2 {
		t.Fatalf("expected 2 services, got %v", data["services"])
	}
	first := services[0].(map[string]any)
	if first["name"] != "myapp" {
		t.Errorf("expected first service name=myapp, got %v", first["name"])
	}
}

func TestTagsAddJSON_NotFound(t *testing.T) {
	setTagsMocks(t)
	mockDefaults()
	mockRegistryWithServices(nil)

	err := tagsAddRun(&bytes.Buffer{}, "nonexistent", "tag:test", true)
	if err == nil {
		t.Fatal("expected error for nonexistent service")
	}
	ce, ok := err.(*output.CodeError)
	if !ok {
		t.Fatalf("expected *output.CodeError, got %T: %v", err, err)
	}
	if ce.Code != output.ExitNotFound {
		t.Errorf("expected exit code %d, got %d", output.ExitNotFound, ce.Code)
	}
}

func TestTagsAddJSON_Success(t *testing.T) {
	setTagsMocks(t)
	mockDefaults()
	mockRegistryWithServices([]registry.Service{
		{Name: "myapp", Tags: []string{"tag:tsmain"}},
	})
	var buf bytes.Buffer
	got := captureStdout(t, func() {
		if err := tagsAddRun(&buf, "myapp", "tag:shared", true); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	data := dataMap(t, got)
	if data["service"] != "myapp" {
		t.Errorf("expected service=myapp, got %v", data["service"])
	}
	if data["tag"] != "tag:shared" {
		t.Errorf("expected tag=tag:shared, got %v", data["tag"])
	}
	if data["already_existed"] != false {
		t.Errorf("expected already_existed=false, got %v", data["already_existed"])
	}
}

func TestTagsAddJSON_Duplicate(t *testing.T) {
	setTagsMocks(t)
	mockDefaults()
	mockRegistryWithServices([]registry.Service{
		{Name: "myapp", Tags: []string{"tag:tsmain", "tag:shared"}},
	})

	var buf bytes.Buffer
	got := captureStdout(t, func() {
		if err := tagsAddRun(&buf, "myapp", "tag:shared", true); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	data := dataMap(t, got)
	if data["already_existed"] != true {
		t.Errorf("expected already_existed=true, got %v", data["already_existed"])
	}
}

func TestTagsSetJSON(t *testing.T) {
	setTagsMocks(t)
	mockDefaults()
	mockRegistryWithServices([]registry.Service{
		{Name: "myapp", Tags: []string{"tag:tsmain", "tag:old"}},
	})
	var buf bytes.Buffer
	got := captureStdout(t, func() {
		if err := tagsSetRun(&buf, "myapp", "tag:shared", true); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	res := parseResult(t, got)
	if res.Command != "tags set" {
		t.Errorf("expected command='tags set', got %s", res.Command)
	}

	data := dataMap(t, got)
	if data["service"] != "myapp" {
		t.Errorf("expected service=myapp, got %v", data["service"])
	}
	tags, ok := data["tags"].([]any)
	if !ok || len(tags) != 1 || tags[0] != "tag:shared" {
		t.Errorf("expected tags=[tag:shared], got %v", data["tags"])
	}
}

func TestTagsSetJSON_NotFound(t *testing.T) {
	setTagsMocks(t)
	mockDefaults()
	mockRegistryWithServices(nil)

	err := tagsSetRun(&bytes.Buffer{}, "nosvc", "tag:test", true)
	if err == nil {
		t.Fatal("expected error for nonexistent service")
	}
	ce, ok := err.(*output.CodeError)
	if !ok {
		t.Fatalf("expected *output.CodeError, got %T: %v", err, err)
	}
	if ce.Code != output.ExitNotFound {
		t.Errorf("expected exit code %d, got %d", output.ExitNotFound, ce.Code)
	}
}

func TestTagsSetDefaultJSON(t *testing.T) {
	setTagsMocks(t)
	testenv.SetHome(t, t.TempDir())

	tagsUpdateGlobalFn = func(mutate func(*config.GlobalConfig) error) error {
		cfg := config.GlobalConfig{}
		return mutate(&cfg)
	}

	var buf bytes.Buffer
	got := captureStdout(t, func() {
		if err := tagsSetDefaultRun(&buf, "tag:new", true); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	res := parseResult(t, got)
	if res.Command != "tags set-default" {
		t.Errorf("expected command='tags set-default', got %s", res.Command)
	}

	data := dataMap(t, got)
	if data["tag"] != "tag:new" {
		t.Errorf("expected tag=tag:new, got %v", data["tag"])
	}
}

func TestTagsPullJSON(t *testing.T) {
	setTagsMocks(t)
	mockDefaults()
	tagsReadTagsFn = func(ctx context.Context) ([]string, error) {
		return []string{"tag:tsmain", "tag:shared"}, nil
	}

	var buf bytes.Buffer
	got := captureStdout(t, func() {
		if err := tagsPullRun(context.Background(), &buf, true); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	res := parseResult(t, got)
	if res.Command != "tags pull" {
		t.Errorf("expected command='tags pull', got %s", res.Command)
	}

	data := dataMap(t, got)
	tags, ok := data["tags"].([]any)
	if !ok || len(tags) != 2 {
		t.Fatalf("expected 2 tags, got %v", data["tags"])
	}
	if data["default_tag"] != "tag:tsmain" {
		t.Errorf("expected default_tag=tag:tsmain, got %v", data["default_tag"])
	}
}

func TestTagsPullJSON_NoAPIClientSkipped(t *testing.T) {
	setTagsMocks(t)
	mockDefaults()
	tagsReadTagsFn = func(ctx context.Context) ([]string, error) {
		return nil, tailapi.ErrNoAPIClient
	}

	var buf bytes.Buffer
	got := captureStdout(t, func() {
		if err := tagsPullRun(context.Background(), &buf, true); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	res := parseResult(t, got)
	if res.Command != "tags pull" {
		t.Errorf("expected command='tags pull', got %s", res.Command)
	}

	data := dataMap(t, got)
	if data["skipped"] != true {
		t.Fatalf("expected skipped=true, got %v", data["skipped"])
	}
	if data["skip_reason"] != tailapi.ErrNoAPIClient.Error() {
		t.Fatalf("skip_reason = %v, want %q", data["skip_reason"], tailapi.ErrNoAPIClient.Error())
	}
}

func TestTagsDeleteRemoteJSON_Success(t *testing.T) {
	setTagsMocks(t)
	mockDefaults()
	mockRegistryWithServices(nil)
	tagsDeleteTagFn = func(ctx context.Context, tag string) error { return nil }

	var buf bytes.Buffer
	got := captureStdout(t, func() {
		if err := tagsDeleteRemoteRun(context.Background(), &buf, "tag:other", true, true, true); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	res := parseResult(t, got)
	if res.Command != "tags delete-remote" {
		t.Errorf("expected command='tags delete-remote', got %s", res.Command)
	}

	data := dataMap(t, got)
	if data["tag"] != "tag:other" {
		t.Errorf("expected tag=tag:other, got %v", data["tag"])
	}
	if data["remote_acl_tag_owner_rule_removed"] != true {
		t.Errorf("expected remote_acl_tag_owner_rule_removed=true, got %v", data["remote_acl_tag_owner_rule_removed"])
	}
}

func TestTagsDeleteRemoteJSON_DefaultTag(t *testing.T) {
	setTagsMocks(t)
	mockDefaults()

	err := tagsDeleteRemoteRun(context.Background(), &bytes.Buffer{}, "tag:tsmain", false, false, true)
	if err == nil {
		t.Fatal("expected error for default tag")
	}
	ce, ok := err.(*output.CodeError)
	if !ok {
		t.Fatalf("expected *output.CodeError, got %T: %v", err, err)
	}
	if ce.Code != output.ExitConflict {
		t.Errorf("expected exit code %d, got %d", output.ExitConflict, ce.Code)
	}
}

func TestTagsDeleteRemoteJSON_TagInUse(t *testing.T) {
	setTagsMocks(t)
	mockDefaults()
	mockRegistryWithServices([]registry.Service{
		{Name: "myapp", Tags: []string{"tag:shared"}},
	})

	err := tagsDeleteRemoteRun(context.Background(), &bytes.Buffer{}, "tag:shared", false, false, true)
	if err == nil {
		t.Fatal("expected error for tag in use")
	}
	ce, ok := err.(*output.CodeError)
	if !ok {
		t.Fatalf("expected *output.CodeError, got %T: %v", err, err)
	}
	if ce.Code != output.ExitConflict {
		t.Errorf("expected exit code %d, got %d", output.ExitConflict, ce.Code)
	}
}

func TestTagsDeleteRemoteJSON_NoAPIClientAuthError(t *testing.T) {
	setTagsMocks(t)
	mockDefaults()
	mockRegistryWithServices(nil)
	tagsDeleteTagFn = func(ctx context.Context, tag string) error {
		return tailapi.ErrNoAPIClient
	}

	err := tagsDeleteRemoteRun(context.Background(), &bytes.Buffer{}, "tag:other", true, true, true)
	if err == nil {
		t.Fatal("expected auth error for missing API client")
	}
	ce, ok := err.(*output.CodeError)
	if !ok {
		t.Fatalf("expected *output.CodeError, got %T: %v", err, err)
	}
	if ce.Code != output.ExitAuth {
		t.Fatalf("code = %d, want %d", ce.Code, output.ExitAuth)
	}
	if !strings.Contains(ce.Message, "API access token") || !strings.Contains(ce.Message, "tslink login --api-key-stdin") {
		t.Fatalf("message = %q, want auth-specific guidance", ce.Message)
	}
	if strings.Contains(ce.Message, "tslink login --api-key ") {
		t.Fatalf("message recommends argv credential form: %q", ce.Message)
	}
}

// --- Logs JSON ---

func TestLogsJSON(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "test.log")
	content := "line1\nline2\nline3\n"
	os.WriteFile(logFile, []byte(content), 0o644)

	lines, err := tailFile(logFile, 3, "")
	if err != nil {
		t.Fatal(err)
	}

	result := LogsResult{Lines: lines, Count: len(lines), File: logFile}
	got := captureStdout(t, func() {
		output.Success("logs", result)
	})

	res := parseResult(t, got)
	if !res.OK {
		t.Error("expected ok=true")
	}
	if res.Command != "logs" {
		t.Errorf("expected command=logs, got %s", res.Command)
	}

	data := dataMap(t, got)
	if data["count"] != float64(3) {
		t.Errorf("expected count=3, got %v", data["count"])
	}
	logLines, ok := data["lines"].([]any)
	if !ok || len(logLines) != 3 {
		t.Fatalf("expected 3 lines, got %v", data["lines"])
	}
	if logLines[0] != "line1" {
		t.Errorf("expected first line=line1, got %v", logLines[0])
	}
	if data["file"] != logFile {
		t.Errorf("expected file=%s, got %v", logFile, data["file"])
	}
}

// --- Remove JSON with device warning ---

func TestRemoveJSON_WithDeviceWarning(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	_, _ = registry.Add(regPath, registry.Service{Name: "web", Type: registry.TypeProxy, Target: "http://localhost:3000"})

	oldDelete := deleteDevicesFn
	t.Cleanup(func() { deleteDevicesFn = oldDelete })
	deleteDevicesFn = func(ctx context.Context, target tailapi.CleanupTarget) (tailapi.CleanupResult, error) {
		return tailapi.CleanupResult{}, os.ErrPermission
	}

	var out, errOut bytes.Buffer
	got := captureStdout(t, func() {
		if err := removeService(regPath, testOwnershipPath(regPath), "web", &out, &errOut, true); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	data := dataMap(t, got)
	if data["device_cleaned"] != false {
		t.Errorf("expected device_cleaned=false, got %v", data["device_cleaned"])
	}
	warning, ok := data["device_warning"].(string)
	if !ok || warning == "" {
		t.Errorf("expected device_warning to be set, got %v", data["device_warning"])
	}
}
