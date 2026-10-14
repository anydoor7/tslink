package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/anydoor7/tslink/internal/output"
	"github.com/anydoor7/tslink/internal/registry"
	"github.com/spf13/pflag"
)

// healthSetFixture writes a registry whose services carry every kind of field
// health set must keep: an allow list, tags, preserve-host, request limits,
// people scope, a gateway route and an owner portal.
func healthSetFixture(t *testing.T) (configDir, regPath string) {
	t.Helper()
	paths := gatewayCommandPaths(t)
	if _, err := changePortal(paths, portalArguments{Owner: "owner", Gateway: true}, true); err != nil {
		t.Fatal(err)
	}
	for _, svc := range []registry.Service{
		{Name: "app", Type: registry.TypeProxy, Target: "http://localhost:9", Tags: []string{"tag:web"}, AllowedUsers: []string{"bob@example.com", "tag:robot"}, PreserveHost: true, RequestLimits: registry.RecommendedUploadLimits(), PortalRoute: &registry.PortalRoute{Port: 8443}},
		{Name: "files", Type: registry.TypeFile, Path: t.TempDir()},
		{Name: "db", Type: registry.TypeTCP, Target: "localhost:5432", Health: &registry.HealthConfig{Interval: "2m"}},
	} {
		if _, err := registry.Add(paths.Registry, svc); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := registry.ChangePerson(paths.Registry, "alice", []string{"app"}, nil, false, false); err != nil {
		t.Fatal(err)
	}
	return filepath.Dir(paths.Registry), paths.Registry
}

// registryWithoutHealth decodes the registry generically and removes each
// service's health block, so a comparison covers every other stored field,
// including fields this test does not know by name.
func registryWithoutHealth(t *testing.T, data []byte) (map[string]any, map[string]any) {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	health := map[string]any{}
	for _, raw := range doc["services"].([]any) {
		svc := raw.(map[string]any)
		health[svc["name"].(string)] = svc["health"]
		delete(svc, "health")
	}
	return doc, health
}

func compiledHealthSet(t *testing.T, configDir string, args ...string) (output.Result, map[string]any, int) {
	t.Helper()
	stdout, stderr, code := runCompiledTSLinkWithConfigDir(t, configDir, "", append([]string{"health", "set"}, append(args, "--json")...)...)
	results := parseCompiledJSONLines(t, stdout)
	if len(results) != 1 {
		t.Fatalf("health set %v: %d envelopes, stderr=%s stdout=%s", args, len(results), stderr, stdout)
	}
	var data map[string]any
	if results[0].Data != nil {
		b, _ := json.Marshal(results[0].Data)
		_ = json.Unmarshal(b, &data)
	}
	return results[0], data, code
}

func TestCompiledHealthSetChangesOnlyHealth(t *testing.T) {
	configDir, regPath := healthSetFixture(t)
	original, err := os.ReadFile(regPath)
	if err != nil {
		t.Fatal(err)
	}
	beforeDoc, beforeHealth := registryWithoutHealth(t, original)
	if beforeHealth["app"] != nil {
		t.Fatalf("fixture app already has health: %v", beforeHealth["app"])
	}

	result, data, code := compiledHealthSet(t, configDir, "app", "--path", "/health", "--status-max", "401")
	if code != 0 || !result.OK || result.Command != "health set" || result.SchemaVersion != 1 {
		t.Fatalf("health set: code=%d %+v", code, result)
	}
	want := map[string]any{"app": "app", "type": "proxy", "changed": true, "defaults": false, "health": map[string]any{"path": "/health", "status_min": float64(200), "status_max": float64(401), "timeout": "5s", "interval": "1m"}}
	if !reflect.DeepEqual(data, want) {
		t.Fatalf("data = %v\nwant %v", data, want)
	}
	updated, err := os.ReadFile(regPath)
	if err != nil {
		t.Fatal(err)
	}
	afterDoc, afterHealth := registryWithoutHealth(t, updated)
	if !reflect.DeepEqual(beforeDoc, afterDoc) {
		t.Fatalf("health set changed other fields:\nbefore %s\nafter  %s", original, updated)
	}
	if !reflect.DeepEqual(afterHealth["app"], map[string]any{"path": "/health", "status_max": float64(401)}) || !reflect.DeepEqual(afterHealth["db"], beforeHealth["db"]) || afterHealth["files"] != nil {
		t.Fatalf("health blocks = %v", afterHealth)
	}

	// Repeating the same change writes nothing.
	result, data, code = compiledHealthSet(t, configDir, "app", "--path", "/health", "--status-max", "401")
	if again, _ := os.ReadFile(regPath); code != 0 || data["changed"] != false || !bytes.Equal(again, updated) {
		t.Fatalf("repeat: code=%d data=%v", code, data)
	}

	// Refusals are atomic: nothing is written.
	for _, tc := range []struct {
		args []string
		code string
		exit int
	}{
		{[]string{"app", "--status-min", "500", "--status-max", "200"}, "usage_error", 2},
		{[]string{"app", "--timeout", "31s"}, "usage_error", 2},
		{[]string{"app", "--path", "/ready?token=x"}, "usage_error", 2},
		{[]string{"app"}, "usage_error", 2},
		{[]string{"files", "--path", "/health"}, registry.CodeHealthOptionUnsupported, 2},
		{[]string{"db", "--status-max", "401"}, registry.CodeHealthOptionUnsupported, 2},
		{[]string{"missing", "--path", "/health"}, "not_found", 5},
	} {
		result, _, code := compiledHealthSet(t, configDir, tc.args...)
		if code != tc.exit || result.OK || result.Error == nil || result.Error.Code != tc.code {
			t.Fatalf("%v: exit=%d %+v, want %s exit %d", tc.args, code, result.Error, tc.code, tc.exit)
		}
		if after, _ := os.ReadFile(regPath); !bytes.Equal(after, updated) {
			t.Fatalf("%v: refused change modified the registry", tc.args)
		}
	}

	// Timeout and interval apply to file and TCP services; HTTP fields are absent.
	_, data, code = compiledHealthSet(t, configDir, "files", "--interval", "5m")
	if code != 0 || !reflect.DeepEqual(data["health"], map[string]any{"timeout": "5s", "interval": "5m"}) || data["type"] != "file" {
		t.Fatalf("file: code=%d data=%v", code, data)
	}
	_, data, code = compiledHealthSet(t, configDir, "db", "--timeout", "1s")
	if code != 0 || !reflect.DeepEqual(data["health"], map[string]any{"timeout": "1s", "interval": "2m"}) {
		t.Fatalf("tcp keeps the stored interval: code=%d data=%v", code, data)
	}

	// --reset removes the block; the registry returns to its original bytes
	// once every service is back where it started.
	for _, args := range [][]string{{"app", "--reset"}, {"files", "--reset"}, {"db", "--reset", "--interval", "2m"}} {
		if _, _, code := compiledHealthSet(t, configDir, args...); code != 0 {
			t.Fatalf("%v: exit %d", args, code)
		}
	}
	reset, err := os.ReadFile(regPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(reset, original) {
		t.Fatalf("reset did not restore the registry:\nbefore %s\nafter  %s", original, reset)
	}

	// Human output names the effective check.
	stdout, stderr, code := runCompiledTSLinkWithConfigDir(t, configDir, "", "health", "set", "app", "--path", "/ready")
	if code != 0 || !strings.Contains(stdout, "app health check updated: GET /ready, status 200-399, timeout 5s, interval 1m") {
		t.Fatalf("human output: exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestMCPHealthSetOwnerStdio(t *testing.T) {
	_, regPath := healthSetFixture(t)
	paths := sharePaths{Registry: regPath, PID: filepath.Join(filepath.Dir(regPath), "tslink.pid"), Snapshot: filepath.Join(filepath.Dir(regPath), "runtime.json")}
	actions := defaultMCPActions(paths, io.Discard)
	before, err := os.ReadFile(regPath)
	if err != nil {
		t.Fatal(err)
	}
	r, err := callMCPTool(context.Background(), actions, "health_set", json.RawMessage(`{"app":"app","status_max":401}`))
	if err != nil || r.IsError {
		t.Fatalf("health_set: %v %+v", err, r)
	}
	validateAgainstToolOutputSchema(t, "health_set", r.StructuredContent)
	b, _ := json.Marshal(r.StructuredContent)
	if !strings.Contains(string(b), `"status_max":401`) || !strings.Contains(string(b), `"changed":true`) {
		t.Fatalf("structured content %s", b)
	}
	after, err := os.ReadFile(regPath)
	if err != nil {
		t.Fatal(err)
	}
	beforeDoc, _ := registryWithoutHealth(t, before)
	afterDoc, afterHealth := registryWithoutHealth(t, after)
	if !reflect.DeepEqual(beforeDoc, afterDoc) || !reflect.DeepEqual(afterHealth["app"], map[string]any{"status_max": float64(401)}) {
		t.Fatalf("health_set changed more than health: %s", after)
	}
	r, err = callMCPTool(context.Background(), actions, "health_set", json.RawMessage(`{"app":"files","path":"/x"}`))
	if err != nil || !r.IsError {
		t.Fatalf("inapplicable field accepted: %v %+v", err, r)
	}
	if b, _ := json.Marshal(r); !strings.Contains(string(b), `\"code\":\"`+registry.CodeHealthOptionUnsupported) {
		t.Fatalf("refusal = %s", b)
	}
}

func runHealthSetRoot(t *testing.T, args ...string) (string, error) {
	t.Helper()
	reset := func() {
		c, _, err := rootCmd.Find([]string{"health", "set"})
		if err != nil {
			t.Fatal(err)
		}
		c.Flags().VisitAll(func(f *pflag.Flag) {
			if err := f.Value.Set(f.DefValue); err != nil {
				t.Fatal(err)
			}
			f.Changed = false
		})
		resetRootJSONFlag(t)
	}
	reset()
	t.Cleanup(reset)
	rootCmd.SetArgs(append([]string{"health", "set"}, args...))
	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	t.Cleanup(func() { rootCmd.SetOut(nil) })
	var err error
	stdout := captureStdout(t, func() { err = rootCmd.Execute() })
	return buf.String() + stdout, err
}

func TestHealthSetCommandInProcess(t *testing.T) {
	_, regPath := healthSetFixture(t)
	out, err := runHealthSetRoot(t, "app", "--path", "/ready", "--body", "ok")
	if err != nil || !strings.Contains(out, "app health check updated: GET /ready, status 200-399, body check, timeout 5s, interval 1m") {
		t.Fatalf("%q %v", out, err)
	}
	out, err = runHealthSetRoot(t, "db", "--timeout", "1s")
	if err != nil || !strings.Contains(out, "db health check updated: timeout 1s, interval 2m") {
		t.Fatalf("%q %v", out, err)
	}
	out, err = runHealthSetRoot(t, "files", "--reset")
	if err != nil || !strings.Contains(out, "files health check unchanged: timeout 5s, interval 1m (defaults)") {
		t.Fatalf("%q %v", out, err)
	}
	out, err = runHealthSetRoot(t, "app", "--status-min", "200", "--status-max", "401", "--interval", "30s", "--json")
	if err != nil || !strings.Contains(out, `"command":"health set"`) || !strings.Contains(out, `"status_max":401`) || strings.Contains(out, `"ok"`+`:false`) {
		t.Fatalf("%q %v", out, err)
	}
	// The body text never appears in a result; only that a check exists.
	if strings.Contains(out, `"body_contains"`) || !strings.Contains(out, `"body_check":true`) {
		t.Fatalf("%q", out)
	}
	if _, err := runHealthSetRoot(t, "app"); output.ExitCode(err) != output.ExitUsage || !strings.Contains(err.Error(), "nothing to change") {
		t.Fatalf("no-op request: %v", err)
	}
	if _, err := runHealthSetRoot(t, "missing", "--path", "/x"); output.ExitCode(err) != output.ExitNotFound {
		t.Fatalf("unknown app: %v", err)
	}
	reg, err := registry.Load(regPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, svc := range reg.Services {
		if svc.Name == "app" && (svc.Health == nil || *svc.Health != (registry.HealthConfig{Path: "/ready", BodyContains: "ok", StatusMin: 200, StatusMax: 401, Interval: "30s"})) {
			t.Fatalf("%+v", svc.Health)
		}
	}
}
