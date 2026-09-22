package cmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/registry"
	tsruntime "github.com/monody0007/tslink/internal/runtime"
	"github.com/monody0007/tslink/internal/tailapi"
)

// removeNodeStateFixture builds a config directory holding one registered
// service, its recorded node ownership, and its tsnet state directory, plus a
// second service that must survive every case below.
func removeNodeStateFixture(t *testing.T, name string) (regPath, ownershipPath, stateDir string) {
	t.Helper()
	configDir := t.TempDir()
	regPath = filepath.Join(configDir, "registry.json")
	ownershipPath = filepath.Join(configDir, "node-ownership.json")

	for _, svc := range []string{name, "bystander"} {
		if _, err := registry.Add(regPath, registry.Service{Name: svc, Type: registry.TypeProxy, Target: "http://localhost:3000"}); err != nil {
			t.Fatalf("Add(%q) error = %v", svc, err)
		}
		dir := filepath.Join(config.NodesDirIn(configDir), svc)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("MkdirAll(%q) error = %v", dir, err)
		}
		if err := os.WriteFile(filepath.Join(dir, "tailscaled.state"), []byte("machine key"), 0o600); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}
	}
	if err := tsruntime.RecordOwnedNode(ownershipPath, name, "node-"+name, removeNowFn()); err != nil {
		t.Fatalf("RecordOwnedNode() error = %v", err)
	}
	return regPath, ownershipPath, filepath.Join(config.NodesDirIn(configDir), name)
}

func stubDaemonRunning(t *testing.T, running bool) {
	t.Helper()
	orig := removeDaemonRunningFn
	removeDaemonRunningFn = func() bool { return running }
	t.Cleanup(func() { removeDaemonRunningFn = orig })
}

func stubDeleteDevices(t *testing.T, result tailapi.CleanupResult, err error) {
	t.Helper()
	orig := deleteDevicesFn
	deleteDevicesFn = func(context.Context, tailapi.CleanupTarget) (tailapi.CleanupResult, error) {
		return result, err
	}
	t.Cleanup(func() { deleteDevicesFn = orig })
}

func assertStateDir(t *testing.T, dir string, wantPresent bool) {
	t.Helper()
	_, err := os.Stat(dir)
	switch {
	case wantPresent && err != nil:
		t.Fatalf("Stat(%q) err = %v, want the node state kept", dir, err)
	case !wantPresent && !os.IsNotExist(err):
		t.Fatalf("Stat(%q) err = %v, want the node state removed", dir, err)
	}
}

// TestRemoveDeletesLocalNodeStateAfterAConfirmedRemoteDeletion is the case the
// fix exists for: a remove issued while no daemon is running. The daemon's own
// hot-reload path never sees the transition, so nothing else will ever delete
// the directory.
func TestRemoveDeletesLocalNodeStateAfterAConfirmedRemoteDeletion(t *testing.T) {
	regPath, ownershipPath, stateDir := removeNodeStateFixture(t, "web")
	configDir := filepath.Dir(regPath)
	stubDaemonRunning(t, false)
	stubDeleteDevices(t, tailapi.CleanupResult{
		Matched:              []string{"web"},
		Deleted:              []string{"web"},
		ResolvedOwnershipIDs: []string{"node-web"},
	}, nil)

	result, err := removeServiceResult(regPath, ownershipPath, "web")
	if err != nil || !result.Removed {
		t.Fatalf("removeServiceResult() = %+v err=%v", result, err)
	}
	if result.DeviceWarning != "" {
		t.Fatalf("DeviceWarning = %q, want none", result.DeviceWarning)
	}
	assertStateDir(t, stateDir, false)
	// The other registered service keeps its identity. A removal that widened
	// to the nodes directory would pass every assertion above.
	assertStateDir(t, filepath.Join(config.NodesDirIn(configDir), "bystander"), true)
}

// TestRemoveDeletesLocalNodeStateWhenNoRemoteNodeWasEverRecorded covers the
// second safe case: nothing was ever enrolled, and the API reported no
// hostname match either, so there is no identity to protect.
func TestRemoveDeletesLocalNodeStateWhenNoRemoteNodeWasEverRecorded(t *testing.T) {
	configDir := t.TempDir()
	regPath := filepath.Join(configDir, "registry.json")
	ownershipPath := filepath.Join(configDir, "node-ownership.json")
	if _, err := registry.Add(regPath, registry.Service{Name: "never", Type: registry.TypeProxy, Target: "http://localhost:3000"}); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	stateDir := filepath.Join(config.NodesDirIn(configDir), "never")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	stubDaemonRunning(t, false)
	stubDeleteDevices(t, tailapi.CleanupResult{}, nil)

	if _, err := removeServiceResult(regPath, ownershipPath, "never"); err != nil {
		t.Fatalf("removeServiceResult() error = %v", err)
	}
	assertStateDir(t, stateDir, false)
}

// TestRemoveKeepsLocalNodeStateWhenTheRemoteSideIsUnconfirmed is the negative
// half. Each row is a way this process failed to establish that the local key
// authenticates to nothing; keeping the directory is the only safe answer to
// all of them.
func TestRemoveKeepsLocalNodeStateWhenTheRemoteSideIsUnconfirmed(t *testing.T) {
	for _, tc := range []struct {
		name          string
		daemonRunning bool
		cleanup       tailapi.CleanupResult
		cleanupErr    error
	}{
		{
			name:          "a daemon is running and removes it itself",
			daemonRunning: true,
			cleanup:       tailapi.CleanupResult{Deleted: []string{"web"}, ResolvedOwnershipIDs: []string{"node-web"}},
		},
		{
			name:    "the API listed a hostname match TSLink cannot prove it owns",
			cleanup: tailapi.CleanupResult{Matched: []string{"web"}, Protected: []string{"web"}, Skipped: true, SkipReason: "ownership unproven"},
		},
		{
			name:    "cleanup was skipped without an API client",
			cleanup: tailapi.CleanupResult{Skipped: true, SkipReason: tailapi.ErrNoAPIClient.Error()},
		},
		{
			name:       "the delete call failed",
			cleanupErr: errors.New("delete TSLink-owned device failed"),
		},
		{
			name:    "the recorded node was neither deleted nor accounted for",
			cleanup: tailapi.CleanupResult{Matched: []string{"web"}},
		},
		{
			name:    "only some of the recorded nodes resolved",
			cleanup: tailapi.CleanupResult{Deleted: []string{"web"}, ResolvedOwnershipIDs: []string{"node-other"}},
		},
		{
			// Every recorded node resolved AND a second device carrying this
			// service's hostname was protected. The recorded ones are gone, so
			// the all-resolved check passes; the protected device is one this
			// installation may still hold the key for, which is why protection
			// has to be its own refusal rather than a consequence of the other.
			name:    "a second hostname match stayed protected although every recorded node resolved",
			cleanup: tailapi.CleanupResult{Matched: []string{"web"}, Deleted: []string{"web"}, ResolvedOwnershipIDs: []string{"node-web"}, Protected: []string{"web"}, Skipped: true, SkipReason: "ownership unproven"},
		},
		{
			name:    "the cleanup reported skipped although every recorded node resolved",
			cleanup: tailapi.CleanupResult{Deleted: []string{"web"}, ResolvedOwnershipIDs: []string{"node-web"}, Skipped: true, SkipReason: "partial device listing"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			regPath, ownershipPath, stateDir := removeNodeStateFixture(t, "web")
			stubDaemonRunning(t, tc.daemonRunning)
			stubDeleteDevices(t, tc.cleanup, tc.cleanupErr)

			if _, err := removeServiceResult(regPath, ownershipPath, "web"); err != nil {
				t.Fatalf("removeServiceResult() error = %v", err)
			}
			assertStateDir(t, stateDir, true)
		})
	}
}

// TestRemoveReportsAFailedNodeStateDeletionWithoutFailingTheRemove pins that a
// directory that cannot be deleted is reported rather than silently dropped,
// and does not turn a completed removal into an error.
func TestRemoveReportsAFailedNodeStateDeletionWithoutFailingTheRemove(t *testing.T) {
	regPath, ownershipPath, stateDir := removeNodeStateFixture(t, "web")
	stubDaemonRunning(t, false)
	stubDeleteDevices(t, tailapi.CleanupResult{Deleted: []string{"web"}, ResolvedOwnershipIDs: []string{"node-web"}}, nil)
	orig := removeNodeStateFn
	removeNodeStateFn = func(string, string) error { return errors.New("injected removal failure") }
	t.Cleanup(func() { removeNodeStateFn = orig })

	result, err := removeServiceResult(regPath, ownershipPath, "web")
	if err != nil {
		t.Fatalf("removeServiceResult() error = %v, want the removal to succeed", err)
	}
	if !result.Removed {
		t.Fatal("Removed = false, want the registry removal to stand")
	}
	if result.DeviceWarning == "" {
		t.Fatal("DeviceWarning = \"\", want the failed node-state deletion reported")
	}
	assertStateDir(t, stateDir, true)
}

// TestRemoveNodeStateUsesTheRegistrysOwnConfigDir pins the derivation. The
// process-wide config directory is set to a different tree holding a
// same-named service, and must be untouched.
func TestRemoveNodeStateUsesTheRegistrysOwnConfigDir(t *testing.T) {
	regPath, ownershipPath, stateDir := removeNodeStateFixture(t, "web")
	elsewhere := t.TempDir()
	t.Setenv(config.ConfigDirEnv, elsewhere)
	elsewhereState := filepath.Join(config.NodesDirIn(elsewhere), "web")
	if err := os.MkdirAll(elsewhereState, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	stubDaemonRunning(t, false)
	stubDeleteDevices(t, tailapi.CleanupResult{Deleted: []string{"web"}, ResolvedOwnershipIDs: []string{"node-web"}}, nil)

	if _, err := removeServiceResult(regPath, ownershipPath, "web"); err != nil {
		t.Fatalf("removeServiceResult() error = %v", err)
	}
	assertStateDir(t, stateDir, false)
	assertStateDir(t, elsewhereState, true)
}
