package cmd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/registry"
	"github.com/anydoor7/tslink/internal/tailapi"
)

// mcpWantHints is each tool's behaviour as the tool descriptions state it:
// {readOnly, destructive, idempotent, openWorld}. It is written out apart from
// the product table so a change to either shows here.
var mcpWantHints = map[string][4]bool{
	"share":          {false, false, false, true},
	"add":            {false, true, false, true},
	"list":           {true, false, true, false},
	"unshare":        {false, true, true, true},
	"status":         {true, false, true, false},
	"url":            {true, false, true, false},
	"tags_list":      {true, false, true, false},
	"tags_set":       {false, true, true, true},
	"access_explain": {true, false, true, false},
	"doctor":         {true, false, true, true},
	"logs":           {true, false, true, false},
	"invite_user":    {false, false, false, true},
	"invite_device":  {false, false, false, true},
	"invite_list":    {true, false, true, true},
	"invite_revoke":  {false, true, true, true},
	"invite_resend":  {false, false, false, true},
	"apps_detect":    {true, false, true, false},
	"recipe_list":    {true, false, true, false},
	"recipe_plan":    {true, false, true, false},
	"recipe_apply":   {false, false, true, true},
	"template_list":  {true, false, true, false},
	"template_plan":  {true, false, true, false},
	"template_apply": {false, false, true, true},
}

// TestMCPToolsDeclareEveryBehaviourHint is A3-6: no tool carried annotations,
// so a client applied the specification's defaults to all nineteen -- every
// read-only tool looked destructive and open-world, and nothing marked unshare
// apart from list. Every tool now states all four hints explicitly, over the
// protocol a client reads.
func TestMCPToolsDeclareEveryBehaviourHint(t *testing.T) {
	stdout := runMCPSession(t, initializedMCPInput(`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`), fakeMCPActions())
	frame := mcpFrameByID(t, decodeMCPResponses(t, stdout), float64(2))
	result, _ := frame["result"].(map[string]any)
	tools, _ := result["tools"].([]any)
	if len(tools) != len(mcpWantHints) || len(mcpToolDefinitions) != len(mcpWantHints) {
		t.Fatalf("tools/list has %d tools, the server declares %d, this table covers %d", len(tools), len(mcpToolDefinitions), len(mcpWantHints))
	}
	for _, raw := range tools {
		tool, _ := raw.(map[string]any)
		name, _ := tool["name"].(string)
		want, known := mcpWantHints[name]
		if !known {
			t.Errorf("tool %s has no expected hints", name)
			continue
		}
		annotations, _ := tool["annotations"].(map[string]any)
		for i, hint := range []string{"readOnlyHint", "destructiveHint", "idempotentHint", "openWorldHint"} {
			value, present := annotations[hint].(bool)
			if !present {
				t.Errorf("%s: %s absent from %v; a client would apply the specification's default", name, hint, annotations)
				continue
			}
			if value != want[i] {
				t.Errorf("%s: %s = %v, want %v", name, hint, value, want[i])
			}
		}
	}
}

// configTreeDigest records every file under the roots, so a tool that writes,
// creates or removes anything there changes it.
func configTreeDigest(t *testing.T, roots ...string) map[string]string {
	t.Helper()
	digest := map[string]string{}
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			value := info.Mode().String()
			if entry.Type().IsRegular() {
				data, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				sum := sha256.Sum256(data)
				value += " " + hex.EncodeToString(sum[:])
			}
			digest[path] = value
			return nil
		})
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
	return digest
}

// TestMCPReadOnlyToolsWriteNothing holds readOnlyHint to what the tools do:
// every tool the table marks read-only is called over the protocol against a
// registered service, a log file and the config directory, and none of them
// may change a file there. The keyring starts empty and then holds one key
// without metadata, whatever ran before this test, so a tool that recorded a
// metadata backfill would show here (B7-1); a write tool in the same harness
// is the control that such a record is seen.
func TestMCPReadOnlyToolsWriteNothing(t *testing.T) {
	stubAppsDetection(t)
	stubDoctorTailscaleSSH(t, false, nil)
	restoreShareSeams(t)
	storeCredentialWithoutMetadata(t)
	oldInviteList := inviteListFn
	t.Cleanup(func() { inviteListFn = oldInviteList })
	inviteListFn = func(context.Context, []tailapi.DeviceTarget) (tailapi.InviteList, error) {
		return tailapi.InviteList{}, nil
	}
	paths := mcpSharePaths(t)
	if _, err := registry.Add(paths.Registry, registry.Service{Name: "web", Type: registry.TypeProxy, Target: "http://localhost:3000"}); err != nil {
		t.Fatal(err)
	}
	logDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(logDir, "tslink.err.log"), []byte("time=2026-09-29T12:00:00Z level=INFO msg=started\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldLogDir := logsLogDirFn
	logsLogDirFn = func() (string, error) { return logDir, nil }
	t.Cleanup(func() { logsLogDirFn = oldLogDir })
	configDir, err := config.Dir()
	if err != nil {
		t.Fatal(err)
	}
	roots := []string{filepath.Dir(paths.Registry), logDir, configDir}
	arguments := map[string]string{"url": `{"name":"web"}`, "access_explain": `{"service":"web"}`, "template_plan": `{"name":"local-web"}`}

	actions := defaultMCPActions(paths, io.Discard)
	readOnly := 0
	for _, definition := range mcpToolDefinitions {
		hints := mcpToolHints[definition.Name]
		if hints == nil || !hints.ReadOnlyHint {
			continue
		}
		readOnly++
		args, ok := arguments[definition.Name]
		if !ok {
			args = mcpToolMinimalArguments[definition.Name]
		}
		before := configTreeDigest(t, roots...)
		call := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"` + definition.Name + `","arguments":` + args + `}}`
		stdout := runMCPSession(t, initializedMCPInput(call), actions)
		if frame := mcpFrameByID(t, decodeMCPResponses(t, stdout), float64(2)); frame["result"] == nil {
			t.Fatalf("%s: no tool result: %v", definition.Name, frame)
		}
		after := configTreeDigest(t, roots...)
		for path, value := range after {
			if before[path] != value {
				t.Errorf("read-only tool %s changed %s", definition.Name, strings.TrimPrefix(path, "/"))
			}
		}
		for path := range before {
			if _, kept := after[path]; !kept {
				t.Errorf("read-only tool %s removed %s", definition.Name, path)
			}
		}
	}
	if readOnly != 13 {
		t.Fatalf("%d tools are marked read-only, want the 13 that only read", readOnly)
	}

	// Control: add is a write tool. With the daemon reported running it reads
	// the new service's URL evidence the way the status-reporting commands
	// read status, so in this same harness it records the key's metadata.
	oldIsRunning := isRunningFn
	t.Cleanup(func() { isRunningFn = oldIsRunning })
	isRunningFn = func(string) bool { return true }
	before := configTreeDigest(t, roots...)
	call := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"add","arguments":{"name":"api","type":"proxy","target":"http://localhost:3001"}}}`
	if frame := mcpFrameByID(t, decodeMCPResponses(t, runMCPSession(t, initializedMCPInput(call), actions)), float64(2)); frame["result"] == nil {
		t.Fatalf("add: no tool result: %v", frame)
	}
	after := configTreeDigest(t, roots...)
	for _, path := range []string{filepath.Join(configDir, "credential-meta.json"), filepath.Join(configDir, "credentials.lock"), paths.Registry} {
		if before[path] == after[path] {
			t.Errorf("control: add left %s as it was; a read-only tool's record would go unseen", path)
		}
	}
}
