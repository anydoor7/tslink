package cmd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/anydoor7/tslink/internal/output"
	"github.com/anydoor7/tslink/internal/testwait"
)

// TestMCPURLWaitIsCapped keeps every MCP tool wait bounded: an agent can no
// longer park `tslink mcp` for as long as it likes with one url call.
func TestMCPURLWaitIsCapped(t *testing.T) {
	if mcpMaxURLWait != 5*time.Minute {
		t.Fatalf("url wait cap = %v, want the documented 5m", mcpMaxURLWait)
	}
	if got, err := parseMCPWait("5m"); err != nil || got != 5*time.Minute {
		t.Fatalf("parseMCPWait(5m) = %v, %v; the cap itself is allowed", got, err)
	}
	for _, raw := range []string{"5m1s", "87600h"} {
		_, err := parseMCPWait(raw)
		if err == nil || output.ExitCode(err) != output.ExitUsage || !strings.Contains(err.Error(), "5m0s") {
			t.Fatalf("parseMCPWait(%q) error = %v, want a usage error naming the 5m0s cap", raw, err)
		}
	}
	description := mcpToolByName(t, "url").InputSchema["properties"].(map[string]any)["wait"].(map[string]any)["description"].(string)
	if !strings.Contains(description, "5m") {
		t.Fatalf("url wait description %q does not state the cap", description)
	}

	// Over the wire the refusal is a tool result the model can read, the same
	// as an unparseable wait, and the url action never runs.
	var ran atomic.Bool
	actions := fakeMCPActions()
	actions.url = func(context.Context, string, time.Duration) (any, error) {
		ran.Store(true)
		return URLResult{}, nil
	}
	stdout := runMCPSession(t, initializedMCPInput(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"url","arguments":{"name":"web","wait":"87600h"}}}`), actions)
	result, _ := mcpFrameByID(t, decodeMCPResponses(t, stdout), float64(2))["result"].(map[string]any)
	if result == nil || result["isError"] != true || !strings.Contains(stdout, "5m0s") {
		t.Fatalf("over-cap wait was not refused with the cap named: %s", stdout)
	}
	if ran.Load() {
		t.Fatal("url action ran with an over-cap wait")
	}
}

// withMCPEOFWatchdog shortens the post-EOF watchdog and the cancellation grace
// for one test.
func withMCPEOFWatchdog(t *testing.T, delay, grace time.Duration) {
	t.Helper()
	oldDelay, oldGrace := mcpEOFWatchdogDelay, mcpCancelGrace
	t.Cleanup(func() { mcpEOFWatchdogDelay, mcpCancelGrace = oldDelay, oldGrace })
	mcpEOFWatchdogDelay, mcpCancelGrace = delay, grace
}

func TestMCPEOFWatchdogDefaultsOutlastEveryToolWait(t *testing.T) {
	if mcpEOFWatchdogDelay != 6*time.Minute {
		t.Fatalf("post-EOF watchdog = %v, want the 5m url wait cap plus one minute", mcpEOFWatchdogDelay)
	}
	if mcpCancelGrace <= 0 || mcpCancelGrace > 10*time.Second {
		t.Fatalf("cancellation grace = %v, want a short positive grace", mcpCancelGrace)
	}
}

// Exercise the shipped budgets too: shortened test timers alone cannot prove
// that a default session cancels cooperative work and abandons stuck work.
func TestMCPEOFWatchdogDefaultBudget(t *testing.T) {
	for _, cooperative := range []bool{true, false} {
		name := "ignores cancellation"
		if cooperative {
			name = "context-aware"
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				started, release := make(chan struct{}), make(chan struct{})
				defer close(release)
				actions := fakeMCPActions()
				actions.url = func(ctx context.Context, _ string, _ time.Duration) (any, error) {
					close(started)
					if cooperative {
						<-ctx.Done()
						return nil, ctx.Err()
					}
					<-release
					return URLResult{}, nil
				}
				call := initializedMCPInput(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"url","arguments":{"name":"web"}}}`)
				_, took, err := runMCPUntil(t, call, actions, started, 7*time.Minute)
				want := 6 * time.Minute
				if !cooperative {
					want += 5 * time.Second
				}
				if !errors.Is(err, errMCPEOFWatchdog) || took != want {
					t.Fatalf("default session = %v after %v, want watchdog after %v", err, took, want)
				}
			})
		})
	}
}

// runMCPUntil closes stdin only after the target operation is in flight. Call
// inside synctest so startup and real fixture I/O do not consume the watchdog
// budget, and elapsed time measures the shutdown protocol rather than load.
func runMCPUntil(t *testing.T, input string, actions mcpActions, started <-chan struct{}, limit time.Duration) (string, time.Duration, error) {
	t.Helper()
	var stdout bytes.Buffer
	in, writer := io.Pipe()
	defer in.Close()
	defer writer.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runMCPStdio(ctx, in, &stdout, actions) }()
	go func() { _, _ = io.WriteString(writer, input) }()
	select {
	case <-started:
	case err := <-done:
		t.Fatalf("session ended before the target operation started: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("target operation did not start before EOF")
	}
	start := time.Now()
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	synctest.Wait()
	select {
	case err := <-done:
		return stdout.String(), time.Since(start), err
	case <-time.After(limit):
		t.Fatalf("tslink mcp outlived its closed stdin by more than %v", limit)
		return "", 0, nil
	}
}

func TestMCPEOFWatchdogCancelsACallStuckPastEveryBound(t *testing.T) {
	withMCPEOFWatchdog(t, 300*time.Millisecond, 300*time.Millisecond)
	call := initializedMCPInput(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"url","arguments":{"name":"web","wait":"1s"}}}`)

	t.Run("context-aware call", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			started := make(chan struct{})
			var cancelled atomic.Bool
			actions := fakeMCPActions()
			actions.url = func(ctx context.Context, _ string, _ time.Duration) (any, error) {
				close(started)
				<-ctx.Done()
				cancelled.Store(true)
				return nil, ctx.Err()
			}
			_, took, err := runMCPUntil(t, call, actions, started, 5*time.Second)
			if !errors.Is(err, errMCPEOFWatchdog) {
				t.Fatalf("session error = %v, want the post-EOF watchdog", err)
			}
			if !cancelled.Load() {
				t.Fatal("the stuck call never saw cancellation")
			}
			if took != 300*time.Millisecond {
				t.Fatalf("session ended after %v, want exactly the watchdog delay", took)
			}
		})
	})

	t.Run("call that ignores cancellation", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			started := make(chan struct{})
			release := make(chan struct{})
			defer close(release)
			actions := fakeMCPActions()
			actions.url = func(context.Context, string, time.Duration) (any, error) {
				close(started)
				<-release
				return URLResult{}, nil
			}
			_, took, err := runMCPUntil(t, call, actions, started, 5*time.Second)
			if !errors.Is(err, errMCPEOFWatchdog) {
				t.Fatalf("session error = %v, want the post-EOF watchdog", err)
			}
			if took != 600*time.Millisecond {
				t.Fatalf("session ended after %v, want exactly the watchdog delay plus the grace", took)
			}
		})
	})
}

// TestMCPEOFWatchdogLeavesABoundedCallAlone is the other half of the contract:
// piped use (`printf ... | tslink mcp`) closes stdin at once and must still
// get the answer of a call that finishes inside its own bound.
func TestMCPEOFWatchdogLeavesABoundedCallAlone(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		withMCPEOFWatchdog(t, 2*time.Second, 300*time.Millisecond)
		var cancelled atomic.Bool
		started := make(chan struct{})
		actions := fakeMCPActions()
		actions.url = func(ctx context.Context, name string, wait time.Duration) (any, error) {
			close(started)
			select {
			case <-ctx.Done():
				cancelled.Store(true)
				return nil, ctx.Err()
			case <-time.After(wait):
				return URLResult{Name: name, URL: "https://" + name + ".tail.ts.net", State: "exact"}, nil
			}
		}
		stdout, took, err := runMCPUntil(t, initializedMCPInput(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"url","arguments":{"name":"web","wait":"500ms"}}}`), actions, started, 10*time.Second)
		if err != nil {
			t.Fatalf("session error = %v", err)
		}
		if cancelled.Load() {
			t.Fatal("a call inside its own bound was cancelled after end of input")
		}
		if took != 500*time.Millisecond {
			t.Fatalf("bounded call took %v, want its 500ms wait", took)
		}
		result, _ := mcpFrameByID(t, decodeMCPResponses(t, stdout), float64(2))["result"].(map[string]any)
		if result == nil || result["isError"] == true {
			t.Fatalf("bounded call after end of input was not answered: %s", stdout)
		}
	})
}

// TestMCPWatchdogEndsTheCommandWithOneErrorLine pins what the user sees when
// the watchdog fires: `tslink mcp` returns an error main prints as a single
// "Error: mcp stdio: ..." line with no Next: hints, and exits 1.
func TestMCPWatchdogEndsTheCommandWithOneErrorLine(t *testing.T) {
	// The command installs real OS signal handlers, outside synctest's clock.
	// Keep that boundary real, but establish the call before sending EOF.
	withMCPEOFWatchdog(t, 200*time.Millisecond, 200*time.Millisecond)
	started := make(chan struct{})
	actions := fakeMCPActions()
	actions.url = func(ctx context.Context, _ string, _ time.Duration) (any, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	call := initializedMCPInput(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"url","arguments":{"name":"web","wait":"1s"}}}`)
	in, writer := io.Pipe()
	defer in.Close()
	defer writer.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runMCPCommand(ctx, in, io.Discard, actions) }()
	go func() { _, _ = io.WriteString(writer, call) }()
	testwait.Recv(t, started, "command dispatched the call before EOF")
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	err := testwait.Recv(t, done, "command returned after the watchdog")
	if !errors.Is(err, errMCPEOFWatchdog) || !strings.HasPrefix(err.Error(), "mcp stdio: ") || strings.Contains(err.Error(), "\n") {
		t.Fatalf("command error = %q, want one mcp stdio line naming the watchdog", err)
	}
	if code := output.ExitCode(err); code != output.ExitError {
		t.Fatalf("exit code = %d, want %d", code, output.ExitError)
	}
	if next := output.NextCommandsForError(err); len(next) != 0 {
		t.Fatalf("Next: hints = %v, want none", next)
	}
}
