package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anydoor7/tslink/internal/output"
	"github.com/anydoor7/tslink/internal/registry"
)

// approveWithRefusedInvitation approves a pending request whose gateway
// invitation the API refuses after the decision commits. breakAudit makes the
// audit receipt of that committed decision fail as well.
func approveWithRefusedInvitation(t *testing.T, breakAudit bool) (RequestDecisionResult, error) {
	t.Helper()
	f := newGatewayInviteFixture(t)
	f.login = "alice@example.com"
	f.fail = true
	restoreShareSeams(t)
	shareIsRunningFn = func(string) bool { return true }
	oldEnsure := ensureDaemonFn
	t.Cleanup(func() { ensureDaemonFn = oldEnsure })
	ensureDaemonFn = func(context.Context, io.Writer, bool) error { return nil }
	svc, err := loadPersistedService(f.paths.Registry, "photos")
	if err != nil {
		t.Fatal(err)
	}
	svc.Requestable = true
	if _, err := registry.Add(f.paths.Registry, svc); err != nil {
		t.Fatal(err)
	}
	request, err := registry.SubmitAccessRequest(f.paths.Registry, f.login, "photos", "never", "", peopleNowFn())
	if err != nil {
		t.Fatal(err)
	}
	if breakAudit {
		// A directory where the journal file belongs makes its receipt fail.
		if err := os.Mkdir(filepath.Join(filepath.Dir(f.paths.Registry), "mcp-audit.json"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return decideRequest(f.paths.Registry, requestDecisionArguments{ID: request.ID, For: "never", AckNever: true}, true, peopleNowFn())
}

// A decision that commits and then fails downstream must not lose that failure
// when its audit receipt also fails: the caller gets both, the primary first.
func TestRequestsApproveKeepsTransportErrorWhenAuditAlsoFails(t *testing.T) {
	// Control: with a healthy journal the refused invitation is the error. This
	// also proves the scenario reaches the failure the audit error must not hide.
	_, control := approveWithRefusedInvitation(t, false)
	if control == nil || strings.Contains(control.Error(), "audit receipt") {
		t.Fatalf("control did not fail on the refused invitation alone: %v", control)
	}
	primary := control.Error()

	result, err := approveWithRefusedInvitation(t, true)
	if err == nil || !result.Changed || result.Access == nil {
		t.Fatalf("committed decision or retained access state lost: %+v, %v", result, err)
	}
	message := err.Error()
	audit := strings.Index(message, "audit receipt unavailable")
	if audit < 0 {
		t.Fatalf("audit failure lost: %v", err)
	}
	if !strings.HasPrefix(message, primary) {
		t.Fatalf("primary failure replaced by the audit failure: %q, want prefix %q", message, primary)
	}
	if code, ok := registry.ErrorCode(err); !ok || code != "access_setup_incomplete" {
		t.Fatalf("error code = %q, %v; want the access setup code", code, ok)
	}
}

// The joined failure reaches people as a CLI --json envelope and as an MCP tool
// result. Both must carry the primary failure's code, next steps and data with
// a message naming the failed receipt too. A coded primary failure is the case
// plain errors.Join loses: the envelope renders only the first coded error.
func TestAuditedFailureSerializesBothFailuresThroughEnvelopes(t *testing.T) {
	envelopes := func(err error) map[string]output.ErrorObject {
		return map[string]output.ErrorObject{
			"cli": *output.NewFailureForError("requests approve", err).Error,
			"mcp": mcpFailureEnvelope(t, makeMCPToolErrorResult(err)),
		}
	}
	audit := errors.New("change committed but audit receipt unavailable: unsafe or oversized MCP journal")

	t.Run("real approve with a refused invitation", func(t *testing.T) {
		_, control := approveWithRefusedInvitation(t, false)
		_, err := approveWithRefusedInvitation(t, true)
		alone := envelopes(control)
		for surface, got := range envelopes(err) {
			if got.Code != "access_setup_incomplete" || !strings.HasPrefix(got.Message, alone[surface].Message) || !strings.Contains(got.Message, "audit receipt unavailable") {
				t.Fatalf("%s: %+v, want the primary %q first and the audit failure", surface, got, alone[surface].Message)
			}
			// Data is a typed result on the CLI and decoded JSON on MCP; compare
			// what a consumer reads.
			var data struct {
				Changed bool            `json:"changed"`
				Access  json.RawMessage `json:"access"`
			}
			if encoded, _ := json.Marshal(got.Data); json.Unmarshal(encoded, &data) != nil || !data.Changed || len(data.Access) == 0 {
				t.Fatalf("%s: committed state lost from data: %s", surface, encoded)
			}
		}
	})

	t.Run("coded primary failure", func(t *testing.T) {
		primary := registry.CodedError{Code: "not_found", Message: "service not found: photos", Next: []string{"tslink list --json"}}
		// Probe validity: a plain join hides the receipt failure in both
		// envelopes, so the assertions below can fail.
		for surface, got := range envelopes(errors.Join(primary, audit)) {
			if strings.Contains(got.Message, "audit receipt") {
				t.Fatalf("%s: plain join no longer hides the receipt failure; the test proves nothing: %+v", surface, got)
			}
		}
		joined := joinAuditFailure(primary, audit)
		var reached registry.CodedError
		if !errors.As(joined, &reached) || reached.Code != primary.Code || !errors.Is(joined, audit) {
			t.Fatalf("the chain must reach both failures: %v", joined)
		}
		for surface, got := range envelopes(joined) {
			if got.Code != "not_found" || strings.Join(got.Next, "|") != "tslink list --json" || got.Message != primary.Message+"\n"+audit.Error() {
				t.Fatalf("%s: %+v", surface, got)
			}
		}
	})

	t.Run("receipt failure alone keeps its own type", func(t *testing.T) {
		if err := joinAuditFailure(nil, audit); err != audit {
			t.Fatalf("got %#v", err)
		}
	})
}
