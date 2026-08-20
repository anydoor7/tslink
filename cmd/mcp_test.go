package cmd

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/monody0007/tslink/internal/registry"
	"github.com/monody0007/tslink/internal/tailapi"
)

func fakeMCPActions() mcpActions {
	return mcpActions{
		share: func(_ context.Context, target, name string, ephemeral bool) (ShareResult, error) {
			if target == "error" {
				return ShareResult{}, errors.New("share failed")
			}
			return ShareResult{Status: authStatusNeedsLogin, AuthURL: "https://login.tailscale.com/a/mcp"}, nil
		},
		list: func() (any, error) {
			return map[string]any{"services": []mcpServiceSummary{{Name: "demo", Type: registry.TypeProxy, State: "pending"}}}, nil
		},
		unshare: func(name string) (any, error) { return map[string]any{"ok": name == "demo"}, nil },
		status: func() (any, error) {
			return mcpStatusSummary{Authenticated: false, DaemonRunning: true, ServiceCount: 1, Status: authStatusNeedsLogin, AuthURL: "https://login.tailscale.com/a/mcp"}, nil
		},
	}
}

func decodeMCPResponses(t *testing.T, output string) []map[string]any {
	t.Helper()
	var responses []map[string]any
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		var response map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &response); err != nil {
			t.Fatalf("unmarshal frame: %v (%s)", err, scanner.Text())
		}
		responses = append(responses, response)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return responses
}

func TestMCPTranscriptInitializeListAndNeedsLoginShare(t *testing.T) {
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"share","arguments":{"target":"./report.html"}}}`,
	}, "\n") + "\n"
	var stdout bytes.Buffer
	server := newMCPServer(strings.NewReader(input), &stdout, fakeMCPActions())
	if err := server.serve(context.Background()); err != nil {
		t.Fatal(err)
	}
	frames := decodeMCPResponses(t, stdout.String())
	if len(frames) != 3 {
		t.Fatalf("frames = %d, want 3: %s", len(frames), stdout.String())
	}
	initialize := frames[0]["result"].(map[string]any)
	if initialize["protocolVersion"] != mcpProtocolVersion {
		t.Fatalf("initialize = %+v", initialize)
	}
	listed := frames[1]["result"].(map[string]any)["tools"].([]any)
	if len(listed) != 4 {
		t.Fatalf("tools = %d", len(listed))
	}
	call := frames[2]["result"].(map[string]any)
	structured := call["structuredContent"].(map[string]any)
	if structured["status"] != authStatusNeedsLogin || structured["auth_url"] != "https://login.tailscale.com/a/mcp" {
		t.Fatalf("share result = %+v", structured)
	}
	if call["isError"] != nil {
		t.Fatalf("needs_login must be a successful tool result: %+v", call)
	}
	t.Logf("MCP client frames (one JSON-RPC frame per line):\n%s", input)
	t.Logf("MCP server frames (one JSON-RPC frame per line):\n%s", stdout.String())
}

func TestMCPToolSchemasAreClosedAndModelFocused(t *testing.T) {
	if len(mcpToolDefinitions) != 4 {
		t.Fatalf("tools = %d", len(mcpToolDefinitions))
	}
	wantNames := []string{"share", "list", "unshare", "status"}
	for i, tool := range mcpToolDefinitions {
		if tool.Name != wantNames[i] || tool.Description == "" {
			t.Fatalf("tool[%d] = %+v", i, tool)
		}
		if tool.InputSchema["type"] != "object" || tool.InputSchema["additionalProperties"] != false {
			t.Fatalf("schema %s = %+v", tool.Name, tool.InputSchema)
		}
		if tool.OutputSchema["type"] != "object" || tool.OutputSchema["additionalProperties"] != false {
			t.Fatalf("output schema %s = %+v", tool.Name, tool.OutputSchema)
		}
	}
	shareSchema := mcpToolDefinitions[0].InputSchema
	required := shareSchema["required"].([]string)
	properties := shareSchema["properties"].(map[string]any)
	if len(required) != 1 || required[0] != "target" || len(properties) != 3 {
		t.Fatalf("share schema = %+v", shareSchema)
	}
	nameDescription := properties["name"].(map[string]any)["description"].(string)
	if !strings.Contains(nameDescription, "reused only if it already has this name") || !strings.Contains(nameDescription, "numeric suffix") {
		t.Fatalf("share name description = %q", nameDescription)
	}
	unshareProperties := mcpToolDefinitions[2].OutputSchema["properties"].(map[string]any)
	unshareOKDescription := unshareProperties["ok"].(map[string]any)["description"].(string)
	if !strings.Contains(unshareOKDescription, "idempotent") || !strings.Contains(unshareOKDescription, "absent service is successful") || !strings.Contains(unshareOKDescription, "removed false") {
		t.Fatalf("unshare ok description = %q, want idempotent missing-service semantics", unshareOKDescription)
	}
	if mcpToolDefinitions[1].InputSchema["required"] != nil || mcpToolDefinitions[3].InputSchema["required"] != nil {
		t.Fatal("no-argument tools unexpectedly require fields")
	}
}

func TestMCPProtocolErrorsAndLifecycle(t *testing.T) {
	cases := []struct {
		name      string
		input     string
		wantCode  float64
		wantCount int
	}{
		{"parse", "{\n", -32700, 1},
		{"invalid request", `{"jsonrpc":"1.0","id":1,"method":"initialize"}` + "\n", -32600, 1},
		{"invalid null id", `{"jsonrpc":"2.0","id":null,"method":"initialize","params":{}}` + "\n", -32600, 1},
		{"batch", `[{"jsonrpc":"2.0","id":1,"method":"ping"}]` + "\n", -32600, 1},
		{"before initialize", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}` + "\n", -32002, 1},
		{"unknown notification", `{"jsonrpc":"2.0","method":"notifications/unknown"}` + "\n", 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stdout bytes.Buffer
			server := newMCPServer(strings.NewReader(tc.input), &stdout, fakeMCPActions())
			if err := server.serve(context.Background()); err != nil {
				t.Fatal(err)
			}
			frames := decodeMCPResponses(t, stdout.String())
			if len(frames) != tc.wantCount {
				t.Fatalf("frames=%d output=%q", len(frames), stdout.String())
			}
			if tc.wantCount > 0 {
				errorObject := frames[0]["error"].(map[string]any)
				if errorObject["code"] != tc.wantCode {
					t.Fatalf("error = %+v", errorObject)
				}
			}
		})
	}

	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":"init","method":"initialize","params":{"protocolVersion":"future"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"initialize","params":{"protocolVersion":"2025-11-25"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":3,"method":"unknown"}`,
		`{"jsonrpc":"2.0","id":4,"method":"ping"}`,
	}, "\n") + "\n"
	var stdout bytes.Buffer
	if err := newMCPServer(strings.NewReader(input), &stdout, fakeMCPActions()).serve(context.Background()); err != nil {
		t.Fatal(err)
	}
	frames := decodeMCPResponses(t, stdout.String())
	if len(frames) != 4 || frames[0]["id"] != "init" || frames[0]["result"].(map[string]any)["protocolVersion"] != mcpProtocolVersion {
		t.Fatalf("frames = %+v", frames)
	}
	if frames[1]["error"].(map[string]any)["code"] != float64(-32600) || frames[2]["error"].(map[string]any)["code"] != float64(-32601) {
		t.Fatalf("errors = %+v", frames)
	}
}

func TestMCPRequestParamsAcceptMetadataAndExtensions(t *testing.T) {
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"metadata-test","version":"1"},"_meta":{"progressToken":0},"client_extension":true}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized","params":{"_meta":{"source":"test"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"ping","params":{"_meta":{"progressToken":"ping"},"extension":"accepted"}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/list","params":{"_meta":{"progressToken":"list"},"cursor":"ignored-by-this-server"}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"status","arguments":{},"_meta":{"progressToken":0},"client_extension":{"trace":"accepted"}}}`,
	}, "\n") + "\n"
	var stdout bytes.Buffer
	if err := newMCPServer(strings.NewReader(input), &stdout, fakeMCPActions()).serve(context.Background()); err != nil {
		t.Fatal(err)
	}
	frames := decodeMCPResponses(t, stdout.String())
	if len(frames) != 4 {
		t.Fatalf("frames=%d output=%s", len(frames), stdout.String())
	}
	for _, frame := range frames {
		if frame["error"] != nil {
			t.Fatalf("metadata-bearing request rejected: %+v", frame)
		}
	}
}

func initializedMCPInput(call string) string {
	return strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		call,
	}, "\n") + "\n"
}

func TestMCPToolCallsValidateArgumentsAndReturnExecutionErrors(t *testing.T) {
	cases := []struct {
		name         string
		call         string
		wantProtocol bool
		wantToolErr  bool
	}{
		{"missing share target", `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"share","arguments":{}}}`, true, false},
		{"unknown share field", `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"share","arguments":{"target":"3000","extra":true}}}`, true, false},
		{"unknown tool", `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"other","arguments":{}}}`, true, false},
		{"list arguments", `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"list","arguments":{"extra":1}}}`, true, false},
		{"unshare arguments", `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"unshare","arguments":{}}}`, true, false},
		{"status arguments", `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"status","arguments":{"extra":1}}}`, true, false},
		{"execution error", `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"share","arguments":{"target":"error","ephemeral":false}}}`, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stdout bytes.Buffer
			server := newMCPServer(strings.NewReader(initializedMCPInput(tc.call)), &stdout, fakeMCPActions())
			if err := server.serve(context.Background()); err != nil {
				t.Fatal(err)
			}
			frames := decodeMCPResponses(t, stdout.String())
			last := frames[len(frames)-1]
			if tc.wantProtocol {
				if last["error"].(map[string]any)["code"] != float64(-32602) {
					t.Fatalf("response = %+v", last)
				}
			} else if tc.wantToolErr {
				result := last["result"].(map[string]any)
				if result["isError"] != true || !strings.Contains(result["content"].([]any)[0].(map[string]any)["text"].(string), "share failed") {
					t.Fatalf("response = %+v", last)
				}
			}
		})
	}
}

func TestMCPAllToolsAndOptionalEphemeral(t *testing.T) {
	var ephemeral bool
	actions := fakeMCPActions()
	actions.share = func(_ context.Context, _, _ string, got bool) (ShareResult, error) {
		ephemeral = got
		return ShareResult{Name: "demo", URL: "https://demo.tail.ts.net", Status: shareStatusReady}, nil
	}
	calls := []string{
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"share","arguments":{"target":"3000","ephemeral":false}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"list"}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"unshare","arguments":{"name":"demo"}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"status","arguments":{}}}`,
	}
	input := initializedMCPInput(strings.Join(calls, "\n"))
	var stdout bytes.Buffer
	if err := newMCPServer(strings.NewReader(input), &stdout, actions).serve(context.Background()); err != nil {
		t.Fatal(err)
	}
	frames := decodeMCPResponses(t, stdout.String())
	if len(frames) != 5 || ephemeral {
		t.Fatalf("frames=%d ephemeral=%v output=%s", len(frames), ephemeral, stdout.String())
	}
	for _, frame := range frames[1:] {
		if frame["error"] != nil {
			t.Fatalf("tool error: %+v", frame)
		}
	}
}

func TestDefaultMCPActionsUseLocalRegistryAndRedactedStatus(t *testing.T) {
	restoreShareSeams(t)
	withStatusURLSeams(t, false, 0, time.Time{})
	dir := t.TempDir()
	paths := sharePaths{
		Registry:    filepath.Join(dir, "registry.json"),
		PID:         filepath.Join(dir, "pid"),
		Snapshot:    filepath.Join(dir, "runtime.json"),
		AuthHandoff: filepath.Join(dir, "auth.json"),
	}
	if _, err := registry.Add(paths.Registry, registry.Service{Name: "demo", Type: registry.TypeProxy, Target: "http://localhost:3000"}); err != nil {
		t.Fatal(err)
	}
	sharePollableStatusFn = func(_, _, _, _ string) (StatusResult, error) {
		return StatusResult{DaemonRunning: true, CredentialStored: true, AuthStatus: authStatusNeedsLogin, AuthURL: "https://login.tailscale.com/a/status", ServiceCount: 1}, nil
	}
	oldDelete := deleteDevicesFn
	t.Cleanup(func() { deleteDevicesFn = oldDelete })
	deleteDevicesFn = func(_ context.Context, target tailapi.CleanupTarget) (tailapi.CleanupResult, error) {
		return tailapi.CleanupResult{Deleted: []string{target.Hostname}}, nil
	}
	shareIsRunningFn = func(string) bool { return true }
	shareResolveEndpointOnceFn = func(_, _, _, name string) (serviceURLResolution, error) {
		return serviceURLResolution{Result: URLResult{Name: name, URL: "https://" + name + ".tail.ts.net"}}, nil
	}
	actions := defaultMCPActions(paths, os.Stderr)
	shared, err := actions.share(context.Background(), "3000", "", true)
	if err != nil || shared.URL != "https://port-3000.tail.ts.net" {
		t.Fatalf("share = %+v err=%v", shared, err)
	}
	listValue, err := actions.list()
	if err != nil {
		t.Fatal(err)
	}
	listed := listValue.(map[string]any)["services"].([]mcpServiceSummary)
	if len(listed) != 2 || listed[0].Name != "demo" || listed[0].URL != nil {
		t.Fatalf("list = %+v", listValue)
	}
	statusValue, err := actions.status()
	if err != nil {
		t.Fatal(err)
	}
	status := statusValue.(mcpStatusSummary)
	if status.Status != authStatusNeedsLogin || status.AuthURL == "" || status.Authenticated || !status.CredentialStored || status.NodeAuthorized {
		t.Fatalf("status = %+v", status)
	}
	statusJSON, err := json.Marshal(status)
	if err != nil || bytes.Contains(statusJSON, []byte("tskey-")) {
		t.Fatalf("status serialization exposed credential material: %s err=%v", statusJSON, err)
	}
	removed, err := actions.unshare("demo")
	removedSummary, ok := removed.(mcpUnshareSummary)
	if err != nil || !ok || !removedSummary.OK || !removedSummary.Removed || !removedSummary.DeviceCleaned || removedSummary.DeviceCleanupSkipped {
		t.Fatalf("unshare = %+v err=%v", removed, err)
	}
	if _, err := registry.Add(paths.Registry, registry.Service{Name: "partial", Type: registry.TypeProxy, Target: "http://localhost:4000"}); err != nil {
		t.Fatal(err)
	}
	deleteDevicesFn = func(_ context.Context, target tailapi.CleanupTarget) (tailapi.CleanupResult, error) {
		return tailapi.CleanupResult{Matched: []string{target.Hostname}, Protected: []string{target.Hostname}, Skipped: true, SkipReason: "ownership could not be proven"}, nil
	}
	partialValue, err := actions.unshare("partial")
	partial := partialValue.(mcpUnshareSummary)
	if err != nil || !partial.OK || !partial.Removed || !partial.DeviceCleanupSkipped || partial.DeviceSkipReason != "ownership could not be proven" {
		t.Fatalf("partial unshare = %+v err=%v", partial, err)
	}
	if _, err := actions.unshare("Bad_Name"); err == nil {
		t.Fatal("invalid name accepted")
	}
	listValue, err = actions.list()
	if err != nil {
		t.Fatal(err)
	}
	if remaining := listValue.(map[string]any)["services"].([]mcpServiceSummary); len(remaining) != 1 || remaining[0].Name != "port-3000" {
		t.Fatalf("list = %+v", listValue)
	}
}

func TestDefaultMCPActionsUnshareReportsSuccessWithoutAPIClient(t *testing.T) {
	restoreShareSeams(t)
	dir := t.TempDir()
	paths := sharePaths{Registry: filepath.Join(dir, "registry.json")}
	if _, err := registry.Add(paths.Registry, registry.Service{Name: "zero-credential", Type: registry.TypeProxy, Target: "http://localhost:3000"}); err != nil {
		t.Fatal(err)
	}
	oldDelete := deleteDevicesFn
	t.Cleanup(func() { deleteDevicesFn = oldDelete })
	deleteDevicesFn = func(context.Context, tailapi.CleanupTarget) (tailapi.CleanupResult, error) {
		return tailapi.CleanupResult{Skipped: true, SkipReason: tailapi.ErrNoAPIClient.Error()}, nil
	}

	value, err := defaultMCPActions(paths, os.Stderr).unshare("zero-credential")
	summary, ok := value.(mcpUnshareSummary)
	if err != nil || !ok {
		t.Fatalf("unshare = %T(%+v) err=%v", value, value, err)
	}
	if !summary.OK || !summary.Removed || !summary.DeviceCleanupSkipped || summary.DeviceSkipReason != tailapi.ErrNoAPIClient.Error() {
		t.Fatalf("unshare summary = %+v", summary)
	}
}

func TestMCPUnshareMissingAgreesWithCLIDefaultIdempotency(t *testing.T) {
	restoreShareSeams(t)
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	const name = "missing"

	var cliOut, cliErrOut bytes.Buffer
	cliErr := removeServiceWithOptions(regPath, name, &cliOut, &cliErrOut, false, false)
	if cliErr != nil {
		t.Fatalf("CLI default remove returned error for missing service: %v", cliErr)
	}
	if got := cliOut.String(); got != "→ missing not registered, nothing to remove\n" {
		t.Fatalf("CLI default remove output = %q", got)
	}
	if cliErrOut.Len() != 0 {
		t.Fatalf("CLI default remove stderr = %q", cliErrOut.String())
	}

	value, mcpErr := defaultMCPActions(sharePaths{Registry: regPath}, os.Stderr).unshare(name)
	summary, ok := value.(mcpUnshareSummary)
	if mcpErr != nil || !ok {
		t.Fatalf("MCP unshare = %T(%+v) err=%v", value, value, mcpErr)
	}
	if summary.OK != (cliErr == nil) {
		t.Fatalf("MCP ok=%v disagrees with CLI default success=%v", summary.OK, cliErr == nil)
	}
	if !summary.OK || summary.Name != name || summary.Removed || summary.DeviceCleaned || summary.DeviceCleanupSkipped || summary.DeviceSkipReason != "" || summary.DeviceWarning != "" {
		t.Fatalf("MCP missing-service detail = %+v, want success with removed=false and preserved detail fields", summary)
	}
}

func TestMCPToolResultMarshalFailureAndOversizeInput(t *testing.T) {
	result := makeMCPToolResult(make(chan int), nil)
	if !result.IsError || len(result.Content) != 1 {
		t.Fatalf("result = %+v", result)
	}
	result = makeMCPToolResult(nil, errors.New("failed"))
	if !result.IsError || result.Content[0].Text != "failed" {
		t.Fatalf("error result = %+v", result)
	}
	result = makeMCPToolResult("scalar", nil)
	if !result.IsError {
		t.Fatalf("scalar result = %+v", result)
	}

	oversize := strings.Repeat("x", mcpMaxRecordBytes+2) + "\n" + `{"jsonrpc":"2.0","id":99,"method":"ping"}` + "\n"
	var stdout bytes.Buffer
	server := newMCPServer(strings.NewReader(oversize), &stdout, fakeMCPActions())
	if err := server.serve(context.Background()); err != nil {
		t.Fatalf("oversize input terminated session: %v", err)
	}
	frames := decodeMCPResponses(t, stdout.String())
	if len(frames) != 2 || frames[0]["error"].(map[string]any)["code"] != float64(-32600) || frames[1]["id"] != float64(99) || frames[1]["error"] != nil {
		t.Fatalf("frames = %+v", frames)
	}
}

func TestMCPInitializeAndDecodeValidation(t *testing.T) {
	for _, id := range []json.RawMessage{json.RawMessage(`true`), json.RawMessage(`{}`), json.RawMessage(`"unterminated`)} {
		if validMCPRequestID(id) {
			t.Fatalf("validMCPRequestID(%s) = true", id)
		}
	}
	var target struct{}
	if err := decodeMCPParams(json.RawMessage(`{} {}`), &target); err == nil {
		t.Fatal("multiple values accepted")
	}
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"initialize","params":{"protocolVersion":7}}`,
	}, "\n") + "\n"
	var stdout bytes.Buffer
	if err := newMCPServer(strings.NewReader(input), &stdout, fakeMCPActions()).serve(context.Background()); err != nil {
		t.Fatal(err)
	}
	frames := decodeMCPResponses(t, stdout.String())
	if len(frames) != 2 {
		t.Fatalf("frames = %+v", frames)
	}
	for _, frame := range frames {
		if frame["error"].(map[string]any)["code"] != float64(-32602) {
			t.Fatalf("frame = %+v", frame)
		}
	}
}

func TestMCPCommandRunsStdioWithoutNonFrames(t *testing.T) {
	restoreShareSeams(t)
	resetRootJSONFlag(t)
	dir := t.TempDir()
	shareEnsureDirFn = func() error { return nil }
	shareRegistryPathFn = func() (string, error) { return filepath.Join(dir, "registry.json"), nil }
	sharePIDPathFn = func() (string, error) { return filepath.Join(dir, "pid"), nil }
	shareSnapshotPathFn = func() (string, error) { return filepath.Join(dir, "runtime.json"), nil }
	shareAuthHandoffPathFn = func() (string, error) { return filepath.Join(dir, "auth.json"), nil }
	mcpCmd, _, err := rootCmd.Find([]string{"mcp"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		mcpCmd.SetIn(nil)
		mcpCmd.SetOut(nil)
		mcpCmd.SetErr(nil)
	})
	mcpCmd.SetIn(strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25"}}` + "\n"))
	var stdout, stderr bytes.Buffer
	mcpCmd.SetOut(&stdout)
	mcpCmd.SetErr(&stderr)
	if err := mcpCmd.RunE(mcpCmd, nil); err != nil {
		t.Fatal(err)
	}
	frames := decodeMCPResponses(t, stdout.String())
	if len(frames) != 1 || stderr.Len() != 0 {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestCompiledMCPStdioStdoutContainsOnlyJSONRPCFrames(t *testing.T) {
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"status","arguments":{}}}`,
	}, "\n") + "\n"
	stdout, stderr, exitCode := runCompiledTSLinkWithConfigDir(t, t.TempDir(), input, "mcp")
	if exitCode != 0 || stderr != "" {
		t.Fatalf("exit=%d stderr=%q stdout=%s", exitCode, stderr, stdout)
	}
	frames := decodeMCPResponses(t, stdout)
	if len(frames) != 3 {
		t.Fatalf("stdout contains non-frame data or missing frames: %q", stdout)
	}
	for _, frame := range frames {
		if frame["jsonrpc"] != "2.0" {
			t.Fatalf("non-JSON-RPC stdout frame: %+v", frame)
		}
	}
}

func TestCompiledMCPStdoutPurityProbeMatrix(t *testing.T) {
	initialized := func(request string) string { return initializedMCPInput(request) }
	cases := []struct {
		name  string
		input string
		args  []string
	}{
		{"initialize", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25"}}` + "\n", []string{"mcp"}},
		{"initialize metadata", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","_meta":{"progressToken":0}}}` + "\n", []string{"mcp"}},
		{"malformed then ping", "{\n" + `{"jsonrpc":"2.0","id":2,"method":"ping"}` + "\n", []string{"mcp"}},
		{"invalid JSON-RPC version", `{"jsonrpc":"1.0","id":1,"method":"initialize"}` + "\n", []string{"mcp"}},
		{"null id", `{"jsonrpc":"2.0","id":null,"method":"initialize","params":{}}` + "\n", []string{"mcp"}},
		{"before initialize", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}` + "\n", []string{"mcp"}},
		{"unknown notification", `{"jsonrpc":"2.0","method":"notifications/unknown"}` + "\n", []string{"mcp"}},
		{"unsupported version", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"future"}}` + "\n", []string{"mcp"}},
		{"double initialize", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25"}}` + "\n" + `{"jsonrpc":"2.0","id":2,"method":"initialize","params":{"protocolVersion":"2025-11-25"}}` + "\n", []string{"mcp"}},
		{"unknown method", initialized(`{"jsonrpc":"2.0","id":2,"method":"unknown"}`), []string{"mcp"}},
		{"ping", `{"jsonrpc":"2.0","id":1,"method":"ping"}` + "\n", []string{"mcp"}},
		{"tools list", initialized(`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{"_meta":{"progressToken":"list"}}}`), []string{"mcp"}},
		{"status", initialized(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"status","arguments":{}}}`), []string{"mcp"}},
		{"status metadata", initialized(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"status","arguments":{},"_meta":{"progressToken":0}}}`), []string{"mcp"}},
		{"unknown tool", initialized(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"unknown","arguments":{}}}`), []string{"mcp"}},
		{"invalid share argument", initialized(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"share","arguments":{"target":"3000","extra":true}}}`), []string{"mcp"}},
		{"missing share target", initialized(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"share","arguments":{}}}`), []string{"mcp"}},
		{"wrong share target type", initialized(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"share","arguments":{"target":3000}}}`), []string{"mcp"}},
		{"missing unshare name", initialized(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"unshare","arguments":{}}}`), []string{"mcp"}},
		{"oversize then ping", strings.Repeat("x", mcpMaxRecordBytes+2) + "\n" + `{"jsonrpc":"2.0","id":99,"method":"ping"}` + "\n", []string{"mcp"}},
		{"two oversize then ping", strings.Repeat("x", mcpMaxRecordBytes+2) + "\n" + strings.Repeat("y", mcpMaxRecordBytes+2) + "\n" + `{"jsonrpc":"2.0","id":99,"method":"ping"}` + "\n", []string{"mcp"}},
		{"share exposure conflict", initialized(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"share","arguments":{"target":"3000"}}}`), []string{"mcp"}},
		{"share requested-name conflict", initialized(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"share","arguments":{"target":"3000","name":"requested-name"}}}`), []string{"mcp"}},
		{"batch", `[{"jsonrpc":"2.0","id":1,"method":"ping"}]` + "\n", []string{"mcp"}},
		{"bad flag", "", []string{"mcp", "--badflag"}},
		{"extra argument", "", []string{"mcp", "extra"}},
		{"JSON flag", "", []string{"mcp", "--json"}},
	}
	if len(cases) != 27 {
		t.Fatalf("probe scenarios = %d, want 27", len(cases))
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			configDir := t.TempDir()
			var seed *registry.Service
			switch tc.name {
			case "share exposure conflict":
				seed = &registry.Service{Name: "public-demo", Type: registry.TypeProxy, Target: "http://localhost:3000", Ephemeral: true, Funnel: true, PublicAck: true}
			case "share requested-name conflict":
				seed = &registry.Service{Name: "existing-name", Type: registry.TypeProxy, Target: "http://localhost:3000", Ephemeral: true}
			}
			if seed != nil {
				if _, err := registry.Add(filepath.Join(configDir, "registry.json"), *seed); err != nil {
					t.Fatal(err)
				}
			}
			stdout, _, _ := runCompiledTSLinkWithConfigDir(t, configDir, tc.input, tc.args...)
			for _, line := range strings.Split(strings.TrimSuffix(stdout, "\n"), "\n") {
				if line == "" {
					continue
				}
				var frame map[string]any
				if err := json.Unmarshal([]byte(line), &frame); err != nil || frame["jsonrpc"] != "2.0" {
					t.Fatalf("stray stdout bytes: %q err=%v", stdout, err)
				}
			}
		})
	}
	t.Logf("stdout_purity_probe=%d/%d clean (including exposure and requested-name conflicts)", len(cases), len(cases))
}

func TestMCPCommandRejectsJSONWithoutWritingStdout(t *testing.T) {
	resetRootJSONFlag(t)
	mcpCmd, _, err := rootCmd.Find([]string{"mcp"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		mcpCmd.SetOut(nil)
		mcpCmd.SetErr(nil)
	})
	setRootJSONFlag(t, true)
	var stdout, stderr bytes.Buffer
	mcpCmd.SetOut(&stdout)
	mcpCmd.SetErr(&stderr)
	err = mcpCmd.RunE(mcpCmd, nil)
	if err == nil || !outputSilent(err) || stdout.Len() != 0 || !strings.Contains(stderr.String(), "stdout is reserved") {
		t.Fatalf("err=%v stdout=%q stderr=%q", err, stdout.String(), stderr.String())
	}
}

func outputSilent(err error) bool {
	return err != nil && strings.TrimSpace(err.Error()) == ""
}

func TestMCPServeStopsOnWriterError(t *testing.T) {
	w := errorWriter{}
	server := newMCPServer(strings.NewReader("{\n"), w, fakeMCPActions())
	if err := server.serve(context.Background()); err == nil || err.Error() != "write failed" {
		t.Fatalf("err = %v", err)
	}
}

type errorWriter struct{}

func (errorWriter) Write([]byte) (int, error) { return 0, fmt.Errorf("write failed") }
