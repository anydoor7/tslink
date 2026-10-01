package cmd

import (
	"errors"
	"testing"

	tsruntime "github.com/anydoor7/tslink/internal/runtime"
	"github.com/anydoor7/tslink/internal/tailapi"
)

// B6a-3's invariant on the remove path (audit X4-3 found it in the
// reconciler): without a daemon, remove deletes the local state itself, and it
// used to forget the service's resolved ownership rows first, whatever became
// of the directory. A directory it then failed to delete, or kept because a
// hostname match stayed protected, had no row left through which a later
// reconciliation could find it. The rows now go only with the directory.
func TestRemoveWithoutADaemonKeepsTheRowsOfStateItDidNotDelete(t *testing.T) {
	resolved := tailapi.CleanupResult{Matched: []string{"web"}, Deleted: []string{"web"}, ResolvedOwnershipIDs: []string{"node-web"}}
	for _, tc := range []struct {
		name      string
		cleanup   tailapi.CleanupResult
		removeErr error
		wantRows  int
		wantState bool
	}{
		{name: "control: the state is deleted", cleanup: resolved, wantRows: 0, wantState: false},
		{name: "the state could not be deleted", cleanup: resolved, removeErr: errors.New("injected removal failure"), wantRows: 1, wantState: true},
		{name: "a hostname match stayed protected", cleanup: tailapi.CleanupResult{Matched: []string{"web"}, Deleted: []string{"web"}, ResolvedOwnershipIDs: []string{"node-web"}, Protected: []string{"web"}, Skipped: true}, wantRows: 1, wantState: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			regPath, ownershipPath, stateDir := removeNodeStateFixture(t, "web")
			stubDaemonRunning(t, false)
			stubDeleteDevices(t, tc.cleanup, nil)
			if tc.removeErr != nil {
				orig := removeNodeStateFn
				removeNodeStateFn = func(string, string) error { return tc.removeErr }
				t.Cleanup(func() { removeNodeStateFn = orig })
			}

			result, err := removeServiceResult(regPath, ownershipPath, "web")
			if err != nil || !result.Removed {
				t.Fatalf("removeServiceResult() = %+v, %v", result, err)
			}
			assertStateDir(t, stateDir, tc.wantState)
			ledger, err := tsruntime.LoadOwnership(ownershipPath)
			if err != nil {
				t.Fatal(err)
			}
			if len(ledger.Nodes) != tc.wantRows {
				t.Fatalf("ledger = %+v, want %d row(s)", ledger.Nodes, tc.wantRows)
			}
			for _, node := range ledger.Nodes {
				if node.RetiredAt == nil {
					t.Fatalf("kept row %+v is not retired, so a later reconciliation would not act on it", node)
				}
			}
		})
	}
}
