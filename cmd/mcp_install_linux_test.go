//go:build linux

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

func TestReview2LinuxInstallEffectBoundaries(t *testing.T) {
	for _, boundary := range []string{"preparation", "daemon-reload", "enable", "reset-failed", "restart"} {
		for _, state := range []string{"active-control", "expired", "cancelled"} {
			t.Run(boundary+"/"+state, func(t *testing.T) {
				dir := isolateBootstrap(t)
				resetRootJSONFlag(t)
				home := t.TempDir()
				oldHome, oldExe, oldEval := linuxUserHomeDirFn, linuxExecutablePathFn, linuxEvalSymlinksFn
				oldConflict, oldManager := installDaemonConflictFn, systemctlCombinedOutput
				t.Cleanup(func() {
					linuxUserHomeDirFn, linuxExecutablePathFn, linuxEvalSymlinksFn = oldHome, oldExe, oldEval
					installDaemonConflictFn, systemctlCombinedOutput = oldConflict, oldManager
				})
				linuxUserHomeDirFn = func() (string, error) { return home, nil }
				linuxEvalSymlinksFn = func(p string) (string, error) { return p, nil }
				installDaemonConflictFn = func(context.Context) error { return nil }
				installDaemonFn = installDaemonLocked
				current := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
				expiry := current.Add(time.Hour)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				reached := false
				invalidate := func() {
					reached = true
					if state == "expired" {
						current = expiry
					}
					if state == "cancelled" {
						cancel()
					}
				}
				linuxExecutablePathFn = func() (string, error) {
					if boundary == "preparation" {
						invalidate()
					}
					return filepath.Join(home, "fake-tslink"), nil
				}
				var calls []string
				systemctlCombinedOutput = func(managerCtx context.Context, args ...string) ([]byte, error) {
					if _, ok := mcpscope.FromContext(managerCtx); !ok {
						t.Fatal("manager lost caller session")
					}
					verb := args[1]
					calls = append(calls, verb)
					if verb == boundary {
						invalidate()
					}
					if verb == "show" {
						return nil, errors.New("fixture manager refusal")
					}
					return nil, nil
				}
				actions := defaultMCPActions(sharePaths{Registry: filepath.Join(dir, "registry.json"), PID: filepath.Join(dir, "tslink.pid"), Snapshot: filepath.Join(dir, "runtime.json")}, io.Discard)
				session := mcpscope.Session{Who: "owner", Scope: mcpscope.Scope{Role: "owner"}, ExpiresAt: &expiry}
				actions.session, actions.nowFn = &session, func() time.Time { return current }
				result, err := callMCPTool(ctx, actions, "add", json.RawMessage(`{"name":"photos","target":"localhost:3000","type":"proxy"}`))
				data, readErr := os.ReadFile(filepath.Join(home, ".config", "systemd", "user", systemdServiceName))
				t.Logf("boundary=%s reached=%v unit_bytes=%d manager=%v result=%s", boundary, reached, len(data), calls, mcpResultCode(result, err))
				if !reached {
					t.Fatal("boundary not exercised")
				}
				if state == "active-control" {
					if readErr != nil || len(data) == 0 || len(calls) != 5 {
						t.Fatalf("positive control: read=%v calls=%v", readErr, calls)
					}
				} else {
					want := map[string]int{"preparation": 0, "daemon-reload": 1, "enable": 2, "reset-failed": 3, "restart": 4}[boundary]
					if len(calls) != want {
						t.Fatalf("new manager command after inactive boundary: %v; want %d", calls, want)
					}
					if boundary == "preparation" && !os.IsNotExist(readErr) {
						t.Fatalf("inactive preparation wrote unit: %v", readErr)
					}
				}
			})
		}
	}
}
