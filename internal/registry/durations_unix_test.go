//go:build !windows

package registry

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/testwait"
)

func TestDurationSpecialFilesRefusedThroughRuntime(t *testing.T) {
	for _, kind := range []string{"fifo", "directory", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv(config.ConfigDirEnv, dir)
			path := filepath.Join(dir, "config.json")
			switch kind {
			case "fifo":
				if err := syscall.Mkfifo(path, 0600); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				target := filepath.Join(dir, "target")
				if err := os.WriteFile(target, []byte(`{}`), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			}
			// A FIFO with no peer blocks an open forever: returning is the property.
			loaded := make(chan error, 1)
			go func() { _, err := config.LoadLifetimePolicy(); loaded <- err }()
			if err := testwait.Recv(t, loaded, "special config load returned instead of stalling"); err == nil || !strings.Contains(err.Error(), "unsafe state file") {
				t.Fatalf("config %s: %v", kind, err)
			}
			if err := os.Rename(path, filepath.Join(dir, "registry.json")); err != nil {
				t.Fatal(err)
			}
			if _, err := ExtendDuration(filepath.Join(dir, "registry.json"), ExtendOptions{Service: "public", Value: "1h", Now: time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)}); err == nil || !strings.Contains(err.Error(), "unsafe state file") {
				t.Fatalf("registry %s: %v", kind, err)
			}
		})
	}
}
