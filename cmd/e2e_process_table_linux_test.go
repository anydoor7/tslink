//go:build linux

package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// e2eHostProcessTable reads the same procfs data the product's own
// process-identity check does (internal/daemon/daemon_linux.go) and runs
// nothing.
//
// The listing skips other users' processes by the owner of their /proc entry
// before anything inside it is read, and takes the command name and start time
// of this user's processes from /proc/<pid>/stat. The exe link is read only for
// the candidates e2eCandidateExecutables selects from that listing.
var e2eHostProcessTable = e2eProcessTable{
	list:       e2eListUserProcesses,
	executable: e2eProcessExecutable,
	// comm keeps the first TASK_COMM_LEN-1 (15) bytes of the executable's name.
	commandMax: 15,
}

func e2eListUserProcesses() ([]e2eProcess, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	uid := uint32(os.Getuid())
	var listing []e2eProcess
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
		raw, err := os.ReadFile(filepath.Join(dir, "stat"))
		if err != nil {
			// Exited since the listing.
			continue
		}
		command, start, ok := e2eParseProcStat(raw)
		if !ok {
			continue
		}
		listing = append(listing, e2eProcess{PID: pid, Command: command, Start: start})
	}
	return listing, nil
}

// e2eParseProcStat returns the command name (field 2) and the start time
// (field 22, clock ticks after boot, taken when the process was forked) of a
// /proc/<pid>/stat record. The command name is parenthesised and may itself
// contain spaces and parentheses, so the fields after it are counted from the
// last ')'.
func e2eParseProcStat(raw []byte) (string, int64, bool) {
	open := bytes.IndexByte(raw, '(')
	end := bytes.LastIndexByte(raw, ')')
	if open < 0 || end < open {
		return "", 0, false
	}
	// rest[0] is field 3 (state), so field 22 is rest[19].
	rest := strings.Fields(string(raw[end+1:]))
	if len(rest) < 20 {
		return "", 0, false
	}
	start, err := strconv.ParseInt(rest[19], 10, 64)
	if err != nil {
		return "", 0, false
	}
	return string(raw[open+1 : end]), start, true
}

func e2eProcessExecutable(pid int) (string, error) {
	return os.Readlink(filepath.Join("/proc", strconv.Itoa(pid), "exe"))
}
