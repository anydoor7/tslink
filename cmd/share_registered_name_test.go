package cmd

import (
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"slices"
	"testing"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/registry"
)

// Exercise real registration (including suffix allocation and reuse), the
// Cobra handler, and MCP's actions.share and wire serializers. Only daemon and
// endpoint/enrollment observations are injected; no tailnet is contacted.
func TestShareRegisteredNameJSONAndMCP(t *testing.T) {
	for _, transport := range []string{"cli", "mcp"} {
		t.Run(transport, func(t *testing.T) {
			for _, registration := range []string{"collision", "reuse"} {
				t.Run(registration, func(t *testing.T) {
					for _, mode := range []string{"ready", "pending-status", "pending-start"} {
						t.Run(mode, func(t *testing.T) {
							paths := registeredNameFixture(t, mode)
							requestedName := "preview"
							if registration == "reuse" {
								seedRegisteredName(t, paths.Registry, "3000", "preview")
								// An implicit retry reuses the actual target name.
								requestedName = ""
							}
							var data map[string]any
							if transport == "cli" {
								resetRootJSONFlag(t)
								command, _, err := rootCmd.Find([]string{"share"})
								if err != nil {
									t.Fatal(err)
								}
								setCommandTestContext(t, command)
								for flag, value := range map[string]string{"name": requestedName, "ephemeral": "true", "wait": "0s"} {
									old := command.Flags().Lookup(flag).Value.String()
									t.Cleanup(func() { _ = command.Flags().Set(flag, old) })
									if err := command.Flags().Set(flag, value); err != nil {
										t.Fatal(err)
									}
								}
								setRootJSONFlag(t, true)
								encoded := captureStdout(t, func() {
									if err := command.RunE(command, []string{"3000"}); err != nil {
										t.Fatal(err)
									}
								})
								var envelope struct {
									OK      bool           `json:"ok"`
									Command string         `json:"command"`
									Data    map[string]any `json:"data"`
								}
								if err := json.Unmarshal([]byte(encoded), &envelope); err != nil || !envelope.OK || envelope.Command != "share" {
									t.Fatalf("CLI share envelope: %s; err=%v", encoded, err)
								}
								data = envelope.Data
								t.Logf("CLI JSON: %s", encoded)
							} else {
								args, err := json.Marshal(map[string]any{"target": "3000", "name": requestedName})
								if err != nil {
									t.Fatal(err)
								}
								input := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"share","arguments":` + string(args) + `,` + mcpCurrentRevisionMeta + `}}` + "\n"
								stdout := runMCPSession(t, input, defaultMCPActions(paths, io.Discard))
								frame := mcpFrameByID(t, decodeMCPResponses(t, stdout), float64(1))
								call, ok := frame["result"].(map[string]any)
								if !ok || call["isError"] == true {
									t.Fatalf("MCP share failure: %s", stdout)
								}
								data, ok = call["structuredContent"].(map[string]any)
								if !ok {
									t.Fatalf("MCP structuredContent missing: %s", stdout)
								}
								var textData map[string]any
								content := call["content"].([]any)[0].(map[string]any)
								if err := json.Unmarshal([]byte(content["text"].(string)), &textData); err != nil {
									t.Fatal(err)
								}
								if textData["name"] != "preview-2" {
									t.Errorf("MCP text registered name = %#v, want preview-2", textData["name"])
								}
								t.Logf("MCP JSON-RPC: %s", stdout)
							}
							reg, err := registry.Load(paths.Registry)
							if err != nil {
								t.Fatal(err)
							}
							if len(reg.Services) != 2 || reg.Services[0].Name != "preview" || reg.Services[0].Target != "http://localhost:4000" || reg.Services[1].Name != "preview-2" || reg.Services[1].Target != "http://localhost:3000" {
								t.Fatalf("collision/reuse registry fixture: %+v", reg.Services)
							}
							if data["name"] != "preview-2" {
								t.Errorf("registered name = %#v, want preview-2 (status=%v)", data["name"], data["status"])
							}
							if mode == "ready" {
								if data["status"] != "ready" || data["url"] != "https://preview-2.example.invalid" {
									t.Errorf("ready evidence = %+v", data)
								}
							} else if data["status"] != "needs_login" || data["auth_url"] != "https://example.invalid/enroll-fixture" {
								t.Errorf("pending enrollment evidence = %+v", data)
							}
						})
					}
				})
			}
		})
	}
}

func registeredNameFixture(t *testing.T, mode string) sharePaths {
	t.Helper()
	restoreShareSeams(t)
	dir := t.TempDir()
	t.Setenv(config.ConfigDirEnv, dir)
	paths := sharePaths{Registry: filepath.Join(dir, "registry.json"), Ownership: filepath.Join(dir, "ownership.json"), PID: filepath.Join(dir, "pid"), Snapshot: filepath.Join(dir, "runtime.json"), AuthHandoff: filepath.Join(dir, "auth.json")}
	seedRegisteredName(t, paths.Registry, "4000", "preview")
	shareEnsureDirFn = func() error { return nil }
	shareRegistryPathFn = func() (string, error) { return paths.Registry, nil }
	shareOwnershipPathFn = func() (string, error) { return paths.Ownership, nil }
	sharePIDPathFn = func() (string, error) { return paths.PID, nil }
	shareSnapshotPathFn = func() (string, error) { return paths.Snapshot, nil }
	shareAuthHandoffPathFn = func() (string, error) { return paths.AuthHandoff, nil }
	shareIsRunningFn = func(string) bool { return mode != "pending-start" }
	shareStartDaemonFn = func(context.Context, io.Writer) (shareDaemonStart, error) {
		return shareDaemonStart{Status: authStatusNeedsLogin, AuthURL: "https://example.invalid/enroll-fixture"}, nil
	}
	shareResolveEndpointOnceFn = func(_ context.Context, _, _, _, name string) (serviceURLResolution, error) {
		if mode != "ready" {
			return serviceURLResolution{}, registry.URLNotReadyError(name)
		}
		return serviceURLResolution{Result: URLResult{Name: name, URL: "https://" + name + ".example.invalid"}}, nil
	}
	sharePollableStatusFn = func(_ context.Context, _, _, _, _ string) (StatusResult, error) {
		return StatusResult{AuthStatus: authStatusNeedsLogin, AuthURL: "https://example.invalid/enroll-fixture"}, nil
	}
	return paths
}

func seedRegisteredName(t *testing.T, path, target, name string) {
	t.Helper()
	spec, err := inferShareTarget(target, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := registerShare(path, spec, name); err != nil {
		t.Fatal(err)
	}
}

func TestMCPShareNameSchemaRequired(t *testing.T) {
	input := `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{` + mcpCurrentRevisionMeta + `}}` + "\n"
	stdout := runMCPSession(t, input, fakeMCPActions())
	frame := mcpFrameByID(t, decodeMCPResponses(t, stdout), float64(1))
	for _, raw := range frame["result"].(map[string]any)["tools"].([]any) {
		tool := raw.(map[string]any)
		if tool["name"] != "share" {
			continue
		}
		schema := tool["outputSchema"].(map[string]any)
		if !slices.Contains(schema["required"].([]any), any("name")) {
			t.Error("share output schema must require name on every successful result")
		}
		name := schema["properties"].(map[string]any)["name"].(map[string]any)
		if name["type"] != "string" || name["minLength"] != float64(1) {
			t.Errorf("share name schema must require a nonempty string: %+v", name)
		}
		return
	}
	t.Fatal("tools/list omitted share")
}
