package daemon

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/anydoor7/tslink/internal/atomicfile"
	"github.com/anydoor7/tslink/internal/filelock"
)

// SupervisorChild owns a direct child. Stop must be bounded and Wait must reap
// it. No global test seam is read by the Wait goroutine.
type SupervisorChild struct {
	PID  int
	Wait func() error
	Stop func() error
}

type SupervisorState struct {
	State     string        `json:"state"`
	DaemonPID int           `json:"daemon_pid,omitempty"`
	Failures  int           `json:"failures"`
	Backoff   time.Duration `json:"backoff_ns,omitempty"`
	NextStart time.Time     `json:"next_start,omitempty"`
	Reason    string        `json:"reason,omitempty"`
	UpdatedAt time.Time     `json:"updated_at"`
}

type SupervisorClock struct {
	Now   func() time.Time
	Sleep func(context.Context, time.Duration) error
}

var supervisorNowFn = time.Now

// NewSupervisorClock captures the seam on the calling goroutine.
func NewSupervisorClock() SupervisorClock {
	return SupervisorClock{Now: supervisorNowFn, Sleep: func(ctx context.Context, d time.Duration) error {
		timer := time.NewTimer(d)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return nil
		}
	}}
}

var ErrSupervisorAlreadyRunning = errors.New("supervisor already running for this config directory")

// WithSupervisorLock holds a nonblocking, process-lifetime config lock. Keep
// the lock file after exit: unlinking it would let contenders lock two inodes.
func WithSupervisorLock(path string, run func() error) error {
	if err := atomicfile.ConvergePrivateFile(path); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	locked, err := filelock.TryLock(f)
	if err != nil {
		return err
	}
	if !locked {
		return ErrSupervisorAlreadyRunning
	}
	defer filelock.Unlock(f)
	return run()
}

// RunSupervisor restarts every unexpected exit, including exit zero. Only an
// explicit cancellation stops supervision. Eight consecutive runs shorter than
// five minutes trip a persistent breaker; the caller records it and exits zero
// so the scheduler's backstop cannot undo the breaker.
func RunSupervisor(ctx context.Context, clock SupervisorClock, start func() (SupervisorChild, error), record func(SupervisorState) error) error {
	failures := 0
	write := func(state SupervisorState) error {
		state.Failures = failures
		state.UpdatedAt = clock.Now().UTC()
		return record(state)
	}
	stopped := func() error { return write(SupervisorState{State: "stopped", Reason: "intentional_stop"}) }
	for {
		if ctx.Err() != nil {
			return stopped()
		}
		if err := write(SupervisorState{State: "starting"}); err != nil {
			return err
		}
		started := clock.Now()
		child, err := start()
		reason := "child_start_failed"
		if err == nil {
			done := make(chan error, 1)
			go func() { done <- child.Wait() }()
			if err := write(SupervisorState{State: "running", DaemonPID: child.PID}); err != nil {
				// Never leave a child behind because recording failed. The Windows
				// job also closes on return if graceful shutdown fails.
				if stopErr := child.Stop(); stopErr != nil {
					return stopErr
				}
				<-done
				return err
			}
			select {
			case <-ctx.Done():
				if err := child.Stop(); err != nil {
					_ = write(SupervisorState{State: "failed", Reason: "child_stop_failed"})
					return err
				}
				<-done
				return stopped()
			case <-done:
				// A stop arriving at the exit boundary wins over a restart.
				if ctx.Err() != nil {
					return stopped()
				}
			}
			reason = "child_exited"
			if clock.Now().Sub(started) >= 5*time.Minute {
				failures = 0
			}
		}
		failures++
		if failures >= 8 {
			return write(SupervisorState{State: "circuit_open", Reason: "eight_consecutive_unstable_runs"})
		}
		delay := min(time.Second<<uint(failures-1), 60*time.Second)
		if err := write(SupervisorState{State: "restarting", Backoff: delay, NextStart: clock.Now().Add(delay).UTC(), Reason: reason}); err != nil {
			return err
		}
		if err := clock.Sleep(ctx, delay); err != nil {
			return stopped()
		}
	}
}
