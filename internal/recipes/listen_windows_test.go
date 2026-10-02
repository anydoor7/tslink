//go:build windows

package recipes

import (
	"context"
	"strings"
	"testing"
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
