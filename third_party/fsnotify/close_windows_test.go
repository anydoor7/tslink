//go:build windows && go1.25

package fsnotify

import (
	"golang.org/x/sys/windows"
	"testing"
	"testing/synctest"
)

// Delay only the reader's launch. Close still uses the actual completion port
// and readEvents shutdown; virtual time proves callers wait without a sleep.
func TestWindowsCloseJoinsPublicChannels(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		port, err := windows.CreateIoCompletionPort(windows.InvalidHandle, 0, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		w := &readDirChangesW{Events: make(chan Event), Errors: make(chan error), port: port,
			watches: make(watchMap), input: make(chan *input, 1), done: make(chan chan<- error, 1)}
		results := make(chan error, 8)
		go func() { results <- w.Close() }()
		synctest.Wait()
		if !w.isClosed() {
			t.Fatal("first Close did not claim ownership")
		}
		for i := 0; i < 7; i++ {
			go func() { results <- w.Close() }()
		}
		synctest.Wait()
		if n := len(results); n != 0 {
			t.Errorf("%d Close callers returned before the reader closed public channels", n)
		}
		go w.readEvents()
		for i := 0; i < 8; i++ {
			if err := <-results; err != nil {
				t.Error(err)
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
	})
}
