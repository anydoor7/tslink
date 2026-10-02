//go:build windows

package recipes

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestWindowsInventory(t *testing.T) {
	old := netstatOutputFn
	t.Cleanup(func() { netstatOutputFn = old })
	netstatOutputFn = func(context.Context) ([]byte, error) {
		return []byte("TCP 127.0.0.1:8123 0.0.0.0:0 LISTENING 42\r\n"), nil
	}
	got, err := listeningTCP(context.Background())
	if err != nil || len(got) != 1 || got[0].Port != 8123 {
		t.Fatalf("inventory=%v %v", got, err)
	}
	netstatOutputFn = func(context.Context) ([]byte, error) { return nil, fmt.Errorf("fixture command failed") }
	if _, err := listeningTCP(context.Background()); err == nil || !strings.Contains(err.Error(), "netstat") {
		t.Fatalf("failure cause: %v", err)
	}
}
