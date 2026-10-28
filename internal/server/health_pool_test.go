package server

import (
	"context"
	"testing"
	"testing/synctest"
	"time"
)

func TestHealthQueueTimeoutIsNotAttempted(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
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
		if elapsed := time.Since(start); elapsed != 5*time.Second {
			t.Fatal("admission did not use its separate five-second budget", elapsed)
		}
		for i := 0; i < 4; i++ {
			<-pool.slots
		}
		got, attempted = boundedHealthRead(context.Background(), pool, "queued", time.Second, "timeout", func(ctx context.Context) string {
			deadline, ok := ctx.Deadline()
			if !ok || time.Until(deadline) != time.Second {
				t.Error("admitted read lost its budget")
			}
			return "ok"
		})
		if got != "ok" || !attempted {
			t.Fatal("admission did not recover", got, attempted)
		}
	})
}

// A read that has returned its value is finished: the same name must be
// admitted again straight away. Releasing the flight after delivering the
// value let an immediate next cycle see it as still running and skip a check
// (gateway_health_test observed degraded instead of down on CI).
func TestHealthCompletedReadReleasesBeforeDelivery(t *testing.T) {
	pool := newHealthReadPool()
	for i := 0; i < 20000; i++ {
		got, attempted := boundedHealthRead(context.Background(), pool, "photos", time.Second, "timeout", func(context.Context) string { return "ok" })
		if got != "ok" || !attempted {
			t.Fatal("read not completed", i, got, attempted)
		}
		pool.mu.Lock()
		_, busy := pool.flights["photos"]
		pool.mu.Unlock()
		if busy || len(pool.slots) != 0 {
			t.Fatalf("iteration %d: completed read still held flight=%v slots=%d", i, busy, len(pool.slots))
		}
	}
}
