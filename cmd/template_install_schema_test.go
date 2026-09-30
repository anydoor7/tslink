package cmd

import (
	"context"
	"encoding/json"
	"io"
	"testing"
)

func TestTemplateInstallReceiptOnlyInApplySchema(t *testing.T) {
	paths := mcpSharePaths(t)
	oldEnsure := ensureDaemonFn
	t.Cleanup(func() { ensureDaemonFn = oldEnsure })
	ensureDaemonFn = func(ctx context.Context, _ io.Writer, _ bool) error {
		noteDaemonInstall(ctx, DaemonInstalled{Manager: "systemd", Path: "/scratch/tslink.service", Undo: "tslink uninstall"})
		return nil
	}
	actions := defaultMCPActions(paths, io.Discard)
	for _, tool := range []string{"template_plan", "template_apply"} {
		schema := mcpToolByName(t, tool).OutputSchema
		_, declared := schema["properties"].(map[string]any)["daemon_installed"]
		if declared != (tool == "template_apply") {
			t.Errorf("%s daemon_installed declared=%v", tool, declared)
		}
		result, err := callMCPTool(context.Background(), actions, tool, json.RawMessage(`{"name":"local-web"}`))
		if err != nil || result.IsError {
			t.Fatalf("%s: %+v, %v", tool, result, err)
		}
		payload := mcpResultStructured(t, result)
		validateAgainstToolOutputSchema(t, tool, payload)
		_, emitted := payload["daemon_installed"]
		if emitted != (tool == "template_apply") {
			t.Errorf("%s daemon_installed emitted=%v", tool, emitted)
		}
	}
}
