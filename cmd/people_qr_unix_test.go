//go:build !windows

package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
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
			start := time.Now()
			if err := qrPNG("https://home.tailnet.ts.net", file); err == nil {
				t.Fatal("unsafe destination accepted")
			}
			if time.Since(start) > time.Second {
				t.Fatal("QR write stalled")
			}
			data, err := os.ReadFile(target)
			if err != nil || !bytes.Equal(data, []byte("keep")) {
				t.Fatal(data, err)
			}
		})
	}
}
