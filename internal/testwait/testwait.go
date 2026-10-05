// Package testwait bounds test-side waits for asynchronous effects.
//
// A hang guard proves that an effect eventually happens. Its timeout exists
// only so a genuine hang fails with a named message instead of the binary's
// -timeout panic, so it must never encode how fast the effect is. Short
// real-time guards turn scheduler latency on loaded runners (race detector,
// hosted Windows, CPU contention) into test failures; every guard here waits
// up to Budget instead.
//
// Product time bounds ("shutdown finishes within 1s") belong in
// testing/synctest or an inner deadline assertion, never here. Do not call
// these helpers inside a synctest bubble: the bubble's fake clock would expire
// the guard as soon as every goroutine is durably blocked. Call them only from
// the test goroutine, as t.Fatal requires.
package testwait

import (
	"testing"
	"time"
)

// MaxBudget caps one guard so a hung test still names its wait well before
// the go test default 10-minute -timeout.
const MaxBudget = 2 * time.Minute

// reserve is kept back from the binary deadline for cleanup, goroutine joins
// and failure reporting after a guard expires.
const reserve = 30 * time.Second

// minBudget keeps a guard meaningful when the binary deadline is already close.
const minBudget = time.Second

type deadliner interface {
	Deadline() (time.Time, bool)
}

// Budget returns how long one hang guard may wait: MaxBudget, shortened so
// the guard expires at least reserve before the test binary's deadline. Once
// less than reserve+minBudget remains, the budget floors at minBudget, so the
// reserve is best effort and guards in the binary's last 30 seconds are short.
func Budget(t testing.TB) time.Duration {
	t.Helper()
	return budgetAt(t, time.Now())
}

func budgetAt(t testing.TB, now time.Time) time.Duration {
	budget := MaxBudget
	if d, ok := t.(deadliner); ok {
		if deadline, ok := d.Deadline(); ok {
			if left := deadline.Sub(now) - reserve; left < budget {
				budget = left
			}
		}
	}
	if budget < minBudget {
		budget = minBudget
	}
	return budget
}

// Recv returns the next value received from ch, or the zero value if ch is
// closed. It fails the test if neither happens within Budget.
func Recv[T any](t testing.TB, ch <-chan T, what string) T {
	t.Helper()
	budget := Budget(t)
	timer := time.NewTimer(budget)
	defer timer.Stop()
	select {
	case v := <-ch:
		return v
	case <-timer.C:
		t.Fatalf("%s: did not happen within the %v hang guard", what, budget)
	}
	var zero T
	return zero
}

// Until polls cond until it reports true. It fails the test if cond is still
// false after Budget. Prefer Recv on a channel the code under test signals;
// use Until only for state with no notification.
func Until(t testing.TB, what string, cond func() bool) {
	t.Helper()
	budget := Budget(t)
	deadline := time.Now().Add(budget)
	delay := time.Millisecond
	for !cond() {
		if !time.Now().Before(deadline) {
			t.Fatalf("%s: still false after the %v hang guard", what, budget)
			return
		}
		time.Sleep(delay)
		if delay < 50*time.Millisecond {
			delay *= 2
		}
	}
}
