package cmd

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/monody0007/tslink/internal/output"
	"github.com/monody0007/tslink/internal/registry"
)

// mcpToolMinimalArguments is one valid call per tool, so a test can reach
// each tool's action.
var mcpToolMinimalArguments = map[string]string{
	"share":          `{"target":"3000"}`,
	"add":            `{"name":"web","type":"proxy","target":"http://localhost:3000"}`,
	"list":           `{}`,
	"unshare":        `{"name":"web"}`,
	"status":         `{}`,
	"url":            `{"name":"web"}`,
	"tags_list":      `{}`,
	"tags_set":       `{"service":"web","tag":"tag:web"}`,
	"access_explain": `{"service":"web"}`,
	"doctor":         `{}`,
	"logs":           `{}`,
	"invite_user":    `{"email":"alice@example.com"}`,
	"invite_device":  `{"service":"web","email":"alice@example.com"}`,
	"invite_list":    `{}`,
	"invite_revoke":  `{"kind":"user","invite_id":"1"}`,
	"invite_resend":  `{"kind":"user","invite_id":"1"}`,
	"template_list":  `{}`,
	"template_plan":  `{"name":"x"}`,
	"template_apply": `{"name":"x"}`,
}

// mcpRefusal is what every action of refusingMCPActions returns: a stable
// code, next steps and structured data, the whole failure object.
func mcpRefusal(tool string) error {
	return &registry.StableCodeError{
		Code: registry.CodeURLNotReady,
		Next: []string{"tslink status --json"},
		Err:  output.ErrConflictWithData("refused by "+tool, map[string]any{"tool": tool}),
	}
}

func refusingMCPActions() mcpActions {
	return mcpActions{
		share: func(context.Context, shareRequest) (ShareResult, error) { return ShareResult{}, mcpRefusal("share") },
		add:   func(context.Context, AddParams, bool) (any, error) { return nil, mcpRefusal("add") },
		list:  func() (any, error) { return nil, mcpRefusal("list") },
		unshare: func(context.Context, string) (any, error) {
			return nil, mcpRefusal("unshare")
		},
		status: func() (any, error) { return nil, mcpRefusal("status") },
		url: func(context.Context, string, time.Duration) (any, error) {
			return nil, mcpRefusal("url")
		},
		tagsList:      func() (any, error) { return nil, mcpRefusal("tags_list") },
		tagsSet:       func(string, string) (any, error) { return nil, mcpRefusal("tags_set") },
		accessExplain: func(string) (any, error) { return nil, mcpRefusal("access_explain") },
		doctor:        func(bool) (any, error) { return nil, mcpRefusal("doctor") },
		logs:          func(mcpLogsArguments) (any, error) { return nil, mcpRefusal("logs") },
		inviteUser: func(context.Context, string, string, bool) (any, error) {
			return nil, mcpRefusal("invite_user")
		},
		inviteDevice: func(context.Context, mcpInviteDeviceArguments) (any, error) {
			return nil, mcpRefusal("invite_device")
		},
		inviteList:   func(context.Context, bool) (any, error) { return nil, mcpRefusal("invite_list") },
		inviteRevoke: func(context.Context, string, string) (any, error) { return nil, mcpRefusal("invite_revoke") },
		inviteResend: func(context.Context, string, string) (any, error) { return nil, mcpRefusal("invite_resend") },
		templateList: func() (any, error) { return nil, mcpRefusal("template_list") },
		templatePlan: func(string) (any, error) { return nil, mcpRefusal("template_plan") },
		templateApply: func(context.Context, string, bool) (any, error) {
			return nil, mcpRefusal("template_apply")
		},
	}
}

// decodeMCPFailureText decodes the failure object an error result's text
// carries.
func decodeMCPFailureText(t *testing.T, text string) map[string]any {
	t.Helper()
	var failure map[string]any
	if err := json.Unmarshal([]byte(text), &failure); err != nil {
		t.Fatalf("error text %q is not a failure object: %v", text, err)
	}
	return failure
}

// mcpToolErrorFailure checks one tools/call answer is a tool error result
// without structuredContent, and returns the failure object its text carries.
func mcpToolErrorFailure(t *testing.T, tool, arguments string, actions mcpActions) map[string]any {
	t.Helper()
	call := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"` + tool + `","arguments":` + arguments + `}}`
	frame := mcpFrameByID(t, decodeMCPResponses(t, runMCPSession(t, initializedMCPInput(call), actions)), float64(2))
	if frame["error"] != nil {
		t.Fatalf("%s %s: protocol error %+v, want a tool result", tool, arguments, frame["error"])
	}
	result, _ := frame["result"].(map[string]any)
	if result == nil || result["isError"] != true {
		t.Fatalf("%s %s: result %+v, want isError", tool, arguments, frame)
	}
	if structured, present := result["structuredContent"]; present {
		t.Fatalf("%s %s: error result carries structuredContent %v, which no closed output schema admits", tool, arguments, structured)
	}
	content, _ := result["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("%s %s: content = %+v, want one text item", tool, arguments, result["content"])
	}
	text, _ := content[0].(map[string]any)["text"].(string)
	var failure map[string]any
	if err := json.Unmarshal([]byte(text), &failure); err != nil {
		t.Fatalf("%s %s: error text %q is not a failure object: %v", tool, arguments, text, err)
	}
	return failure
}

// TestMCPRefusalsReachEveryClientIntact is A3-1: every refusal came back with
// structuredContent {ok, code, error}, which matches none of the closed output
// schemas, so a client that validates it (the TypeScript SDK's whole v1 line)
// threw away the refusal; and the text the model reads held only the message.
// Now an error result has no structuredContent, and its text is the whole
// failure object: code, message, next and data.
func TestMCPRefusalsReachEveryClientIntact(t *testing.T) {
	actions := refusingMCPActions()
	for _, tool := range mcpToolDefinitions {
		arguments, ok := mcpToolMinimalArguments[tool.Name]
		if !ok {
			t.Fatalf("no minimal arguments for tool %s", tool.Name)
		}
		t.Run(tool.Name, func(t *testing.T) {
			failure := mcpToolErrorFailure(t, tool.Name, arguments, actions)
			if failure["code"] != registry.CodeURLNotReady {
				t.Fatalf("failure code = %v, want %s: %v", failure["code"], registry.CodeURLNotReady, failure)
			}
			if message, _ := failure["message"].(string); !strings.Contains(message, "refused by "+tool.Name) {
				t.Fatalf("failure message = %v", failure["message"])
			}
			if next, _ := failure["next"].([]any); len(next) != 1 || next[0] != "tslink status --json" {
				t.Fatalf("failure next = %v", failure["next"])
			}
			if data, _ := failure["data"].(map[string]any); data["tool"] != tool.Name {
				t.Fatalf("failure data = %v", failure["data"])
			}
		})
	}
	if len(mcpToolMinimalArguments) != len(mcpToolDefinitions) {
		t.Fatalf("minimal arguments cover %d tools, the server declares %d", len(mcpToolMinimalArguments), len(mcpToolDefinitions))
	}
}

// TestMCPArgumentErrorsAreToolErrorsThatNameTheField: arguments that do not
// fit a tool's schema used to be the JSON-RPC error "Invalid share arguments",
// which dropped the decoder's reason. The MCP specification classes input
// validation errors as tool execution errors a model can correct, so they are
// usage_error tool results naming the field.
func TestMCPArgumentErrorsAreToolErrorsThatNameTheField(t *testing.T) {
	actions := fakeMCPActions()
	cases := []struct {
		tool, arguments, field string
	}{
		{"share", `{"target":"3000","ephemral":true}`, `"ephemral"`},
		{"share", `{"target":"3000","ephemeral":"true"}`, "ephemeral"},
		{"share", `{}`, "target"},
		{"add", `{"type":"proxy","target":"http://localhost:3000"}`, "name"},
		{"url", `{"name":"web","wait":5}`, "wait"},
		{"invite_revoke", `{"invite_id":"1"}`, "kind"},
	}
	for _, tc := range cases {
		failure := mcpToolErrorFailure(t, tc.tool, tc.arguments, actions)
		if failure["code"] != output.StableErrorCode(output.ExitUsage) {
			t.Errorf("%s %s: code = %v, want usage_error", tc.tool, tc.arguments, failure["code"])
		}
		if message, _ := failure["message"].(string); !strings.Contains(message, tc.field) || !strings.Contains(message, tc.tool) {
			t.Errorf("%s %s: message %q does not name the tool and %s", tc.tool, tc.arguments, message, tc.field)
		}
	}
	// Every tool: an unknown argument is named.
	for _, tool := range mcpToolDefinitions {
		arguments := strings.TrimSuffix(mcpToolMinimalArguments[tool.Name], "}")
		if arguments != "{" {
			arguments += ","
		}
		arguments += `"bogus_field":1}`
		failure := mcpToolErrorFailure(t, tool.Name, arguments, actions)
		if message, _ := failure["message"].(string); failure["code"] != "usage_error" || !strings.Contains(message, "bogus_field") {
			t.Errorf("%s with an unknown argument: failure = %v", tool.Name, failure)
		}
	}
}
