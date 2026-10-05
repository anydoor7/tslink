//go:build !windows

package registry

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/anydoor7/tslink/internal/testwait"
)

func TestPortalSpecialFilesDoNotStall(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "registry.json")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	// A FIFO with no peer blocks an open forever: returning is the property.
	preflight := make(chan error, 1)
	go func() { _, _, err := PortalPreflight(fifo); preflight <- err }()
	if err := testwait.Recv(t, preflight, "portal preflight returned instead of stalling on a FIFO"); err == nil {
		t.Fatal("FIFO admitted")
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
