//go:build linux && !appengine && go1.25

package fsnotify

import (
	"errors"
	"os"
	"testing"
	"testing/synctest"
	"time"
)

func assertInotifyChannelsClosed(t *testing.T, w *inotify) {
	t.Helper()
	select {
	case _, ok := <-w.Events:
		if ok {
			t.Fatal("Events still delivers after Close")
		}
	default:
		t.Fatal("Events not closed when Close returned")
	}
	select {
	case err, ok := <-w.Errors:
		if ok {
			t.Fatalf("Errors still delivers after Close: %v", err)
		}
	default:
		t.Fatal("Errors not closed when Close returned")
	}
}

func TestInotifyCloseJoinsPublicChannels(t *testing.T) {
	for range 100 {
		w, err := NewWatcher()
		if err != nil {
			t.Fatal(err)
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		assertInotifyChannelsClosed(t, w.b.(*inotify))
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		assertInotifyChannelsClosed(t, w.b.(*inotify))
	}
}

// Hold the reader's completion separately from stop notification. Repeated
// Close must join it too, without depending on the reader being scheduled first.
func TestInotifyRepeatedCloseJoinsReader(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		events, errs := make(chan Event), make(chan error)
		w := &inotify{shared: newShared(events, errs), Events: events, Errors: errs, doneResp: make(chan struct{})}
		w.shared.close()
		returned := make(chan error, 1)
		go func() { returned <- w.Close() }()
		synctest.Wait()
		select {
		case err := <-returned:
			t.Fatalf("Close returned before reader completion: %v", err)
		default:
		}
		// Run the actual production reader exit path with stop already closed.
		go w.readEvents()
		if err := <-returned; err != nil {
			t.Fatal(err)
		}
		assertInotifyChannelsClosed(t, w)
	})
}

// Both unread channels must be interruptible by stop. Model the reader at its
// actual send boundary, with an open descriptor and the real Close join.
func TestInotifyCloseUnblocksUnreadChannels(t *testing.T) {
	for _, kind := range []string{"Events", "Errors"} {
		t.Run(kind, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				r, writer, err := os.Pipe()
				if err != nil {
					t.Fatal(err)
				}
				defer r.Close()
				defer writer.Close()
				events, errs := make(chan Event), make(chan error)
				w := &inotify{shared: newShared(events, errs), Events: events, Errors: errs, inotifyFile: r, doneResp: make(chan struct{})}
				sent := make(chan bool, 1)
				go func() {
					if kind == "Events" {
						sent <- w.sendEvent(Event{Op: Create})
					} else {
						sent <- w.sendError(errors.New("unread control"))
					}
					// Execute the real reader's channel-close epilogue after stop.
					w.readEvents()
				}()
				synctest.Wait()
				select {
				case <-sent:
					t.Fatal("unread send did not block")
				default:
				}
				start := time.Now()
				if err := w.Close(); err != nil {
					t.Fatal(err)
				}
				if <-sent {
					t.Fatal("unread send was delivered")
				}
				if time.Since(start) != 0 {
					t.Fatal("Close required a timer to interrupt unread send")
				}
				assertInotifyChannelsClosed(t, w)
			})
		})
	}
}
