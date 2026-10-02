//go:build darwin || linux

package health

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestNotifierConfigBoundedFiles(t *testing.T) {
	dir := t.TempDir()
	control := filepath.Join(dir, "valid.json")
	if err := os.WriteFile(control, []byte(`{"command":["/fake/notifier"]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if c, err := LoadNotifier(control); err != nil || c.Kind() != "command" {
		t.Fatal("positive control", c, err)
	}
	for _, kind := range []string{"fifo", "directory", "symlink", "oversize", "partial"} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(dir, kind)
			var err error
			switch kind {
			case "fifo":
				err = syscall.Mkfifo(path, 0600)
			case "directory":
				err = os.Mkdir(path, 0700)
			case "symlink":
				err = os.Symlink(control, path)
			case "oversize":
				err = os.WriteFile(path, []byte(strings.Repeat(" ", (64<<10)+1)), 0600)
			case "partial":
				err = os.WriteFile(path, []byte(`{"command":[`), 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { _, err := LoadNotifier(path); done <- err }()
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("unsafe notifier configuration accepted")
				}
			case <-time.After(time.Second):
				// Rescue a regressed FIFO read, producing an assertion failure
				// instead of a worker that survives until the package timeout.
				if kind == "fifo" {
					fd, err := syscall.Open(path, syscall.O_WRONLY|syscall.O_NONBLOCK, 0)
					if err == nil {
						_ = syscall.Close(fd)
					}
				}
				t.Fatal("notifier configuration read stalled")
			}
		})
	}
	if c, err := LoadNotifier(filepath.Join(dir, "missing")); err != nil || c.Kind() != "none" {
		t.Fatal("missing file should disable notifications", c, err)
	}
}
