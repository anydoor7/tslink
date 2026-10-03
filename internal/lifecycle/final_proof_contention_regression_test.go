package lifecycle

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/registry"
	tsruntime "github.com/anydoor7/tslink/internal/runtime"
	"github.com/anydoor7/tslink/internal/tailapi"
)

func immediateNodeStateGate(ctx context.Context, fn func() error) (bool, error) {
	if ctx.Err() != nil {
		return false, nil
	}
	return true, fn()
}

// The writer starts only after earlier registry and ownership mutations have
// finished. A remote deletion is confirmed, but local proof must be skipped
// while either lock is held, then succeed on the next uncontended pass.
func TestReconcileSkipsBusyFinalProofAndRetries(t *testing.T) {
	for _, lock := range []string{"registry", "ownership"} {
		t.Run(lock, func(t *testing.T) {
			now := time.Now()
			dir, regPath, ownershipPath := orphanNodeStateFixture(t, now, "old-node")
			statePath := filepath.Join(dir, "nodes", "orphan", "tailscaled.state")
			entered := make(chan struct{})
			release := make(chan struct{})
			writerDone := make(chan error, 1)
			oldCleanup := cleanupDevicesFn
			t.Cleanup(func() { cleanupDevicesFn = oldCleanup })
			cleanupDevicesFn = func(context.Context, []tailapi.CleanupTarget, bool) (tailapi.CleanupResult, error) {
				go func() {
					if lock == "registry" {
						writerDone <- registry.WithLockedFileState(regPath, func(*registry.Registry, registry.RegistryFileState) error {
							close(entered)
							<-release
							return nil
						})
					} else {
						writerDone <- tsruntime.WithLockedOwnership(ownershipPath, func(tsruntime.OwnershipLedger) error {
							close(entered)
							<-release
							return nil
						})
					}
				}()
				select {
				case <-entered:
				case err := <-writerDone:
					return tailapi.CleanupResult{}, err
				case <-time.After(5 * time.Second):
					return tailapi.CleanupResult{}, context.DeadlineExceeded
				}
				return tailapi.CleanupResult{ResolvedOwnershipIDs: []string{"old-node"}}, nil
			}
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() {
				_, err := Reconcile(ctx, Options{RegistryPath: regPath, OwnershipPath: ownershipPath, Now: now, CleanLocalNodeState: true, LocalNodeStateInUse: nothingHoldsNodeState, TryWithNodeStateLock: immediateNodeStateGate})
				done <- err
			}()
			returned := false
			select {
			case err := <-done:
				returned = true
				if err != nil {
					t.Errorf("Reconcile: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Error("final cleanup waited on a held proof lock")
			}
			cancel()
			assertNodeState(t, dir, "orphan", true)
			close(release)
			if err := <-writerDone; err != nil {
				t.Fatal(err)
			}
			if !returned {
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					t.Fatal("reconciliation did not finish after writer released")
				}
			}
			cleanupDevicesFn = func(context.Context, []tailapi.CleanupTarget, bool) (tailapi.CleanupResult, error) {
				return tailapi.CleanupResult{ResolvedOwnershipIDs: []string{"old-node"}}, nil
			}
			if _, err := Reconcile(context.Background(), Options{RegistryPath: regPath, OwnershipPath: ownershipPath, Now: now, CleanLocalNodeState: true, LocalNodeStateInUse: nothingHoldsNodeState, TryWithNodeStateLock: immediateNodeStateGate}); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(statePath); !os.IsNotExist(err) {
				t.Fatalf("uncontended retry left state in place: %v", err)
			}
			assertNodeState(t, dir, "live", true)
		})
	}
}

func TestReconcileCanceledBeforeFinalProofKeepsState(t *testing.T) {
	now := time.Now()
	dir, regPath, ownershipPath := orphanNodeStateFixture(t, now, "old-node")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	oldCleanup := cleanupDevicesFn
	t.Cleanup(func() { cleanupDevicesFn = oldCleanup })
	cleanupDevicesFn = func(context.Context, []tailapi.CleanupTarget, bool) (tailapi.CleanupResult, error) {
		cancel()
		return tailapi.CleanupResult{ResolvedOwnershipIDs: []string{"old-node"}}, nil
	}
	if _, err := Reconcile(ctx, Options{RegistryPath: regPath, OwnershipPath: ownershipPath, Now: now, CleanLocalNodeState: true, LocalNodeStateInUse: nothingHoldsNodeState, TryWithNodeStateLock: immediateNodeStateGate}); err != nil {
		t.Fatal(err)
	}
	assertNodeState(t, dir, "orphan", true)
}

func TestReconcileSkipsBusyStartupGate(t *testing.T) {
	now := time.Now()
	dir, regPath, ownershipPath := orphanNodeStateFixture(t, now, "old-node")
	stubCleanupDevices(t, tailapi.CleanupResult{ResolvedOwnershipIDs: []string{"old-node"}}, nil)
	gate := make(chan struct{}, 1)
	gate <- struct{}{}
	defer func() { <-gate }()
	callbackCalled := false
	if _, err := Reconcile(context.Background(), Options{
		RegistryPath: regPath, OwnershipPath: ownershipPath, Now: now,
		CleanLocalNodeState: true, LocalNodeStateInUse: nothingHoldsNodeState,
		TryWithNodeStateLock: func(ctx context.Context, fn func() error) (bool, error) {
			select {
			case gate <- struct{}{}:
				defer func() { <-gate }()
				callbackCalled = true
				return true, fn()
			default:
				return false, nil
			}
		},
	}); err != nil {
		t.Fatal(err)
	}
	if callbackCalled {
		t.Fatal("final proof ran without owning startup gate")
	}
	assertNodeState(t, dir, "orphan", true)
}

// The daemon path keeps resolved old rows until the final check. A newer row,
// including a reactivated row with the same ID, must still protect state.
func TestReconcileNonblockingProofPreservesNewIdentity(t *testing.T) {
	for _, mode := range []string{"unchanged_orphan_control", "readded", "new_ownership", "same_id_reactivated"} {
		t.Run(mode, func(t *testing.T) {
			now := time.Now()
			dir, regPath, ownershipPath := orphanNodeStateFixture(t, now, "old-node")
			statePath := filepath.Join(dir, "nodes", "orphan", "tailscaled.state")
			oldCleanup := cleanupDevicesFn
			t.Cleanup(func() { cleanupDevicesFn = oldCleanup })
			cleanupDevicesFn = func(context.Context, []tailapi.CleanupTarget, bool) (tailapi.CleanupResult, error) {
				if mode == "readded" {
					if _, err := registry.Add(regPath, registry.Service{Name: "orphan", Type: registry.TypeProxy, Target: "http://localhost:4001"}); err != nil {
						return tailapi.CleanupResult{}, err
					}
				}
				if mode != "unchanged_orphan_control" {
					if err := os.WriteFile(statePath, []byte("new-identity"), 0o600); err != nil {
						return tailapi.CleanupResult{}, err
					}
					id := "new-node"
					if mode == "same_id_reactivated" {
						id = "old-node"
					}
					if err := tsruntime.RecordOwnedNode(ownershipPath, "orphan", id, now.Add(time.Second)); err != nil {
						return tailapi.CleanupResult{}, err
					}
				}
				return tailapi.CleanupResult{ResolvedOwnershipIDs: []string{"old-node"}}, nil
			}
			if _, err := Reconcile(context.Background(), Options{
				RegistryPath: regPath, OwnershipPath: ownershipPath, Now: now,
				CleanLocalNodeState: true, LocalNodeStateInUse: nothingHoldsNodeState,
				TryWithNodeStateLock: immediateNodeStateGate,
			}); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(statePath)
			if mode == "unchanged_orphan_control" {
				if !os.IsNotExist(err) {
					t.Fatalf("old orphan state was not removed: %v", err)
				}
				ledger, err := tsruntime.LoadOwnership(ownershipPath)
				if err != nil {
					t.Fatal(err)
				}
				for _, node := range ledger.Nodes {
					if node.ServiceName == "orphan" {
						t.Fatal("resolved orphan ownership row remained after successful cleanup")
					}
				}
			} else if err != nil || string(data) != "new-identity" {
				t.Fatalf("new identity state changed: content=%q err=%v", data, err)
			}
			if mode != "unchanged_orphan_control" {
				ledger, err := tsruntime.LoadOwnership(ownershipPath)
				if err != nil {
					t.Fatal(err)
				}
				foundNew := false
				for _, node := range ledger.Nodes {
					if node.ServiceName != "orphan" {
						continue
					}
					// Local state is still present, so retain the old retired row
					// as retry evidence. A new identity must never be forgotten.
					if node.NodeID == "old-node" && mode != "same_id_reactivated" {
						if node.RetiredAt == nil {
							t.Fatal("old retry evidence lost its retirement proof")
						}
						continue
					}
					if node.RetiredAt != nil || node.RecordedAt.Before(now.Add(time.Second)) {
						t.Fatalf("stale orphan row remained after new identity: %+v", node)
					}
					foundNew = true
				}
				if !foundNew {
					t.Fatal("new ownership row was removed")
				}
			}
			assertNodeState(t, dir, "live", true)
		})
	}
}
