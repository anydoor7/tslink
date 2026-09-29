package cmd

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/monody0007/tslink/internal/output"
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

// runMCPUntil runs a finite session and fails the test if it outlives limit,
// instead of hanging the package.
func runMCPUntil(t *testing.T, input string, actions mcpActions, limit time.Duration) (string, error, time.Duration) {
	t.Helper()
	var stdout bytes.Buffer
	done := make(chan error, 1)
	start := time.Now()
	go func() { done <- runMCPStdio(context.Background(), strings.NewReader(input), &stdout, actions) }()
	select {
	case err := <-done:
		return stdout.String(), err, time.Since(start)
	case <-time.After(limit):
		t.Fatalf("tslink mcp outlived its closed stdin by more than %v", limit)
		return "", nil, 0
	}
}

func TestMCPEOFWatchdogCancelsACallStuckPastEveryBound(t *testing.T) {
	withMCPEOFWatchdog(t, 300*time.Millisecond, 300*time.Millisecond)
	call := initializedMCPInput(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"url","arguments":{"name":"web","wait":"1s"}}}`)

	t.Run("context-aware call", func(t *testing.T) {
		var cancelled atomic.Bool
		actions := fakeMCPActions()
		actions.url = func(ctx context.Context, _ string, _ time.Duration) (any, error) {
			<-ctx.Done()
			cancelled.Store(true)
			return nil, ctx.Err()
		}
		_, err, took := runMCPUntil(t, call, actions, 5*time.Second)
		if !errors.Is(err, errMCPEOFWatchdog) {
			t.Fatalf("session error = %v, want the post-EOF watchdog", err)
		}
		if !cancelled.Load() {
			t.Fatal("the stuck call never saw cancellation")
		}
		if took < 300*time.Millisecond {
			t.Fatalf("session ended after %v, before the watchdog delay", took)
		}
	})

	t.Run("call that ignores cancellation", func(t *testing.T) {
		release := make(chan struct{})
		t.Cleanup(func() { close(release) })
		actions := fakeMCPActions()
		actions.url = func(context.Context, string, time.Duration) (any, error) {
			<-release
			return URLResult{}, nil
		}
		_, err, took := runMCPUntil(t, call, actions, 5*time.Second)
		if !errors.Is(err, errMCPEOFWatchdog) {
			t.Fatalf("session error = %v, want the post-EOF watchdog", err)
		}
		if took < 600*time.Millisecond {
			t.Fatalf("session ended after %v, before the watchdog delay plus the grace", took)
		}
	})
}

// TestMCPEOFWatchdogLeavesABoundedCallAlone is the other half of the contract:
// piped use (`printf ... | tslink mcp`) closes stdin at once and must still
// get the answer of a call that finishes inside its own bound.
func TestMCPEOFWatchdogLeavesABoundedCallAlone(t *testing.T) {
	withMCPEOFWatchdog(t, 2*time.Second, 300*time.Millisecond)
	var cancelled atomic.Bool
	actions := fakeMCPActions()
	actions.url = func(ctx context.Context, name string, wait time.Duration) (any, error) {
		select {
		case <-ctx.Done():
			cancelled.Store(true)
			return nil, ctx.Err()
		case <-time.After(wait):
			return URLResult{Name: name, URL: "https://" + name + ".tail.ts.net", State: "exact"}, nil
		}
	}
	stdout, err, _ := runMCPUntil(t, initializedMCPInput(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"url","arguments":{"name":"web","wait":"500ms"}}}`), actions, 10*time.Second)
	if err != nil {
		t.Fatalf("session error = %v", err)
	}
	if cancelled.Load() {
		t.Fatal("a call inside its own bound was cancelled after end of input")
	}
	result, _ := mcpFrameByID(t, decodeMCPResponses(t, stdout), float64(2))["result"].(map[string]any)
	if result == nil || result["isError"] == true {
		t.Fatalf("bounded call after end of input was not answered: %s", stdout)
	}
}
