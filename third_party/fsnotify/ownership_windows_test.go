//go:build windows

package fsnotify

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// Use the real completion port, but fix the ordering instead of relying on a
// Remove racing with a directory write. The retired OVERLAPPED remains rooted
// until the reader has consumed the packet. The event handle models Windows
// reissuing the retired handle value to an unrelated owner.
func TestWindowsLateCompletionOwnership(t *testing.T) {
	for _, replace := range []bool{false, true} {
		t.Run(fmt.Sprintf("replacement=%t", replace), func(t *testing.T) {
			dir, other := t.TempDir(), t.TempDir()
			b, err := newBackend(make(chan Event, 64), make(chan error, 64))
			if err != nil {
				t.Fatal(err)
			}
			w := b.(*readDirChangesW)
			defer w.Close()
			if err := w.Add(dir); err != nil {
				t.Fatal(err)
			}
			w.mu.Lock()
			var retired *watch
			for _, indexes := range w.watches {
				for _, entry := range indexes {
					retired = entry
				}
			}
			w.mu.Unlock()
			if err := w.Remove(dir); err != nil {
				t.Fatal(err)
			}
			if replace {
				if err := w.Add(dir); err != nil {
					t.Fatal(err)
				}
			}
			if err := w.Add(other); err != nil {
				t.Fatal(err)
			}

			foreign, err := windows.CreateEvent(nil, 1, 0, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				// On the bad implementation the reader already closed this handle.
				if windows.SetEvent(foreign) == nil {
					windows.CloseHandle(foreign)
				}
			}()
			// Copy the inode: the already-drained real cancellation completion
			// belongs to retired; this synthetic packet has no outstanding I/O.
			stale := &watch{ino: &inode{handle: foreign, volume: retired.ino.volume, index: retired.ino.index}}
			if err := windows.PostQueuedCompletionStatus(w.port, 0, 0, &stale.ov); err != nil {
				t.Fatal(err)
			}
			// The single reader processes this input after the queued packet.
			if err := w.Add(other); err != nil {
				t.Fatal(err)
			}
			runtime.KeepAlive(stale)
			runtime.KeepAlive(retired)
			if err := windows.SetEvent(foreign); err != nil {
				t.Errorf("late completion closed another owner's handle: %v", err)
			}
			for len(w.Errors) > 0 {
				t.Errorf("late completion produced error: %v", <-w.Errors)
			}
			want := 1
			if replace {
				want++
			}
			if got := len(w.WatchList()); got != want {
				t.Errorf("late completion changed watch ownership: got %d, want %d", got, want)
			}
			assertWindowsCreate(t, w, other, "unrelated")
			if replace {
				assertWindowsCreate(t, w, dir, "replacement")
			}
		})
	}
}

func assertWindowsCreate(t *testing.T, w *readDirChangesW, dir, name string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("event positive control"), 0600); err != nil {
		t.Fatal(err)
	}
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case event, ok := <-w.Events:
			if !ok {
				t.Fatal("Events closed before CREATE")
			}
			if event.Name == path && event.Has(Create) {
				t.Logf("observed CREATE %s", name)
				return
			}
		case err := <-w.Errors:
			t.Fatalf("active watch error: %v", err)
		case <-timer.C:
			t.Fatalf("missing CREATE for %s", name)
		}
	}
}

func TestWindowsSendErrorCloseToken(t *testing.T) {
	w := &readDirChangesW{Errors: make(chan error), done: make(chan chan<- error, 1)}
	reply := make(chan error)
	w.done <- reply
	if w.sendError(windows.ERROR_INVALID_HANDLE) {
		t.Fatal("sendError reported delivery during Close")
	}
	select {
	case got := <-w.done:
		if got != reply {
			t.Fatal("Close reply channel changed")
		}
	default:
		t.Fatal("sendError consumed Close handshake")
	}
	// Live errors, including INVALID_HANDLE, must still reach the caller.
	w.Errors = make(chan error, 1)
	want := os.NewSyscallError("CancelIo", windows.ERROR_INVALID_HANDLE)
	if !w.sendError(want) {
		t.Fatal("live error delivery failed")
	}
	select {
	case got := <-w.Errors:
		if got != want || !errors.Is(got, windows.ERROR_INVALID_HANDLE) {
			t.Fatalf("error changed: %v", got)
		}
	default:
		t.Fatal("live CancelIo error swallowed")
	}
}

func TestWindowsRepeatedWatchLifecycle(t *testing.T) {
	for round := 0; round < 10; round++ {
		dir, other := t.TempDir(), t.TempDir()
		b, err := newBackend(make(chan Event, 128), make(chan error, 128))
		if err != nil {
			t.Fatal(err)
		}
		w := b.(*readDirChangesW)
		for i := 0; i < 10; i++ {
			if err := w.Add(dir); err != nil {
				t.Fatal(err)
			}
			assertWindowsCreate(t, w, dir, fmt.Sprintf("active-%d", i))
			if err := w.Remove(dir); err != nil {
				t.Fatal(err)
			}
		}
		if err := w.Add(other); err != nil {
			t.Fatal(err)
		}
		assertWindowsCreate(t, w, other, "still-live")
		for i := 0; i < 2; i++ {
			if err := w.Close(); err != nil {
				t.Fatal(err)
			}
		}
		for err := range w.Errors {
			t.Errorf("lifecycle error: %v", err)
		}
		if err := w.Add(dir); !errors.Is(err, ErrClosed) {
			t.Fatalf("Add after Close: %v", err)
		}
	}
}
