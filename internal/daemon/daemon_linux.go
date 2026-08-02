//go:build linux

package daemon

import (
	"bytes"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const linuxClockTicksPerSecond = 100

// Linux process inspection uses procfs so minimal images do not need ps.
func defaultProcessExecutable(pid int) (string, error) {
	return os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
}

func defaultProcessStartTime(pid int) (time.Time, error) {
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return time.Time{}, err
	}
	closeParen := bytes.LastIndexByte(stat, ')')
	if closeParen < 0 || closeParen+2 > len(stat) {
		return time.Time{}, fmt.Errorf("malformed /proc/%d/stat", pid)
	}
	fields := strings.Fields(string(stat[closeParen+2:]))
	// fields starts at proc field 3 (state), so index 19 is field 22
	// (starttime, in USER_HZ ticks since boot).
	if len(fields) <= 19 {
		return time.Time{}, fmt.Errorf("/proc/%d/stat has %d fields after comm", pid, len(fields))
	}
	startTicks, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse /proc/%d/stat starttime: %w", pid, err)
	}

	procStat, err := os.ReadFile("/proc/stat")
	if err != nil {
		return time.Time{}, err
	}
	var bootSeconds int64
	for _, line := range strings.Split(string(procStat), "\n") {
		if strings.HasPrefix(line, "btime ") {
			bootSeconds, err = strconv.ParseInt(strings.TrimSpace(strings.TrimPrefix(line, "btime ")), 10, 64)
			if err != nil {
				return time.Time{}, fmt.Errorf("parse /proc/stat btime: %w", err)
			}
			break
		}
	}
	if bootSeconds == 0 {
		return time.Time{}, fmt.Errorf("/proc/stat has no btime")
	}
	return time.Unix(bootSeconds, 0).Add(time.Duration(startTicks) * time.Second / linuxClockTicksPerSecond), nil
}

func defaultProcessArguments(pid int) ([]string, error) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil {
		return nil, err
	}
	parts := bytes.Split(data, []byte{0})
	if len(parts) > 0 && len(parts[len(parts)-1]) == 0 {
		parts = parts[:len(parts)-1]
	}
	if len(parts) == 0 {
		return nil, fmt.Errorf("empty /proc/%d/cmdline", pid)
	}
	args := make([]string, len(parts))
	for i, part := range parts {
		args[i] = string(part)
	}
	return args, nil
}
