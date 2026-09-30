package cmd

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/registry"
)

// mcpWantHints is each tool's behaviour as the tool descriptions state it:
// {readOnly, destructive, idempotent, openWorld}. It is written out apart from
// the product table so a change to either shows here.
var mcpWantHints = map[string][4]bool{
	"share":          {false, false, true, true},
	"add":            {false, true, true, true},
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
// may change a file there.
func TestMCPReadOnlyToolsWriteNothing(t *testing.T) {
	stubDoctorTailscaleSSH(t, false, nil)
	restoreShareSeams(t)
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
	if readOnly != 10 {
		t.Fatalf("%d tools are marked read-only, want the 10 that only read", readOnly)
	}
}
