package cmd

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/registry"
	tsruntime "github.com/anydoor7/tslink/internal/runtime"
	"github.com/anydoor7/tslink/internal/tailapi"
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
			name:          "a daemon is running and its reconciler removes it",
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
		// Protected without Skipped is not a state internal/tailapi produces
		// today: CleanupStaleNodesResultWithDryRun sets Skipped whenever it
		// protects anything. This row exists because the guard must not depend
		// on that coupling holding. Without it, deleting the Protected clause
		// changes nothing observable, and a later producer that reports a
		// protected hostname without the Skipped flag would silently start
		// deleting the local key of a device TSLink could not prove it owns.
		{
			name:    "a hostname match was protected without the skipped flag",
			cleanup: tailapi.CleanupResult{Matched: []string{"web"}, Deleted: []string{"web"}, ResolvedOwnershipIDs: []string{"node-web"}, Protected: []string{"web"}},
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

// TestRemoveStatesWhyLocalNodeStateWasKept pins the explanation, not just the
// decision. A directory that is still there with nothing said about it is
// indistinguishable, from outside, from a cleanup that never ran.
//
// Only partial resolution gets a reason of its own: a protected hostname and a
// skipped cleanup already reach the caller through device_cleanup_skipped and
// device_skip_reason, and repeating them here would make the field useless as a
// signal that something went unexplained. Those two rows assert exactly that --
// the fact is reported, and reported once.
//
// The reason travels on the result, never on stderr. `tslink remove` reports
// through its result envelope and the compiled-binary contract tests assert an
// empty stderr on success, so every row here also asserts that this command
// logged nothing.
func TestRemoveStatesWhyLocalNodeStateWasKept(t *testing.T) {
	for _, tc := range []struct {
		name           string
		cleanup        tailapi.CleanupResult
		wantKeptReason string
		wantSkipReason string
	}{
		{
			name:           "partial resolution is reported nowhere else",
			cleanup:        tailapi.CleanupResult{Deleted: []string{"web"}, ResolvedOwnershipIDs: []string{"node-other"}},
			wantKeptReason: "neither deleted nor confirmed absent",
		},
		{
			name:           "a protected hostname is already reported as a skip",
			cleanup:        tailapi.CleanupResult{Matched: []string{"web"}, Protected: []string{"web"}, Skipped: true, SkipReason: "ownership unproven"},
			wantSkipReason: "ownership unproven",
		},
		{
			name:           "a skipped cleanup is already reported as a skip",
			cleanup:        tailapi.CleanupResult{Skipped: true, SkipReason: tailapi.ErrNoAPIClient.Error()},
			wantSkipReason: tailapi.ErrNoAPIClient.Error(),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			regPath, ownershipPath, stateDir := removeNodeStateFixture(t, "web")
			stubDaemonRunning(t, false)
			stubDeleteDevices(t, tc.cleanup, nil)
			logs := captureRemoveLogs(t)

			result, err := removeServiceResult(regPath, ownershipPath, "web")
			if err != nil {
				t.Fatalf("removeServiceResult() error = %v", err)
			}
			assertStateDir(t, stateDir, true)

			if tc.wantKeptReason != "" && !strings.Contains(result.NodeStateKeptReason, tc.wantKeptReason) {
				t.Fatalf("NodeStateKeptReason = %q, want it to contain %q", result.NodeStateKeptReason, tc.wantKeptReason)
			}
			if tc.wantSkipReason != "" {
				if result.DeviceSkipReason != tc.wantSkipReason {
					t.Fatalf("DeviceSkipReason = %q, want %q", result.DeviceSkipReason, tc.wantSkipReason)
				}
				if result.NodeStateKeptReason != "" {
					t.Fatalf("NodeStateKeptReason = %q, want empty: this cause is already carried by device_skip_reason", result.NodeStateKeptReason)
				}
			}
			if logs.String() != "" {
				t.Fatalf("remove wrote to the log; its contract is an empty stderr on success:\n%s", logs.String())
			}
		})
	}

	// Control: a removal that does delete the directory reports no keep reason,
	// so the first row above is reading a field that is actually conditional.
	t.Run("no keep reason when the state is deleted", func(t *testing.T) {
		regPath, ownershipPath, stateDir := removeNodeStateFixture(t, "web")
		stubDaemonRunning(t, false)
		stubDeleteDevices(t, tailapi.CleanupResult{Deleted: []string{"web"}, ResolvedOwnershipIDs: []string{"node-web"}}, nil)
		logs := captureRemoveLogs(t)

		result, err := removeServiceResult(regPath, ownershipPath, "web")
		if err != nil {
			t.Fatalf("removeServiceResult() error = %v", err)
		}
		assertStateDir(t, stateDir, false)
		if result.NodeStateKeptReason != "" {
			t.Fatalf("NodeStateKeptReason = %q for a deleted directory, want empty", result.NodeStateKeptReason)
		}
		if logs.String() != "" {
			t.Fatalf("remove wrote to the log:\n%s", logs.String())
		}
	})

	// The keep branch that defers to a running daemon is the one that fires on
	// every removal on a machine with a live daemon. It must also stay silent.
	t.Run("deferring to a running daemon is silent", func(t *testing.T) {
		regPath, ownershipPath, stateDir := removeNodeStateFixture(t, "web")
		stubDaemonRunning(t, true)
		stubDeleteDevices(t, tailapi.CleanupResult{Deleted: []string{"web"}, ResolvedOwnershipIDs: []string{"node-web"}}, nil)
		logs := captureRemoveLogs(t)

		result, err := removeServiceResult(regPath, ownershipPath, "web")
		if err != nil {
			t.Fatalf("removeServiceResult() error = %v", err)
		}
		assertStateDir(t, stateDir, true)
		if result.NodeStateKeptReason != "" {
			t.Fatalf("NodeStateKeptReason = %q, want empty for the ordinary defer-to-daemon case", result.NodeStateKeptReason)
		}
		if logs.String() != "" {
			t.Fatalf("remove wrote to the log on the branch that fires whenever a daemon is running:\n%s", logs.String())
		}
	})
}

// captureRemoveLogs redirects the default logger for one test and returns the
// buffer, so a test can assert that this command logged nothing at all.
func captureRemoveLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return &buf
}
