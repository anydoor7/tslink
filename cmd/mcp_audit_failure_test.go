package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/anydoor7/tslink/internal/mcpaudit"
	"github.com/anydoor7/tslink/internal/mcpscope"
	"github.com/anydoor7/tslink/internal/output"
	"github.com/anydoor7/tslink/internal/registry"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// mcpFailureEnvelope decodes the error object an MCP client reads from a
// failed tool result.
func mcpFailureEnvelope(t *testing.T, result *mcp.CallToolResult) output.ErrorObject {
	t.Helper()
	if result == nil || !result.IsError || len(result.Content) != 1 {
		t.Fatalf("not a failed tool result: %+v", result)
	}
	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("content is %T", result.Content[0])
	}
	var failure output.ErrorObject
	if err := json.Unmarshal([]byte(text.Text), &failure); err != nil {
		t.Fatal(err)
	}
	return failure
}

// dataFailure is a tool failure that also carries machine-readable data, as a
// partially applied change does.
type dataFailure struct{ registry.CodedError }

func (dataFailure) ErrorData() any {
	return map[string]any{"changed": true, "applied": []string{"photos"}, "bytes": 12345678901}
}

// A mutating tool that fails is followed by a completion receipt. When that
// receipt also fails, the caller must still learn why the tool failed: the
// tool's own code, message and next steps stay first, and the receipt failure
// is added, never substituted. A tool that succeeded and then lost its receipt
// still reports the audit failure alone.
func TestMCPCompletionAuditFailureKeepsTheToolFailure(t *testing.T) {
	coded := registry.CodedError{Code: "not_found", Message: "service not found: photos", Next: []string{"tslink list --json"}}
	for _, tc := range []struct {
		name, role string
		primary    error
	}{
		{"owner", "owner", coded},
		{"app-operator", "app-operator", coded},
		{"owner_with_data", "owner", dataFailure{coded}},
	} {
		role, primary := tc.role, tc.primary
		t.Run(tc.name, func(t *testing.T) {
			run := func(toolErr error, completionFails bool) (*mcp.CallToolResult, int) {
				a := scopeFixtureActions()
				s := mcpscope.Session{Who: "agent", Scope: testRoleScope(role)}
				a.session = &s
				a.appRestart = func(context.Context, string) (any, error) {
					if toolErr != nil {
						return nil, toolErr
					}
					return map[string]any{}, nil
				}
				calls := 0
				a.audit = func(_ context.Context, e mcpaudit.Entry) error {
					calls++
					if e.Phase == "completion" && completionFails {
						return errors.New("secret-token")
					}
					return nil
				}
				result, err := callMCPTool(context.Background(), a, "app_restart", json.RawMessage(`{"app":"photos"}`))
				if err != nil {
					t.Fatal(err)
				}
				return result, calls
			}

			// Control: a healthy journal reports the tool failure alone. For a
			// reduced role that is already the generic scoped failure.
			control, calls := run(primary, false)
			want := mcpFailureEnvelope(t, control)
			if calls != 2 || want.Code != "not_found" || strings.Contains(want.Message, "audit") {
				t.Fatalf("control: calls=%d %+v", calls, want)
			}

			result, calls := run(primary, true)
			got := mcpFailureEnvelope(t, result)
			if calls != 2 {
				t.Fatalf("receipts attempted: %d", calls)
			}
			gotData, _ := json.Marshal(got.Data)
			wantData, _ := json.Marshal(want.Data)
			if tc.name == "owner_with_data" && len(wantData) < 10 {
				t.Fatalf("control carries no data to preserve: %s", wantData)
			}
			if string(gotData) != string(wantData) {
				t.Fatalf("the tool failure's data changed: %s, want %s", gotData, wantData)
			}
			if got.Code != want.Code || strings.Join(got.Next, "|") != strings.Join(want.Next, "|") {
				t.Fatalf("the tool failure was replaced by the audit failure: %+v, want code %q next %v", got, want.Code, want.Next)
			}
			if !strings.HasPrefix(got.Message, want.Message) || !strings.Contains(got.Message, mcpAuditUnavailable(true).(registry.CodedError).Message) {
				t.Fatalf("message must carry the tool failure then the receipt failure: %q (tool failure alone: %q)", got.Message, want.Message)
			}
			if encoded, _ := json.Marshal(result); strings.Contains(string(encoded), "secret-token") {
				t.Fatalf("journal error leaked: %s", encoded)
			}

			// A successful tool whose receipt fails is still an audit failure.
			result, _ = run(nil, true)
			if got := mcpFailureEnvelope(t, result); got.Code != "mcp_audit_unavailable" {
				t.Fatalf("success without a receipt: %+v", got)
			}
		})
	}
}
