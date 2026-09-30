package cmd

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/filelock"
	"github.com/monody0007/tslink/internal/lifecycle"
	"github.com/monody0007/tslink/internal/registry"
	tsruntime "github.com/monody0007/tslink/internal/runtime"
	"github.com/monody0007/tslink/internal/tailapi"
)

// B6a-4 (audit X4-4). remove used to snapshot the service's ownership rows,
// wait for the registry lock, and then retire only the snapshot. A node that
// the daemon enrolled during that wait kept a row without retired_at, and
// once the service was unregistered that row withheld its device deletion for
// good ("unknown retirement provenance"). The retirement now reads the ledger
// under its lock, inside the registry lock, in the same step as the
// unregistration.
//
// X4's reproduction: the test holds the real registry flock, starts remove,
// records an enrollment with the real ownership writer while remove waits,
// then releases the lock.
func TestRemoveRetiresANodeRecordedWhileItWaitedForTheRegistryLock(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(config.ConfigDirEnv, dir)
	regPath := filepath.Join(dir, "registry.json")
	ownedPath := filepath.Join(dir, "node-ownership.json")
	if _, err := registry.Add(regPath, registry.Service{Name: "starting", Type: registry.TypeProxy, Target: "http://127.0.0.1:3000"}); err != nil {
		t.Fatal(err)
	}
	stubDaemonRunning(t, true)
	stubDeleteDevices(t, tailapi.CleanupResult{Protected: []string{"starting"}}, nil)
	lock, err := os.OpenFile(regPath+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := filelock.Lock(lock); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := removeServiceResultContext(context.Background(), regPath, ownedPath, "starting")
		done <- err
	}()
	// remove exposes no hook between its start and the registry flock, so
	// give it time to block there; the control below proves it did not pass.
	select {
	case err := <-done:
		t.Fatalf("control: remove finished while the registry lock was held: %v", err)
	case <-time.After(250 * time.Millisecond):
	}
	if err := tsruntime.RecordOwnedNode(ownedPath, "starting", "node-enrolled-during-remove", time.Now()); err != nil {
		_ = filelock.Unlock(lock)
		<-done
		t.Fatal(err)
	}
	if err := filelock.Unlock(lock); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	ledger, err := tsruntime.LoadOwnership(ownedPath)
	if err != nil {
		t.Fatal(err)
	}
	dormant, err := lifecycle.UnretiredOrphanServices(regPath, ownedPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Nodes) != 1 || ledger.Nodes[0].NodeID != "node-enrolled-during-remove" || ledger.Nodes[0].RetiredAt == nil || len(dormant) != 0 {
		t.Fatalf("after remove: ledger = %+v, dormant services = %v; want the row recorded during the wait retired", ledger.Nodes, dormant)
	}
}

// The registry write fails after the retirement was recorded: the rows go
// back exactly as they were, and the registry error is what remove returns.
// The failure is injected at the one point inside the removal a test can
// reach, the clock read that stamps retired_at: it turns the registry
// directory into a symlink, which atomicfile refuses to write through.
func TestRemovePutsTheRowsBackWhenTheRegistryWriteFailsAfterRetiring(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("replaces the registry directory with a symlink while it is locked")
	}
	root := t.TempDir()
	realDir := filepath.Join(root, "config")
	regPath := filepath.Join(realDir, "registry.json")
	ownershipPath := filepath.Join(root, "ledger", "node-ownership.json")
	if _, err := registry.Add(regPath, registry.Service{Name: "web", Type: registry.TypeProxy, Target: "http://127.0.0.1:3000"}); err != nil {
		t.Fatal(err)
	}
	recordedAt := time.Date(2035, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := tsruntime.RecordOwnedNode(ownershipPath, "web", "node-web", recordedAt); err != nil {
		t.Fatal(err)
	}
	stubDaemonRunning(t, true)
	stubDeleteDevices(t, tailapi.CleanupResult{}, nil)
	origNow := removeNowFn
	t.Cleanup(func() { removeNowFn = origNow })
	removeNowFn = func() time.Time {
		moved := realDir + ".moved"
		if err := os.Rename(realDir, moved); err != nil {
			t.Errorf("move the registry directory: %v", err)
		} else if err := os.Symlink(moved, realDir); err != nil {
			t.Errorf("symlink the registry directory: %v", err)
		}
		return recordedAt.Add(time.Hour)
	}

	_, err := removeServiceResult(regPath, ownershipPath, "web")
	if err == nil || strings.Contains(err.Error(), "before unregistering") {
		t.Fatalf("removeServiceResult() error = %v, want the registry write failure itself", err)
	}
	assertStillRegistered(t, regPath, "web")
	ledger, err := tsruntime.LoadOwnership(ownershipPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Nodes) != 1 || ledger.Nodes[0].RetiredAt != nil || !ledger.Nodes[0].RecordedAt.Equal(recordedAt) {
		t.Fatalf("ledger after the failed removal = %+v, want web's row as it was", ledger.Nodes)
	}
}
