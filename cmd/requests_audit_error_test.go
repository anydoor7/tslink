package cmd

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
