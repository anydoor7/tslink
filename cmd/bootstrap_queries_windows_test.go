//go:build windows

package cmd

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/mcpscope"
)

func TestBootstrapQueriesCheckCaller(t *testing.T) {
	for _, state := range []string{"active-control", "expired", "cancelled"} {
		t.Run(state, func(t *testing.T) {
			isolateWindowsTask(t)
			current := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
			expiry := current.Add(time.Hour)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx = mcpscope.WithClock(mcpscope.WithSession(ctx, mcpscope.Session{Who: "owner", Scope: mcpscope.Scope{Role: "owner"}, ExpiresAt: &expiry}), func() time.Time { return current })
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
			queries, installs, lostContext := 0, 0, 0
			windowsSchedulerFn = func(managerCtx context.Context, op, name string, data []byte) (windowsSchedulerStatus, error) {
				if op != "query" {
					t.Fatalf("unexpected operation %s", op)
				}
				queries++
				if _, ok := mcpscope.FromContext(managerCtx); !ok {
					lostContext++
				}
				return windowsSchedulerStatus{}, nil
			}
			installDaemonFn = func(context.Context, io.Writer) error {
				installs++
				return errors.New("fixture ends before installation")
			}
			err := ensureDaemon(ctx, io.Discard, false)
			t.Logf("boundary=%v queries=%d lost_context=%d installs=%d error=%v", reached, queries, lostContext, installs, err)
			if !reached {
				t.Fatal("did not reach post-lock boundary")
			}
			if lostContext != 0 {
				t.Fatal("manager query lost caller session")
			}
			if state == "active-control" {
				if queries == 0 || installs != 1 {
					t.Fatalf("control queries=%d installs=%d", queries, installs)
				}
			} else {
				if installs != 0 {
					t.Fatal("inactive bootstrap reached installer")
				}
				if queries != 0 {
					t.Fatalf("inactive bootstrap started %d manager queries", queries)
				}
			}
		})
	}
}

func bootstrapIsolateQueryProcess(t *testing.T) { isolateWindowsTask(t) }

func bootstrapWriteDefinition(t *testing.T) string {
	t.Helper()
	path, err := supervisorPath()
	if err != nil {
		t.Fatal(err)
	}
	dir, err := absoluteConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(windowsConfigEnvironment(dir)+"\r\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func bootstrapObserveQueries(t *testing.T, observe func(context.Context)) {
	t.Helper()
	windowsSchedulerFn = func(ctx context.Context, op, _ string, _ []byte) (windowsSchedulerStatus, error) {
		if op != "query" {
			t.Fatalf("unexpected scheduler operation %s", op)
		}
		observe(ctx)
		return windowsSchedulerStatus{}, nil
	}
}

func bootstrapInspectInstaller(t *testing.T, ctx context.Context, path string, existing bool) {
	t.Helper()
	// Windows has no Unix conflict-capture seam; both scope checks query the
	// scheduler through the same checked adapter used during installation.
	if existing {
		_ = checkSupervisorProcessScope(ctx)
	} else {
		_ = checkUnregisteredSupervisor(ctx)
	}
}

// bootstrapSetQueryProcess runs each scheduler query as a real test child and
// reports each query's error in order, so a cancellation test can tell a query
// ended by its caller from one ended by its own managerQueryTimeout.
func bootstrapSetQueryProcess(t *testing.T, exe string) <-chan error {
	queries := make(chan error, 16)
	windowsSchedulerFn = func(managerCtx context.Context, op, name string, data []byte) (windowsSchedulerStatus, error) {
		if op != "query" {
			t.Fatalf("unexpected manager mutation: %s", op)
		}
		_, err := runBoundedManagerCommandContext(managerCtx, exe, managerQueryTimeout, "-test.run=^TestManagerCallerContextHelper$")
		select {
		case queries <- err:
		default:
		}
		return windowsSchedulerStatus{}, err
	}
	return queries
}
