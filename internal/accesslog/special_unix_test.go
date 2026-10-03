//go:build !windows

package accesslog

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestSpecialFileAndSymlinkRefusal(t *testing.T) {
	for _, kind := range []string{"fifo", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			logdir := filepath.Join(dir, "access-log")
			os.Mkdir(logdir, 0700)
			path := filepath.Join(logdir, "2030-07-10-000000.jsonl")
			if kind == "fifo" {
				if err := syscall.Mkfifo(path, 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				target := filepath.Join(t.TempDir(), "private")
				os.WriteFile(target, []byte("control"), 0600)
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					b, _ := os.ReadFile(target)
					if string(b) != "control" {
						t.Error("symlink target modified")
					}
				})
			}
			start := time.Now()
			s := newTestStore(t, dir, Options{}, func() time.Time { return testTime })
			s.Record(event("app", "alice", "/ok"))
			closeStore(t, s)
			if time.Since(start) > time.Second {
				t.Fatal("special-file I/O blocked")
			}
			if s.Health().Drops != 1 || s.Health().Error == "" {
				t.Fatalf("special file health %+v", s.Health())
			}
			if _, e := Query(dir, Filter{}); e == nil {
				t.Fatal("special file query admitted")
			}
		})
	}
	dir := t.TempDir()
	target := t.TempDir()
	os.Symlink(target, filepath.Join(dir, "access-log"))
	if _, e := New(dir, Options{}, nil); e == nil {
		t.Fatal("symlink directory writer admitted")
	}
	if _, e := Query(dir, Filter{}); e == nil {
		t.Fatal("symlink directory query admitted")
	}
	dir = t.TempDir()
	os.Mkdir(filepath.Join(dir, "access-log"), 0700)
	os.Mkdir(filepath.Join(dir, "access-log", "writer.lock"), 0700)
	if _, e := New(dir, Options{}, nil); e == nil {
		t.Fatal("special lock admitted")
	}
}
