package lifecycle

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/registry"
	tsruntime "github.com/monody0007/tslink/internal/runtime"
	"github.com/monody0007/tslink/internal/tailapi"
)

// The reconciler is the one authority that deletes a removed service's node
// state, and it finds that state only through the service's retired ownership
// rows. When a live tsnet server still holds the directory (the daemon has not
// stopped the node yet), the rows must survive the run, so the next run, after
// the node is stopped, deletes the state and only then forgets them.
func TestReconcileKeepsOwnershipRowsWhileANodeHoldsTheState(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	ownershipPath := filepath.Join(dir, "node-ownership.json")
	now := time.Date(2030, 8, 31, 12, 0, 0, 0, time.UTC)
	if _, err := registry.Add(regPath, registry.Service{Name: "keep", Type: registry.TypeProxy, Target: "http://localhost:3000"}); err != nil {
		t.Fatal(err)
	}
	if err := tsruntime.RecordOwnedNode(ownershipPath, "removed", "node-removed", now); err != nil {
		t.Fatal(err)
	}
	if err := tsruntime.MarkOwnedNodeIDsRetired(ownershipPath, []string{"node-removed"}, now); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(config.NodesDirIn(dir), "removed", "tailscaled.state")
	if err := os.MkdirAll(filepath.Dir(state), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(state, []byte("node key"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldCleanup := cleanupDevicesFn
	t.Cleanup(func() { cleanupDevicesFn = oldCleanup })
	cleanupDevicesFn = func(_ context.Context, targets []tailapi.CleanupTarget, _ bool) (tailapi.CleanupResult, error) {
		// The device is already gone: every recorded ID resolves as absent.
		var ids []string
		for _, target := range targets {
			ids = append(ids, target.NodeIDs...)
		}
		return tailapi.CleanupResult{ResolvedOwnershipIDs: ids}, nil
	}
	held := true
	options := Options{
		RegistryPath:        regPath,
		OwnershipPath:       ownershipPath,
		Now:                 now,
		CleanLocalNodeState: true,
		LocalNodeStateInUse: func(name string) bool { return held && name == "removed" },
	}

	if _, err := Reconcile(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(state); err != nil {
		t.Fatalf("state held by a live node was deleted: %v", err)
	}
	ledger, err := tsruntime.LoadOwnership(ownershipPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Nodes) != 1 || ledger.Nodes[0].NodeID != "node-removed" || ledger.Nodes[0].RetiredAt == nil {
		t.Fatalf("ledger after a held run = %+v, want the retired row kept", ledger.Nodes)
	}

	held = false
	if _, err := Reconcile(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Fatalf("state after the node stopped: stat err = %v, want it deleted", err)
	}
	ledger, err = tsruntime.LoadOwnership(ownershipPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Nodes) != 0 {
		t.Fatalf("ledger after the state was deleted = %+v, want the row forgotten", ledger.Nodes)
	}
}
