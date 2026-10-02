//go:build windows

package recipes

import (
	"context"
	"unsafe"

	"golang.org/x/sys/windows"
)

var getExtendedTCPTable = windows.NewLazySystemDLL("iphlpapi.dll").NewProc("GetExtendedTcpTable")

var windowsTCPTableCallFn windowsTCPTableCall = func(table []byte, size *uint32, family uint32) uint32 {
	var address unsafe.Pointer
	if len(table) != 0 {
		address = unsafe.Pointer(&table[0])
	}
	// TCP_TABLE_OWNER_PID_LISTENER (2) supports both AF_INET and AF_INET6.
	status, _, _ := getExtendedTCPTable.Call(uintptr(address), uintptr(unsafe.Pointer(size)), 0, uintptr(family), 2, 0)
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
