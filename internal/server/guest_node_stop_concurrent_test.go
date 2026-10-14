package server

import (
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/anydoor7/tslink/internal/testwait"
)

// Pause Close inside endCause: the gate is closed and the flight ended,
// but the node cause has not reached the request context yet. A concurrent
// SSE write then runs guestResponse.authorized; the event must keep node_closed.
func TestGuestStopDuringConcurrentSSEWriteKeepsNodeClosed(t *testing.T) {
	f := newGuestFixture(t, "", false, true)
	g := cleanupGate(f)
	ready := make(chan struct{})
	stopEntered := make(chan struct{})
	write := make(chan struct{})
	written := make(chan struct{})
	resume := make(chan struct{})
	var once sync.Once
	var delayed atomic.Bool
	releaseStop := func() { once.Do(func() { close(resume) }) }
	defer releaseStop()
	g.app = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, "hello")
		w.(http.Flusher).Flush()
		flight := w.(*guestResponse).flight
		originalCancel := flight.cancel
		flight.cancel = func(cause error) {
			if cause == errNodeClosed && delayed.CompareAndSwap(false, true) {
				close(stopEntered)
				<-resume
			}
			originalCancel(cause)
		}
		close(ready)
		<-write
		_, err := io.WriteString(w, "later")
		close(written)
		if err != nil {
			panic(http.ErrAbortHandler)
		}
	})
	cookies := f.login()
	releaseReader := f.holdCounterFlush()
	req, _ := http.NewRequest(http.MethodGet, f.base+"/race", nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	resp, err := f.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	testwait.Recv(t, ready, "live SSE flight")
	prefix := make([]byte, 5)
	if _, err := io.ReadFull(resp.Body, prefix); err != nil || string(prefix) != "hello" {
		t.Fatal(string(prefix), err)
	}
	closed := make(chan struct{}, 1)
	go func() {
		f.s.mu.Lock()
		f.s.stopNodeLocked("photos")
		f.s.mu.Unlock()
		closed <- struct{}{}
	}()
	testwait.Recv(t, stopEntered, "Close before node cancellation")
	close(write)
	testwait.Recv(t, written, "concurrent authorized SSE write")
	io.Copy(io.Discard, resp.Body)
	e, all := guestEvents(t, f, "/race")
	releaseReader()
	releaseStop()
	testwait.Recv(t, closed, "node stopped")
	t.Logf("concurrent SSE write: wire=%d event=%+v all=%+v", resp.StatusCode, e, all)
	if e.Decision != "allowed" || e.Status != http.StatusOK || e.Reason != "node_closed" || e.ErrorClass != "" {
		t.Fatalf("valid guest cut by stop lost server reason: %+v", e)
	}
}
