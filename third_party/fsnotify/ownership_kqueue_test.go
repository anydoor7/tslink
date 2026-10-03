//go:build freebsd || openbsd || netbsd || dragonfly || darwin

package fsnotify

import (
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func ownershipWait(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("watcher operation did not complete")
	}
}

// This retains the causal real-HTTP regression: pause the reader immediately
// before its watch close, overlap Close, allocate unrelated listeners, and then
// resume the reader. A raw double-close destroys an otherwise healthy backend.
func TestDescriptorOwnership(t *testing.T) {
	for _, overlap := range []bool{false, true} {
		name := "joined-remove-control"
		if overlap {
			name = "close-overlaps-remove"
		}
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "watched")
			if err := os.WriteFile(path, []byte("watched"), 0600); err != nil {
				t.Fatal(err)
			}
			w, err := NewWatcher()
			if err != nil {
				t.Fatal(err)
			}
			defer w.Close()
			kq := w.b.(*kqueue)
			reached := make(chan int, 1)
			release := make(chan struct{})
			var once sync.Once
			resume := func() { once.Do(func() { close(release) }) }
			defer resume()
			kq.beforeRemoveClose = func(fd int) { reached <- fd; <-release }
			if err := w.Add(path); err != nil {
				t.Fatal(err)
			}
			eventsDone, errorsDone := make(chan struct{}), make(chan struct{})
			go func() {
				for range w.Events {
				}
				close(eventsDone)
			}()
			go func() {
				for range w.Errors {
				}
				close(errorsDone)
			}()
			if err := os.Rename(path, path+"-renamed"); err != nil {
				t.Fatal(err)
			}
			var watchedFD int
			select {
			case watchedFD = <-reached:
			case <-time.After(3 * time.Second):
				t.Fatal("reader removal barrier not reached")
			}
			if !overlap {
				resume()
				deadline := time.After(3 * time.Second)
				for len(w.WatchList()) != 0 {
					select {
					case <-deadline:
						t.Fatal("control removal did not finish")
					default:
						time.Sleep(time.Millisecond)
					}
				}
			}
			closed := make(chan struct{})
			go func() { _ = w.Close(); close(closed) }()
			ownershipWait(t, kq.done)
			// Old Close returns while the reader is paused. A joined Close must
			// remain blocked; the timeout bounds the observation, not guest access.
			select {
			case <-closed:
			case <-time.After(50 * time.Millisecond):
			}
			var backends []*httptest.Server
			for i := 0; i < 16; i++ {
				b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusNoContent)
				}))
				defer b.Close()
				backends = append(backends, b)
				raw, err := b.Listener.(syscall.Conn).SyscallConn()
				if err != nil {
					t.Fatal(err)
				}
				if err := raw.Control(func(fd uintptr) {
					if int(fd) == watchedFD {
						t.Logf("backend %s reused watch fd %d", b.URL, fd)
					}
				}); err != nil {
					t.Fatal(err)
				}
			}
			resume()
			ownershipWait(t, closed)
			ownershipWait(t, eventsDone)
			ownershipWait(t, errorsDone)
			client := &http.Client{Transport: &http.Transport{DialContext: (&net.Dialer{Timeout: time.Second}).DialContext}, Timeout: time.Second}
			defer client.CloseIdleConnections()
			for _, b := range backends {
				resp, err := client.Get(b.URL)
				if err != nil {
					t.Fatalf("backend listener lost after watcher Close: %v", err)
				}
				_, _ = io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
				if resp.StatusCode != http.StatusNoContent {
					t.Fatalf("backend status=%d", resp.StatusCode)
				}
			}
		})
	}
}

func TestOwnershipCloseJoinsEveryCaller(t *testing.T) {
	w, err := NewWatcher()
	if err != nil {
		t.Fatal(err)
	}
	kq := w.b.(*kqueue)
	// The reader cannot dispose its descriptors until this operation releases
	// ownership. Close must cancel sends without taking this lock itself.
	kq.opMu.Lock()
	results := make(chan error, 8)
	for i := 0; i < cap(results); i++ {
		go func() { results <- w.Close() }()
	}
	ownershipWait(t, kq.done)
	select {
	case err := <-results:
		kq.opMu.Unlock()
		t.Fatalf("Close returned before descriptor cleanup: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	kq.opMu.Unlock()
	for i := 0; i < cap(results); i++ {
		select {
		case err := <-results:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("concurrent Close did not join")
		}
	}
	if _, ok := <-w.Events; ok {
		t.Fatal("Events open after Close")
	}
	if _, ok := <-w.Errors; ok {
		t.Fatal("Errors open after Close")
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestOwnershipRetainedDescriptors(t *testing.T) {
	for cycle := 0; cycle < 20; cycle++ {
		dir := t.TempDir()
		path := filepath.Join(dir, "file")
		if err := os.WriteFile(path, []byte("watched"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("missing", filepath.Join(dir, "dangling")); err != nil {
			t.Fatal(err)
		}
		w, err := NewWatcher()
		if err != nil {
			t.Fatal(err)
		}
		if err := w.Add(dir); err != nil {
			t.Fatal(err)
		}
		if err := w.Add(path); err != nil {
			t.Fatal(err)
		}
		kq := w.b.(*kqueue)
		kq.watches.mu.RLock()
		fds := []int{kq.kq, kq.closepipe[0], kq.closepipe[1]}
		for fd := range kq.watches.wd {
			fds = append(fds, fd)
		}
		kq.watches.mu.RUnlock()
		if len(fds) < 5 {
			t.Fatalf("descriptor control missing: %v", fds)
		}
		for _, fd := range fds {
			if _, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0); err != nil {
				t.Fatalf("fd %d not live before Close: %v", fd, err)
			}
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		for _, fd := range fds {
			if _, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0); !errors.Is(err, unix.EBADF) {
				t.Errorf("retained descriptor %d after Close: %v", fd, err)
			}
		}
		if len(kq.watches.wd)+len(kq.watches.path)+len(kq.watches.byUser)+len(kq.watches.byDir)+len(kq.watches.seen) != 0 {
			t.Fatal("retained watch state after Close")
		}
		if err := w.Add(path); !errors.Is(err, ErrClosed) {
			t.Fatalf("Add after Close: %v", err)
		}
		if err := w.Remove(path); err != nil {
			t.Fatalf("Remove after Close: %v", err)
		}
		if got := w.WatchList(); len(got) != 0 {
			t.Fatalf("WatchList after Close: %v", got)
		}
	}
}

func TestOwnershipConcurrentPublicOperations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(path, []byte("watched"), 0600); err != nil {
		t.Fatal(err)
	}
	for cycle := 0; cycle < 20; cycle++ {
		w, err := NewWatcher()
		if err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		start := make(chan struct{})
		active := make(chan struct{}, 8)
		for n := 0; n < 8; n++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				for i := 0; i < 20; i++ {
					if err := w.Add(path); err != nil && !errors.Is(err, ErrClosed) {
						t.Errorf("concurrent Add: %v", err)
						return
					}
					_ = w.WatchList()
					if err := w.Remove(path); err != nil && !errors.Is(err, ErrNonExistentWatch) {
						t.Errorf("concurrent Remove: %v", err)
						return
					}
					if i == 9 {
						active <- struct{}{}
					}
				}
			}()
		}
		close(start)
		// Prove that all workers exercise live Add/Remove before racing Close.
		for n := 0; n < cap(active); n++ {
			select {
			case <-active:
			case <-time.After(3 * time.Second):
				t.Fatal("live public operations stalled")
			}
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		wg.Wait()
		kq := w.b.(*kqueue)
		if len(kq.watches.wd)+len(kq.watches.path)+len(kq.watches.byUser) != 0 {
			t.Fatal("concurrent operation escaped Close")
		}
	}
}

func TestOwnershipRegistrationFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	fd, err := unix.Open(path, openMode, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(fd)
	// A real open watch fd with a failed registration syscall. No reader is
	// started: the invalid queue is deliberately an error-path fixture.
	w := &kqueue{shared: newShared(nil, nil), kq: -1, watches: newWatches()}
	w.watches.add(path, "", fd, false)
	if _, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0); err != nil {
		t.Fatal(err)
	}
	if err := w.Add(path); !errors.Is(err, unix.EBADF) {
		t.Fatalf("registration failure: %v", err)
	}
	if _, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0); err != nil {
		t.Fatalf("failed re-registration released an owned descriptor: %v", err)
	}
}
