//go:build darwin

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/mcpscope"
)

// Keep the real MCP action, registry, supervisor lock, installer and plist write.
// Only OS manager execution and executable discovery are replaced, so this
// cannot install, stop or bootstrap a real service.
func TestReview2InstallRechecksAfterPreparation(t *testing.T) {
	for _, state := range []string{"active-control", "expired", "cancelled"} {
		t.Run(state, func(t *testing.T) {
			dir := isolateBootstrap(t)
			resetRootJSONFlag(t)
			home := t.TempDir()
			oldHome, oldExe, oldEval, oldUID := userHomeDirFn, executablePathFn, evalSymlinksFn, userUIDFn
			oldConflict, oldLaunchctl := installDaemonConflictFn, launchctlCombinedOutput
			t.Cleanup(func() {
				userHomeDirFn, executablePathFn, evalSymlinksFn, userUIDFn = oldHome, oldExe, oldEval, oldUID
				installDaemonConflictFn, launchctlCombinedOutput = oldConflict, oldLaunchctl
			})
			userHomeDirFn = func() (string, error) { return home, nil }
			userUIDFn = func() int { return 99991 }
			installDaemonConflictFn = func() error { return nil }
			evalSymlinksFn = func(path string) (string, error) { return path, nil }
			installDaemonFn = installDaemonLocked
			now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
			expiry := now.Add(time.Hour)
			current := now
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			reached := false
			executablePathFn = func() (string, error) {
				reached = true
				if state == "expired" {
					current = expiry
				}
				if state == "cancelled" {
					cancel()
				}
				return filepath.Join(home, "fake-tslink"), nil
			}
			effects := 0
			launchctlCombinedOutput = func(ctx context.Context, args ...string) ([]byte, error) {
				if len(args) > 0 && (args[0] == "bootout" || args[0] == "bootstrap") {
					effects++
				}
				return []byte("fixture manager refuses operation"), errors.New("fixture manager refusal")
			}
			a := defaultMCPActions(sharePaths{Registry: filepath.Join(dir, "registry.json"), PID: filepath.Join(dir, "tslink.pid"), Snapshot: filepath.Join(dir, "runtime.json")}, io.Discard)
			s := mcpscope.Session{Who: "owner", Scope: mcpscope.Scope{Role: "owner"}, ExpiresAt: &expiry}
			a.session = &s
			a.nowFn = func() time.Time { return current }
			result, err := callMCPTool(ctx, a, "add", json.RawMessage(`{"name":"photos","target":"localhost:3000","type":"proxy"}`))
			path := filepath.Join(home, "Library", "LaunchAgents", plistLabel+".plist")
			data, readErr := os.ReadFile(path)
			t.Logf("state=%s preparation=%v plist_bytes=%d manager_effect_calls=%d result=%s", state, reached, len(data), effects, mcpResultCode(result, err))
			if !reached {
				t.Fatal("did not reach real installer preparation")
			}
			if state == "active-control" {
				if readErr != nil || len(data) == 0 || effects == 0 {
					t.Fatalf("positive control did not write/call manager: %v %d", readErr, effects)
				}
			} else if readErr == nil || effects != 0 {
				t.Fatalf("inactive MCP installation wrote plist=%v and invoked manager effects=%d after preparation", readErr == nil, effects)
			}
		})
	}
}

func TestReview2InstallRechecksAfterManagerWait(t *testing.T) {
	for _, state := range []string{"active-control", "expired", "cancelled"} {
		t.Run(state, func(t *testing.T) {
			dir := isolateBootstrap(t)
			resetRootJSONFlag(t)
			home := t.TempDir()
			oldHome, oldExe, oldEval, oldUID := userHomeDirFn, executablePathFn, evalSymlinksFn, userUIDFn
			oldConflict, oldLaunchctl := installDaemonConflictFn, launchctlCombinedOutput
			t.Cleanup(func() {
				userHomeDirFn, executablePathFn, evalSymlinksFn, userUIDFn = oldHome, oldExe, oldEval, oldUID
				installDaemonConflictFn, launchctlCombinedOutput = oldConflict, oldLaunchctl
			})
			userHomeDirFn = func() (string, error) { return home, nil }
			userUIDFn = func() int { return 99991 }
			installDaemonConflictFn = func() error { return nil }
			evalSymlinksFn = func(path string) (string, error) { return path, nil }
			installDaemonFn = installDaemonLocked
			now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
			expiry := now.Add(time.Hour)
			current := now
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			reached := false
			executablePathFn = func() (string, error) {
				reached = true

				return filepath.Join(home, "fake-tslink"), nil
			}
			effects, proofs, bootstraps := 0, 0, 0
			launchctlCombinedOutput = func(ctx context.Context, args ...string) ([]byte, error) {
				switch args[0] {
				case "bootout":
					effects++
					return nil, nil
				case "print":
					proofs++
					if state == "expired" {
						current = expiry
					}
					if state == "cancelled" {
						cancel()
					}
					return []byte("Could not find service"), errors.New("not found")
				case "bootstrap":
					effects++
					bootstraps++
					return []byte("fixture manager refuses operation"), errors.New("fixture manager refusal")
				default:
					t.Fatalf("unexpected manager command %v", args)
					return nil, nil
				}
			}
			a := defaultMCPActions(sharePaths{Registry: filepath.Join(dir, "registry.json"), PID: filepath.Join(dir, "tslink.pid"), Snapshot: filepath.Join(dir, "runtime.json")}, io.Discard)
			s := mcpscope.Session{Who: "owner", Scope: mcpscope.Scope{Role: "owner"}, ExpiresAt: &expiry}
			a.session = &s
			a.nowFn = func() time.Time { return current }
			result, err := callMCPTool(ctx, a, "add", json.RawMessage(`{"name":"photos","target":"localhost:3000","type":"proxy"}`))
			path := filepath.Join(home, "Library", "LaunchAgents", plistLabel+".plist")
			data, readErr := os.ReadFile(path)
			t.Logf("state=%s preparation=%v plist_bytes=%d proofs=%d bootstraps=%d result=%s", state, reached, len(data), proofs, bootstraps, mcpResultCode(result, err))
			if !reached {
				t.Fatal("did not reach real installer preparation")
			}
			if state == "active-control" {
				if readErr != nil || len(data) == 0 || effects == 0 || proofs == 0 || bootstraps != 1 {
					t.Fatalf("positive control did not write/call manager: %v %d", readErr, effects)
				}
			} else if bootstraps != 0 {
				t.Fatalf("inactive MCP installation started %d new bootstrap effects after manager wait", bootstraps)
			}
		})
	}
}
