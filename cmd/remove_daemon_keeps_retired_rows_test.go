package cmd

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/lifecycle"
	tsruntime "github.com/anydoor7/tslink/internal/runtime"
	"github.com/anydoor7/tslink/internal/tailapi"
	"github.com/anydoor7/tslink/internal/testenv"
)

// While a daemon runs, `tslink remove` leaves a removed service's node state
// to the daemon's lifecycle reconciler, the one authority that deletes it. The
// reconciler finds the service only through its retired ownership rows, so
// remove keeps them instead of forgetting them; the reconciler then confirms
// the remote side again, deletes the state and forgets the rows.
func TestRemoveKeepsRetiredOwnershipRowsWhileADaemonRuns(t *testing.T) {
	regPath, ownershipPath, stateDir := removeNodeStateFixture(t, "web")
	configDir := filepath.Dir(regPath)
	stubDaemonRunning(t, true)
	stubDeleteDevices(t, tailapi.CleanupResult{Matched: []string{"web"}, Deleted: []string{"web"}, ResolvedOwnershipIDs: []string{"node-web"}}, nil)

	result, err := removeServiceResult(regPath, ownershipPath, "web")
	if err != nil || !result.Removed || !result.DeviceCleaned {
		t.Fatalf("removeServiceResult() = %+v err=%v", result, err)
	}
	assertStateDir(t, stateDir, true)
	ledger, err := tsruntime.LoadOwnership(ownershipPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Nodes) != 1 || ledger.Nodes[0].NodeID != "node-web" || ledger.Nodes[0].RetiredAt == nil {
		t.Fatalf("ledger after remove with a daemon running = %+v, want web's row kept and retired", ledger.Nodes)
	}

	// The daemon's next lifecycle tick, against a tailnet where the device is
	// already gone, with the node stopped.
	fake := testenv.NewStatefulTailnet(t)
	t.Setenv(tailapi.APIBaseURLEnv, fake.URL())
	t.Setenv("TSLINK_API_KEY", "test-placeholder")
	if _, err := lifecycle.Reconcile(context.Background(), lifecycle.Options{
		RegistryPath:        regPath,
		OwnershipPath:       ownershipPath,
		CleanLocalNodeState: true,
		LocalNodeStateInUse: func(string) bool { return false },
	}); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	assertStateDir(t, stateDir, false)
	assertStateDir(t, filepath.Join(config.NodesDirIn(configDir), "bystander"), true)
	ledger, err = tsruntime.LoadOwnership(ownershipPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Nodes) != 0 {
		t.Fatalf("ledger after the reconciler finished = %+v, want web's row forgotten", ledger.Nodes)
	}
}

// Without a daemon nothing else will ever look, so remove finishes the job
// itself: the resolved rows are forgotten and the state deleted.
func TestRemoveWithoutADaemonForgetsResolvedRowsAndDeletesState(t *testing.T) {
	regPath, ownershipPath, stateDir := removeNodeStateFixture(t, "web")
	stubDaemonRunning(t, false)
	stubDeleteDevices(t, tailapi.CleanupResult{Matched: []string{"web"}, Deleted: []string{"web"}, ResolvedOwnershipIDs: []string{"node-web"}}, nil)

	if _, err := removeServiceResult(regPath, ownershipPath, "web"); err != nil {
		t.Fatalf("removeServiceResult() error = %v", err)
	}
	assertStateDir(t, stateDir, false)
	ledger, err := tsruntime.LoadOwnership(ownershipPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Nodes) != 0 {
		t.Fatalf("ledger = %+v, want web's row forgotten", ledger.Nodes)
	}
	if _, err := os.Stat(regPath); err != nil {
		t.Fatal(err)
	}
}
