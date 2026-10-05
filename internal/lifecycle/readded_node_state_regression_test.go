package lifecycle

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/registry"
	tsruntime "github.com/anydoor7/tslink/internal/runtime"
	"github.com/anydoor7/tslink/internal/tailapi"
	"github.com/anydoor7/tslink/internal/testwait"
)

// The API seam models a concurrent re-add while the remote cleanup is in flight.
// Real registry, ledger and node-state file operations are confined to t.TempDir.
func TestReconcilePreservesReaddedNodeState(t *testing.T) {
	for _, tc := range []struct {
		name         string
		readd        bool
		newOwnership bool
	}{
		{name: "orphan_control"},
		{name: "readded_during_cleanup", readd: true, newOwnership: true},
		{name: "new_ownership_without_registry", newOwnership: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Now()
			dir, regPath, ownershipPath := orphanNodeStateFixture(t, now, "old-node")
			statePath := filepath.Join(dir, "nodes", "orphan", "tailscaled.state")
			old := cleanupDevicesFn
			t.Cleanup(func() { cleanupDevicesFn = old })
			cleanupDevicesFn = func(context.Context, []tailapi.CleanupTarget, bool) (tailapi.CleanupResult, error) {
				if tc.readd {
					if _, err := registry.Add(regPath, registry.Service{Name: "orphan", Type: registry.TypeProxy, Target: "http://localhost:4001"}); err != nil {
						t.Fatal(err)
					}
				}
				if tc.newOwnership {
					if err := os.WriteFile(statePath, []byte("newly-enrolled-identity"), 0o600); err != nil {
						t.Fatal(err)
					}
					if err := tsruntime.RecordOwnedNode(ownershipPath, "orphan", "new-node", now.Add(time.Second)); err != nil {
						t.Fatal(err)
					}
				}
				return tailapi.CleanupResult{Deleted: []string{"orphan"}, ResolvedOwnershipIDs: []string{"old-node"}}, nil
			}
			_, err := Reconcile(context.Background(), Options{RegistryPath: regPath, OwnershipPath: ownershipPath, Now: now, CleanLocalNodeState: true, LocalNodeStateInUse: nothingHoldsNodeState})
			if err != nil {
				t.Fatal(err)
			}
			data, stateErr := os.ReadFile(statePath)
			ledger, err := tsruntime.LoadOwnership(ownershipPath)
			if err != nil {
				t.Fatal(err)
			}
			freshRecord := false
			for _, node := range ledger.Nodes {
				if node.NodeID == "new-node" {
					freshRecord = true
				}
			}
			t.Logf("readd=%t newOwnership=%t state_exists=%t new_ownership_record=%t", tc.readd, tc.newOwnership, stateErr == nil, freshRecord)
			if tc.newOwnership {
				if !freshRecord {
					t.Fatal("new ownership control was not recorded")
				}
				if stateErr != nil || string(data) != "newly-enrolled-identity" {
					t.Fatal("reconciler deleted a re-added service's new identity while retaining its new ownership record")
				}
			} else if !os.IsNotExist(stateErr) {
				t.Fatal("positive control did not clean the orphan")
			}
		})
	}
}

// A re-add that arrives after the final check waits for the registry lock.
// Its new state is written only after the old orphan directory is removed.
func TestReconcileSerializesFinalRemovalWithReadd(t *testing.T) {
	now := time.Now()
	dir, regPath, ownershipPath := orphanNodeStateFixture(t, now, "old-node")
	statePath := filepath.Join(dir, "nodes", "orphan", "tailscaled.state")
	oldCleanup := cleanupDevicesFn
	cleanupDevicesFn = func(context.Context, []tailapi.CleanupTarget, bool) (tailapi.CleanupResult, error) {
		return tailapi.CleanupResult{ResolvedOwnershipIDs: []string{"old-node"}}, nil
	}
	t.Cleanup(func() { cleanupDevicesFn = oldCleanup })
	oldRemove := removeNodeStateFn
	addDone := make(chan error, 1)
	removeNodeStateFn = func(configDir, name string) error {
		started := make(chan struct{})
		go func() {
			close(started)
			_, err := registry.Add(regPath, registry.Service{Name: "orphan", Type: registry.TypeProxy, Target: "http://localhost:4001"})
			if err == nil {
				err = os.MkdirAll(filepath.Dir(statePath), 0o700)
			}
			if err == nil {
				err = os.WriteFile(statePath, []byte("state-after-readd"), 0o600)
			}
			addDone <- err
		}()
		<-started
		select {
		case err := <-addDone:
			return fmt.Errorf("re-add overtook the final node-state deletion: %v", err)
		case <-time.After(100 * time.Millisecond):
		}
		return oldRemove(configDir, name)
	}
	t.Cleanup(func() { removeNodeStateFn = oldRemove })
	result, err := Reconcile(context.Background(), Options{RegistryPath: regPath, OwnershipPath: ownershipPath, Now: now, CleanLocalNodeState: true, LocalNodeStateInUse: nothingHoldsNodeState})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("cleanup warnings = %v", result.Warnings)
	}
	if err := testwait.Recv(t, addDone, "re-add finished after cleanup"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(statePath)
	if err != nil || string(data) != "state-after-readd" {
		t.Fatalf("new state = %q, error = %v", data, err)
	}
}

func TestReconcileWaitsForInProgressStartupBeforeRemovingState(t *testing.T) {
	for _, readd := range []bool{false, true} {
		name := "unchanged_orphan"
		if readd {
			name = "starting_readd"
		}
		t.Run(name, func(t *testing.T) {
			now := time.Now()
			dir, regPath, ownershipPath := orphanNodeStateFixture(t, now, "old-node")
			statePath := filepath.Join(dir, "nodes", "orphan", "tailscaled.state")
			oldCleanup := cleanupDevicesFn
			cleanupDevicesFn = func(context.Context, []tailapi.CleanupTarget, bool) (tailapi.CleanupResult, error) {
				return tailapi.CleanupResult{ResolvedOwnershipIDs: []string{"old-node"}}, nil
			}
			t.Cleanup(func() { cleanupDevicesFn = oldCleanup })
			gate := make(chan struct{}, 1)
			gate <- struct{}{} // startup owns the gate but has not published its node
			released := false
			defer func() {
				if !released {
					<-gate
				}
			}()
			attempted := make(chan struct{})
			type reconcileResult struct {
				result Result
				err    error
			}
			done := make(chan reconcileResult, 1)
			go func() {
				result, err := Reconcile(context.Background(), Options{
					RegistryPath: regPath, OwnershipPath: ownershipPath, Now: now,
					CleanLocalNodeState: true, LocalNodeStateInUse: nothingHoldsNodeState,
					WithNodeStateLock: func(fn func() error) error {
						close(attempted)
						gate <- struct{}{}
						defer func() { <-gate }()
						return fn()
					},
				})
				done <- reconcileResult{result, err}
			}()
			testwait.Recv(t, attempted, "cleanup reached the startup gate")
			if readd {
				if _, err := registry.Add(regPath, registry.Service{Name: "orphan", Type: registry.TypeProxy, Target: "http://localhost:4001"}); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(statePath, []byte("starting-node-state"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := tsruntime.RecordOwnedNode(ownershipPath, "orphan", "new-node", now.Add(time.Second)); err != nil {
					t.Fatal(err)
				}
			}
			<-gate
			released = true
			if outcome := testwait.Recv(t, done, "cleanup resumed after startup"); outcome.err != nil || len(outcome.result.Warnings) != 0 {
				t.Fatalf("cleanup error=%v warnings=%v", outcome.err, outcome.result.Warnings)
			}
			data, err := os.ReadFile(statePath)
			if readd {
				if err != nil || string(data) != "starting-node-state" {
					t.Fatalf("starting state = %q, error = %v", data, err)
				}
			} else if !os.IsNotExist(err) {
				t.Fatalf("unchanged orphan state remained: %v", err)
			}
		})
	}
}
