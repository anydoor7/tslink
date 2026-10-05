//go:build !windows

package server

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestGuestSpecialRegistryFailsClosed(t *testing.T) {
	for _, kind := range []string{"fifo", "symlink", "directory"} {
		t.Run(kind, func(t *testing.T) {
			f := newGuestFixture(t, "", false, true)
			// Own both the fault and restore windows. An OS restore event may
			// otherwise flush pending login counters before the recovery read.
			defer f.holdCounterFlush()()
			cookies := f.login()
			raw, err := os.ReadFile(f.path)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(f.path); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "fifo":
				err = syscall.Mkfifo(f.path, 0600)
			case "symlink":
				// Valid target bytes distinguish refusing symlinks from a
				// missing-target error or an invalid-registry refusal.
				target := filepath.Join(t.TempDir(), "valid-registry.json")
				if err := os.WriteFile(target, raw, 0600); err != nil {
					t.Fatal(err)
				}
				err = os.Symlink(target, f.path)
			case "directory":
				err = os.Mkdir(f.path, 0700)
			}
			if err != nil {
				t.Fatal(err)
			}
			// A gate that opened the FIFO would block without a writer, so a
			// refusal that is not immediate fails as a hang, not by elapsed time.
			r, _ := f.request("GET", "/", "", cookies)
			if r.StatusCode != 503 || r.Header.Get("Retry-After") != "1" || f.hits.Load() != 0 {
				t.Fatalf("special file refusal: status=%d hits=%d", r.StatusCode, f.hits.Load())
			}
			if err := os.Remove(f.path); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(f.path, raw, 0600); err != nil {
				t.Fatal(err)
			}
			// Temporary unavailability must preserve the original session.
			r, _ = f.request("GET", "/", "", cookies)
			if r.StatusCode != 204 || f.hits.Load() != 1 {
				t.Fatalf("special-file recovery: status=%d hits=%d", r.StatusCode, f.hits.Load())
			}
		})
	}
}
