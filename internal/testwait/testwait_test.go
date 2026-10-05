package testwait

import (
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

// fakeT records Fatalf instead of stopping the goroutine, so the expiry paths
// can be observed. Its deadline is configurable.
type fakeT struct {
	testing.TB
	deadline    time.Time
	hasDeadline bool
	fatal       string
}

func (f *fakeT) Helper() {}

func (f *fakeT) Fatalf(format string, args ...any) { f.fatal = fmt.Sprintf(format, args...) }

func (f *fakeT) Deadline() (time.Time, bool) { return f.deadline, f.hasDeadline }

// noDeadlineT is a testing.TB without a Deadline method, like testing.B.
type noDeadlineT struct{ testing.TB }

func TestBudget(t *testing.T) {
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		tb   testing.TB
		want time.Duration
	}{
		{"no deadline method", noDeadlineT{t}, MaxBudget},
		{"no deadline set", &fakeT{TB: t}, MaxBudget},
		{"far deadline", &fakeT{TB: t, deadline: now.Add(time.Hour), hasDeadline: true}, MaxBudget},
		{"near deadline keeps reserve", &fakeT{TB: t, deadline: now.Add(reserve + 40*time.Second), hasDeadline: true}, 40 * time.Second},
		{"past deadline floors", &fakeT{TB: t, deadline: now.Add(-time.Minute), hasDeadline: true}, minBudget},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := budgetAt(tc.tb, now); got != tc.want {
				t.Fatalf("budget = %v, want %v", got, tc.want)
			}
		})
	}
	if got := Budget(t); got < minBudget || got > MaxBudget {
		t.Fatalf("Budget(t) = %v, outside [%v, %v]", got, minBudget, MaxBudget)
	}
}

func TestRecvReturnsValueAndClosedZero(t *testing.T) {
	ch := make(chan int, 1)
	ch <- 7
	if got := Recv(t, ch, "value"); got != 7 {
		t.Fatalf("Recv = %d, want 7", got)
	}
	close(ch)
	if got := Recv(t, ch, "close"); got != 0 {
		t.Fatalf("Recv on closed = %d, want 0", got)
	}
}

func TestRecvWaitsForLateSend(t *testing.T) {
	ch := make(chan string)
	go func() {
		time.Sleep(20 * time.Millisecond)
		ch <- "late"
	}()
	if got := Recv(t, ch, "late send"); got != "late" {
		t.Fatalf("Recv = %q, want late", got)
	}
}

func TestRecvFailsNamedAfterBudget(t *testing.T) {
	f := &fakeT{TB: t, deadline: time.Now().Add(-time.Minute), hasDeadline: true}
	start := time.Now()
	got := Recv(f, make(chan error), "writer finished")
	if got != nil {
		t.Fatalf("Recv after expiry = %v, want zero", got)
	}
	if want := "writer finished: did not happen within the 1s hang guard"; f.fatal != want {
		t.Fatalf("fatal = %q, want %q", f.fatal, want)
	}
	if elapsed := time.Since(start); elapsed < minBudget {
		t.Fatalf("expired after %v, before the %v budget", elapsed, minBudget)
	}
}

func TestUntilPollsUntilTrue(t *testing.T) {
	var calls atomic.Int32
	Until(t, "third poll", func() bool { return calls.Add(1) >= 3 })
	if n := calls.Load(); n != 3 {
		t.Fatalf("cond called %d times, want 3", n)
	}
}

func TestUntilFailsNamedAfterBudget(t *testing.T) {
	f := &fakeT{TB: t, deadline: time.Now().Add(-time.Minute), hasDeadline: true}
	start := time.Now()
	Until(f, "records observed", func() bool { return false })
	if want := "records observed: still false after the 1s hang guard"; f.fatal != want {
		t.Fatalf("fatal = %q, want %q", f.fatal, want)
	}
	if elapsed := time.Since(start); elapsed < minBudget {
		t.Fatalf("expired after %v, before the %v budget", elapsed, minBudget)
	}
}
