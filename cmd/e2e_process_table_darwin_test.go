//go:build darwin

package cmd

import (
	"errors"
	"os"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// e2eHostProcessTable reads the same sysctl data the product's own
// process-identity check does (internal/daemon/daemon_darwin.go) and runs
// nothing, so a sandbox that forbids /bin/ps does not break it.
//
// The listing (kern.proc.uid) asks only for this user's processes and carries
// each one's command name and start time. Only for the candidates
// e2eCandidateExecutables selects from that listing is the executable path
// read, and only the path: see e2eProcessExecutable.
var e2eHostProcessTable = e2eProcessTable{
	list:       e2eListUserProcesses,
	executable: e2eProcessExecutable,
	// p_comm keeps the first MAXCOMLEN (16) bytes of the executable's name.
	commandMax: 16,
}

func e2eListUserProcesses() ([]e2eProcess, error) {
	procs, err := unix.SysctlKinfoProcSlice("kern.proc.uid", os.Getuid())
	if err != nil {
		return nil, err
	}
	listing := make([]e2eProcess, 0, len(procs))
	for _, proc := range procs {
		start := proc.Proc.P_starttime
		listing = append(listing, e2eProcess{
			PID:     int(proc.Proc.P_pid),
			Command: unix.ByteSliceToString(proc.Proc.P_comm[:]),
			// Microseconds since the epoch, taken when the process was forked.
			Start: start.Sec*1_000_000 + int64(start.Usec),
		})
	}
	return listing, nil
}

// proc_info(2) arguments of libproc's proc_pidpath, which asks the kernel for
// a process's executable path and nothing else (sys/proc_info.h).
const (
	procInfoCallPIDInfo    = 2    // PROC_INFO_CALL_PIDINFO
	procPIDPathInfo        = 11   // PROC_PIDPATHINFO
	procPIDPathInfoMaxSize = 4096 // PROC_PIDPATHINFO_MAXSIZE
)

// e2eProcessExecutable returns pid's executable path the way proc_pidpath
// does, without cgo. It must not use kern.procargs2: that sysctl copies the
// process's argv and its whole environment into this test process along with
// the path, and a candidate can be any tslink the contributor's shell, editor
// or monitoring starts while the tests run.
func e2eProcessExecutable(pid int) (string, error) {
	buf := make([]byte, procPIDPathInfoMaxSize)
	// On success the call returns 0, not the length; the path ends at the
	// first NUL.
	_, _, errno := syscall.Syscall6(syscall.SYS_PROC_INFO, procInfoCallPIDInfo, uintptr(pid), procPIDPathInfo, 0,
		uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if errno != 0 {
		return "", errno
	}
	path := unix.ByteSliceToString(buf)
	if path == "" {
		return "", errors.New("proc_info returned no executable path")
	}
	return path, nil
}
