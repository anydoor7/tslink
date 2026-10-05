package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/mcpscope"
	"github.com/anydoor7/tslink/internal/output"
	"github.com/anydoor7/tslink/internal/registry"
	"github.com/anydoor7/tslink/internal/testwait"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Keep MCP dispatch, default read actions, real supervision queries and files.
// Invalidate at liveness inspection, after dispatch authorizes the request.
func TestReadToolManagerQueriesKeepCaller(t *testing.T) {
	for _, role := range []string{"owner", "viewer"} {
		for _, tool := range []string{"status", "health", "doctor"} {
			for _, state := range []string{"active-control", "expired", "cancelled"} {
				t.Run(role+"/"+tool+"/"+state, func(t *testing.T) {
					_ = newDoctorTestEnv(t, nil)
					bootstrapIsolateQueryProcess(t)
					bootstrapWriteDefinition(t)
					detectSupervisionFn = detectSupervisionContext
					dir, err := config.Dir()
					if err != nil {
						t.Fatal(err)
					}
					paths := sharePaths{Registry: filepath.Join(dir, "registry.json"), PID: filepath.Join(dir, "tslink.pid"), Snapshot: filepath.Join(dir, "runtime.json"), AuthHandoff: filepath.Join(dir, "auth-handoff.json")}
					current := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
					expiry := current.Add(time.Hour)
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					reached := false
					isRunningFn = func(string) bool {
						reached = true
						if state == "expired" {
							current = expiry
						}
						if state == "cancelled" {
							cancel()
						}
						return false
					}
					queries, lost := 0, 0
					bootstrapObserveQueries(t, func(ctx context.Context) {
						queries++
						if _, ok := mcpscope.FromContext(ctx); !ok {
							lost++
						}
					})
					a := defaultMCPActions(paths, io.Discard)
					a.session = &mcpscope.Session{Who: "read-test", Scope: mcpscope.Scope{Role: role, Apps: []string{"photos"}}, ExpiresAt: &expiry}
					a.nowFn = func() time.Time { return current }
					result, err := callMCPTool(ctx, a, tool, json.RawMessage(`{}`))
					t.Logf("boundary=%v queries=%d lost_session=%d result_code=%s error=%v", reached, queries, lost, mcpResultCode(result, err), err)
					if !reached {
						t.Fatal("read did not reach liveness boundary")
					}
					if lost != 0 {
						t.Errorf("manager queries lost %d caller sessions", lost)
					}
					if state == "active-control" {
						if queries == 0 {
							t.Fatal("control reached no queries")
						}
						if mcpResultCode(result, err) != "ok" {
							t.Fatal("active MCP read failed", result, err)
						}
					} else if queries != 0 {
						t.Errorf("inactive caller reached %d manager queries", queries)
					}
				})
			}
		}
	}
}

// A real marked child proves that request cancellation interrupts a manager
// query after the process has started; the active control reaches the child.
func TestReadToolManagerProcessCancellation(t *testing.T) {
	for _, state := range []string{"active-control", "cancelled"} {
		t.Run(state, func(t *testing.T) {
			bootstrapIsolateQueryProcess(t)
			bootstrapWriteDefinition(t)
			detectSupervisionFn = detectSupervisionContext
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(t.TempDir(), "read-query-started")
			t.Setenv("TSLINK_TEST_MANAGER_MARKER", marker)
			t.Setenv("TSLINK_TEST_MANAGER_WAIT", "")
			if state == "cancelled" {
				t.Setenv("TSLINK_TEST_MANAGER_WAIT", "yes")
			}
			queries := bootstrapSetQueryProcess(t, exe)
			dir, _ := config.Dir()
			a := defaultMCPActions(sharePaths{Registry: filepath.Join(dir, "registry.json"), PID: filepath.Join(dir, "tslink.pid"), Snapshot: filepath.Join(dir, "runtime.json"), AuthHandoff: filepath.Join(dir, "auth-handoff.json")}, io.Discard)
			a.session = &mcpscope.Session{Who: "viewer-test", Scope: mcpscope.Scope{Role: "viewer", Apps: []string{"photos"}}}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			type called struct {
				result *mcp.CallToolResult
				err    error
			}
			done := make(chan called, 1)
			go func() {
				result, err := callMCPTool(ctx, a, "status", json.RawMessage(`{}`))
				done <- called{result, err}
			}()
			if state == "cancelled" {
				testwait.Until(t, "read query child started", func() bool { _, err := os.Stat(marker); return err == nil })
				cancel()
			}
			call := testwait.Recv(t, done, "status tool returned")
			result, err := call.result, call.err
			if _, e := os.Stat(marker); e != nil {
				t.Fatalf("query child did not run: %v result=%v error=%v", e, result, err)
			}
			if state == "cancelled" {
				// The child sleeps for an hour: Canceled means the caller ended the
				// query, DeadlineExceeded that it ran to managerQueryTimeout.
				var queryErr error
				select {
				case queryErr = <-queries:
				default:
					t.Fatal("no manager query ran")
				}
				t.Logf("query error=%v result_code=%s", queryErr, mcpResultCode(result, err))
				if !errors.Is(queryErr, context.Canceled) {
					t.Fatalf("read query ignored cancellation: %v", queryErr)
				}
			}
		})
	}
}

func TestSetupInspectionErrorContract(t *testing.T) {
	for _, caller := range []string{"cli-active-control", "cli-cancelled", "mcp-cancelled"} {
		t.Run(caller, func(t *testing.T) {
			bootstrapIsolateQueryProcess(t)
			path := bootstrapWriteDefinition(t)
			// An existing definition bound to another config always refuses bootstrap.
			if err := os.WriteFile(path, []byte("unrelated definition"), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if caller == "mcp-cancelled" {
				ctx = mcpscope.WithSession(ctx, mcpscope.Session{Who: "owner", Scope: mcpscope.Scope{Role: "owner"}})
			}
			reached := false
			isRunningFn = func(string) bool {
				reached = true
				if strings.HasSuffix(caller, "cancelled") {
					cancel()
				}
				return false
			}
			var err error
			if strings.HasPrefix(caller, "cli-") {
				addCmd, _, findErr := rootCmd.Find([]string{"add"})
				if findErr != nil {
					t.Fatal(findErr)
				}
				resetCommandLocalFlags(t, addCmd)
				oldCtx := addCmd.Context()
				t.Cleanup(func() { addCmd.SetContext(oldCtx) })
				addCmd.SetContext(ctx)
				addCmd.SetOut(io.Discard)
				addCmd.SetErr(io.Discard)
				for k, v := range map[string]string{"proxy": "localhost:3000", "wait": "0", "no-daemon-install": "false"} {
					if e := addCmd.Flags().Set(k, v); e != nil {
						t.Fatal(e)
					}
				}
				err = addCmd.RunE(addCmd, []string{"photos"})
			} else {
				err = ensureDaemon(ctx, io.Discard, false)
			}
			if err == nil {
				t.Fatal("inspection unexpectedly succeeded")
			}
			envelope := output.NewFailureForError("add", err)
			t.Logf("boundary=%v exit=%d stable_code=%s message=%s", reached, envelope.Code, envelope.Error.Code, envelope.Error.Message)
			if !reached {
				t.Fatal("did not reach post-lock inspection")
			}
			want := "daemon_setup_failed"
			if caller == "mcp-cancelled" {
				want = "mcp_scope_denied"
			}
			if envelope.Error.Code != want {
				t.Fatalf("stable code=%s want=%s", envelope.Error.Code, want)
			}
			if strings.HasPrefix(caller, "cli-") && !strings.Contains(envelope.Error.Message, "Configuration remains in the registry") {
				t.Error("CLI lost configuration-retained guidance")
			}
		})
	}
}

// Exercise the remaining shared consumers through dispatch and real files.
// No endpoint is ready: the active URL/share controls still query supervision.
func TestReadToolSharedConsumersKeepCaller(t *testing.T) {
	for _, tool := range []string{"list", "url", "share", "events", "doctor"} {
		for _, state := range []string{"active-control", "expired", "cancelled"} {
			t.Run(tool+"/"+state, func(t *testing.T) {
				_ = newDoctorTestEnv(t, nil)
				bootstrapIsolateQueryProcess(t)
				bootstrapWriteDefinition(t)
				detectSupervisionFn = detectSupervisionContext
				dir, err := config.Dir()
				if err != nil {
					t.Fatal(err)
				}
				paths := sharePaths{Registry: filepath.Join(dir, "registry.json"), PID: filepath.Join(dir, "pid"), Snapshot: filepath.Join(dir, "runtime.json"), AuthHandoff: filepath.Join(dir, "auth-handoff.json")}
				if _, err := registry.Add(paths.Registry, registry.Service{Name: "photos", Type: registry.TypeProxy, Target: "http://localhost:3000", Ephemeral: true}); err != nil {
					t.Fatal(err)
				}
				now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
				expiry := now.Add(time.Hour)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				reached := false
				isRunningFn = func(string) bool {
					reached = true
					if state == "expired" {
						now = expiry
					}
					if state == "cancelled" {
						cancel()
					}
					return false
				}
				oldRunning := shareIsRunningFn
				shareIsRunningFn = func(string) bool { return true }
				t.Cleanup(func() { shareIsRunningFn = oldRunning })
				queries := 0
				bootstrapObserveQueries(t, func(queryCtx context.Context) {
					queries++
					s, ok := mcpscope.FromContext(queryCtx)
					if !ok || s.Who != "shared-reader" {
						t.Error("shared consumer lost caller context")
					}
				})
				a := defaultMCPActions(paths, io.Discard)
				a.session = &mcpscope.Session{Who: "shared-reader", Scope: mcpscope.Scope{Role: "owner"}, ExpiresAt: &expiry}
				a.nowFn = func() time.Time { return now }
				// Keep the real share chain, with no endpoint wait required by this test.
				a.share = func(ctx context.Context, req shareRequest) (ShareResult, error) {
					return executeShare(ctx, paths, req, 0, io.Discard)
				}
				switch tool {
				case "events":
					ctx = mcpscope.WithClock(mcpscope.WithSession(ctx, *a.session), a.nowFn)
					_, _ = mcpEventsSnapshotFn(a)(ctx)
				case "url":
					_, _ = callMCPTool(ctx, a, tool, json.RawMessage(`{"name":"photos"}`))
				case "share":
					_, _ = callMCPTool(ctx, a, tool, json.RawMessage(`{"name":"photos","target":"localhost:3000"}`))
				default:
					_, _ = callMCPTool(ctx, a, tool, json.RawMessage(`{}`))
				}
				t.Logf("boundary=%v manager_queries=%d", reached, queries)
				if !reached {
					t.Fatal("consumer did not reach liveness inspection")
				}
				if state == "active-control" && queries == 0 {
					t.Fatal("active consumer reached no manager queries")
				}
				if state != "active-control" && queries != 0 {
					t.Fatalf("inactive consumer started %d manager queries", queries)
				}
			})
		}
	}
}

// Compensation must retain provenance while completing already-started cleanup.
// The original session and context remain inactive and cannot start new work.
func TestManagerCompensationKeepsCaller(t *testing.T) {
	type callerKey struct{}
	for _, state := range []string{"cli", "active-control", "expired", "cancelled"} {
		t.Run(state, func(t *testing.T) {
			bootstrapIsolateQueryProcess(t)
			bootstrapWriteDefinition(t)
			now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
			expiry := now.Add(time.Hour)
			ctx, cancel := context.WithCancel(context.WithValue(context.Background(), callerKey{}, "caller-marker"))
			defer cancel()
			if state != "cli" {
				ctx = mcpscope.WithClock(mcpscope.WithSession(ctx, mcpscope.Session{Who: "cleanup-owner", Identity: mcpscope.Identity{Login: "cleanup-owner"}, Scope: mcpscope.Scope{Role: "owner"}, ExpiresAt: &expiry}), func() time.Time { return now })
			}
			if state == "expired" {
				now = expiry
			}
			if state == "cancelled" {
				cancel()
			}
			cleanup := managerCompensationContext(ctx)
			if cleanup == context.Background() || cleanup.Value(callerKey{}) != "caller-marker" {
				t.Fatal("compensation lost caller context")
			}
			if err := mcpscope.CheckEffect(cleanup); err != nil {
				t.Fatalf("bounded cleanup was refused: %v", err)
			}
			if state == "expired" || state == "cancelled" {
				if err := mcpscope.CheckEffect(ctx); err == nil {
					t.Fatal("compensation reactivated the original request")
				}
			}
			if original, ok := mcpscope.FromContext(ctx); ok {
				got, ok := mcpscope.FromContext(cleanup)
				if !ok || got.Who != original.Who || got.Identity != original.Identity || got.Scope.Role != original.Scope.Role || got.ExpiresAt != nil {
					t.Fatal("compensation changed caller attribution", got)
				}
				if original.ExpiresAt != &expiry {
					t.Fatal("original deadline changed")
				}
			}
			queries := 0
			bootstrapObserveQueries(t, func(queryCtx context.Context) {
				queries++
				if queryCtx.Value(callerKey{}) != "caller-marker" {
					t.Error("cleanup manager query lost caller provenance")
				}
				if state != "cli" {
					if s, ok := mcpscope.FromContext(queryCtx); !ok || s.Who != "cleanup-owner" {
						t.Error("cleanup query lost session")
					}
				}
			})
			_ = detectSupervisionContext(cleanup, "", false, 0)
			if queries == 0 {
				t.Fatal("compensation control reached no manager queries")
			}
		})
	}
}
