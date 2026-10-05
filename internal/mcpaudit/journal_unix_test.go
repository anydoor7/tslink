//go:build !windows

package mcpaudit

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/filelock"
	"github.com/anydoor7/tslink/internal/testwait"
)

func TestJournalSpecialFilesAndBoundedLock(t *testing.T) {
	for _, kind := range []string{"fifo", "symlink", "lock-fifo"} {
		t.Run(kind, func(t *testing.T) {
			j := Journal{Path: filepath.Join(t.TempDir(), "audit.json")}
			path := j.Path
			if kind == "lock-fifo" {
				path += ".lock"
			}
			if kind == "symlink" {
				target := filepath.Join(t.TempDir(), "target")
				if err := os.WriteFile(target, []byte("[]"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			} else if err := syscall.Mkfifo(path, 0600); err != nil {
				t.Fatal(err)
			}
			// Opening a FIFO with no peer blocks forever, so returning at all is
			// the property; how fast the refusal happens is not.
			recorded := make(chan error, 1)
			go func() { recorded <- j.Record(context.Background(), auditFixture()) }()
			if err := testwait.Recv(t, recorded, "Record returned instead of blocking on the special file"); err == nil {
				t.Fatal("special file accepted")
			}
			if kind != "lock-fifo" {
				if _, err := j.Read(); err == nil {
					t.Fatal("special file read")
				}
			}
		})
	}
	j := Journal{Path: filepath.Join(t.TempDir(), "audit.json")}
	f, err := os.OpenFile(j.Path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := filelock.Lock(f); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	if err := j.Record(ctx, auditFixture()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if err := filelock.Unlock(f); err != nil {
		t.Fatal(err)
	}
	if err := j.Record(context.Background(), auditFixture()); err != nil {
		t.Fatal("unmutated lock control", err)
	}
}

func TestJournalUnreadableFilePreserved(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses file permissions")
	}
	j := Journal{Path: filepath.Join(t.TempDir(), "audit.json")}
	if err := os.WriteFile(j.Path, []byte("[]"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(j.Path, 0000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(j.Path, 0600) })
	if _, err := j.Read(); err == nil {
		t.Fatal("unreadable journal accepted")
	}
	if err := j.Record(context.Background(), auditFixture()); err == nil {
		t.Fatal("unreadable journal replaced")
	}
	if err := os.Chmod(j.Path, 0600); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(j.Path)
	if err != nil || string(data) != "[]" {
		t.Fatal(string(data), err)
	}
	if err := j.Record(context.Background(), auditFixture()); err != nil {
		t.Fatal("readable control failed", err)
	}
}
