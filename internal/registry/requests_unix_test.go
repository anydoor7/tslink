//go:build !windows

package registry

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/duration"
)

func TestAccessRequestSpecialFileAndWriterFailure(t *testing.T) {
	for _, kind := range []string{"fifo", "directory", "symlink", "missing", "partial", "oversize"} {
		t.Run(kind, func(t *testing.T) {
			path := requestStore(t)
			backup := path + ".backup"
			if err := os.Rename(path, backup); err != nil {
				t.Fatal(err)
			}
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
				if err := os.Symlink(backup, path); err != nil {
					t.Fatal(err)
				}
			case "partial":
				if err := os.WriteFile(path, []byte(`{"schema_version":2,`), 0600); err != nil {
					t.Fatal(err)
				}
			case "oversize":
				if err := os.WriteFile(path, make([]byte, 4<<20+1), 0600); err != nil {
					t.Fatal(err)
				}
			}
			start := time.Now()
			if _, err := SubmitAccessRequest(path, "alice", "photos", "", "", requestTestNow); err == nil {
				t.Fatal("special registry accepted", kind)
			}
			if _, _, err := DecideAccessRequest(path, "id", RequestApproved, "8h", "", false, duration.Policy{}, requestTestNow); err == nil {
				t.Fatal("decision accepted", kind)
			}
			if time.Since(start) > time.Second {
				t.Fatal("new I/O stalled", kind)
			}
			if kind != "missing" {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Rename(backup, path); err != nil {
				t.Fatal(err)
			}
			if _, err := SubmitAccessRequest(path, "alice", "photos", "", "", requestTestNow); err != nil {
				t.Fatal("restored control failed", err)
			}
		})
	}
	path := requestStore(t)
	// A writer in another process cannot hold an HTTP submit indefinitely.
	lock, err := os.OpenFile(path+".lock", os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, err = SubmitAccessRequest(path, "alice", "photos", "", "", requestTestNow)
	requestCode(t, err, "access_request_busy")
	if time.Since(start) > time.Second {
		t.Fatal("busy writer stalled submit")
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	if _, err := SubmitAccessRequest(path, "alice", "photos", "", "", requestTestNow); err != nil {
		t.Fatal(err)
	}
	if _, err := ListAccessRequests(filepath.Join(t.TempDir(), "missing"), requestTestNow); err != nil {
		t.Fatal(err)
	}
}
