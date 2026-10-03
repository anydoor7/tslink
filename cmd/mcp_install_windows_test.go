//go:build windows

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

func TestReview2WindowsInstallEffectBoundaries(t *testing.T) {
	for _, boundary := range []string{"query", "preparation", "register"} {
		for _, state := range []string{"active-control", "expired", "cancelled"} {
			t.Run(boundary+"/"+state, func(t *testing.T) {
				dir, _ := isolateWindowsTask(t)
				resetRootJSONFlag(t)
				installing := false
				installDaemonFn = func(ctx context.Context, out io.Writer) error {
					installing = true
					return installDaemonLocked(ctx, out)
				}
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
				windowsExecutablePathFn = func() (string, error) {
					if boundary == "preparation" {
						invalidate()
					}
					return fixtureTaskSpec().Executable, nil
				}
				var calls []string
				windowsSchedulerFn = func(managerCtx context.Context, op, name string, definition []byte) (windowsSchedulerStatus, error) {
					if !installing {
						return windowsSchedulerStatus{}, nil
					}
					if _, ok := mcpscope.FromContext(managerCtx); !ok {
						t.Fatal("manager lost caller session")
					}
					calls = append(calls, op)
					if op == boundary {
						invalidate()
					}
					switch op {
					case "query":
						return windowsSchedulerStatus{}, nil
					case "register":
						return windowsSchedulerStatus{Exists: true, Enabled: true, State: 3, XML: string(definition)}, nil
					case "run":
						return windowsSchedulerStatus{}, errors.New("fixture manager refusal")
					default:
						t.Fatalf("unexpected manager effect %s", op)
						return windowsSchedulerStatus{}, nil
					}
				}
				actions := defaultMCPActions(sharePaths{Registry: filepath.Join(dir, "registry.json"), PID: filepath.Join(dir, "tslink.pid"), Snapshot: filepath.Join(dir, "runtime.json")}, io.Discard)
				session := mcpscope.Session{Who: "owner", Scope: mcpscope.Scope{Role: "owner"}, ExpiresAt: &expiry}
				actions.session, actions.nowFn = &session, func() time.Time { return current }
				result, err := callMCPTool(ctx, actions, "add", json.RawMessage(`{"name":"photos","target":"localhost:3000","type":"proxy"}`))
				path, _ := windowsTaskPath()
				data, readErr := os.ReadFile(path)
				t.Logf("boundary=%s reached=%v XML_bytes=%d manager=%v result=%s", boundary, reached, len(data), calls, mcpResultCode(result, err))
				if !reached {
					t.Fatal("boundary not exercised")
				}
				if state == "active-control" {
					if readErr != nil || len(data) == 0 || len(calls) != 3 {
						t.Fatalf("positive control: %v %v", readErr, calls)
					}
				} else {
					want := 1
					if boundary == "register" {
						want = 2
					}
					if len(calls) != want {
						t.Fatalf("new manager command after inactive boundary: %v; want %d", calls, want)
					}
					if boundary != "register" && !os.IsNotExist(readErr) {
						t.Fatalf("inactive preparation wrote task XML: %v", readErr)
					}
				}
			})
		}
	}
}

func TestReview2WindowsStartupRechecksAfterPreparation(t *testing.T) {
	for _, state := range []string{"active-control", "expired", "cancelled"} {
		t.Run(state, func(t *testing.T) {
			isolateWindowsTask(t)
			current := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
			expiry := current.Add(time.Hour)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx = mcpscope.WithClock(mcpscope.WithSession(ctx, mcpscope.Session{Who: "owner", Scope: mcpscope.Scope{Role: "owner"}, ExpiresAt: &expiry}), func() time.Time { return current })
			reached := false
			windowsExecutablePathFn = func() (string, error) {
				reached = true
				if state == "expired" {
					current = expiry
				}
				if state == "cancelled" {
					cancel()
				}
				return fixtureTaskSpec().Executable, nil
			}
			command := windowsTestCommand()
			command.SetContext(ctx)
			if err := command.Flags().Set("startup", "true"); err != nil {
				t.Fatal(err)
			}
			err := runInstallLocked(command, nil)
			path, _ := windowsStartupScriptPath()
			data, readErr := os.ReadFile(path)
			if !reached {
				t.Fatal("preparation not exercised")
			}
			if state == "active-control" {
				if err != nil || readErr != nil || len(data) == 0 {
					t.Fatalf("positive control: %v %v", err, readErr)
				}
			} else if err == nil || !os.IsNotExist(readErr) {
				t.Fatalf("inactive preparation wrote Startup script: %v %v", err, readErr)
			}
		})
	}
}
