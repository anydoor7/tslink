//go:build linux

package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
)

// e2eUserProcessExecutables maps the PID of every process this user owns to
// its executable (/proc/<pid>/exe), the same procfs data the product's own
// process-identity check reads (internal/daemon/daemon_linux.go). Processes of
// other users are skipped by the owner of their /proc entry before anything
// inside it is read, and nothing is executed.
func e2eUserProcessExecutables() (map[int]string, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	uid := uint32(os.Getuid())
	executables := map[int]string{}
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || !entry.IsDir() {
			continue
		}
		dir := filepath.Join("/proc", entry.Name())
		info, err := os.Stat(dir)
		if err != nil {
			continue
		}
		if stat, ok := info.Sys().(*syscall.Stat_t); !ok || stat.Uid != uid {
			continue
		}
		exe, err := os.Readlink(filepath.Join(dir, "exe"))
		if err != nil {
			// Exited since the listing, a kernel thread, or a zombie.
			continue
		}
		executables[pid] = exe
	}
	if len(executables) == 0 {
		return nil, errors.New("/proc listed no readable process of this user, not even this one")
	}
	return executables, nil
}
