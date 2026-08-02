//go:build darwin

package daemon

import (
	"bytes"
	"encoding/binary"
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

func darwinProcessArguments(pid int) (string, []string, error) {
	data, err := unix.SysctlRaw("kern.procargs2", pid)
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
