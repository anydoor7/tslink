package recipes

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"strconv"
)

const (
	windowsAFInet             = 2
	windowsAFInet6            = 23
	windowsTCPListen          = 2
	windowsInsufficientBuffer = 122
)

// decodeWindowsTCPTable reads MIB_TCPTABLE_OWNER_PID / MIB_TCP6TABLE_OWNER_PID.
// DWORD fields are little endian; address bytes and the first two port bytes
// are network order. Both layouts have four-byte alignment, including the
// entry count. Keep this independent of syscalls so fixtures run on every OS.
func decodeWindowsTCPTable(table []byte, family uint32) ([]Listener, error) {
	rowSize, stateOffset, addrOffset, addrSize, portOffset := 24, 0, 4, 4, 8
	switch family {
	case windowsAFInet:
	case windowsAFInet6:
		rowSize, stateOffset, addrOffset, addrSize, portOffset = 56, 48, 0, 16, 20
	default:
		return nil, fmt.Errorf("unsupported TCP address family %d", family)
	}
	if len(table) < 4 {
		return nil, fmt.Errorf("truncated TCP table entry count")
	}
	count := uint64(binary.LittleEndian.Uint32(table[:4]))
	if count > uint64((len(table)-4)/rowSize) {
		return nil, fmt.Errorf("truncated TCP table rows: %d entries", count)
	}
	var result []Listener
	for i := 0; i < int(count); i++ {
		row := table[4+i*rowSize : 4+(i+1)*rowSize]
		if binary.LittleEndian.Uint32(row[stateOffset:stateOffset+4]) != windowsTCPListen {
			continue
		}
		address := net.IP(row[addrOffset : addrOffset+addrSize]).String()
		port := binary.BigEndian.Uint16(row[portOffset : portOffset+2])
		result = append(result, loopbackListeners(net.JoinHostPort(address, strconv.Itoa(int(port))))...)
	}
	return result, nil
}

type windowsTCPTableCall func([]byte, *uint32, uint32) uint32

// readWindowsTCPTable bounds retries if listeners change between sizing and
// reading. The API returns its error code directly, rather than GetLastError.
func readWindowsTCPTable(ctx context.Context, family uint32, call windowsTCPTableCall) ([]byte, error) {
	var table []byte
	for attempt := 0; attempt < 4; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		size := uint32(len(table))
		status := call(table, &size, family)
		if status == 0 {
			if size > uint32(len(table)) {
				return nil, fmt.Errorf("TCP table size exceeds buffer")
			}
			return table[:size], nil
		}
		if status != windowsInsufficientBuffer {
			return nil, fmt.Errorf("GetExtendedTcpTable family %d: Windows error %d", family, status)
		}
		if size < 4 || size > 16*1024*1024 {
			return nil, fmt.Errorf("invalid TCP table buffer size %d", size)
		}
		table = make([]byte, size)
	}
	return nil, fmt.Errorf("TCP table changed during enumeration; retry discovery")
}
