//go:build darwin

package daemon

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"time"

	"golang.org/x/sys/unix"
)

// Darwin process inspection uses native sysctl data. It avoids a runtime
// dependency on ps and preserves argv boundaries when install paths contain
// whitespace.
func defaultProcessExecutable(pid int) (string, error) {
	executablePath, _, err := darwinProcessArguments(pid)
	return executablePath, err
}

func defaultProcessStartTime(pid int) (time.Time, error) {
	info, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return time.Time{}, err
	}
	if info == nil || info.Proc.P_pid != int32(pid) {
		return time.Time{}, fmt.Errorf("kern.proc.pid returned no record for process %d", pid)
	}
	return time.Unix(0, info.Proc.P_starttime.Nano()), nil
}

func defaultProcessArguments(pid int) ([]string, error) {
	_, args, err := darwinProcessArguments(pid)
	return args, err
}

// darwinProcArgs reads a process's argv block. It is a seam so the retry in
// readDarwinProcArgs can be exercised without racing a real fork/exec.
//
// This and the two knobs below are package-level vars that tests swap out. That
// is the shape that caused a data race in internal/server (serverNowFn, fixed
// 2026-09-16): a goroutine outliving the test that installed a stub reads the
// seam after t.Cleanup restored it. It is safe here only because every reader is
// synchronous -- readDarwinProcArgs returns before its caller does, and no test
// in this package runs t.Parallel. Note that the daemon does have a goroutine
// reader: cmd/serve.go polls IsProcessRunning every daemonParentPoll. It
// never swaps the seam, so there is no race today; a test that both stubs these
// vars and starts that loop would create one.
var darwinProcArgs = func(pid int) ([]byte, error) {
	return unix.SysctlRaw("kern.procargs2", pid)
}

// darwinProcArgsAttempts and darwinProcArgsRetryDelay bound the retry.
//
// The ceiling is set by the fastest caller, not by the measurement: cmd/serve.go
// polls IsProcessRunning every daemonParentPoll = 10ms, so a budget above that
// would make one poll overrun the next whenever the target is unreadable. 8 x 1ms
// = 7ms worst case stays under it.
//
// Measured need, 2026-09-16, 8 concurrent spawners under 12-way CPU load: of
// 9,600 freshly started processes, 150 hit the not-ready window; ~95% cleared
// within the first 4ms, and the stragglers cleared one millisecond later -- i.e.
// 6 attempts sufficed for every case observed. 8 leaves margin for a slower
// machine without crossing the poll interval.
var (
	darwinProcArgsAttempts   = 8
	darwinProcArgsRetryDelay = time.Millisecond
)

// errProcArgsNotReady marks "this process's argv is not readable *yet*", as
// opposed to "not readable". Only this class is retried.
var errProcArgsNotReady = errors.New("kern.procargs2 argv not ready")

// readDarwinProcArgs reads and parses a process's argv, retrying while the
// kernel says the answer is not ready yet.
//
// The window is the gap between fork returning a PID and exec finishing, and it
// shows up in two different disguises -- which is the whole reason this function
// retries the parse and not just the syscall:
//
//   - the sysctl fails with EIO;
//   - the sysctl *succeeds* with err == nil and hands back a block that has no
//     executable path in it yet.
//
// The second form is why an EIO-only retry was not enough. Measured on the same
// 9,600-process run: EIO 150 occurrences, argv-present-but-empty 9. An EIO-only
// rule never fires on the second form, because there is no error to match on.
//
// Why retrying matters at all: every error out of here reaches
// verifyProcessProduct, which wraps anything that is not an identity mismatch,
// and identityVerifiedOrUnavailable then reads "could not determine" as "assume
// it is ours". So a transient here makes IsProcessRunning answer true for a
// foreign PID. Note that the two production callers want opposite things from
// that answer -- cmd/stop.go asks before signalling, while cmd/serve.go's poll
// loop shuts the daemon down on false -- so flipping the unknown case to
// fail-closed would trade a rare wrong yes for a rare wrong self-shutdown. The
// condition clears on its own; waiting for it is the honest fix.
//
// EINVAL is deliberately not retried. It is the kernel's answer both inside this
// window and for any process we simply may not read -- pid 1 and a nonexistent
// pid both return it (verified 2026-09-16). Retrying it would make every check of
// a foreign PID pay the full budget on every 10ms poll, to recover a form
// measured at 1 occurrence in 9,600.
func readDarwinProcArgs(pid int) (string, []string, error) {
	var err error
	for attempt := 1; attempt <= darwinProcArgsAttempts; attempt++ {
		if attempt > 1 {
			time.Sleep(darwinProcArgsRetryDelay)
		}
		var executablePath string
		var args []string
		executablePath, args, err = readDarwinProcArgsOnce(pid)
		if err == nil {
			return executablePath, args, nil
		}
		if !errors.Is(err, errProcArgsNotReady) {
			return "", nil, err
		}
	}
	// Name the budget so an exhausted retry is distinguishable in the log from a
	// single unlucky read; without it, giving up is silent and looks exactly like
	// never having retried.
	return "", nil, fmt.Errorf("after %d attempts: %w", darwinProcArgsAttempts, err)
}

func readDarwinProcArgsOnce(pid int) (string, []string, error) {
	data, err := darwinProcArgs(pid)
	if err != nil {
		if errors.Is(err, unix.EIO) {
			return "", nil, fmt.Errorf("%w: %w", errProcArgsNotReady, err)
		}
		return "", nil, err
	}
	// Everything below here is "the block came back but is not populated yet".
	if len(data) < 4 {
		return "", nil, fmt.Errorf("%w: kern.procargs2 returned %d bytes", errProcArgsNotReady, len(data))
	}

	argc := int(binary.NativeEndian.Uint32(data[:4]))
	if argc <= 0 {
		return "", nil, fmt.Errorf("%w: kern.procargs2 returned invalid argc %d", errProcArgsNotReady, argc)
	}
	cursor := 4
	executablePath, next, ok := readNULTerminated(data, cursor)
	if !ok || executablePath == "" {
		return "", nil, fmt.Errorf("%w: kern.procargs2 returned no executable path", errProcArgsNotReady)
	}
	cursor = next
	for cursor < len(data) && data[cursor] == 0 {
		cursor++
	}

	args := make([]string, 0, argc)
	for len(args) < argc {
		arg, next, ok := readNULTerminated(data, cursor)
		if !ok {
			return "", nil, fmt.Errorf("%w: kern.procargs2 returned %d of %d arguments", errProcArgsNotReady, len(args), argc)
		}
		args = append(args, arg)
		cursor = next
	}
	return executablePath, args, nil
}

func darwinProcessArguments(pid int) (string, []string, error) {
	return readDarwinProcArgs(pid)
}

func readNULTerminated(data []byte, offset int) (string, int, bool) {
	if offset < 0 || offset >= len(data) {
		return "", offset, false
	}
	relativeEnd := bytes.IndexByte(data[offset:], 0)
	if relativeEnd < 0 {
		return "", offset, false
	}
	end := offset + relativeEnd
	return string(data[offset:end]), end + 1, true
}
