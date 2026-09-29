package cmd

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// mcpSlowWriter is a stdout that takes its time, and records any write still
// in progress, or started, once the caller has been told the session is over.
type mcpSlowWriter struct {
	delay     time.Duration
	writing   atomic.Int32
	returned  atomic.Bool
	violation atomic.Bool
}

func (w *mcpSlowWriter) Write(p []byte) (int, error) {
	if w.returned.Load() {
		w.violation.Store(true)
	}
	w.writing.Add(1)
	defer w.writing.Add(-1)
	time.Sleep(w.delay)
	return len(p), nil
}

// TestMCPAbandonedSessionStopsWritingBeforeItReturns covers the grace path,
// where runMCPStdio gives up on a session whose work has not finished and
// returns without the SDK having finished. Here the unfinished work is the
// initialize answer itself, stuck in a slow write past the watchdog and the
// grace. Whatever the session wrote to out must be complete when runMCPStdio
// returns, and nothing may write to out afterwards; otherwise a caller that
// reads its own buffer races the abandoned session.
func TestMCPAbandonedSessionStopsWritingBeforeItReturns(t *testing.T) {
	withMCPEOFWatchdog(t, 50*time.Millisecond, 50*time.Millisecond)
	out := &mcpSlowWriter{delay: 500 * time.Millisecond}
	input := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"ping"}` + "\n"
	done := make(chan error, 1)
	go func() { done <- runMCPStdio(context.Background(), strings.NewReader(input), out, fakeMCPActions()) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("abandoned session did not return")
	}
	if n := out.writing.Load(); n != 0 {
		t.Fatalf("runMCPStdio returned with %d write(s) to out still in progress", n)
	}
	out.returned.Store(true)
	time.Sleep(700 * time.Millisecond)
	if out.violation.Load() {
		t.Fatal("the abandoned session wrote to out after runMCPStdio returned")
	}
}
