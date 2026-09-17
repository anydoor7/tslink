//go:build darwin

package daemon

import (
	"errors"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// These tests pin readDarwinProcArgs's three answers to EIO: keep waiting, give
// up, and (for every other errno) do not wait at all. The seam exists because
// the real condition is a fork/exec race -- it reproduces on this machine at
// roughly 3 in 600 fresh processes under load, which is often enough to make the
// suite flaky and far too rare to assert on.
func stubProcArgs(t *testing.T, fn func(pid int) ([]byte, error)) *int {
	t.Helper()
	calls := 0
	prevFn, prevDelay := darwinProcArgs, darwinProcArgsRetryDelay
	darwinProcArgs = func(pid int) ([]byte, error) {
		calls++
		return fn(pid)
	}
	darwinProcArgsRetryDelay = 0
	t.Cleanup(func() {
		darwinProcArgs, darwinProcArgsRetryDelay = prevFn, prevDelay
	})
	return &calls
}

func TestReadDarwinProcArgsRetriesPastATransientEIO(t *testing.T) {
	want := []byte("argv block")
	// Fail twice, then succeed. One retry is all the real race needed, so a
	// single-failure fixture would also be satisfied by a rule that only ever
	// retries once.
	remaining := 2
	calls := stubProcArgs(t, func(int) ([]byte, error) {
		if remaining > 0 {
			remaining--
			return nil, unix.EIO
		}
		return want, nil
	})

	got, err := readDarwinProcArgs(4242)
	if err != nil {
		t.Fatalf("readDarwinProcArgs() error = %v, want nil after the EIO clears", err)
	}
	if string(got) != string(want) {
		t.Fatalf("readDarwinProcArgs() = %q, want %q", got, want)
	}
	if *calls != 3 {
		t.Fatalf("sysctl called %d times, want 3 (two EIO plus the success)", *calls)
	}
}

func TestReadDarwinProcArgsGivesUpOnAPersistentEIO(t *testing.T) {
	calls := stubProcArgs(t, func(int) ([]byte, error) {
		return nil, unix.EIO
	})

	if _, err := readDarwinProcArgs(4242); !errors.Is(err, unix.EIO) {
		t.Fatalf("readDarwinProcArgs() error = %v, want it to still report EIO", err)
	}
	if *calls != darwinProcArgsAttempts {
		t.Fatalf("sysctl called %d times, want exactly %d; an unbounded retry would block the daemon",
			*calls, darwinProcArgsAttempts)
	}
}

func TestReadDarwinProcArgsDoesNotRetryOtherErrnos(t *testing.T) {
	// ESRCH and EINVAL are answers, not "not yet". Retrying them would add
	// latency to every lookup of a dead or unreadable PID for nothing.
	for _, errno := range []unix.Errno{unix.ESRCH, unix.EINVAL, unix.EPERM} {
		t.Run(errno.Error(), func(t *testing.T) {
			calls := stubProcArgs(t, func(int) ([]byte, error) {
				return nil, errno
			})

			if _, err := readDarwinProcArgs(4242); !errors.Is(err, errno) {
				t.Fatalf("readDarwinProcArgs() error = %v, want %v", err, errno)
			}
			if *calls != 1 {
				t.Fatalf("sysctl called %d times for %v, want 1", *calls, errno)
			}
		})
	}
}

func TestReadDarwinProcArgsWaitsBetweenAttempts(t *testing.T) {
	// The delay is the whole mechanism: retrying in a tight loop finishes well
	// inside the window the kernel needs and would not have fixed anything.
	prevDelay := darwinProcArgsRetryDelay
	prevFn := darwinProcArgs
	darwinProcArgsRetryDelay = 5 * time.Millisecond
	darwinProcArgs = func(int) ([]byte, error) { return nil, unix.EIO }
	t.Cleanup(func() { darwinProcArgs, darwinProcArgsRetryDelay = prevFn, prevDelay })

	start := time.Now()
	_, _ = readDarwinProcArgs(4242)
	elapsed := time.Since(start)

	wantAtLeast := time.Duration(darwinProcArgsAttempts-1) * darwinProcArgsRetryDelay
	if elapsed < wantAtLeast {
		t.Fatalf("readDarwinProcArgs() took %v, want at least %v (%d waits)",
			elapsed, wantAtLeast, darwinProcArgsAttempts-1)
	}
}
