package lifecycle

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/monody0007/tslink/internal/registry"
	tsruntime "github.com/monody0007/tslink/internal/runtime"
	"github.com/monody0007/tslink/internal/tailapi"
)

// The CLI supplies neither node-state gate. Once local state is absent,
// a temporary writer must not leave remotely resolved ownership rows behind.
func TestReconcileCLICleanupWaitsForProofLocks(t *testing.T) {
	for _, lock := range []string{"none", "registry", "ownership"} {
		t.Run(lock, func(t *testing.T) {
			now := time.Now()
			dir, regPath, ownershipPath := orphanNodeStateFixture(t, now, "old-node")
			if err := os.RemoveAll(filepath.Join(dir, "nodes", "orphan")); err != nil {
				t.Fatal(err)
			}
			entered := make(chan struct{})
			release := make(chan struct{})
			writerDone := make(chan error, 1)
			oldCleanup := cleanupDevicesFn
			t.Cleanup(func() { cleanupDevicesFn = oldCleanup })
			cleanupDevicesFn = func(context.Context, []tailapi.CleanupTarget, bool) (tailapi.CleanupResult, error) {
				if lock != "none" {
					go func() {
						hold := func() error { close(entered); <-release; return nil }
						if lock == "registry" {
							writerDone <- registry.WithLockedFileState(regPath, func(*registry.Registry, registry.RegistryFileState) error { return hold() })
						} else {
							writerDone <- tsruntime.WithOwnershipLock(ownershipPath, hold)
						}
					}()
					select {
					case <-entered:
					case err := <-writerDone:
						return tailapi.CleanupResult{}, err
					}
				}
				return tailapi.CleanupResult{ResolvedOwnershipIDs: []string{"old-node"}}, nil
			}
			done := make(chan error, 1)
			go func() {
				_, err := Reconcile(context.Background(), Options{RegistryPath: regPath, OwnershipPath: ownershipPath, Now: now})
				done <- err
			}()
			returned := false
			if lock != "none" {
				select {
				case <-entered:
				case err := <-done:
					t.Fatalf("Reconcile returned before the lock holder entered: %v", err)
				}
				select {
				case err := <-done:
					returned = true
					t.Errorf("CLI cleanup returned while the %s lock was held: %v", lock, err)
				case <-time.After(50 * time.Millisecond):
				}
				close(release)
				if err := <-writerDone; err != nil {
					t.Fatal(err)
				}
			}
			if !returned {
				select {
				case err := <-done:
					if err != nil {
						t.Fatal(err)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("cleanup did not finish after the proof lock was released")
				}
			}
			ledger, err := tsruntime.LoadOwnership(ownershipPath)
			if err != nil {
				t.Fatal(err)
			}
			for _, row := range ledger.Nodes {
				if row.ServiceName == "orphan" {
					t.Errorf("resolved ownership row remains after writer released: %+v", row)
				}
			}
		})
	}
}
