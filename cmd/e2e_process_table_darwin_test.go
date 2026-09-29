//go:build darwin

package cmd

import (
	"bytes"
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// e2eUserProcessExecutables maps the PID of every process this user owns to
// the executable path it was started with. It reads the same sysctl data the
// product's own process-identity check does (internal/daemon/daemon_darwin.go),
// asks only for this user's processes (kern.proc.uid), and runs nothing: no
// argv of another user's process is read, and a sandbox that forbids /bin/ps
// does not break it.
func e2eUserProcessExecutables() (map[int]string, error) {
	procs, err := unix.SysctlKinfoProcSlice("kern.proc.uid", os.Getuid())
	if err != nil {
		return nil, err
	}
	executables := make(map[int]string, len(procs))
	for _, proc := range procs {
		pid := int(proc.Proc.P_pid)
		raw, err := unix.SysctlRaw("kern.procargs2", pid)
		if err != nil || len(raw) < 4 {
			// Exited since the listing, a zombie, or not exec'd yet.
			continue
		}
		path, _, found := bytes.Cut(raw[4:], []byte{0})
		if !found || len(path) == 0 {
			continue
		}
		executables[pid] = string(path)
	}
	if len(executables) == 0 {
		return nil, errors.New("kern.proc.uid listed no readable process of this user, not even this one")
	}
	return executables, nil
}
