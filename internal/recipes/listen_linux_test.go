//go:build linux

package recipes

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestLinuxInventory(t *testing.T) {
	old := readProcFn
	t.Cleanup(func() { readProcFn = old })
	readProcFn = func(path string) ([]byte, error) {
		if strings.HasSuffix(path, "tcp6") {
			return nil, os.ErrNotExist
		}
		return []byte("0: 0100007F:1FBB 0:0 0A"), nil
	}
	got, err := listeningTCP(context.Background())
	if err != nil || len(got) != 1 || got[0].Port != 8123 {
		t.Fatalf("inventory=%v %v", got, err)
	}
	readProcFn = func(string) ([]byte, error) { return nil, fmt.Errorf("fixture permission denied") }
	if _, err := listeningTCP(context.Background()); err == nil || !strings.Contains(err.Error(), "enumerate TCP") {
		t.Fatalf("failure cause: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := listeningTCP(ctx); err != context.Canceled {
		t.Fatalf("cancellation=%v", err)
	}
}
