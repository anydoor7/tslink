package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/anydoor7/tslink/internal/output"
	"github.com/anydoor7/tslink/internal/registry"
	tsruntime "github.com/anydoor7/tslink/internal/runtime"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestB3AInstalledAutostartReportedWhenSettleFails(t *testing.T) {
	for _, tool := range []string{"share", "add", "template_apply"} {
		t.Run(tool, func(t *testing.T) {
			isolateBootstrap(t)
			restoreShareSeams(t)
			paths := mcpSharePaths(t)
			marker, err := supervisorPath()
			if err != nil {
				t.Fatal(err)
			}
			installDaemonFn = func(context.Context, io.Writer) error {
				if err := os.MkdirAll(filepath.Dir(marker), 0700); err != nil {
					return err
				}
				return os.WriteFile(marker, []byte("fake autostart definition; no manager called"), 0600)
			}
			args := map[string]string{"share": `{"target":"3000"}`, "add": `{"name":"web","type":"proxy","target":"localhost:3000"}`, "template_apply": `{"name":"local-web"}`}
			result, err := callMCPTool(context.Background(), defaultMCPActions(paths, io.Discard), tool, json.RawMessage(args[tool]))
			if err != nil || !result.IsError {
				t.Fatalf("wanted setup failure got %+v %v", result, err)
			}
			if _, err := os.Stat(marker); err != nil {
				t.Fatalf("positive control: autostart not left on disk: %v", err)
			}
			failure := mcpToolResultFailure(t, result).Error
			encoded, _ := json.Marshal(failure)
			t.Logf("autostart exists=true tool=%s failure=%s", tool, encoded)
			if !bytes.Contains(encoded, []byte(`"daemon_installed"`)) {
				t.Errorf("installed autostart survives setup failure but agent gets no daemon_installed")
			}
		})
	}
}

func TestB3AShareDropsRecordedInstallOnLaterError(t *testing.T) {
	actions := refusingMCPActions()
	actions.share = func(ctx context.Context, _ shareRequest) (ShareResult, error) {
		noteDaemonInstall(ctx, DaemonInstalled{Manager: "systemd", Path: "/scratch/tslink.service", Undo: "tslink uninstall"})
		return ShareResult{}, registry.URLNotReadyError("web")
	}
	result, err := callMCPTool(context.Background(), actions, "share", json.RawMessage(`{"target":"3000"}`))
	if err != nil {
		t.Fatal(err)
	}
	failure := mcpToolResultFailure(t, result).Error
	wire, _ := json.Marshal(failure)
	if !bytes.Contains(wire, []byte(`"daemon_installed"`)) {
		t.Errorf("completed install discarded after later error: %s", wire)
	}
}

func TestB3ARealShareKeepsInstallAfterEndpointFailure(t *testing.T) {
	dir := isolateBootstrap(t)
	restoreShareSeams(t)
	paths := mcpSharePaths(t)
	marker, err := supervisorPath()
	if err != nil {
		t.Fatal(err)
	}
	installDaemonFn = func(context.Context, io.Writer) error {
		if err := os.MkdirAll(filepath.Dir(marker), 0700); err != nil {
			return err
		}
		if err := os.WriteFile(marker, []byte("fake installed autostart"), 0600); err != nil {
			return err
		}
		isRunningFn = func(string) bool { return true }
		detectSupervisionFn = func(context.Context, string, bool, int) Supervision {
			return Supervision{Manager: supervisorName(), Installed: true, RestartOnExit: true, Autostart: true}
		}
		return tsruntime.Save(filepath.Join(dir, "runtime.json"), tsruntime.NewSnapshot(4242, time.Now(), "fixture", time.Now(), nil))
	}
	shareResolveEndpointOnceFn = func(_ context.Context, _, _, _, name string) (serviceURLResolution, error) {
		return serviceURLResolution{}, output.ErrNotFound("service removed during endpoint wait: " + name)
	}
	var notices bytes.Buffer
	result, err := callMCPTool(context.Background(), defaultMCPActions(paths, &notices), "share", json.RawMessage(`{"target":"3000"}`))
	if err != nil || !result.IsError {
		t.Fatalf("want endpoint failure got %+v %v", result, err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("probe installed nothing", err)
	}
	if !strings.Contains(notices.String(), "ready and autostart is verified") {
		t.Fatalf("install never completed: %s", &notices)
	}
	text := result.Content[0].(*mcp.TextContent).Text
	t.Logf("install confirmed=true autostart exists=true failure=%s", text)
	if !strings.Contains(text, "daemon_installed") {
		t.Error("real executeShare loses completed install on later endpoint failure")
	}
}

func TestDaemonInstallRollbackLeavesNoReceipt(t *testing.T) {
	isolateBootstrap(t)
	path, err := supervisorPath()
	if err != nil {
		t.Fatal(err)
	}
	installDaemonFn = func(context.Context, io.Writer) error {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte("fake autostart"), 0600); err != nil {
			return err
		}
		if err := os.Remove(path); err != nil {
			return err
		}
		return errors.New("verification failed; definition rolled back")
	}
	ctx, record := recordDaemonInstall(context.Background())
	if err := ensureDaemon(ctx, io.Discard, false); err == nil || !strings.Contains(err.Error(), "definition rolled back") {
		t.Fatalf("want rollback error: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("definition not removed: %v", err)
	}
	if record.installed != nil {
		t.Fatalf("rolled-back install reported: %+v", record.installed)
	}
}

func TestDaemonInstallFailureMergesExistingData(t *testing.T) {
	actions := refusingMCPActions()
	actions.share = func(ctx context.Context, _ shareRequest) (ShareResult, error) {
		noteDaemonInstall(ctx, DaemonInstalled{Manager: "systemd", Path: "/scratch/tslink.service", Undo: "tslink uninstall"})
		return ShareResult{}, receiptDataError{}
	}
	result, err := callMCPTool(context.Background(), actions, "share", json.RawMessage(`{"target":"3000"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || result.StructuredContent != nil {
		t.Fatalf("invalid refusal shape: %+v", result)
	}
	var failure struct {
		Code string
		Data map[string]any
	}
	if err := json.Unmarshal([]byte(result.Content[0].(*mcp.TextContent).Text), &failure); err != nil {
		t.Fatal(err)
	}
	if failure.Code != "internal_error" || failure.Data["kept"] != "original" || failure.Data["daemon_installed"] == nil {
		t.Fatalf("install or existing failure data lost: %+v", failure)
	}
}

type receiptDataError struct{}

func (receiptDataError) Error() string  { return "later failure" }
func (receiptDataError) ErrorData() any { return map[string]any{"kept": "original"} }
