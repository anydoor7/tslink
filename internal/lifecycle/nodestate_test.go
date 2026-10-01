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

// orphanNodeStateFixture registers one live service and leaves ownership
// records for one orphan, then writes a tsnet state directory for each.
func orphanNodeStateFixture(t *testing.T, now time.Time, orphanNodeIDs ...string) (configDir, regPath, ownershipPath string) {
	t.Helper()
	configDir = t.TempDir()
	regPath = filepath.Join(configDir, "registry.json")
	ownershipPath = filepath.Join(configDir, "node-ownership.json")

	if _, err := registry.Add(regPath, registry.Service{Name: "live", Type: registry.TypeProxy, Target: "http://localhost:4000"}); err != nil {
		t.Fatalf("Add(live) error = %v", err)
	}
	if err := tsruntime.RecordOwnedNode(ownershipPath, "live", "node-live", now); err != nil {
		t.Fatalf("RecordOwnedNode(live) error = %v", err)
	}
	for _, nodeID := range orphanNodeIDs {
		if err := tsruntime.RecordOwnedNode(ownershipPath, "orphan", nodeID, now); err != nil {
			t.Fatalf("RecordOwnedNode(orphan) error = %v", err)
		}
		if err := tsruntime.MarkOwnedNodeIDsRetired(ownershipPath, []string{nodeID}, now); err != nil {
			t.Fatalf("MarkOwnedNodeIDsRetired() error = %v", err)
		}
	}
	for _, name := range []string{"live", "orphan"} {
		dir := filepath.Join(config.NodesDirIn(configDir), name)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("MkdirAll(%q) error = %v", dir, err)
		}
		if err := os.WriteFile(filepath.Join(dir, "tailscaled.state"), []byte("machine key"), 0o600); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}
	}
	return configDir, regPath, ownershipPath
}

func stubCleanupDevices(t *testing.T, result tailapi.CleanupResult, err error) {
	t.Helper()
	orig := cleanupDevicesFn
	cleanupDevicesFn = func(context.Context, []tailapi.CleanupTarget, bool) (tailapi.CleanupResult, error) {
		return result, err
	}
	t.Cleanup(func() { cleanupDevicesFn = orig })
}

func assertNodeState(t *testing.T, configDir, name string, wantPresent bool) {
	t.Helper()
	dir := filepath.Join(config.NodesDirIn(configDir), name)
	_, err := os.Stat(dir)
	switch {
	case wantPresent && err != nil:
		t.Fatalf("Stat(%q) err = %v, want the node state kept", dir, err)
	case !wantPresent && !os.IsNotExist(err):
		t.Fatalf("Stat(%q) err = %v, want the node state removed", dir, err)
	}
}

func nothingHoldsNodeState(string) bool { return false }

// TestReconcileRemovesOrphanNodeStateAfterAConfirmedDeletion is the positive
// case, and the live service beside it is the control: a sweep that widened
// past "orphan with every recorded node resolved" would take the live service's
// identity with it and still satisfy the first assertion.
func TestReconcileRemovesOrphanNodeStateAfterAConfirmedDeletion(t *testing.T) {
	now := time.Date(2030, 8, 31, 12, 0, 0, 0, time.UTC)
	configDir, regPath, ownershipPath := orphanNodeStateFixture(t, now, "node-orphan")
	stubCleanupDevices(t, tailapi.CleanupResult{
		Matched:              []string{"orphan"},
		Deleted:              []string{"orphan"},
		ResolvedOwnershipIDs: []string{"node-orphan"},
	}, nil)

	result, err := Reconcile(context.Background(), Options{
		RegistryPath:        regPath,
		OwnershipPath:       ownershipPath,
		Now:                 now,
		CleanLocalNodeState: true,
		LocalNodeStateInUse: nothingHoldsNodeState,
	})
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("warnings = %v, want none", result.Warnings)
	}
	assertNodeState(t, configDir, "orphan", false)
	assertNodeState(t, configDir, "live", true)
}

// TestReconcileKeepsOrphanNodeStateWhenTheRunCannotProveTheRemoteIsGone is the
// negative half. Every row leaves the directory in place.
func TestReconcileKeepsOrphanNodeStateWhenTheRunCannotProveTheRemoteIsGone(t *testing.T) {
	now := time.Date(2030, 8, 31, 12, 0, 0, 0, time.UTC)
	confirmed := tailapi.CleanupResult{Deleted: []string{"orphan"}, ResolvedOwnershipIDs: []string{"node-orphan"}}

	for _, tc := range []struct {
		name       string
		options    Options
		cleanup    tailapi.CleanupResult
		cleanupErr error
		nodeIDs    []string
	}{
		{name: "dry run", options: Options{DryRun: true, CleanLocalNodeState: true, LocalNodeStateInUse: nothingHoldsNodeState}, cleanup: confirmed, nodeIDs: []string{"node-orphan"}},
		{name: "caller did not opt in", options: Options{LocalNodeStateInUse: nothingHoldsNodeState}, cleanup: confirmed, nodeIDs: []string{"node-orphan"}},
		{name: "caller opted in without an in-use predicate", options: Options{CleanLocalNodeState: true}, cleanup: confirmed, nodeIDs: []string{"node-orphan"}},
		{name: "a tsnet server still holds the state", options: Options{CleanLocalNodeState: true, LocalNodeStateInUse: func(string) bool { return true }}, cleanup: confirmed, nodeIDs: []string{"node-orphan"}},
		{name: "a hostname match was protected", options: Options{CleanLocalNodeState: true, LocalNodeStateInUse: nothingHoldsNodeState}, cleanup: tailapi.CleanupResult{Matched: []string{"orphan"}, Protected: []string{"orphan"}, Skipped: true}, nodeIDs: []string{"node-orphan"}},
		{name: "the cleanup call failed", options: Options{CleanLocalNodeState: true, LocalNodeStateInUse: nothingHoldsNodeState}, cleanupErr: errors.New("delete failed"), nodeIDs: []string{"node-orphan"}},
		{name: "only one of two recorded nodes resolved", options: Options{CleanLocalNodeState: true, LocalNodeStateInUse: nothingHoldsNodeState}, cleanup: confirmed, nodeIDs: []string{"node-orphan", "node-orphan-2"}},
		// Every recorded node resolved AND a second device carrying this
		// service's hostname was protected. The all-resolved check passes here,
		// so protection has to refuse on its own or this row deletes the key
		// that protected device may still be using.
		{name: "a second hostname match stayed protected although every recorded node resolved", options: Options{CleanLocalNodeState: true, LocalNodeStateInUse: nothingHoldsNodeState}, cleanup: tailapi.CleanupResult{Matched: []string{"orphan"}, Deleted: []string{"orphan"}, ResolvedOwnershipIDs: []string{"node-orphan"}, Protected: []string{"orphan"}, Skipped: true}, nodeIDs: []string{"node-orphan"}},
		{name: "the cleanup reported skipped although every recorded node resolved", options: Options{CleanLocalNodeState: true, LocalNodeStateInUse: nothingHoldsNodeState}, cleanup: tailapi.CleanupResult{Deleted: []string{"orphan"}, ResolvedOwnershipIDs: []string{"node-orphan"}, Skipped: true}, nodeIDs: []string{"node-orphan"}},
		// Protected without Skipped is not a state internal/tailapi produces
		// today: CleanupStaleNodesResultWithDryRun sets Skipped whenever it
		// protects anything. This row exists because the guard must not depend
		// on that coupling holding. Without it, deleting the Protected clause
		// changes nothing observable, and a later producer that reports a
		// protected hostname without the Skipped flag would silently start
		// deleting the local key of a device TSLink could not prove it owns.
		{name: "a hostname match was protected without the skipped flag", options: Options{CleanLocalNodeState: true, LocalNodeStateInUse: nothingHoldsNodeState}, cleanup: tailapi.CleanupResult{Matched: []string{"orphan"}, Deleted: []string{"orphan"}, ResolvedOwnershipIDs: []string{"node-orphan"}, Protected: []string{"orphan"}}, nodeIDs: []string{"node-orphan"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			configDir, regPath, ownershipPath := orphanNodeStateFixture(t, now, tc.nodeIDs...)
			stubCleanupDevices(t, tc.cleanup, tc.cleanupErr)

			options := tc.options
			options.RegistryPath = regPath
			options.OwnershipPath = ownershipPath
			options.Now = now
			if _, err := Reconcile(context.Background(), options); err != nil {
				t.Fatalf("Reconcile() error = %v", err)
			}
			assertNodeState(t, configDir, "orphan", true)
			assertNodeState(t, configDir, "live", true)
		})
	}
}

// TestReconcileReportsAFailedOrphanNodeStateRemoval pins that a directory that
// could not be deleted reaches the caller as a warning instead of disappearing.
func TestReconcileReportsAFailedOrphanNodeStateRemoval(t *testing.T) {
	now := time.Date(2030, 8, 31, 12, 0, 0, 0, time.UTC)
	_, regPath, ownershipPath := orphanNodeStateFixture(t, now, "node-orphan")
	stubCleanupDevices(t, tailapi.CleanupResult{Deleted: []string{"orphan"}, ResolvedOwnershipIDs: []string{"node-orphan"}}, nil)
	orig := removeNodeStateFn
	removeNodeStateFn = func(string, string) error { return errors.New("injected removal failure") }
	t.Cleanup(func() { removeNodeStateFn = orig })

	result, err := Reconcile(context.Background(), Options{
		RegistryPath:        regPath,
		OwnershipPath:       ownershipPath,
		Now:                 now,
		CleanLocalNodeState: true,
		LocalNodeStateInUse: nothingHoldsNodeState,
	})
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if len(result.Warnings) == 0 {
		t.Fatal("warnings = none, want the failed node-state removal reported")
	}
}

// TestReconcileNodeStateRemovalUsesTheRegistrysOwnConfigDir pins the same
// derivation the remove command uses: the process-wide config directory is a
// different tree and must be untouched.
func TestReconcileNodeStateRemovalUsesTheRegistrysOwnConfigDir(t *testing.T) {
	now := time.Date(2030, 8, 31, 12, 0, 0, 0, time.UTC)
	configDir, regPath, ownershipPath := orphanNodeStateFixture(t, now, "node-orphan")
	elsewhere := t.TempDir()
	t.Setenv(config.ConfigDirEnv, elsewhere)
	elsewhereState := filepath.Join(config.NodesDirIn(elsewhere), "orphan")
	if err := os.MkdirAll(elsewhereState, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	stubCleanupDevices(t, tailapi.CleanupResult{Deleted: []string{"orphan"}, ResolvedOwnershipIDs: []string{"node-orphan"}}, nil)

	if _, err := Reconcile(context.Background(), Options{
		RegistryPath:        regPath,
		OwnershipPath:       ownershipPath,
		Now:                 now,
		CleanLocalNodeState: true,
		LocalNodeStateInUse: nothingHoldsNodeState,
	}); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	assertNodeState(t, configDir, "orphan", false)
	if _, err := os.Stat(elsewhereState); err != nil {
		t.Fatalf("Stat(%q) err = %v, want the process default config dir untouched", elsewhereState, err)
	}
}
