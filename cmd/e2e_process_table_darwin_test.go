//go:build darwin

package cmd

import (
	"bytes"
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// e2eHostProcessTable reads the same sysctl data the product's own
// process-identity check does (internal/daemon/daemon_darwin.go) and runs
// nothing, so a sandbox that forbids /bin/ps does not break it.
//
// The listing (kern.proc.uid) asks only for this user's processes and carries
// each one's command name and start time. kern.procargs2, which returns a
// process's executable path together with its argv and its whole environment,
// is called only for the candidates e2eCandidateExecutables selects from that
// listing.
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

func e2eProcessExecutable(pid int) (string, error) {
	raw, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil {
		return "", err
	}
	if len(raw) < 4 {
		return "", errors.New("short kern.procargs2 record")
	}
	path, _, found := bytes.Cut(raw[4:], []byte{0})
	if !found || len(path) == 0 {
		return "", errors.New("kern.procargs2 record without an executable path")
	}
	return string(path), nil
}
