//go:build windows

package recipes

import (
	"context"
	"fmt"
	"net"
	"strings"
	"syscall"
	"testing"
	"unsafe"
)

func TestWindowsInventory(t *testing.T) {
	old := windowsTCPTableCallFn
	t.Cleanup(func() { windowsTCPTableCallFn = old })
	windowsTCPTableCallFn = func(b []byte, size *uint32, family uint32) uint32 {
		ip := "127.0.0.1"
		if family == windowsAFInet6 {
			ip = "::1"
		}
		fixture := windowsTableFixture(family, []string{ip}, []uint32{2}, []uint16{8123})
		*size = uint32(len(fixture))
		if len(b) < len(fixture) {
			return 122
		}
		copy(b, fixture)
		return 0
	}
	got, err := listeningTCP(context.Background())
	if err != nil || len(got) != 2 || got[0].Host != "127.0.0.1" || got[1].Host != "::1" {
		t.Fatalf("inventory=%v %v", got, err)
	}
	windowsTCPTableCallFn = func([]byte, *uint32, uint32) uint32 { return 5 }
	if _, err := listeningTCP(context.Background()); err == nil || !strings.Contains(err.Error(), "Windows error 5") {
		t.Fatalf("failure cause: %v", err)
	}
}

// Exercise the adapter beneath windowsTCPTableCallFn, rather than replacing
// the adapter and accidentally hiding the table class passed to iphlpapi.
func TestWindowsTCPTableSyscallArguments(t *testing.T) {
	old := windowsTCPTableSyscallFn
	t.Cleanup(func() { windowsTCPTableSyscallFn = old })
	for _, family := range []uint32{windowsAFInet, windowsAFInet6} {
		for _, buffered := range []bool{false, true} {
			t.Run(fmt.Sprintf("family=%d/buffer=%t", family, buffered), func(t *testing.T) {
				var table []byte
				var address uintptr
				if buffered {
					table = make([]byte, 128)
					address = uintptr(unsafe.Pointer(&table[0]))
				}
				size := uint32(len(table))
				calls := 0
				windowsTCPTableSyscallFn = func(args ...uintptr) (uintptr, uintptr, error) {
					calls++
					want := []uintptr{address, uintptr(unsafe.Pointer(&size)), 0, uintptr(family), 3, 0}
					if len(args) != len(want) {
						t.Fatalf("syscall arguments: %v", args)
					}
					for i := range want {
						if args[i] != want[i] {
							t.Errorf("syscall argument %d=%d want %d", i, args[i], want[i])
						}
					}
					// The API status is authoritative, not GetLastError.
					return 122, 0, syscall.Errno(5)
				}
				if status := windowsTCPTableCallFn(table, &size, family); status != 122 || calls != 1 {
					t.Fatalf("status=%d calls=%d", status, calls)
				}
			})
		}
	}
}

func TestWindowsNativeListenerDiscovery(t *testing.T) {
	var want []Listener
	for _, address := range []struct{ network, host string }{{"tcp4", "127.0.0.1"}, {"tcp6", "::1"}} {
		ln, err := net.Listen(address.network, net.JoinHostPort(address.host, "0"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { ln.Close() })
		want = append(want, Listener{Host: address.host, Port: ln.Addr().(*net.TCPAddr).Port})
	}
	// No seam is replaced: both listeners must be found through the real API.
	got, err := listeningTCP(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, listener := range want {
		found := false
		for _, candidate := range got {
			if candidate == listener {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("real API did not find %+v in %v", listener, got)
		} else {
			t.Logf("real iphlpapi found %+v", listener)
		}
	}
}
