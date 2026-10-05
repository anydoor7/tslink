//go:build !windows

package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/anydoor7/tslink/internal/testwait"
)

func TestPeopleQRSpecialFilesRefused(t *testing.T) {
	for _, kind := range []string{"fifo", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			file := filepath.Join(dir, "qr.png")
			target := filepath.Join(dir, "existing.png")
			if err := os.WriteFile(target, []byte("keep"), 0600); err != nil {
				t.Fatal(err)
			}
			if kind == "fifo" {
				if err := syscall.Mkfifo(file, 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Symlink(target, file); err != nil {
					t.Fatal(err)
				}
			}
			// A FIFO with no peer blocks an open forever: returning is the property.
			wrote := make(chan error, 1)
			go func() { wrote <- qrPNG("https://home.tailnet.ts.net", file) }()
			if err := testwait.Recv(t, wrote, "QR write returned instead of stalling on the destination"); err == nil {
				t.Fatal("unsafe destination accepted")
			}
			data, err := os.ReadFile(target)
			if err != nil || !bytes.Equal(data, []byte("keep")) {
				t.Fatal(data, err)
			}
		})
	}
}
