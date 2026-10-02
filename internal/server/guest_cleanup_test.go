package server

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/filelock"
	"github.com/anydoor7/tslink/internal/registry"
)

func cleanupGate(f *guestFixture) *guestGate { return f.s.nodes["photos"].handlerCloser.(*guestGate) }
func cleanupFlightCount(g *guestGate) int    { g.mu.Lock(); defer g.mu.Unlock(); return len(g.flights) }
func cleanupWaitEmpty(t *testing.T, g *guestGate) {
	t.Helper()
	until := time.Now().Add(2 * time.Second)
	for cleanupFlightCount(g) != 0 {
		if time.Now().After(until) {
			t.Fatalf("flights retained: %d", cleanupFlightCount(g))
		}
		time.Sleep(5 * time.Millisecond)
	}
}
func cleanupSSE(t *testing.T, f *guestFixture, cookies []*http.Cookie) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("GET", f.base+"/events", nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	resp, err := f.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(resp.Body).ReadString('\n')
	if resp.StatusCode != 200 || line != "data: ready\n" || err != nil {
		resp.Body.Close()
		t.Fatal("SSE control", resp.StatusCode, err)
	}
	return resp
}
func TestGuestAbortedStreamsCleanup(t *testing.T) {
	for _, h2 := range []bool{false, true} {
		t.Run(fmt.Sprintf("h2=%t", h2), func(t *testing.T) {
			f := newGuestFixture(t, "", h2, true)
			var stopped atomic.Int64
			replaceGuestBackend(t, f, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "data: ready\n\n")
				w.(http.Flusher).Flush()
				if r.URL.Path == "/normal" {
					return
				}
				<-r.Context().Done()
				stopped.Add(1)
			}))
			cookies := f.login()
			g := cleanupGate(f)
			req, _ := http.NewRequest("GET", f.base+"/normal", nil)
			for _, c := range cookies {
				req.AddCookie(c)
			}
			resp, e := f.client.Do(req)
			if e != nil {
				t.Fatal(e)
			}
			raw, e := io.ReadAll(resp.Body)
			resp.Body.Close()
			if e != nil || !strings.Contains(string(raw), "ready") {
				t.Fatal("normal EOF control")
			}
			cleanupWaitEmpty(t, g)
			for i := 0; i < 20; i++ {
				resp := cleanupSSE(t, f, cookies)
				resp.Body.Close()
			}
			until := time.Now().Add(2 * time.Second)
			for stopped.Load() != 20 {
				if time.Now().After(until) {
					t.Fatal("backend cancellation missing", stopped.Load())
				}
				time.Sleep(5 * time.Millisecond)
			}
			time.Sleep(150 * time.Millisecond)
			count := cleanupFlightCount(g)
			t.Logf("normal_eof_flights=0 closed_streams=%d retained_flights=%d ", stopped.Load(), count)
			// Keep the resource count before and after gate shutdown in the receipt.
			if e := g.Close(); e != nil {
				t.Fatal(e)
			}
			t.Logf("retained_flights_after_gate_close=%d", cleanupFlightCount(g))
			if count != 0 {
				t.Error("client-disconnected SSE retains flight and expiry timer")
			}
		})
	}
}

func TestGuestAbortedStreamStopsTimer(t *testing.T) {
	f := newGuestFixture(t, "", true, true)
	stopped := make(chan struct{})
	replaceGuestBackend(t, f, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(stopped)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: ready\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	resp := cleanupSSE(t, f, f.login())
	g := cleanupGate(f)
	g.mu.Lock()
	var timer *time.Timer
	for flight := range g.flights {
		flight.mu.Lock()
		timer = flight.timer
		flight.mu.Unlock()
	}
	g.mu.Unlock()
	if timer == nil {
		t.Fatal("active timer control")
	}
	resp.Body.Close()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("backend cancellation control")
	}
	time.Sleep(100 * time.Millisecond)
	active := 0
	if timer.Stop() {
		active++
	}
	t.Logf("closed_streams=1 retained_flights=%d pending_expiry_timers=%d", cleanupFlightCount(g), active)
	if active != 0 {
		t.Error("closed stream retains scheduled expiry timer")
	}
}

func TestGuestHandlerPanicCleanup(t *testing.T) {
	for _, value := range []any{http.ErrAbortHandler, "handler panic"} {
		t.Run(fmt.Sprint(value), func(t *testing.T) {
			f := newGuestFixture(t, "", false, true)
			g := cleanupGate(f)
			var flight *guestFlight
			g.app = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { flight = w.(*guestResponse).flight; panic(value) })
			func() {
				defer func() {
					if recovered := recover(); recovered != value {
						t.Fatalf("panic changed: %v", recovered)
					}
				}()
				g.serveApp(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil), f.grant)
				t.Error("panic suppressed")
			}()
			if cleanupFlightCount(g) != 0 {
				t.Fatal("panic retained flight")
			}
			if flight == nil || flight.timer == nil {
				t.Fatal("timer control")
			}
			if flight.timer.Stop() {
				t.Fatal("panic retained timer")
			}
		})
	}
}
func TestGuestCloseDrainsFlights(t *testing.T) {
	f := newGuestFixture(t, "", true, true)
	stopped := make(chan struct{}, 3)
	replaceGuestBackend(t, f, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: ready\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		stopped <- struct{}{}
	}))
	// Hold a real shared reader throughout shutdown. Neither cancellation nor
	// monitor termination implies that all registry readers have released it.
	lock, err := os.OpenFile(f.path+".lock", os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if acquired, err := filelock.TryReadLock(lock); err != nil || !acquired {
		t.Fatal("shared reader control", acquired, err)
	}
	defer filelock.Unlock(lock)
	cookies := f.login()
	g := cleanupGate(f)
	// Keep proxy handlers from returning after cancellation. Close must drain
	// ownership itself, without relying on their deferred cleanup running first.
	app := g.app
	handlersDone := make(chan struct{})
	proxyReturned := make(chan struct{}, 3)
	defer close(handlersDone)
	g.app = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			proxyReturned <- struct{}{}
			<-handlersDone
		}()
		app.ServeHTTP(w, r)
	})
	for range 3 {
		resp := cleanupSSE(t, f, cookies)
		defer resp.Body.Close()
	}
	g.mu.Lock()
	var timers []*time.Timer
	for flight := range g.flights {
		flight.mu.Lock()
		timers = append(timers, flight.timer)
		flight.mu.Unlock()
	}
	g.mu.Unlock()
	if len(timers) != 3 {
		t.Fatal("live flight control", len(timers))
	}
	for range 2 {
		start := time.Now()
		err := g.Close()
		if err == nil || !strings.Contains(err.Error(), "guest counters: registry writer busy") {
			t.Fatal("reader-held Close must report busy", err)
		}
		if time.Since(start) > time.Second {
			t.Fatal("busy Close was not bounded")
		}
		if cleanupFlightCount(g) != 0 {
			t.Fatal("Close retained flights")
		}
		select {
		case <-g.done:
		default:
			t.Fatal("monitor not joined")
		}
	}
	for _, timer := range timers {
		if timer == nil || timer.Stop() {
			t.Fatal("Close retained timer")
		}
	}
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for range 3 {
		select {
		case <-stopped:
		case <-deadline.C:
			t.Fatal("Close did not cancel backend")
		}
	}
	// Join each proxy's I/O before retrying, so its final authorization read
	// cannot race the counter writer. Handler defers are still held above.
	for range 3 {
		select {
		case <-proxyReturned:
		case <-deadline.C:
			t.Fatal("proxy I/O did not finish")
		}
	}
	if err := filelock.Unlock(lock); err != nil {
		t.Fatal(err)
	}
	if err := g.Close(); err != nil {
		t.Fatal("retry after reader release", err)
	}
	for range 2 {
		reg, _, err := registry.Preflight(f.path)
		if err != nil {
			t.Fatal(err)
		}
		if reg.Guests[0].Uses != 3 || reg.Guests[0].Sessions != 1 {
			t.Fatal("retry must persist exactly the observed counters", reg.Guests[0].Uses, reg.Guests[0].Sessions)
		}
		if registry.GuestCounterError(f.path) != nil {
			t.Fatal("successful retry left warning")
		}
		if err := g.Close(); err != nil {
			t.Fatal("repeat successful Close", err)
		}
	}
	t.Log("two reader-held Close calls returned bounded busy; flights=0, timers stopped, backend cancelled, monitor joined; released-reader retry persisted exactly 3 uses/1 session")
}

func TestGuestCloseBusyCounters(t *testing.T) {
	f := newGuestFixture(t, "", true, true)
	f.login()
	g := cleanupGate(f)
	lock, err := os.OpenFile(f.path+".lock", os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := filelock.Lock(lock); err != nil {
		t.Fatal(err)
	}
	err = g.Close()
	if unlockErr := filelock.Unlock(lock); unlockErr != nil {
		t.Fatal(unlockErr)
	}
	if err == nil || !strings.Contains(err.Error(), "guest counters: registry writer busy") {
		t.Fatal("busy flush control", err)
	}
	if cleanupFlightCount(g) != 0 {
		t.Fatal("busy Close retained flight")
	}
	if err := g.Close(); err != nil {
		t.Fatal("counter retry", err)
	}
}
