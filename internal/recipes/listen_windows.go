//go:build windows

package recipes

import (
	"context"
	"unsafe"

	"golang.org/x/sys/windows"
)

var getExtendedTCPTable = windows.NewLazySystemDLL("iphlpapi.dll").NewProc("GetExtendedTcpTable")

// TCP_TABLE_CLASS is zero-based; the listening TCP state is a different enum.
const TCP_TABLE_OWNER_PID_LISTENER = 3

var windowsTCPTableSyscallFn = getExtendedTCPTable.Call

// Keep pointer arguments alive and on the heap across the indirect syscall
// seam, including the first call's lazy DLL loading and possible stack growth.
//
//go:uintptrescapes
func callWindowsTCPTable(args ...uintptr) (uintptr, uintptr, error) {
	return windowsTCPTableSyscallFn(args...)
}

var windowsTCPTableCallFn windowsTCPTableCall = func(table []byte, size *uint32, family uint32) uint32 {
	var address unsafe.Pointer
	if len(table) != 0 {
		address = unsafe.Pointer(&table[0])
	}
	// OWNER_PID tables support both AF_INET and AF_INET6 and match our decoder.
	status, _, _ := callWindowsTCPTable(uintptr(address), uintptr(unsafe.Pointer(size)), 0, uintptr(family), TCP_TABLE_OWNER_PID_LISTENER, 0)
	return uint32(status)
}

func listeningTCP(ctx context.Context) ([]Listener, error) {
	var listeners []Listener
	for _, family := range []uint32{windowsAFInet, windowsAFInet6} {
		table, err := readWindowsTCPTable(ctx, family, windowsTCPTableCallFn)
		if err != nil {
			return nil, err
		}
		rows, err := decodeWindowsTCPTable(table, family)
		if err != nil {
			return nil, err
		}
		listeners = append(listeners, rows...)
	}
	return listeners, nil
}
