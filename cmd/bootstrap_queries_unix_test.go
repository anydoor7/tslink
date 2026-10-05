//go:build darwin || linux

package cmd

import (
	"context"
	"errors"
	"html"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/mcpscope"
)

// Retain the real supervisor lock and bootstrap-scope inspection. Invalidate
// after the lock's lifetime check but before the scope inspection's queries.
func TestBootstrapQueriesCheckCaller(t *testing.T) {
	for _, state := range []string{"active-control", "expired", "cancelled"} {
		t.Run(state, func(t *testing.T) {
			isolateBootstrap(t)
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
			queries, installs := 0, 0
			managerOutputFn = func(ctx context.Context, name string, args ...string) ([]byte, error) {
				queries++
				if _, ok := mcpscope.FromContext(ctx); !ok {
					t.Error("manager query lost caller session")
				}
				if name == "systemctl" {
					return []byte("LoadState=not-found\n"), nil
				}
				return []byte("Could not find service\n"), errors.New("not found")
			}
			installDaemonFn = func(context.Context, io.Writer) error {
				installs++
				return errors.New("fixture ends before installation")
			}
			err := ensureDaemon(ctx, io.Discard, false)
			t.Logf("boundary=%v queries=%d installs=%d error=%v", reached, queries, installs, err)
			if !reached {
				t.Fatal("did not reach post-lock boundary")
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

func bootstrapIsolateQueryProcess(t *testing.T) { isolateBootstrap(t) }

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
	data := "<plist><dict><key>EnvironmentVariables</key><dict><key>TSLINK_CONFIG_DIR</key><string>" + html.EscapeString(dir) + "</string></dict></dict></plist>"
	if runtime.GOOS == "linux" {
		data = "\nEnvironment=\"TSLINK_CONFIG_DIR=" + strings.Trim(strconv.Quote(dir), "\"") + "\"\n"
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	if !supervisorConfigMatches([]byte(data), dir) {
		t.Fatal("definition does not bind isolated config")
	}
	return path
}

func bootstrapObserveQueries(t *testing.T, observe func(context.Context)) {
	t.Helper()
	managerOutputFn = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		observe(ctx)
		if name == "systemctl" {
			return []byte("LoadState=not-found\n"), nil
		}
		return []byte("Could not find service\n"), errors.New("not found")
	}
}

// bootstrapSetQueryProcess runs each manager query as a real test child and
// reports each query's error in order, so a cancellation test can tell a query
// ended by its caller from one ended by its own managerQueryTimeout.
func bootstrapSetQueryProcess(t *testing.T, exe string) <-chan error {
	queries := make(chan error, 16)
	managerOutputFn = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		_, err := boundedManagerOutput(ctx, exe, "-test.run=^TestManagerCallerContextHelper$")
		select {
		case queries <- err:
		default:
		}
		if err != nil {
			return nil, err
		}
		if name == "systemctl" {
			return []byte("LoadState=not-found\n"), nil
		}
		return []byte("Could not find service\n"), errors.New("not found")
	}
	return queries
}
