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

// darwinProcArgs reads a process's argv block. It is a seam so the EIO retry in
// readDarwinProcArgs can be exercised without racing a real fork/exec.
var darwinProcArgs = func(pid int) ([]byte, error) {
	return unix.SysctlRaw("kern.procargs2", pid)
}

// darwinProcArgsAttempts and darwinProcArgsRetryDelay bound that retry. The
// delay is a variable only so tests do not pay it.
var (
	darwinProcArgsAttempts   = 5
	darwinProcArgsRetryDelay = time.Millisecond
)

// readDarwinProcArgs retries kern.procargs2 on EIO.
//
// EIO from this sysctl does not mean inspection is broken; it means the target's
// argv is not readable *yet*, which is what a process looks like in the window
// between fork returning a PID and exec finishing. Every other errno is a real
// answer (ESRCH: gone, EINVAL: not ours to read) and is returned immediately.
//
// The retry is here because the caller cannot distinguish the two. Every error
// out of this function reaches verifyProcessProduct, which wraps anything that
// is not an identity mismatch, and identityVerifiedOrUnavailable then reads
// "could not determine" as "assume it is ours" -- so a transient EIO makes
// IsProcessRunning answer true for a foreign PID, and the daemon becomes willing
// to signal it. Fixing that by treating EIO as a mismatch would trade a rare
// wrong yes for a rare wrong no on the real daemon, which is worse; the
// condition clears on its own, so waiting for it is the honest fix.
//
// Measured 2026-09-16 on this machine under 12-way CPU load: 3 of 600 freshly
// started processes returned EIO, and all 3 succeeded on the first retry 1 ms
// later. Five attempts leaves four times that margin at a 4 ms worst case.
func readDarwinProcArgs(pid int) ([]byte, error) {
	var err error
	for attempt := 0; attempt < darwinProcArgsAttempts; attempt++ {
		if attempt > 0 {
			time.Sleep(darwinProcArgsRetryDelay)
		}
		var data []byte
		data, err = darwinProcArgs(pid)
		if err == nil {
			return data, nil
		}
		if !errors.Is(err, unix.EIO) {
			return nil, err
		}
	}
	return nil, err
}

func darwinProcessArguments(pid int) (string, []string, error) {
	data, err := readDarwinProcArgs(pid)
	if err != nil {
		return "", nil, err
	}
	if len(data) < 4 {
		return "", nil, fmt.Errorf("kern.procargs2 returned %d bytes", len(data))
	}

	argc := int(binary.NativeEndian.Uint32(data[:4]))
	if argc <= 0 {
		return "", nil, fmt.Errorf("kern.procargs2 returned invalid argc %d", argc)
	}
	cursor := 4
	executablePath, next, ok := readNULTerminated(data, cursor)
	if !ok || executablePath == "" {
		return "", nil, fmt.Errorf("kern.procargs2 returned no executable path")
	}
	cursor = next
	for cursor < len(data) && data[cursor] == 0 {
		cursor++
	}

	args := make([]string, 0, argc)
	for len(args) < argc {
		arg, next, ok := readNULTerminated(data, cursor)
		if !ok {
			return "", nil, fmt.Errorf("kern.procargs2 returned %d of %d arguments", len(args), argc)
		}
		args = append(args, arg)
		cursor = next
	}
	return executablePath, args, nil
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
