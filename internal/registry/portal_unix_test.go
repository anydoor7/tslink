//go:build !windows

package registry

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestPortalSpecialFilesDoNotStall(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "registry.json")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if _, _, err := PortalPreflight(fifo); err == nil {
		t.Fatal("FIFO admitted")
	}
	if time.Since(start) > time.Second {
		t.Fatal("FIFO stalled")
	}
	regular := filepath.Join(dir, "regular.json")
	if err := os.WriteFile(regular, []byte(`{"schema_version":2,"services":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.json")
	if err := os.Symlink(regular, link); err != nil {
		t.Fatal(err)
	}
	if _, _, err := PortalPreflight(link); err == nil {
		t.Fatal("symlink registry admitted")
	}
	if _, _, err := PortalPreflight(regular); err != nil {
		t.Fatalf("unmutated regular control: %v", err)
	}
}
