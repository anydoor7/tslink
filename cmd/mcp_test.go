package cmd

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/anydoor7/tslink/internal/accesslog"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/inspect"
	"github.com/anydoor7/tslink/internal/mcpaudit"
	"github.com/anydoor7/tslink/internal/output"
	"github.com/anydoor7/tslink/internal/recipes"
	"github.com/anydoor7/tslink/internal/registry"
	tsruntime "github.com/anydoor7/tslink/internal/runtime"
	"github.com/anydoor7/tslink/internal/tailapi"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func fakeMCPActions() mcpActions {
	return mcpActions{
		extend: func(_ context.Context, args extendArguments) (any, error) {
			return registry.DurationChange{Service: args.Service, Who: args.Who, Audience: "tailnet_member"}, nil
		},
		guest: func(_ context.Context, name string, args guestArguments) (any, error) {
			return map[string]any{"grant": registry.GuestView{}, "grants": []registry.GuestView{}, "link": nil, "message": "guide", "edge_state": "pending"}, nil
		},
		requestsList: func(context.Context) (any, error) {
			return RequestListResult{Requests: []registry.AccessRequest{}}, nil
		},
		requestsDecide: func(_ context.Context, args requestDecisionArguments, approve bool) (any, error) {
			return RequestDecisionResult{Request: registry.AccessRequest{Status: "approved"}}, nil
		},
		accessLog: func(accessLogArguments) (accesslog.Result, error) {
			return accesslog.Result{Events: []accesslog.Event{}, Summary: accesslog.Summary{People: []accesslog.Count{}, Apps: []accesslog.Count{}}}, nil
		},
		accessSummary: func(accessLogArguments) (accesslog.Summary, error) {
			return accesslog.Summary{People: []accesslog.Count{}, Apps: []accesslog.Count{}}, nil
		},
		portalChange: func(_ context.Context, args portalArguments, enable bool) (any, error) {
			return map[string]any{"enabled": enable, "state": "pending", "hostname": args.Hostname}, nil
		},
		personApp: func(_ context.Context, who, app, lifetime string, revoke bool) (any, error) {
			return map[string]any{"person": PeopleView{Login: who, Grants: []PeopleGrantView{{PersonGrant: registry.PersonGrant{App: app}, Active: !revoke}}}, "app": app, "revoked": revoke}, nil
		},
		appRestart: func(_ context.Context, app string) (any, error) {
			return map[string]any{"app": app, "queued": true, "restart_generation": 1}, nil
		},
		auditRead: func() ([]mcpaudit.Entry, error) { return []mcpaudit.Entry{}, nil },
		peopleChange: func(_ context.Context, args peopleArguments, _ bool) (any, error) {
			return PeopleResult{Person: PeopleView{Login: args.Who, Grants: []PeopleGrantView{}}, Invites: []PeopleInviteView{}, Complete: true, Message: "guide", InviteRequirement: peopleInviteRequirement}, nil
		},
		peopleList: func() (any, error) { return PeopleListResult{People: []PeopleView{}}, nil },
		peopleRemove: func(_ context.Context, who string, _ map[string]string) (any, error) {
			return PeopleRemoveResult{Login: who, Removed: true, Revoked: true}, nil
		},
		share: func(_ context.Context, req shareRequest) (ShareResult, error) {
			if req.Target == "error" {
				return ShareResult{}, output.ErrUsage("share failed")
			}
			return ShareResult{Name: "demo", Status: authStatusNeedsLogin, AuthURL: "https://login.tailscale.com/a/mcp"}, nil
		},
		add: func(_ context.Context, params AddParams, _ bool) (any, error) {
			return AddResult{Name: params.Name, Type: registry.TypeProxy, Created: true, URLPending: true}, nil
		},
		list: func(ctx context.Context) (any, error) {
			return map[string]any{"services": []mcpServiceSummary{{Name: "demo", Type: registry.TypeProxy, State: "pending"}}}, nil
		},
		unshare: func(_ context.Context, name string) (any, error) { return map[string]any{"ok": name == "demo"}, nil },
		status: func(ctx context.Context) (any, error) {
			return mcpStatusSummary{GuestLinks: []registry.GuestView{}, Authenticated: false, DaemonRunning: true, ServiceCount: 1, Status: authStatusNeedsLogin, AuthURL: "https://login.tailscale.com/a/mcp"}, nil
		},
		url: func(_ context.Context, name string, _ time.Duration) (any, error) {
			return URLResult{Name: name, URL: "https://" + name + ".tail.ts.net", State: inspect.EndpointStateExact}, nil
		},
		tagsList: func() (any, error) {
			return TagsListResult{Services: []TagsServiceEntry{{Name: "demo", Tags: []string{"tag:tslink"}}}}, nil
		},
		tagsSet: func(ctx context.Context, service, tag string) (any, error) {
			return TagsSetResult{Service: service, Tags: []string{tag}}, nil
		},
		accessExplain: func(service string) (any, error) {
			return buildAccessExplainResult(registry.Service{Name: service, Type: registry.TypeProxy, Target: "http://localhost:3000"}), nil
		},
		doctor: func(context.Context, bool) (any, error) {
			return DoctorResult{GuestLinks: []registry.GuestView{},
				SchemaVersion:   inspect.SchemaVersion,
				ExecutionStatus: doctorExecutionCompleted,
				Status:          doctorStatusOK,
				HealthStatus:    doctorStatusOK,
				CredentialMode:  doctorCredentialNone,
				CredentialTier:  doctorCredentialTierUnknown,
				Findings:        []DoctorFinding{},
				RuntimeSnapshot: StatusRuntimeSnapshotResult{Status: "unknown"},
			}, nil
		},
		logs: func(args mcpLogsArguments) (any, error) {
			return MCPLogsResult{
				Source:   "err",
				File:     "/tmp/tslink.err.log",
				Level:    args.Level,
				Since:    mcpLogsDefaultSince.String(),
				Lines:    []string{"time=2026-09-14T00:00:00Z level=INFO msg=access"},
				Count:    1,
				Matched:  1,
				Redacted: true,
			}, nil
		},
		inviteUser: func(_ context.Context, email, role string, printLink bool) (any, error) {
			invite := tailapi.Invite{Kind: tailapi.InviteKindUser, ID: "1", Email: email, Role: role, Emailed: !printLink}
			return InviteMutationResult{Invite: invite, RemoteSideEffectPlan: invitePlan(invite, "create")}, nil
		},
		inviteDevice: func(_ context.Context, args mcpInviteDeviceArguments) (any, error) {
			invite := tailapi.Invite{Kind: tailapi.InviteKindDevice, ID: "2", Email: args.Email, Service: args.Service, Emailed: !args.PrintLink}
			return InviteMutationResult{Invite: invite, RemoteSideEffectPlan: invitePlan(invite, "create")}, nil
		},
		inviteList: func(context.Context, bool) (any, error) {
			return tailapi.InviteList{
				Complete:      true,
				UserInvites:   []tailapi.Invite{},
				DeviceInvites: []tailapi.Invite{},
				DeviceTargets: []tailapi.InviteTargetStatus{},
			}, nil
		},
		inviteRevoke: func(_ context.Context, kind, id string) (any, error) {
			invite := tailapi.Invite{Kind: kind, ID: id}
			return InviteRevokeResult{Kind: kind, ID: id, Revoked: true, RemoteSideEffectPlan: invitePlan(invite, "revoke")}, nil
		},
		inviteResend: func(_ context.Context, kind, id string) (any, error) {
			invite := tailapi.Invite{Kind: kind, ID: id, Email: "person@example.com", Emailed: true}
			return inviteResendResult(invite), nil
		},
		appsDetect: func(context.Context) (any, error) {
			return recipes.Detection{SchemaVersion: 1, Listeners: []recipes.Listener{}, Matches: []recipes.Match{}, Warnings: []string{}, Complete: true}, nil
		},
		recipeList: func() (any, error) { return recipes.List(), nil },
		recipeApply: func(_ context.Context, req recipeRequest, dry bool) (any, error) {
			result, _, err := planRecipe(req, "/missing/recipe-registry.json")
			return result, err
		},
		templateList: func() (any, error) { return listTemplatesResult(), nil },
		templatePlan: func(name string) (any, error) {
			result, _, err := planTemplateApply(name, nil, true)
			return result, err
		},
		templateApply: func(context.Context, string, bool) (any, error) {
			return TemplateApplyResult{SchemaVersion: inspect.SchemaVersion, Name: "local-web", Summary: "fake", Applied: true, Services: []TemplatePlanItem{}}, nil
		},
	}
}

func decodeMCPResponses(t *testing.T, output string) []map[string]any {
	t.Helper()
	var responses []map[string]any
	scanner := bufio.NewScanner(strings.NewReader(output))
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		// A JSON-RPC batch response is one line carrying an array of frames.
		if line[0] == '[' {
			var batch []map[string]any
			if err := json.Unmarshal(line, &batch); err != nil {
				t.Fatalf("unmarshal batch frame: %v (%s)", err, line)
			}
			responses = append(responses, batch...)
			continue
		}
		var response map[string]any
		if err := json.Unmarshal(line, &response); err != nil {
			t.Fatalf("unmarshal frame: %v (%s)", err, line)
		}
		responses = append(responses, response)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return responses
}

// runMCPSession drives one stdio MCP session to completion and returns
// everything the server wrote to stdout. The session runs over the same
// transport the shipped `tslink mcp` command uses, so a test that asserts on
// these frames is asserting on real wire output rather than on an in-process
// shortcut.
func runMCPSession(t *testing.T, input string, actions mcpActions) string {
	t.Helper()
	stdout, err := tryMCPSession(t, input, actions)
	if err != nil {
		t.Fatalf("mcp session: %v (stdout=%q)", err, stdout)
	}
	return stdout
}

// tryMCPSession is runMCPSession for inputs that are expected to end the
// session, returning the transport error instead of failing the test.
func tryMCPSession(t *testing.T, input string, actions mcpActions) (string, error) {
	t.Helper()
	var stdout bytes.Buffer
	err := runMCPStdio(context.Background(), strings.NewReader(input), &stdout, actions)
	return stdout.String(), err
}

// mcpFrameError returns the JSON-RPC error object of a frame, or nil.
func mcpFrameError(frame map[string]any) map[string]any {
	errorObject, _ := frame["error"].(map[string]any)
	return errorObject
}

// mcpFrameByID finds the frame answering one request id. Tool calls are handled
// concurrently by the SDK, so responses arrive in whatever order the handlers
// finish; a positional lookup would pass or fail on scheduling.
func mcpFrameByID(t *testing.T, frames []map[string]any, id any) map[string]any {
	t.Helper()
	for _, frame := range frames {
		if frame["id"] == id {
			return frame
		}
	}
	t.Fatalf("no frame answering id %v in %+v", id, frames)
	return nil
}

func TestMCPTranscriptInitializeListAndNeedsLoginShare(t *testing.T) {
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"share","arguments":{"target":"./report.html"}}}`,
	}, "\n") + "\n"
	stdout := runMCPSession(t, input, fakeMCPActions())
	frames := decodeMCPResponses(t, stdout)
	if len(frames) != 3 {
		t.Fatalf("frames = %d, want 3: %s", len(frames), stdout)
	}
	initialize := frames[0]["result"].(map[string]any)
	if initialize["protocolVersion"] != mcpLegacyHandshakeVersion {
		t.Fatalf("initialize = %+v", initialize)
	}
	assertMCPToolNames(t, mcpFrameByID(t, frames, float64(2)))
	call := mcpFrameByID(t, frames, float64(3))["result"].(map[string]any)
	structured := call["structuredContent"].(map[string]any)
	if structured["status"] != authStatusNeedsLogin || structured["auth_url"] != "https://login.tailscale.com/a/mcp" {
		t.Fatalf("share result = %+v", structured)
	}
	if call["isError"] != nil {
		t.Fatalf("needs_login must be a successful tool result: %+v", call)
	}
	t.Logf("MCP client frames (one JSON-RPC frame per line):\n%s", input)
	t.Logf("MCP server frames (one JSON-RPC frame per line):\n%s", stdout)
}

// assertMCPToolNames checks a tools/list frame against the declared tool set by
// name, not by count. A count comparison passes while a tool is silently
// renamed, which is the drift the whole MCP test file exists to catch.
func assertMCPToolNames(t *testing.T, frame map[string]any) {
	t.Helper()
	result, ok := frame["result"].(map[string]any)
	if !ok {
		t.Fatalf("tools/list frame carries no result: %+v", frame)
	}
	listed, ok := result["tools"].([]any)
	if !ok {
		t.Fatalf("tools/list result carries no tools array: %+v", result)
	}
	got := map[string]bool{}
	for _, raw := range listed {
		tool, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("tools/list entry is %T, want an object", raw)
		}
		name, _ := tool["name"].(string)
		if name == "" {
			t.Fatalf("tools/list entry has no name: %+v", tool)
		}
		if got[name] {
			t.Fatalf("tools/list repeated %q", name)
		}
		got[name] = true
	}
	if len(got) != len(mcpToolDefinitions) {
		t.Fatalf("tools/list returned %d tools, want %d", len(got), len(mcpToolDefinitions))
	}
	for _, definition := range mcpToolDefinitions {
		if !got[definition.Name] {
			t.Fatalf("tools/list omitted %q", definition.Name)
		}
	}
}

// mcpCurrentRevisionMeta is the per-request _meta a client speaking the current
// revision sends in place of the initialize handshake.
const mcpCurrentRevisionMeta = `"_meta":{"io.modelcontextprotocol/protocolVersion":"` + mcpProtocolVersion + `","io.modelcontextprotocol/clientCapabilities":{},"io.modelcontextprotocol/clientInfo":{"name":"test","version":"1"}}`

// TestMCPTranscriptCurrentRevisionNeedsNoHandshake is the capability the SDK
// migration bought: the current MCP revision removed the initialize handshake
// and moved the protocol version into each request's _meta. A client that
// speaks it sends tools/list and tools/call straight away, and the same tool
// surface answers.
func TestMCPTranscriptCurrentRevisionNeedsNoHandshake(t *testing.T) {
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{` + mcpCurrentRevisionMeta + `}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"share","arguments":{"target":"./report.html"},` + mcpCurrentRevisionMeta + `}}`,
	}, "\n") + "\n"
	stdout := runMCPSession(t, input, fakeMCPActions())
	frames := decodeMCPResponses(t, stdout)
	if len(frames) != 2 {
		t.Fatalf("frames = %d, want 2: %s", len(frames), stdout)
	}
	assertMCPToolNames(t, mcpFrameByID(t, frames, float64(1)))
	call, ok := mcpFrameByID(t, frames, float64(2))["result"].(map[string]any)
	if !ok {
		t.Fatalf("tools/call frame carries no result: %+v", frames[1])
	}
	structured := call["structuredContent"].(map[string]any)
	if structured["status"] != authStatusNeedsLogin || call["isError"] != nil {
		t.Fatalf("share result = %+v", call)
	}
	t.Logf("MCP client frames (one JSON-RPC frame per line):\n%s", input)
	t.Logf("MCP server frames (one JSON-RPC frame per line):\n%s", stdout)
}

// TestMCPInitializeAdvertisesOnlyWhatTheServerDoes pins the initialize result's
// identity and capability block. The SDK's defaults would add a logging feature
// this server has nothing to configure, and a tools.listChanged promise it can
// never keep, since the tool set is fixed at build time.
func TestMCPInitializeAdvertisesOnlyWhatTheServerDoes(t *testing.T) {
	frames := decodeMCPResponses(t, runMCPSession(t, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25"}}`+"\n", fakeMCPActions()))
	result := frames[0]["result"].(map[string]any)
	capabilities := result["capabilities"].(map[string]any)
	tools, ok := capabilities["tools"].(map[string]any)
	if !ok || len(tools) != 0 || len(capabilities) != 1 {
		t.Fatalf("capabilities = %+v, want exactly an empty tools object", capabilities)
	}
	serverInfo := result["serverInfo"].(map[string]any)
	if serverInfo["name"] != mcpServerName || serverInfo["version"] == "" {
		t.Fatalf("serverInfo = %+v", serverInfo)
	}
	instructions, _ := result["instructions"].(string)
	for _, want := range []string{"needs_login", "invite_user", "funnel true"} {
		if !strings.Contains(instructions, want) {
			t.Fatalf("instructions = %q, want it to mention %q", instructions, want)
		}
	}
}

// TestMCPStdioAnswersEveryRequestBeforeEndOfInput is the regression test for the
// transport swap's sharpest edge.
//
// A client that writes its requests and closes stdin is the normal way to
// script an MCP server, and it is how this repository's compiled-binary tests
// drive one. Handing the SDK's session the reader's io.EOF makes it cancel
// every request still in flight, so the last answers are lost — non-
// deterministically, which is worse than losing them every time. runMCPStdio
// therefore turns end-of-input into a drain. Without that, this test loses
// frames within a handful of iterations.
func TestMCPStdioAnswersEveryRequestBeforeEndOfInput(t *testing.T) {
	calls := []string{
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"status","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"list","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"template_list","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":6,"method":"ping"}`,
	}
	input := initializedMCPInput(strings.Join(calls, "\n"))
	for attempt := 0; attempt < 50; attempt++ {
		stdout := runMCPSession(t, input, fakeMCPActions())
		frames := decodeMCPResponses(t, stdout)
		for id := 1; id <= len(calls)+1; id++ {
			frame := mcpFrameByID(t, frames, float64(id))
			if mcpFrameError(frame) != nil {
				t.Fatalf("attempt %d: id %d answered with an error: %+v", attempt, id, frame)
			}
		}
		if len(frames) != len(calls)+1 {
			t.Fatalf("attempt %d: frames = %d, want %d: %s", attempt, len(frames), len(calls)+1, stdout)
		}
	}
}

// TestMCPStdioWaitsForAToolThatOutlivesItsInput keeps a running tool from being
// abandoned because the client finished writing. share can take the CLI's full
// 30s URL wait, and a client that queued it and closed stdin is still entitled
// to the answer.
func TestMCPStdioWaitsForAToolThatOutlivesItsInput(t *testing.T) {
	actions := fakeMCPActions()
	actions.status = func(ctx context.Context) (any, error) {
		time.Sleep(300 * time.Millisecond)
		return mcpStatusSummary{DaemonRunning: true, ServiceCount: 7}, nil
	}
	call := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"status","arguments":{}}}`
	frames := decodeMCPResponses(t, runMCPSession(t, initializedMCPInput(call), actions))
	result, ok := mcpFrameByID(t, frames, float64(2))["result"].(map[string]any)
	if !ok {
		t.Fatalf("slow tool was abandoned at end of input: %+v", frames)
	}
	structured := result["structuredContent"].(map[string]any)
	if structured["service_count"] != float64(7) {
		t.Fatalf("slow tool result = %+v", structured)
	}
}

// TestMCPRecordLimitReaderCountsAndGuards covers the reader's two jobs directly:
// the record accounting the drain depends on, and the two records it refuses.
func TestMCPRecordLimitReaderCountsAndGuards(t *testing.T) {
	t.Run("counts a final record with no trailing newline", func(t *testing.T) {
		reader := newMCPRecordLimitReader(strings.NewReader("one\ntwo"), mcpMaxRecordBytes)
		if _, err := io.ReadAll(io.LimitReader(reader, 7)); err != nil {
			t.Fatal(err)
		}
		// The limit reader stops before EOF, so pull once more to reach it.
		buffer := make([]byte, 8)
		if _, err := reader.Read(buffer); err != io.EOF {
			t.Fatalf("read after input = %v, want io.EOF", err)
		}
		if got := reader.Records(); got != 2 {
			t.Fatalf("records = %d, want 2", got)
		}
	})
	t.Run("blank lines are not records", func(t *testing.T) {
		reader := newMCPRecordLimitReader(strings.NewReader("\n\n{}\n"), mcpMaxRecordBytes)
		buffer := make([]byte, 16)
		if _, err := reader.Read(buffer); err != nil {
			t.Fatal(err)
		}
		if got := reader.Records(); got != 1 {
			t.Fatalf("records = %d, want 1", got)
		}
	})
	t.Run("a batch is refused", func(t *testing.T) {
		reader := newMCPRecordLimitReader(strings.NewReader(" [1]\n"), mcpMaxRecordBytes)
		buffer := make([]byte, 16)
		if _, err := reader.Read(buffer); !errors.Is(err, errMCPBatchUnsupported) {
			t.Fatalf("read = %v, want the batch refusal", err)
		}
	})
	t.Run("an oversize record is refused", func(t *testing.T) {
		reader := newMCPRecordLimitReader(strings.NewReader(strings.Repeat("a", 12)), 8)
		buffer := make([]byte, 16)
		if _, err := reader.Read(buffer); err == nil || !strings.Contains(err.Error(), "exceeds maximum size") {
			t.Fatalf("read = %v, want the size refusal", err)
		}
	})
}

// TestMCPProtocolVersionSupportMatrix pins the revision set this server
// advertises in `tslink mcp --help` against what it actually negotiates, so the
// documented matrix cannot become a claim the server does not honour.
func TestMCPProtocolVersionSupportMatrix(t *testing.T) {
	if mcpSupportedProtocolVersions[0] != mcpProtocolVersion {
		t.Fatalf("supported versions must list %q first, got %v", mcpProtocolVersion, mcpSupportedProtocolVersions)
	}
	for _, version := range mcpSupportedProtocolVersions {
		t.Run(version, func(t *testing.T) {
			input := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"` + version + `"}}` + "\n"
			frames := decodeMCPResponses(t, runMCPSession(t, input, fakeMCPActions()))
			if len(frames) != 1 || mcpFrameError(frames[0]) != nil {
				t.Fatalf("initialize %s = %+v", version, frames)
			}
			negotiated := frames[0]["result"].(map[string]any)["protocolVersion"]
			// The handshake itself is deprecated in the current revision, so a
			// client that asks for it over initialize is answered with the last
			// revision the handshake describes; it reaches the current one by
			// sending _meta instead, which
			// TestMCPTranscriptCurrentRevisionNeedsNoHandshake covers.
			want := version
			if version == mcpProtocolVersion {
				want = mcpLegacyHandshakeVersion
			}
			if negotiated != want {
				t.Fatalf("initialize %s negotiated %v, want %q", version, negotiated, want)
			}
		})
	}

	// An initialize that omits the version entirely, and one that names a
	// revision outside the matrix, are both answered with a version this server
	// actually speaks rather than with the client's own claim.
	for _, params := range []string{`{}`, `{"protocolVersion":"1999-01-01"}`} {
		answered := decodeMCPResponses(t, runMCPSession(t, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":`+params+`}`+"\n", fakeMCPActions()))
		version, _ := answered[0]["result"].(map[string]any)["protocolVersion"].(string)
		if !containsString(mcpSupportedProtocolVersions, version) {
			t.Fatalf("initialize params %s negotiated %q, which is outside %v", params, version, mcpSupportedProtocolVersions)
		}
	}

	// A revision outside the matrix is not silently accepted at its own
	// version: the handshake answers with one this server actually speaks.
	frames := decodeMCPResponses(t, runMCPSession(t, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"1999-01-01"}}`+"\n", fakeMCPActions()))
	negotiated, _ := frames[0]["result"].(map[string]any)["protocolVersion"].(string)
	if !containsString(mcpSupportedProtocolVersions, negotiated) {
		t.Fatalf("unknown client version negotiated %q, which is outside %v", negotiated, mcpSupportedProtocolVersions)
	}

	// And a request that names an unsupported revision in _meta is refused
	// rather than served at some other version.
	unsupported := `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2099-01-01","io.modelcontextprotocol/clientCapabilities":{}}}}` + "\n"
	stdout, _ := tryMCPSession(t, unsupported, fakeMCPActions())
	frames = decodeMCPResponses(t, stdout)
	if len(frames) != 1 || mcpFrameError(frames[0]) == nil {
		t.Fatalf("unsupported _meta version = %+v, want a protocol error", frames)
	}
}

// mcpToolByName returns the definition a test names, failing loudly rather than
// indexing into mcpToolDefinitions. Position-indexed assertions silently
// re-target when a tool is inserted, which is exactly the drift these tests
// exist to catch.
func mcpToolByName(t *testing.T, name string) mcpToolDefinition {
	t.Helper()
	for _, tool := range mcpToolDefinitions {
		if tool.Name == name {
			return tool
		}
	}
	t.Fatalf("MCP tool %q is not defined", name)
	return mcpToolDefinition{}
}

func TestMCPToolSchemasAreClosedAndModelFocused(t *testing.T) {
	wantNames := []string{
		"share", "add", "list", "unshare", "status", "url",
		"tags_list", "tags_set", "access_explain", "doctor", "logs",
		"invite_user", "invite_device", "invite_list", "invite_revoke", "invite_resend",
		"template_list", "template_plan", "template_apply",
		"access_log", "access_summary",
		"apps_detect", "recipe_list", "recipe_plan", "recipe_apply",
		"extend", "guest_create", "guest_list", "guest_show", "guest_revoke",
		"people_add", "people_update", "people_list", "people_remove",
		"portal_enable", "portal_disable",
		"requests_list", "requests_approve", "requests_deny",
		"people_grant", "people_revoke", "app_restart", "health", "mcp_audit",
	}
	if len(mcpToolDefinitions) != len(wantNames) {
		t.Fatalf("tools = %d, want %d", len(mcpToolDefinitions), len(wantNames))
	}
	seen := map[string]bool{}
	for i, tool := range mcpToolDefinitions {
		if tool.Name != wantNames[i] || tool.Description == "" {
			t.Fatalf("tool[%d] = %q, want %q with a description", i, tool.Name, wantNames[i])
		}
		if seen[tool.Name] {
			t.Fatalf("tool %q is defined twice", tool.Name)
		}
		seen[tool.Name] = true
		if tool.InputSchema["type"] != "object" || tool.InputSchema["additionalProperties"] != false {
			t.Fatalf("schema %s = %+v", tool.Name, tool.InputSchema)
		}
		if tool.OutputSchema["type"] != "object" || tool.OutputSchema["additionalProperties"] != false {
			t.Fatalf("output schema %s = %+v", tool.Name, tool.OutputSchema)
		}
		for property, raw := range tool.InputSchema["properties"].(map[string]any) {
			schema, ok := raw.(map[string]any)
			if !ok || schema["type"] == nil {
				t.Fatalf("%s input property %q has no declared type: %+v", tool.Name, property, raw)
			}
		}
	}

	shareSchema := mcpToolByName(t, "share").InputSchema
	required := shareSchema["required"].([]string)
	properties := shareSchema["properties"].(map[string]any)
	if len(required) != 1 || required[0] != "target" || len(properties) != 11 {
		t.Fatalf("share schema = %+v", shareSchema)
	}
	nameDescription := properties["name"].(map[string]any)["description"].(string)
	if !strings.Contains(nameDescription, "reused only if it already has this name") || !strings.Contains(nameDescription, "numeric suffix") {
		t.Fatalf("share name description = %q", nameDescription)
	}
	allowDescription := properties["allow"].(map[string]any)["description"].(string)
	if !strings.Contains(allowDescription, "every member of the user's tailnet") {
		t.Fatalf("share allow description = %q, want the no-allow-list consequence spelled out", allowDescription)
	}

	// The refusal runs in the daemon, but the calling model only sees the tool
	// schema; the description is where it learns the target rule before it
	// picks an address. Deleting the sentence leaves the daemon safe and the
	// model blind, so it is asserted here rather than trusted.
	for _, name := range []string{"share", "add"} {
		targetDescription := mcpToolByName(t, name).InputSchema["properties"].(map[string]any)["target"].(map[string]any)["description"].(string)
		if !strings.Contains(targetDescription, "link-local and cloud-metadata addresses are refused") {
			t.Fatalf("%s target description = %q, want the link-local/cloud-metadata refusal spelled out", name, targetDescription)
		}
	}

	unshareProperties := mcpToolByName(t, "unshare").OutputSchema["properties"].(map[string]any)
	// B3-5 moved the idempotency guidance from the dropped ok field to removed.
	if _, present := unshareProperties["ok"]; present {
		t.Fatal("unshare output schema still declares ok; the result is tslink remove's")
	}
	unshareOKDescription := unshareProperties["removed"].(map[string]any)["description"].(string)
	for _, want := range []string{"idempotent", "service absent", "removed false", "does not guarantee tailnet device cleanup", "device_cleaned", "device_warning"} {
		if !strings.Contains(unshareOKDescription, want) {
			t.Fatalf("unshare ok description = %q, want %q", unshareOKDescription, want)
		}
	}
	for _, name := range []string{"list", "status", "tags_list", "template_list"} {
		if mcpToolByName(t, name).InputSchema["required"] != nil {
			t.Fatalf("no-argument tool %q unexpectedly requires fields", name)
		}
	}
}

// TestMCPSideEffectToolsDeclareTheirEffectInTheFirstSentence pins the one
// safety mechanism this server has for tools that reach outside the machine:
// the calling agent is told, before it decides, that the call sends a real
// email or publishes to the public internet. Withholding those tools was
// rejected as the mechanism; the description is it, so it is asserted.
func TestMCPSideEffectToolsDeclareTheirEffectInTheFirstSentence(t *testing.T) {
	cases := []struct {
		tool  string
		wants []string
	}{
		{"share", []string{"funnel true", "public internet", "ask the user"}},
		{"add", []string{"funnel true", "public internet", "ask the user"}},
		{"invite_user", []string{"Sends a real Tailscale invitation", "confirm"}},
		{"invite_device", []string{"Sends a real device-sharing invitation", "confirm"}},
		{"invite_revoke", []string{"Cancels a real outstanding invitation", "confirm"}},
		{"invite_resend", []string{"Sends another real invitation email", "confirm"}},
	}
	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			description := mcpToolByName(t, tc.tool).Description
			first, _, found := strings.Cut(description, ". ")
			if !found {
				t.Fatalf("%s description has no first sentence: %q", tc.tool, description)
			}
			for _, want := range tc.wants {
				if !strings.Contains(first, want) {
					t.Fatalf("%s first sentence = %q, want it to contain %q", tc.tool, first, want)
				}
			}
		})
	}

	// The read-only and local-only tools must not carry that language, or the
	// warning stops meaning anything.
	for _, name := range []string{"list", "status", "url", "tags_list", "access_explain", "doctor", "logs", "invite_list", "template_list", "template_plan"} {
		first, _, _ := strings.Cut(mcpToolByName(t, name).Description, ". ")
		for _, unwanted := range []string{"public internet", "Sends a real", "Cancels a real"} {
			if strings.Contains(first, unwanted) {
				t.Fatalf("%s first sentence = %q, but it has no such side effect", name, first)
			}
		}
	}
}

func TestMCPListSchemaEnumsReuseManifestValues(t *testing.T) {
	items := mcpListOutputSchema["properties"].(map[string]any)["services"].(map[string]any)["items"].(map[string]any)
	properties := items["properties"].(map[string]any)
	tests := []struct {
		field string
		want  []string
	}{
		{field: "type", want: serviceTypeValues()},
		{field: "state", want: commandJSONResultFields("tslink list")["services[].state"].Values},
		{field: "funnel_state", want: commandJSONResultFields("tslink list")["services[].funnel_state"].Values},
	}
	for _, tc := range tests {
		got := properties[tc.field].(map[string]any)["enum"].([]string)
		if !reflect.DeepEqual(sliceSet(got), sliceSet(tc.want)) {
			t.Fatalf("%s enum=%v manifest=%v", tc.field, got, tc.want)
		}
	}
}

func TestMCPListHealthyAndFailedPayloadsValidateAgainstOutputSchema(t *testing.T) {
	healthy := ListServiceSummary{
		Name:            "healthy",
		Type:            registry.TypeProxy,
		URLPending:      true,
		State:           listStatePending,
		FunnelRequested: false,
		FunnelActive:    false,
		FunnelState:     tsruntime.FunnelStateNotRequested,
	}
	failed := ListServiceSummary{
		Name:            "failed",
		Type:            registry.TypeProxy,
		URLPending:      true,
		State:           tsruntime.ServiceRuntimeFailed,
		FunnelRequested: true,
		FunnelActive:    false,
		FunnelState:     tsruntime.FunnelStateCapabilityMissing,
		Error: &tsruntime.ServiceError{
			Code:    registry.CodeFunnelCapabilityMissing,
			Message: "capability missing",
			Next:    []string{"tslink status --urls --name failed --json"},
		},
	}
	expiresAt := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	remaining := "23h59m"
	funnelled := ListServiceSummary{
		Name:            "public",
		Type:            registry.TypeProxy,
		URLPending:      false,
		URL:             func() *string { url := "https://public.tail.ts.net"; return &url }(),
		State:           inspect.EndpointStateExact,
		FunnelRequested: true,
		FunnelActive:    true,
		FunnelState:     tsruntime.FunnelStateActive,
		FunnelExpiresAt: &expiresAt,
		FunnelRemaining: &remaining,
	}
	for _, tc := range []struct {
		name    string
		service ListServiceSummary
	}{
		{name: "healthy omits error", service: healthy},
		{name: "failed carries error", service: failed},
		{name: "funnel carries its deadline", service: funnelled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wire, err := json.Marshal(map[string]any{"services": []ListServiceSummary{tc.service}})
			if err != nil {
				t.Fatal(err)
			}
			var payload any
			if err := json.Unmarshal(wire, &payload); err != nil {
				t.Fatal(err)
			}
			if err := validateMCPJSONSchema(mcpListOutputSchema, payload, "$"); err != nil {
				t.Fatalf("payload=%s schema error: %v", wire, err)
			}
		})
	}
}

// validateMCPJSONSchema validates the closed JSON Schema subset emitted by the
// MCP definitions: type, enum, minimum, object properties/required/
// additionalProperties, and array items. Keeping the validator independent of
// production serialization lets semantic schema mutations fail the tests.
func validateMCPJSONSchema(schema map[string]any, value any, path string) error {
	if rawType, ok := schema["type"]; ok && !mcpSchemaTypeAllows(rawType, value) {
		return fmt.Errorf("%s type %T does not satisfy %v", path, value, rawType)
	}
	if enum, ok := schema["enum"].([]string); ok {
		text, isString := value.(string)
		if !isString || !containsString(enum, text) {
			return fmt.Errorf("%s value %v is not in enum %v", path, value, enum)
		}
	}
	if minimum, ok := schema["minimum"].(int); ok {
		number, isNumber := value.(float64)
		if !isNumber || number < float64(minimum) {
			return fmt.Errorf("%s value %v is below minimum %d", path, value, minimum)
		}
	}
	if object, ok := value.(map[string]any); ok {
		properties, _ := schema["properties"].(map[string]any)
		if required, ok := schema["required"].([]string); ok {
			for _, name := range required {
				if _, exists := object[name]; !exists {
					return fmt.Errorf("%s missing required property %q", path, name)
				}
			}
		}
		for name, child := range object {
			childSchema, known := properties[name]
			if !known {
				if schema["additionalProperties"] == false {
					return fmt.Errorf("%s has unexpected property %q", path, name)
				}
				continue
			}
			if err := validateMCPJSONSchema(childSchema.(map[string]any), child, path+"."+name); err != nil {
				return err
			}
		}
	}
	if array, ok := value.([]any); ok {
		if items, ok := schema["items"].(map[string]any); ok {
			for i, child := range array {
				if err := validateMCPJSONSchema(items, child, fmt.Sprintf("%s[%d]", path, i)); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func mcpSchemaTypeAllows(raw any, value any) bool {
	allowed := []string{}
	switch typed := raw.(type) {
	case string:
		allowed = []string{typed}
	case []string:
		allowed = typed
	case []any:
		for _, item := range typed {
			if name, ok := item.(string); ok {
				allowed = append(allowed, name)
			}
		}
	}
	for _, name := range allowed {
		switch name {
		case "null":
			if value == nil {
				return true
			}
		case "object":
			if _, ok := value.(map[string]any); ok {
				return true
			}
		case "array":
			if _, ok := value.([]any); ok {
				return true
			}
		case "string":
			if _, ok := value.(string); ok {
				return true
			}
		case "boolean":
			if _, ok := value.(bool); ok {
				return true
			}
		case "integer":
			if number, ok := value.(float64); ok && number == float64(int64(number)) {
				return true
			}
		}
	}
	return false
}

// TestMCPProtocolErrorsAndLifecycle covers what happens to a frame that is not
// a well-formed, in-sequence request.
//
// Before the SDK this package chose the JSON-RPC codes itself and could answer
// a bad frame and carry on; the SDK owns framing now and treats a corrupt
// stream as a reason to stop reading it, which is a stricter answer, not a
// looser one. So the assertions moved off the specific codes and onto the two
// properties that matter to a caller: a frame that is not a valid, in-sequence
// request never produces a successful result, and it never reaches a tool.
func TestMCPProtocolErrorsAndLifecycle(t *testing.T) {
	cases := []struct {
		name string
		// input is fed to a fresh session.
		input string
		// wantErrorFrame requires an error response; without it the frame is
		// only required not to be a result (a corrupt stream may be answered
		// with nothing at all).
		wantErrorFrame bool
		// wantErrorCode, when non-zero, pins a code the protocol specifies
		// rather than one the SDK is free to choose.
		wantErrorCode float64
	}{
		{name: "parse", input: "{\n"},
		{name: "invalid jsonrpc version", input: `{"jsonrpc":"1.0","id":1,"method":"initialize"}` + "\n"},
		{name: "null id", input: `{"jsonrpc":"2.0","id":null,"method":"initialize","params":{}}` + "\n"},
		{name: "batch", input: `[{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"status","arguments":{}}}]` + "\n"},
		{name: "batch after initialize", input: initializedMCPInput(`[{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"status","arguments":{}}}]`)},
		{name: "batch behind leading whitespace", input: "  \t" + `[{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"status","arguments":{}}}]` + "\n"},
		{name: "before initialize", input: `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"status","arguments":{}}}` + "\n", wantErrorFrame: true},
		{name: "unknown notification", input: `{"jsonrpc":"2.0","method":"notifications/unknown"}` + "\n"},
		{name: "unknown method", input: initializedMCPInput(`{"jsonrpc":"2.0","id":2,"method":"unknown"}`), wantErrorFrame: true, wantErrorCode: -32601},
		{name: "duplicate initialize", input: `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25"}}` + "\n" + `{"jsonrpc":"2.0","id":2,"method":"initialize","params":{"protocolVersion":"2025-11-25"}}` + "\n", wantErrorFrame: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			actions := fakeMCPActions()
			actions.status = func(ctx context.Context) (any, error) {
				called = true
				return mcpStatusSummary{}, nil
			}
			stdout, _ := tryMCPSession(t, tc.input, actions)
			frames := decodeMCPResponses(t, stdout)
			if called {
				t.Fatalf("a tool ran for %q", tc.input)
			}
			var errorFrame map[string]any
			for _, frame := range frames {
				if frame["jsonrpc"] != "2.0" {
					t.Fatalf("non-JSON-RPC stdout frame: %+v", frame)
				}
				// initializedMCPInput's own handshake, which carries id 1, is
				// allowed to succeed; the frame under test is the one after it.
				if frame["id"] == float64(1) && mcpFrameError(frame) == nil {
					continue
				}
				if mcpFrameError(frame) != nil {
					errorFrame = frame
					continue
				}
				t.Fatalf("malformed frame produced a result: %+v", frame)
			}
			if tc.wantErrorFrame && errorFrame == nil {
				t.Fatalf("frames = %+v, want an error response", frames)
			}
			if tc.wantErrorCode != 0 && mcpFrameError(errorFrame)["code"] != tc.wantErrorCode {
				t.Fatalf("error = %+v, want code %v", mcpFrameError(errorFrame), tc.wantErrorCode)
			}
		})
	}

	// ping is answered without a handshake, and a session that has been
	// initialized keeps answering after a method it does not implement.
	frames := decodeMCPResponses(t, runMCPSession(t, initializedMCPInput(`{"jsonrpc":"2.0","id":2,"method":"unknown"}`+"\n"+`{"jsonrpc":"2.0","id":3,"method":"ping"}`), fakeMCPActions()))
	if mcpFrameError(mcpFrameByID(t, frames, float64(2))) == nil {
		t.Fatalf("unknown method was not refused: %+v", frames)
	}
	if pong := mcpFrameByID(t, frames, float64(3)); pong["result"] == nil {
		t.Fatalf("ping after an unknown method = %+v", pong)
	}
}

func TestMCPRequestParamsAcceptMetadataAndExtensions(t *testing.T) {
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"metadata-test","version":"1"},"_meta":{"progressToken":0},"client_extension":true}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized","params":{"_meta":{"source":"test"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"ping","params":{"_meta":{"progressToken":"ping"},"extension":"accepted"}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/list","params":{"_meta":{"progressToken":"list"},"client_extension":"accepted"}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"status","arguments":{},"_meta":{"progressToken":0},"client_extension":{"trace":"accepted"}}}`,
	}, "\n") + "\n"
	stdout := runMCPSession(t, input, fakeMCPActions())
	frames := decodeMCPResponses(t, stdout)
	if len(frames) != 4 {
		t.Fatalf("frames=%d output=%s", len(frames), stdout)
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
	// Only an unknown tool is a protocol error. Arguments that do not fit a
	// tool's schema are input validation errors, which the specification
	// makes tool execution errors (A3-1), and name what is wrong.
	cases := []struct {
		name         string
		call         string
		wantProtocol bool
		wantMessage  string
	}{
		{"missing share target", `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"share","arguments":{}}}`, false, "target is required"},
		{"unknown share field", `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"share","arguments":{"target":"3000","extra":true}}}`, false, `unknown field "extra"`},
		{"unknown tool", `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"other","arguments":{}}}`, true, ""},
		{"list arguments", `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"list","arguments":{"extra":1}}}`, false, `unknown field "extra"`},
		{"unshare arguments", `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"unshare","arguments":{}}}`, false, "name is required"},
		{"status arguments", `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"status","arguments":{"extra":1}}}`, false, `unknown field "extra"`},
		{"execution error", `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"share","arguments":{"target":"error","ephemeral":false}}}`, false, "share failed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			frames := decodeMCPResponses(t, runMCPSession(t, initializedMCPInput(tc.call), fakeMCPActions()))
			last := frames[len(frames)-1]
			if tc.wantProtocol {
				if errorObject, _ := last["error"].(map[string]any); errorObject["code"] != float64(-32602) {
					t.Fatalf("response = %+v", last)
				}
				return
			}
			result, _ := last["result"].(map[string]any)
			if result["isError"] != true || result["structuredContent"] != nil {
				t.Fatalf("response = %+v, want an error result without structuredContent", last)
			}
			errorObject := decodeMCPFailureText(t, result["content"].([]any)[0].(map[string]any)["text"].(string))
			if errorObject["code"] != "usage_error" || len(errorObject["next"].([]any)) == 0 || !strings.Contains(errorObject["message"].(string), tc.wantMessage) {
				t.Fatalf("execution error = %+v, want usage_error with next steps and %q", errorObject, tc.wantMessage)
			}
		})
	}
}

func TestMCPAllToolsAndOptionalEphemeral(t *testing.T) {
	var ephemeral bool
	actions := fakeMCPActions()
	actions.share = func(_ context.Context, req shareRequest) (ShareResult, error) {
		ephemeral = req.Ephemeral
		return ShareResult{Name: "demo", URL: "https://demo.tail.ts.net", Status: shareStatusReady}, nil
	}
	calls := []string{
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"share","arguments":{"target":"3000","ephemeral":false}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"list"}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"unshare","arguments":{"name":"demo"}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"status","arguments":{}}}`,
	}
	input := initializedMCPInput(strings.Join(calls, "\n"))
	stdout := runMCPSession(t, input, actions)
	frames := decodeMCPResponses(t, stdout)
	if len(frames) != 5 || ephemeral {
		t.Fatalf("frames=%d ephemeral=%v output=%s", len(frames), ephemeral, stdout)
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
		Registry: filepath.Join(dir, "registry.json"),
		// unshare records the removal in the ownership ledger under its lock,
		// so the fixture carries the ledger path production always resolves.
		Ownership:   filepath.Join(dir, "node-ownership.json"),
		PID:         filepath.Join(dir, "pid"),
		Snapshot:    filepath.Join(dir, "runtime.json"),
		AuthHandoff: filepath.Join(dir, "auth.json"),
	}
	if _, err := registry.Add(paths.Registry, registry.Service{Name: "demo", Type: registry.TypeProxy, Target: "http://localhost:3000"}); err != nil {
		t.Fatal(err)
	}
	oldMCPStatus := mcpStatusFn
	t.Cleanup(func() { mcpStatusFn = oldMCPStatus })
	mcpStatusFn = func(_ context.Context, _, _, _, _ string) (StatusResult, error) {
		return StatusResult{DaemonRunning: true, CredentialStored: true, AuthStatus: authStatusNeedsLogin, AuthURL: "https://login.tailscale.com/a/status", ServiceCount: 1}, nil
	}
	oldDelete := deleteDevicesFn
	t.Cleanup(func() { deleteDevicesFn = oldDelete })
	deleteDevicesFn = func(_ context.Context, target tailapi.CleanupTarget) (tailapi.CleanupResult, error) {
		return tailapi.CleanupResult{Deleted: []string{target.Hostname}}, nil
	}
	shareIsRunningFn = func(string) bool { return true }
	shareResolveEndpointOnceFn = func(_ context.Context, _, _, _, name string) (serviceURLResolution, error) {
		return serviceURLResolution{Result: URLResult{Name: name, URL: "https://" + name + ".tail.ts.net"}}, nil
	}
	actions := defaultMCPActions(paths, os.Stderr)
	shared, err := actions.share(context.Background(), shareRequest{Target: "3000", Ephemeral: true})
	if err != nil || shared.URL != "https://port-3000.tail.ts.net" {
		t.Fatalf("share = %+v err=%v", shared, err)
	}
	listValue, err := actions.list(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	listed := listValue.(map[string]any)["services"].([]mcpServiceSummary)
	if len(listed) != 2 || listed[0].Name != "demo" || listed[0].URL != nil {
		t.Fatalf("list = %+v", listValue)
	}
	statusValue, err := actions.status(context.Background())
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
	removed, err := actions.unshare(context.Background(), "demo")
	removedSummary, ok := removed.(RemoveResult)
	if err != nil || !ok || !removedSummary.Removed || !removedSummary.DeviceCleaned || removedSummary.DeviceCleanupSkipped {
		t.Fatalf("unshare = %+v err=%v", removed, err)
	}
	if _, err := registry.Add(paths.Registry, registry.Service{Name: "partial", Type: registry.TypeProxy, Target: "http://localhost:4000"}); err != nil {
		t.Fatal(err)
	}
	deleteDevicesFn = func(_ context.Context, target tailapi.CleanupTarget) (tailapi.CleanupResult, error) {
		return tailapi.CleanupResult{Matched: []string{target.Hostname}, Protected: []string{target.Hostname}, Skipped: true, SkipReason: "ownership could not be proven"}, nil
	}
	partialValue, err := actions.unshare(context.Background(), "partial")
	partial := partialValue.(RemoveResult)
	if err != nil || !partial.Removed || !partial.DeviceCleanupSkipped || partial.DeviceSkipReason != "ownership could not be proven" {
		t.Fatalf("partial unshare = %+v err=%v", partial, err)
	}
	if _, err := actions.unshare(context.Background(), "Bad_Name"); err == nil {
		t.Fatal("invalid name accepted")
	}
	listValue, err = actions.list(context.Background())
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
	paths := sharePaths{Registry: filepath.Join(dir, "registry.json"), Ownership: filepath.Join(dir, "node-ownership.json")}
	if _, err := registry.Add(paths.Registry, registry.Service{Name: "zero-credential", Type: registry.TypeProxy, Target: "http://localhost:3000"}); err != nil {
		t.Fatal(err)
	}
	oldDelete := deleteDevicesFn
	t.Cleanup(func() { deleteDevicesFn = oldDelete })
	deleteDevicesFn = func(context.Context, tailapi.CleanupTarget) (tailapi.CleanupResult, error) {
		return tailapi.CleanupResult{Skipped: true, SkipReason: tailapi.ErrNoAPIClient.Error()}, nil
	}

	value, err := defaultMCPActions(paths, os.Stderr).unshare(context.Background(), "zero-credential")
	summary, ok := value.(RemoveResult)
	if err != nil || !ok {
		t.Fatalf("unshare = %T(%+v) err=%v", value, value, err)
	}
	if !summary.Removed || !summary.DeviceCleanupSkipped || summary.DeviceSkipReason != tailapi.ErrNoAPIClient.Error() {
		t.Fatalf("unshare summary = %+v", summary)
	}
}

func TestMCPUnshareMissingAgreesWithCLIDefaultIdempotency(t *testing.T) {
	restoreShareSeams(t)
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	const name = "missing"

	var cliOut, cliErrOut bytes.Buffer
	cliErr := removeServiceWithOptions(regPath, testOwnershipPath(regPath), name, &cliOut, &cliErrOut, false, false)
	if cliErr != nil {
		t.Fatalf("CLI default remove returned error for missing service: %v", cliErr)
	}
	if got := cliOut.String(); got != "→ missing not registered, nothing to remove\n" {
		t.Fatalf("CLI default remove output = %q", got)
	}
	if cliErrOut.Len() != 0 {
		t.Fatalf("CLI default remove stderr = %q", cliErrOut.String())
	}

	value, mcpErr := defaultMCPActions(sharePaths{Registry: regPath, Ownership: testOwnershipPath(regPath)}, os.Stderr).unshare(context.Background(), name)
	summary, ok := value.(RemoveResult)
	if mcpErr != nil || !ok {
		t.Fatalf("MCP unshare = %T(%+v) err=%v", value, value, mcpErr)
	}
	// B3-5 dropped unshare's ok field: success is the tool result not being
	// an error, as for every other tool.
	if (mcpErr == nil) != (cliErr == nil) {
		t.Fatalf("MCP success=%v disagrees with CLI default success=%v", mcpErr == nil, cliErr == nil)
	}
	if summary.Name != name || summary.Removed || summary.DeviceCleaned || summary.DeviceCleanupSkipped || summary.DeviceSkipReason != "" || summary.DeviceWarning != "" {
		t.Fatalf("MCP missing-service detail = %+v, want success with removed=false and preserved detail fields", summary)
	}
}

// mcpResultText returns the single text content item of a tool result.
func mcpResultText(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	if len(result.Content) != 1 {
		t.Fatalf("result carries %d content items, want 1: %+v", len(result.Content), result)
	}
	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("result content is %T, want *mcp.TextContent", result.Content[0])
	}
	return text.Text
}

// mcpResultStructured returns the structured content of a tool result.
func mcpResultStructured(t *testing.T, result *mcp.CallToolResult) map[string]any {
	t.Helper()
	structured, ok := result.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("structuredContent is %T, want map[string]any", result.StructuredContent)
	}
	return structured
}

func TestMCPToolResultMarshalFailureAndCodedErrors(t *testing.T) {
	result := makeMCPToolResult(make(chan int), nil)
	if !result.IsError || len(result.Content) != 1 {
		t.Fatalf("result = %+v", result)
	}
	result = makeMCPToolResult(nil, errors.New("failed"))
	failure := decodeMCPFailureText(t, mcpResultText(t, result))
	if !result.IsError || result.StructuredContent != nil || failure["message"] != "failed" || failure["code"] != output.StableErrorCode(output.ExitError) {
		t.Fatalf("error result = %+v", result)
	}
	coded := makeMCPToolResult(nil, registry.ValidateName("Bad_Name"))
	errorObject := decodeMCPFailureText(t, mcpResultText(t, coded))
	if next, _ := errorObject["next"].([]any); !coded.IsError || coded.StructuredContent != nil || errorObject["code"] != registry.CodeInvalidServiceName || len(next) == 0 {
		t.Fatalf("coded error result = %+v", coded)
	}
	result = makeMCPToolResult("scalar", nil)
	if !result.IsError {
		t.Fatalf("scalar result = %+v", result)
	}
}

// TestMCPOversizeRecordIsBounded pins the memory bound on one incoming record.
//
// The pre-SDK server framed the stream itself, so it could answer an oversize
// line with -32600 and carry on. Framing now belongs to the SDK, which decodes
// straight off the reader; what this package still owns is the bound, and the
// property worth keeping is that an unbounded line cannot become an unbounded
// allocation. So the assertion moved from "the server replies -32600 and keeps
// going" to "the stream is abandoned at the limit, the tool behind it never
// runs, and nothing but protocol frames reaches stdout".
func TestMCPOversizeRecordIsBounded(t *testing.T) {
	called := false
	actions := fakeMCPActions()
	actions.status = func(ctx context.Context) (any, error) {
		called = true
		return mcpStatusSummary{}, nil
	}
	// The record has to stay syntactically plausible for the size guard to be
	// what stops it; a line of garbage is rejected as invalid JSON long before
	// it gets big, which proves nothing about the bound.
	oversize := `{"jsonrpc":"2.0","id":98,"method":"ping","params":{"note":"` +
		strings.Repeat("a", mcpMaxRecordBytes+2) + `"}}` + "\n" +
		`{"jsonrpc":"2.0","id":99,"method":"tools/call","params":{"name":"status","arguments":{}}}` + "\n"
	stdout, err := tryMCPSession(t, oversize, actions)
	if err == nil || !strings.Contains(err.Error(), "exceeds maximum size") {
		t.Fatalf("oversize record err = %v, want the size bound to end the session", err)
	}
	if called {
		t.Fatal("a tool ran on a stream that had already exceeded the record limit")
	}
	for _, frame := range decodeMCPResponses(t, stdout) {
		if frame["jsonrpc"] != "2.0" {
			t.Fatalf("non-JSON-RPC stdout frame: %+v", frame)
		}
	}

	// The bound is a limit, not a ceiling on ordinary traffic: a record just
	// under it still round-trips.
	large := strings.Repeat("a", mcpMaxRecordBytes/2)
	stdout = runMCPSession(t, `{"jsonrpc":"2.0","id":1,"method":"ping","params":{"note":"`+large+`"}}`+"\n", fakeMCPActions())
	frames := decodeMCPResponses(t, stdout)
	if len(frames) != 1 || frames[0]["id"] != float64(1) || mcpFrameError(frames[0]) != nil {
		t.Fatalf("under-limit record = %+v", frames)
	}
}

func TestMCPLargeUnderLimitRequestWithInteractiveStdin(t *testing.T) {
	// An MCP host normally keeps stdin open until it receives a response.
	// This control uses the same large ping as the finite-input test, but
	// closes stdin only after reading the reply.
	frame := `{"jsonrpc":"2.0","id":1,"method":"ping","params":{"note":"` +
		strings.Repeat("a", mcpMaxRecordBytes/2) + `"}}` + "\n"
	inReader, inWriter := io.Pipe()
	outReader, outWriter := io.Pipe()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runMCPStdio(ctx, inReader, outWriter, fakeMCPActions()) }()
	t.Cleanup(func() {
		_ = inWriter.Close()
		_ = outReader.Close()
	})
	go func() { _, _ = io.WriteString(inWriter, frame) }()
	reply := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(outReader).ReadString('\n')
		reply <- line
	}()
	select {
	case line := <-reply:
		frames := decodeMCPResponses(t, line)
		if len(frames) != 1 || frames[0]["id"] != float64(1) || mcpFrameError(frames[0]) != nil {
			t.Fatalf("interactive under-limit reply = %+v", frames)
		}
	case <-ctx.Done():
		t.Fatal("interactive MCP host did not receive a reply")
	}
	_ = inWriter.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("interactive MCP session ended with error: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("interactive MCP session did not close after stdin EOF")
	}
}

func TestMCPCancelInterruptsActiveTool(t *testing.T) {
	for _, closeInput := range []bool{false, true} {
		t.Run(fmt.Sprintf("stdin_eof=%v", closeInput), func(t *testing.T) {
			actions := fakeMCPActions()
			started := make(chan struct{})
			stopped := make(chan struct{})
			actions.share = func(ctx context.Context, _ shareRequest) (ShareResult, error) {
				close(started)
				<-ctx.Done()
				close(stopped)
				return ShareResult{}, ctx.Err()
			}
			input := initializedMCPInput(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"share","arguments":{"target":"8080"}}}`)
			inReader, inWriter := io.Pipe()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			defer inWriter.Close()
			var stdout bytes.Buffer
			done := make(chan error, 1)
			go func() { done <- runMCPStdio(ctx, inReader, &stdout, actions) }()
			go func() {
				_, _ = io.WriteString(inWriter, input)
				if closeInput {
					_ = inWriter.Close()
				}
			}()
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("tool was not dispatched")
			}
			cancel()
			select {
			case <-stopped:
			case <-time.After(time.Second):
				t.Fatal("context-aware tool kept running after caller cancellation")
			}
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("session cancellation error = %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("MCP session did not return after cancellation")
			}
		})
	}
}

func TestMCPFiniteEOFBoundaryCases(t *testing.T) {
	for _, tc := range []struct {
		name      string
		input     string
		wantID    float64
		wantError bool
	}{
		{"multiline valid JSON", "{\n\"jsonrpc\":\"2.0\",\n\"id\":1,\"method\":\"ping\"\n}\n", 1, false},
		{"no trailing newline", `{"jsonrpc":"2.0","id":2,"method":"ping"}`, 2, false},
		{"multiple buffered records", `{"jsonrpc":"2.0","id":3,"method":"ping"}` + "\n" + `{"jsonrpc":"2.0","id":4,"method":"ping"}` + "\n", 4, false},
		{"whitespace only", "  \n\t\n", 0, false},
		{"truncated JSON", `{"jsonrpc":"2.0","id":5,"method":"ping"`, 0, true},
		{"notification only", `{"jsonrpc":"2.0","method":"notifications/unknown"}` + "\n", 0, false},
		{"unsolicited client response", `{"jsonrpc":"2.0","id":6,"result":{}}` + "\n", 0, false},
		{"duplicate request ID", `{"jsonrpc":"2.0","id":7,"method":"ping"}` + "\n" + `{"jsonrpc":"2.0","id":7,"method":"ping"}` + "\n", 7, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			var stdout bytes.Buffer
			err := runMCPStdio(ctx, strings.NewReader(tc.input), &stdout, fakeMCPActions())
			if errors.Is(err, context.DeadlineExceeded) {
				t.Fatal("finite input did not settle before deadline")
			}
			if tc.wantError != (err != nil) {
				t.Fatalf("session error = %v, want error=%v", err, tc.wantError)
			}
			frames := decodeMCPResponses(t, stdout.String())
			if tc.wantID != 0 {
				if mcpFrameError(mcpFrameByID(t, frames, tc.wantID)) != nil {
					t.Fatalf("valid request returned protocol error: %+v", frames)
				}
			}
		})
	}
}

// TestMCPArgumentDecodingRejectsTrailingValues keeps the decoder assertion that
// used to live on decodeMCPParams. The pre-SDK server decoded both the params
// object and the arguments object; the SDK owns params now, so the assertion
// follows the surviving decoder, which is the one that matters: a client that
// appends a second JSON value after a tool's arguments must be rejected, not
// silently truncated to the first.
func TestMCPArgumentDecodingRejectsTrailingValues(t *testing.T) {
	var target struct{}
	if err := decodeMCPArguments(json.RawMessage(`{} {}`), &target); err == nil {
		t.Fatal("multiple JSON values accepted")
	}
	if err := decodeMCPArguments(json.RawMessage(``), &target); err != nil {
		t.Fatalf("absent arguments rejected: %v", err)
	}
}

// TestMCPMalformedRequestsAreNeverAnsweredAsCalls covers what validMCPRequestID
// covered before the SDK owned request parsing: a frame whose id is not a
// string or number, and an initialize whose protocolVersion is missing or the
// wrong type, must not be answered as a successful request. The pre-SDK server
// proved that with specific JSON-RPC codes it chose itself; the property that
// survives the transport swap is that no such frame produces a result, and that
// a tool never runs behind one.
func TestMCPMalformedRequestsAreNeverAnsweredAsCalls(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		{"boolean id", `{"jsonrpc":"2.0","id":true,"method":"tools/call","params":{"name":"status","arguments":{}}}`},
		{"object id", `{"jsonrpc":"2.0","id":{},"method":"tools/call","params":{"name":"status","arguments":{}}}`},
		{"unterminated id", `{"jsonrpc":"2.0","id":"unterminated,"method":"tools/call","params":{"name":"status","arguments":{}}}`},
		{"initialize with a numeric version", `{"jsonrpc":"2.0","id":2,"method":"initialize","params":{"protocolVersion":7}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			actions := fakeMCPActions()
			actions.status = func(ctx context.Context) (any, error) {
				called = true
				return mcpStatusSummary{}, nil
			}
			stdout, _ := tryMCPSession(t, tc.input+"\n", actions)
			if called {
				t.Fatalf("a tool ran for %q", tc.input)
			}
			for _, frame := range decodeMCPResponses(t, stdout) {
				if frame["jsonrpc"] != "2.0" {
					t.Fatalf("non-JSON-RPC stdout frame: %+v", frame)
				}
				if frame["result"] != nil {
					t.Fatalf("malformed request produced a result: %+v", frame)
				}
			}
		})
	}
}

func TestMCPCommandRunsStdioWithoutNonFrames(t *testing.T) {
	restoreShareSeams(t)
	resetRootJSONFlag(t)
	dir := t.TempDir()
	shareEnsureDirFn = func() error { return nil }
	shareRegistryPathFn = func() (string, error) { return filepath.Join(dir, "registry.json"), nil }
	shareOwnershipPathFn = func() (string, error) { return filepath.Join(dir, "node-ownership.json"), nil }
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
				for _, frame := range decodeMCPResponses(t, line) {
					if frame["jsonrpc"] != "2.0" {
						t.Fatalf("stray stdout bytes: %q", stdout)
					}
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

// TestMCPStdioStopsOnWriterError keeps the property that a dead stdout ends the
// session instead of spinning: an MCP client that closed the pipe is gone, and
// a server that keeps executing tools for it would be acting on nobody's
// behalf. The pre-SDK server surfaced the writer's own error verbatim; the SDK
// wraps it, so the assertion is that the session fails and the cause is still
// reachable through errors.Is.
func TestMCPStdioStopsOnWriterError(t *testing.T) {
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"status","arguments":{}}}`,
	}, "\n") + "\n"
	err := runMCPStdio(context.Background(), strings.NewReader(input), errorWriter{}, fakeMCPActions())
	if err == nil || !errors.Is(err, errMCPWriteFailed) {
		t.Fatalf("err = %v, want the writer failure to end the session", err)
	}
}

var errMCPWriteFailed = errors.New("write failed")

type errorWriter struct{}

func (errorWriter) Write([]byte) (int, error) { return 0, errMCPWriteFailed }
