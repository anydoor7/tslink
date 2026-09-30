package lifecycle

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/monody0007/tslink/internal/registry"
	tsruntime "github.com/monody0007/tslink/internal/runtime"
	"github.com/monody0007/tslink/internal/tailapi"
)

// B6a-1 (audit X4-1). The reconciler's view of a removed service is read
// before the device API call; whatever changes during that call must stop the
// removal of the service's node state and the forgetting of its rows. These
// tests change one thing each while the stubbed API call is in progress. The
// server package pins the daemon's side of the same boundary, the reservation
// a starting node takes.

func ledgerHolds(t *testing.T, path, nodeID string) bool {
	t.Helper()
	ledger, err := tsruntime.LoadOwnership(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range ledger.Nodes {
		if node.NodeID == nodeID {
			return true
		}
	}
	return false
}

// X4's lifecycle reproduction: the service is registered again and a new node
// enrolls into its state directory and records its NodeID while the device API
// call is in progress.
func TestReconcileKeepsTheStateOfAServiceReaddedAndEnrolledWhileItWaited(t *testing.T) {
	options, state := retiredOrphanFixture(t)
	old := cleanupDevicesFn
	t.Cleanup(func() { cleanupDevicesFn = old })
	cleanupDevicesFn = func(context.Context, []tailapi.CleanupTarget, bool) (tailapi.CleanupResult, error) {
		if _, err := registry.Add(options.RegistryPath, registry.Service{Name: "orphan", Type: registry.TypeProxy, Target: "http://127.0.0.1:4000"}); err != nil {
			t.Error(err)
		}
		if err := os.WriteFile(state, []byte("new enrolled synthetic state"), 0o600); err != nil {
			t.Error(err)
		}
		if err := tsruntime.RecordOwnedNode(options.OwnershipPath, "orphan", "node-orphan-new", options.Now.Add(time.Minute)); err != nil {
			t.Error(err)
		}
		return tailapi.CleanupResult{ResolvedOwnershipIDs: []string{"node-orphan-old"}}, nil
	}
	if _, err := Reconcile(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(state); err != nil {
		t.Fatalf("stale reconciliation deleted the state of the re-added, newly enrolled service: %v", err)
	}
	if !ledgerHolds(t, options.OwnershipPath, "node-orphan-new") {
		t.Fatal("the new node's ownership row is gone")
	}
}

// One change at a time, each with the control that makes no change and lets
// the removal happen.
func TestReconcileRereadsRegistryAndLedgerAfterTheDeviceAPICall(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(t *testing.T, options Options)
		keep   bool
	}{
		{name: "control: nothing changes", change: func(*testing.T, Options) {}, keep: false},
		{name: "the service is registered again", keep: true, change: func(t *testing.T, options Options) {
			if _, err := registry.Add(options.RegistryPath, registry.Service{Name: "orphan", Type: registry.TypeProxy, Target: "http://127.0.0.1:4000"}); err != nil {
				t.Error(err)
			}
		}},
		{name: "a node is recorded for the service", keep: true, change: func(t *testing.T, options Options) {
			if err := tsruntime.RecordOwnedNode(options.OwnershipPath, "orphan", "node-orphan-new", options.Now.Add(time.Minute)); err != nil {
				t.Error(err)
			}
		}},
		{name: "the service's row is retired again", keep: true, change: func(t *testing.T, options Options) {
			if err := tsruntime.MarkOwnedNodeIDsRetired(options.OwnershipPath, []string{"node-orphan-old"}, options.Now.Add(time.Hour)); err != nil {
				t.Error(err)
			}
		}},
		{name: "registry.json turns blank", keep: true, change: func(t *testing.T, options Options) {
			if err := os.WriteFile(options.RegistryPath, nil, 0o600); err != nil {
				t.Error(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			options, state := retiredOrphanFixture(t)
			old := cleanupDevicesFn
			t.Cleanup(func() { cleanupDevicesFn = old })
			cleanupDevicesFn = func(context.Context, []tailapi.CleanupTarget, bool) (tailapi.CleanupResult, error) {
				tc.change(t, options)
				return tailapi.CleanupResult{ResolvedOwnershipIDs: []string{"node-orphan-old"}}, nil
			}
			if _, err := Reconcile(context.Background(), options); err != nil {
				t.Fatal(err)
			}
			_, statErr := os.Stat(state)
			if tc.keep {
				if statErr != nil {
					t.Fatalf("state was removed: %v", statErr)
				}
				if !ledgerHolds(t, options.OwnershipPath, "node-orphan-old") {
					t.Fatal("the row was forgotten although its state was kept")
				}
				return
			}
			if !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("control: state after an undisturbed run: stat err = %v, want it removed", statErr)
			}
			if ledgerHolds(t, options.OwnershipPath, "node-orphan-old") {
				t.Fatal("control: the row outlived its removed state")
			}
		})
	}
}
