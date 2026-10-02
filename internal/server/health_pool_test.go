package server

import (
	"context"
	"testing"
	"time"
)

func TestHealthQueueTimeoutIsNotAttempted(t *testing.T) {
	pool := newHealthReadPool()
	// Occupied slots are not yet timed out. Admission must expire separately
	// from the configured I/O budget and never invoke the read.
	for i := 0; i < 4; i++ {
		pool.slots <- struct{}{}
	}
	called := false
	start := time.Now()
	got, attempted := boundedHealthRead(context.Background(), pool, "queued", time.Minute, "timeout", func(context.Context) string { called = true; return "ok" })
	if got != "timeout" || attempted || called {
		t.Fatal("queue timeout counted as a read", got, attempted, called)
	}
	if elapsed := time.Since(start); elapsed < 5*time.Second || elapsed > 8*time.Second {
		t.Fatal("admission did not use its separate five-second budget", elapsed)
	}
	for i := 0; i < 4; i++ {
		<-pool.slots
	}
	got, attempted = boundedHealthRead(context.Background(), pool, "queued", time.Second, "timeout", func(ctx context.Context) string {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) < 900*time.Millisecond {
			t.Error("admitted read lost its budget")
		}
		return "ok"
	})
	if got != "ok" || !attempted {
		t.Fatal("admission did not recover", got, attempted)
	}
}
