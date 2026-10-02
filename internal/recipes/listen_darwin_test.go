//go:build darwin

package recipes

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"
)

func TestDarwinInventory(t *testing.T) {
	old := lsofOutputFn
	t.Cleanup(func() { lsofOutputFn = old })
	lsofOutputFn = func(context.Context) ([]byte, error) { return []byte("p42\nn127.0.0.1:8123\n"), nil }
	got, err := listeningTCP(context.Background())
	if err != nil || len(got) != 1 || got[0].Port != 8123 {
		t.Fatalf("inventory=%v %v", got, err)
	}
	_, emptyErr := exec.Command("/bin/sh", "-c", "exit 1").Output()
	lsofOutputFn = func(context.Context) ([]byte, error) { return nil, emptyErr }
	got, err = listeningTCP(context.Background())
	if err != nil || len(got) != 0 {
		t.Fatalf("empty inventory: %v %v", got, err)
	}
	lsofOutputFn = func(context.Context) ([]byte, error) { return []byte("partial"), emptyErr }
	if _, err := listeningTCP(context.Background()); err == nil {
		t.Fatal("partial failed inventory accepted")
	}
	lsofOutputFn = func(context.Context) ([]byte, error) { return nil, fmt.Errorf("fixture command failed") }
	if _, err := listeningTCP(context.Background()); err == nil || !strings.Contains(err.Error(), "lsof") {
		t.Fatalf("failure cause: %v", err)
	}
}
