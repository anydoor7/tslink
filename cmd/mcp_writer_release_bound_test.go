package cmd

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// mcpStuckWriter is a stdout whose reader stopped reading: a write to it does
// not return until unblock is closed. It counts the writes that reached it,
// and those that did after the caller was told the session is over.
type mcpStuckWriter struct {
	unblock  chan struct{}
	started  atomic.Int32
	returned atomic.Bool
	late     atomic.Int32
}

func (w *mcpStuckWriter) Write(p []byte) (int, error) {
	if w.returned.Load() {
		w.late.Add(1)
	}
	w.started.Add(1)
	<-w.unblock
	return len(p), nil
}

func newMCPStuckWriter(t *testing.T) (*mcpStuckWriter, func()) {
	t.Helper()
	out := &mcpStuckWriter{unblock: make(chan struct{})}
	unblock := sync.OnceFunc(func() { close(out.unblock) })
	t.Cleanup(unblock)
	return out, unblock
}

// withMCPWriteGrace shortens, for one test, how long a returning session
// waits for a write to out already in progress.
func withMCPWriteGrace(t *testing.T, grace time.Duration) {
	t.Helper()
	old := mcpWriteGrace
	t.Cleanup(func() { mcpWriteGrace = old })
	mcpWriteGrace = grace
}

func TestMCPWriteGraceDefaultIsShort(t *testing.T) {
	if mcpWriteGrace <= 0 || mcpWriteGrace > 10*time.Second {
		t.Fatalf("write grace = %v, want a short positive bound", mcpWriteGrace)
	}
}

// TestMCPAbandonedSessionReturnsPastAWriteThatNeverFinishes covers a client
// that stopped reading stdout. The initialize answer's write never returns,
// so the session cannot finish: the watchdog cancels it and the grace
// expires. runMCPStdio must still return the watchdog error, having waited a
// bounded time for that write, so `tslink mcp` exits non-zero instead of
// hanging.
func TestMCPAbandonedSessionReturnsPastAWriteThatNeverFinishes(t *testing.T) {
	const watchdog, grace, writeGrace = 50 * time.Millisecond, 50 * time.Millisecond, 100 * time.Millisecond
	withMCPEOFWatchdog(t, watchdog, grace)
	withMCPWriteGrace(t, writeGrace)
	out, unblock := newMCPStuckWriter(t)
	input := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"ping"}` + "\n"
	done := make(chan error, 1)
	go func() { done <- runMCPStdio(context.Background(), strings.NewReader(input), out, fakeMCPActions()) }()
	limit := watchdog + grace + writeGrace + 2*time.Second
	var err error
	select {
	case err = <-done:
	case <-time.After(limit):
		t.Fatalf("runMCPStdio did not return within %v while a write to out never finished", limit)
	}
	if out.started.Load() == 0 {
		t.Fatal("no write to out started, so nothing was stuck")
	}
	if !errors.Is(err, errMCPEOFWatchdog) {
		t.Fatalf("runMCPStdio returned %v, want the watchdog error", err)
	}
	// A write queued behind the stuck one gets its turn only now, and is
	// refused.
	out.returned.Store(true)
	unblock()
	time.Sleep(300 * time.Millisecond)
	if n := out.late.Load(); n != 0 {
		t.Fatalf("%d write(s) reached out after runMCPStdio returned", n)
	}
}

// TestMCPWriterReleaseIsBoundedAndRefusesLaterWrites pins release on its own.
// It waits for a write in progress for its grace and no longer, and once it
// has been called nothing more reaches out: not a write queued behind the
// stuck one that gets its turn when that one finally returns, and not a write
// started afterwards.
func TestMCPWriterReleaseIsBoundedAndRefusesLaterWrites(t *testing.T) {
	out, unblock := newMCPStuckWriter(t)
	writer := newMCPNonClosingWriter(out)
	go func() { _, _ = writer.Write([]byte("stuck\n")) }()
	for deadline := time.Now().Add(2 * time.Second); out.started.Load() == 0; time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the first write never reached out")
		}
	}
	queued := make(chan error, 1)
	go func() {
		_, err := writer.Write([]byte("queued\n"))
		queued <- err
	}()
	time.Sleep(20 * time.Millisecond)

	const grace = 100 * time.Millisecond
	released := make(chan struct{})
	start := time.Now()
	go func() {
		writer.release(grace)
		close(released)
	}()
	select {
	case <-released:
	case <-time.After(grace + 2*time.Second):
		t.Fatalf("release waited more than %v for a write that never finishes", grace+2*time.Second)
	}
	if elapsed := time.Since(start); elapsed < grace {
		t.Fatalf("release returned after %v, before its %v grace, while a write was in progress", elapsed, grace)
	}

	unblock()
	select {
	case err := <-queued:
		if !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("write queued behind the stuck one returned %v after release, want io.ErrClosedPipe", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the queued write never returned")
	}
	if _, err := writer.Write([]byte("later\n")); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("write after release returned %v, want io.ErrClosedPipe", err)
	}
	if n := out.started.Load(); n != 1 {
		t.Fatalf("%d writes reached out, want only the one in progress at release", n)
	}
}
