package lifecycle

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/registry"
	tsruntime "github.com/anydoor7/tslink/internal/runtime"
	"github.com/anydoor7/tslink/internal/tailapi"
)

// B6a-3 (audit X4-3). A removed service's retired ownership rows are the only
// record through which a later run finds its local tsnet state. The reconciler
// used to forget them as soon as the remote side resolved, also when the
// local removal failed or the caller was not allowed to remove local state at
// all, leaving the directory orphaned with nothing left to retry through. The
// rows now stay until the state is confirmed gone.

// retiredOrphanFixture registers "keep" and leaves one retired row for the
// removed service "orphan", whose node state holds a marker file.
func retiredOrphanFixture(t *testing.T) (Options, string) {
	t.Helper()
	dir := t.TempDir()
	now := time.Date(2035, 1, 1, 0, 0, 0, 0, time.UTC)
	options := Options{RegistryPath: filepath.Join(dir, "registry.json"), OwnershipPath: filepath.Join(dir, "node-ownership.json"), Now: now, CleanLocalNodeState: true, LocalNodeStateInUse: nothingHoldsNodeState}
	if _, err := registry.Add(options.RegistryPath, registry.Service{Name: "keep", Type: registry.TypeProxy, Target: "http://127.0.0.1:3000"}); err != nil {
		t.Fatal(err)
	}
	if err := tsruntime.RecordOwnedNode(options.OwnershipPath, "orphan", "node-orphan-old", now); err != nil {
		t.Fatal(err)
	}
	if err := tsruntime.MarkOwnedNodeIDsRetired(options.OwnershipPath, []string{"node-orphan-old"}, now); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(config.NodesDirIn(dir), "orphan", "state-marker")
	if err := os.MkdirAll(filepath.Dir(state), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(state, []byte("old synthetic state"), 0o600); err != nil {
		t.Fatal(err)
	}
	return options, state
}

// resolveOrphanCleanup stubs the device API: the orphan's recorded device is
// gone, and every call is counted.
func resolveOrphanCleanup(t *testing.T, result tailapi.CleanupResult) *int {
	t.Helper()
	calls := 0
	old := cleanupDevicesFn
	t.Cleanup(func() { cleanupDevicesFn = old })
	cleanupDevicesFn = func(context.Context, []tailapi.CleanupTarget, bool) (tailapi.CleanupResult, error) {
		calls++
		return result, nil
	}
	return &calls
}

func ledgerRows(t *testing.T, path string) int {
	t.Helper()
	ledger, err := tsruntime.LoadOwnership(path)
	if err != nil {
		t.Fatal(err)
	}
	return len(ledger.Nodes)
}

// X4's failed-removal case: the first run cannot remove the state, keeps the
// row and warns; once the failure clears, the next run finds the service
// through that row, removes the state and only then forgets the row.
func TestReconcileRetriesAFailedStateRemovalThroughTheKeptRow(t *testing.T) {
	options, state := retiredOrphanFixture(t)
	calls := resolveOrphanCleanup(t, tailapi.CleanupResult{ResolvedOwnershipIDs: []string{"node-orphan-old"}})
	oldRemove := removeNodeStateFn
	t.Cleanup(func() { removeNodeStateFn = oldRemove })
	removeNodeStateFn = func(string, string) error { return os.ErrPermission }

	first, err := Reconcile(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Warnings) != 1 {
		t.Fatalf("warnings after the failed removal = %v, want one", first.Warnings)
	}
	if rows := ledgerRows(t, options.OwnershipPath); rows != 1 {
		t.Fatalf("ledger rows after the failed removal = %d, want the row kept for a retry", rows)
	}

	removeNodeStateFn = oldRemove
	if _, err := Reconcile(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	if *calls != 2 {
		t.Fatalf("device cleanup calls = %d, want the retry to reach the service again", *calls)
	}
	if _, err := os.Stat(state); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("state after the retry: stat err = %v, want it removed", err)
	}
	if rows := ledgerRows(t, options.OwnershipPath); rows != 0 {
		t.Fatalf("ledger rows after the state was removed = %d, want the row forgotten", rows)
	}
}

// X4's external-cleanup case: a `tslink cleanup` process between two daemon
// ticks resolves the remote side but may not touch local state. It keeps the
// row, and the next daemon tick finishes.
func TestReconcileByACallerThatMayNotRemoveStateLeavesTheDaemonItsWork(t *testing.T) {
	options, state := retiredOrphanFixture(t)
	calls := resolveOrphanCleanup(t, tailapi.CleanupResult{ResolvedOwnershipIDs: []string{"node-orphan-old"}})
	cli := options
	cli.CleanLocalNodeState = false
	cli.LocalNodeStateInUse = nil

	if _, err := Reconcile(context.Background(), cli); err != nil {
		t.Fatal(err)
	}
	if rows := ledgerRows(t, options.OwnershipPath); rows != 1 {
		t.Fatalf("ledger rows after the CLI cleanup = %d, want the row kept for the daemon", rows)
	}
	if _, err := Reconcile(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	if *calls != 2 {
		t.Fatalf("device cleanup calls = %d, want the daemon tick to reach the service", *calls)
	}
	if _, err := os.Stat(state); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("state after the daemon tick: stat err = %v, want it removed", err)
	}
	if rows := ledgerRows(t, options.OwnershipPath); rows != 0 {
		t.Fatalf("ledger rows after the daemon tick = %d, want the row forgotten", rows)
	}
}

// Every branch that leaves the state in place keeps the rows. Where this run
// does not remove state, a control runs the same branch with no state on disk
// and forgets them, since nothing is left for them to find; a held or failed
// removal confirms nothing gone and keeps them either way.
func TestReconcileKeepsRowsOnEveryBranchThatLeavesStateInPlace(t *testing.T) {
	resolved := tailapi.CleanupResult{ResolvedOwnershipIDs: []string{"node-orphan-old"}}
	for _, tc := range []struct {
		name          string
		options       func(Options) Options
		cleanup       tailapi.CleanupResult
		remove        func(string, string) error
		absentForgets bool
	}{
		{name: "caller may not remove local state", options: func(o Options) Options { o.CleanLocalNodeState = false; return o }, cleanup: resolved, absentForgets: true},
		{name: "caller gave no in-use predicate", options: func(o Options) Options { o.LocalNodeStateInUse = nil; return o }, cleanup: resolved, absentForgets: true},
		{name: "a hostname match was protected", options: func(o Options) Options { return o }, cleanup: tailapi.CleanupResult{ResolvedOwnershipIDs: []string{"node-orphan-old"}, Protected: []string{"orphan"}, Skipped: true}, absentForgets: true},
		{name: "the cleanup reported skipped", options: func(o Options) Options { return o }, cleanup: tailapi.CleanupResult{ResolvedOwnershipIDs: []string{"node-orphan-old"}, Skipped: true}, absentForgets: true},
		{name: "a tsnet server holds the state", options: func(o Options) Options {
			o.LocalNodeStateInUse = func(string) bool { return true }
			return o
		}, cleanup: resolved},
		{name: "the removal failed", options: func(o Options) Options { return o }, cleanup: resolved, remove: func(string, string) error { return os.ErrPermission }},
	} {
		cases := []bool{true}
		if tc.absentForgets {
			cases = append(cases, false)
		}
		for _, statePresent := range cases {
			name := tc.name + "/state present"
			if !statePresent {
				name = tc.name + "/control: no state on disk"
			}
			t.Run(name, func(t *testing.T) {
				options, state := retiredOrphanFixture(t)
				if !statePresent {
					if err := os.RemoveAll(filepath.Dir(state)); err != nil {
						t.Fatal(err)
					}
				}
				resolveOrphanCleanup(t, tc.cleanup)
				if tc.remove != nil {
					old := removeNodeStateFn
					t.Cleanup(func() { removeNodeStateFn = old })
					removeNodeStateFn = tc.remove
				}
				if _, err := Reconcile(context.Background(), tc.options(options)); err != nil {
					t.Fatal(err)
				}
				wantRows := 0
				if statePresent {
					wantRows = 1
				}
				if rows := ledgerRows(t, options.OwnershipPath); rows != wantRows {
					t.Fatalf("ledger rows = %d, want %d", rows, wantRows)
				}
				if statePresent {
					if _, err := os.Stat(state); err != nil {
						t.Fatalf("state was removed on a keep branch: %v", err)
					}
				}
			})
		}
	}
}
