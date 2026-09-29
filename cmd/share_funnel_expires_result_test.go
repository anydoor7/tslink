package cmd

import (
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/monody0007/tslink/internal/registry"
)

// TestShareResultReportsTheFunnelDeadlineItReused pins that reuse can hand a
// caller a Funnel share expiring sooner than the funnel_ttl it asked for, and
// that the result says so. Without funnel_expires_at a 72h request that
// reused a 1h share could not tell.
func TestShareResultReportsTheFunnelDeadlineItReused(t *testing.T) {
	actions, regPath := shareMCPWireActions(t)
	first := callMCPShare(t, actions, `{"target":"3000","funnel":true,"public_ack":true,"funnel_ttl":"1h"}`)
	longer := callMCPShare(t, actions, `{"target":"3000","funnel":true,"public_ack":true,"funnel_ttl":"72h"}`)
	stored, err := registry.Load(regPath)
	if err != nil || len(stored.Services) != 1 || stored.Services[0].FunnelExpiresAt == nil {
		t.Fatalf("registry = %+v err=%v", stored, err)
	}
	want := stored.Services[0].FunnelExpiresAt.UTC()
	for name, result := range map[string]map[string]any{"created": first, "reused for 72h": longer} {
		raw, _ := result["funnel_expires_at"].(string)
		got, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil || !got.Equal(want) {
			t.Fatalf("%s share funnel_expires_at = %q (%v), want the stored deadline %v", name, raw, err, want)
		}
	}
	if time.Until(want) > 2*time.Hour {
		t.Fatalf("the 72h request replaced the 1h deadline: %v", want)
	}

	// A tailnet-only share has no deadline, so the field is absent rather than
	// null or empty, and the result keeps its existing shape.
	private := callMCPShare(t, actions, `{"target":"4000"}`)
	for _, field := range []string{"funnel_expires_at", "funnel_rearmed"} {
		if _, ok := private[field]; ok {
			t.Fatalf("tailnet-only share result carries %s: %v", field, private)
		}
	}
}

// TestCLIShareJSONCarriesTheFunnelDeadline checks the CLI envelope's data,
// which is the same ShareResult executeShare returns to MCP.
func TestCLIShareJSONCarriesTheFunnelDeadline(t *testing.T) {
	_, regPath := shareMCPWireActions(t)
	paths := sharePaths{Registry: regPath}
	result, err := executeShare(context.Background(), paths, shareRequest{Target: "3000", Ephemeral: true, Funnel: true, PublicAck: true, FunnelTTL: "8h", FunnelTTLSet: true}, time.Second, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var data map[string]any
	if err := json.Unmarshal(encoded, &data); err != nil {
		t.Fatal(err)
	}
	raw, _ := data["funnel_expires_at"].(string)
	deadline, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil || time.Until(deadline) < 7*time.Hour || time.Until(deadline) > 8*time.Hour {
		t.Fatalf("share --json data = %s, want funnel_expires_at about 8h out", encoded)
	}
}

// shareMCPWireActions drives MCP share through the real executeShare with
// only the daemon and URL seams stubbed.
func shareMCPWireActions(t *testing.T) (mcpActions, string) {
	t.Helper()
	restoreShareSeams(t)
	dir := t.TempDir()
	shareIsRunningFn = func(string) bool { return true }
	shareResolveEndpointOnceFn = func(_, _, _, name string) (serviceURLResolution, error) {
		return serviceURLResolution{Result: URLResult{Name: name, URL: "https://" + name + ".example.ts.net", State: "exact"}}, nil
	}
	paths := sharePaths{
		Registry:    filepath.Join(dir, "registry.json"),
		Ownership:   filepath.Join(dir, "node-ownership.json"),
		PID:         filepath.Join(dir, "tslink.pid"),
		Snapshot:    filepath.Join(dir, "runtime.json"),
		AuthHandoff: filepath.Join(dir, "auth-handoff.json"),
	}
	return defaultMCPActions(paths, io.Discard), paths.Registry
}

// callMCPShare sends one share call over the stdio transport and returns the
// tool result's structured content, checked against the declared output
// schema.
func callMCPShare(t *testing.T, actions mcpActions, arguments string) map[string]any {
	t.Helper()
	stdout := runMCPSession(t, initializedMCPInput(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"share","arguments":`+arguments+`}}`), actions)
	result, _ := mcpFrameByID(t, decodeMCPResponses(t, stdout), float64(2))["result"].(map[string]any)
	structured, _ := result["structuredContent"].(map[string]any)
	if result == nil || result["isError"] == true || structured == nil {
		t.Fatalf("share failed: %s", stdout)
	}
	if err := validateMCPJSONSchema(mcpShareOutputSchema, structured, "share"); err != nil {
		t.Fatalf("share result does not match its output schema: %v (%v)", err, structured)
	}
	return structured
}
