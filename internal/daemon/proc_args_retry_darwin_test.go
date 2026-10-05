//go:build darwin

package daemon

import (
	"encoding/binary"
	"errors"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"golang.org/x/sys/unix"
)

// These tests pin readDarwinProcArgs's answers to a process whose argv is not
// readable yet: keep waiting, give up, and -- for anything that is a real answer
// rather than "not yet" -- do not wait at all. The seam exists because the real
// condition is a fork/exec race; it reproduces at roughly 150 in 9,600 freshly
// started processes under load, which is often enough to make the suite flaky and
// far too rare to assert on directly.

// procArgsBlock builds the argv block kern.procargs2 returns for a populated
// process, so the not-ready fixtures below differ from a good read in exactly the
// way the kernel's do.
func procArgsBlock(executablePath string, args ...string) []byte {
	block := make([]byte, 4)
	binary.NativeEndian.PutUint32(block, uint32(len(args)))
	block = append(block, []byte(executablePath)...)
	block = append(block, 0)
	for _, arg := range args {
		block = append(block, []byte(arg)...)
		block = append(block, 0)
	}
	return block
}

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
	want := procArgsBlock("/usr/local/bin/tslink", "tslink", "serve")
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

	executablePath, args, err := readDarwinProcArgs(4242)
	if err != nil {
		t.Fatalf("readDarwinProcArgs() error = %v, want nil once the EIO clears", err)
	}
	if executablePath != "/usr/local/bin/tslink" || len(args) != 2 || args[1] != "serve" {
		t.Fatalf("readDarwinProcArgs() = %q, %q; want the parsed argv of the successful read", executablePath, args)
	}
	if *calls != 3 {
		t.Fatalf("sysctl called %d times, want 3 (two EIO plus the success)", *calls)
	}
}

// The second disguise of the same window, and the one an EIO-only rule could not
// see: the read succeeds, and the block it returns has no argv in it yet.
func TestReadDarwinProcArgsRetriesPastAnUnpopulatedBlock(t *testing.T) {
	good := procArgsBlock("/usr/local/bin/tslink", "tslink", "serve")
	empty := make([]byte, 4)
	binary.NativeEndian.PutUint32(empty, 1) // argc says 1; the names are not there

	for _, tc := range []struct {
		scenario string
		notReady []byte
	}{
		{"no executable path", empty},
		{"short block", []byte{0x01}},
		{"zero argc", make([]byte, 4)},
		{"truncated arguments", procArgsBlock("/usr/local/bin/tslink", "tslink")[:26]},
	} {
		t.Run(tc.scenario, func(t *testing.T) {
			remaining := 2
			calls := stubProcArgs(t, func(int) ([]byte, error) {
				if remaining > 0 {
					remaining--
					return tc.notReady, nil
				}
				return good, nil
			})

			executablePath, _, err := readDarwinProcArgs(4242)
			if err != nil {
				t.Fatalf("readDarwinProcArgs() error = %v, want nil once the block is populated", err)
			}
			if executablePath != "/usr/local/bin/tslink" {
				t.Fatalf("readDarwinProcArgs() executable = %q, want the one from the populated block", executablePath)
			}
			if *calls != 3 {
				t.Fatalf("sysctl called %d times, want 3; err == nil so an EIO-only rule would have stopped at 1", *calls)
			}
		})
	}
}

func TestReadDarwinProcArgsGivesUpOnAPersistentNotReady(t *testing.T) {
	calls := stubProcArgs(t, func(int) ([]byte, error) {
		return nil, unix.EIO
	})

	_, _, err := readDarwinProcArgs(4242)
	if !errors.Is(err, unix.EIO) {
		t.Fatalf("readDarwinProcArgs() error = %v, want it to still report EIO", err)
	}
	// Exhausting the budget must be distinguishable from one unlucky read;
	// otherwise giving up is silent and reads exactly like never retrying.
	if !strings.Contains(err.Error(), "after 8 attempts") {
		t.Fatalf("readDarwinProcArgs() error = %q, want it to name the attempts it spent", err)
	}
	if *calls != darwinProcArgsAttempts {
		t.Fatalf("sysctl called %d times, want exactly %d; an unbounded retry would block the daemon",
			*calls, darwinProcArgsAttempts)
	}
}

func TestReadDarwinProcArgsDoesNotRetryRealAnswers(t *testing.T) {
	// ESRCH and EINVAL are answers, not "not yet". EINVAL especially: it is what
	// the kernel returns for pid 1 and for a nonexistent pid, so retrying it would
	// make every check of a foreign PID pay the whole budget on every poll.
	for _, errno := range []unix.Errno{unix.ESRCH, unix.EINVAL, unix.EPERM} {
		t.Run(errno.Error(), func(t *testing.T) {
			calls := stubProcArgs(t, func(int) ([]byte, error) {
				return nil, errno
			})

			if _, _, err := readDarwinProcArgs(4242); !errors.Is(err, errno) {
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

	// Virtual time counts exactly the retry sleeps, independent of the host.
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		_, _, _ = readDarwinProcArgs(4242)
		elapsed := time.Since(start)

		want := time.Duration(darwinProcArgsAttempts-1) * darwinProcArgsRetryDelay
		if elapsed != want {
			t.Fatalf("readDarwinProcArgs() took %v, want exactly %v (%d waits)",
				elapsed, want, darwinProcArgsAttempts-1)
		}
	})
}

// The ceiling is not a taste question: cmd/serve.go polls IsProcessRunning every
// daemonParentPoll (10ms), so a budget above that makes one poll overrun the
// next whenever the target is unreadable. Without this test, both knobs are
// unanchored -- raising attempts to 20 or the delay to 250ms leaves every other
// test in this file green, because they all compare the knobs against themselves.
func TestReadDarwinProcArgsWorstCaseStaysUnderTheFastestCallersInterval(t *testing.T) {
	const fastestCallerInterval = 10 * time.Millisecond // cmd/serve.go daemonParentPoll

	worstCase := time.Duration(darwinProcArgsAttempts-1) * darwinProcArgsRetryDelay
	if worstCase >= fastestCallerInterval {
		t.Fatalf("worst-case retry wall time is %v (%d attempts x %v), want under %v so cmd/serve.go's poll loop cannot overrun itself",
			worstCase, darwinProcArgsAttempts, darwinProcArgsRetryDelay, fastestCallerInterval)
	}
	// And it must be long enough to be worth doing: the measured window needed
	// 6 attempts at 1ms in the worst observed case.
	if worstCase < 5*time.Millisecond {
		t.Fatalf("worst-case retry wall time is %v, want at least 5ms; the measured window needed 6 attempts at 1ms",
			worstCase)
	}
}
