//go:build windows

package daemon

import (
	"golang.org/x/sys/windows"
	"strings"
	"unsafe"
)

// EnginePID identifies the task engine, not necessarily the action PID. Follow
// real process ancestry instead of claiming that a running task owns any tslink.
func IsTaskOwnedProcess(pid int, engines []int, executable string) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	buf := make([]uint16, 32768)
	n := uint32(len(buf))
	err = windows.QueryFullProcessImageName(h, 0, &buf[0], &n)
	windows.CloseHandle(h)
	if err != nil || !strings.EqualFold(windows.UTF16ToString(buf[:n]), executable) {
		return false
	}
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(snapshot)
	parents := map[int]int{}
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	for err = windows.Process32First(snapshot, &entry); err == nil; err = windows.Process32Next(snapshot, &entry) {
		parents[int(entry.ProcessID)] = int(entry.ParentProcessID)
	}
	for depth := 0; depth < 8 && pid > 0; depth++ {
		for _, engine := range engines {
			if pid == engine {
				return true
			}
		}
		pid = parents[pid]
	}
	return false
}
