package cmd

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/registry"
)

// TestNeverStartedInstallReportsDaemonAbsentOnEverySurface is A3-5's probe:
// on an install that never started, status said daemon_running:false next to
// daemon_state:"unknown", because a missing PID file counted as unknown, and
// MCP status had only the boolean. A missing PID file is now absent, and MCP
// status carries daemon_state.
func TestNeverStartedInstallReportsDaemonAbsentOnEverySurface(t *testing.T) {
	dir := t.TempDir()
	paths := sharePaths{
		Registry:    filepath.Join(dir, "registry.json"),
		PID:         filepath.Join(dir, "tslink.pid"),
		Snapshot:    filepath.Join(dir, "runtime.json"),
		AuthHandoff: filepath.Join(dir, "auth-handoff.json"),
		Ownership:   filepath.Join(dir, "node-ownership.json"),
	}
	addStatusTestService(t, paths.Registry, registry.Service{Name: "web", Type: registry.TypeProxy, Target: "http://localhost:3000"})
	withStatusURLSeams(t, false, 0, time.Time{})
	restoreShareSeams(t)
	sharePollableStatusFn = getPollableStatus
	if _, err := os.Stat(paths.PID); !os.IsNotExist(err) {
		t.Fatalf("fixture has a PID file (%v)", err)
	}

	cli, err := getPollableStatus(context.Background(), paths.PID, paths.Registry, paths.Snapshot, paths.AuthHandoff)
	if err != nil {
		t.Fatal(err)
	}
	if cli.DaemonRunning || cli.DaemonState != daemonStateAbsent {
		t.Fatalf("CLI status daemon_running=%v daemon_state=%q, want false and absent", cli.DaemonRunning, cli.DaemonState)
	}
	value, err := defaultMCPActions(paths, io.Discard).status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if mcp := value.(mcpStatusSummary); mcp.DaemonState != daemonStateAbsent || mcp.DaemonRunning {
		t.Fatalf("MCP status = %+v, want daemon_state absent", mcp)
	}
	// The event stream pushes the status tool's payload minus auth_url.
	state, err := buildMCPEventState(map[string]any{"services": []ListServiceSummary{}}, value)
	if err != nil || state.Status.DaemonState != daemonStateAbsent {
		t.Fatalf("event status = %+v (%v), want daemon_state absent", state.Status, err)
	}
	schema := mcpToolByName(t, "status").OutputSchema
	if _, ok := schema["properties"].(map[string]any)["daemon_state"]; !ok || !containsString(requiredFields(schema), "daemon_state") {
		t.Fatalf("status output schema does not require daemon_state: %v", schema["required"])
	}

	// Control: a PID file whose process state cannot be read stays unknown.
	if err := os.WriteFile(paths.PID, []byte("not a pid\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cli, err = getPollableStatus(context.Background(), paths.PID, paths.Registry, paths.Snapshot, paths.AuthHandoff)
	if err != nil {
		t.Fatal(err)
	}
	if cli.DaemonState != daemonStateUnknown {
		t.Fatalf("unreadable PID file: daemon_state = %q, want unknown", cli.DaemonState)
	}
}

func requiredFields(schema map[string]any) []string {
	switch required := schema["required"].(type) {
	case []string:
		return required
	case []any:
		fields := make([]string, 0, len(required))
		for _, field := range required {
			if name, ok := field.(string); ok {
				fields = append(fields, name)
			}
		}
		return fields
	}
	return nil
}
