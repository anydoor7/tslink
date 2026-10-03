package server

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/anydoor7/tslink/internal/registry"
	"github.com/fsnotify/fsnotify"
)

type guestFlight struct {
	gate   *guestGate
	id     string
	expiry time.Time
	cancel context.CancelFunc
	mu     sync.Mutex
	conn   net.Conn
	ended  bool
	timer  *time.Timer
}

func (f *guestFlight) end() {
	f.mu.Lock()
	f.ended = true
	conn := f.conn
	f.mu.Unlock()
	if f.gate != nil {
		f.gate.release(f)
	}
	f.cancel()
	if conn != nil {
		_ = conn.Close()
	}
}

func (g *guestGate) observe(grants []registry.GuestGrant) {
	g.mu.Lock()
	flights := make([]*guestFlight, 0, len(g.flights))
	for flight := range g.flights {
		flights = append(flights, flight)
	}
	g.mu.Unlock()
	g.observeFlights(grants, flights)
}

func (g *guestGate) observeFlights(grants []registry.GuestGrant, flights []*guestFlight) {
	live := make(map[string]registry.GuestGrant, len(grants))
	for _, grant := range grants {
		live[grant.ID] = grant
	}
	at := g.now()
	for _, flight := range flights {
		grant, ok := live[flight.id]
		if !ok || grant.App != g.svc.Name || grant.Revoked || grant.Expired || !at.Before(grant.ExpiresAt) {
			flight.end()
		}
	}
}

func (g *guestGate) monitor(watcher *fsnotify.Watcher) {
	defer close(g.done)
	var events <-chan fsnotify.Event
	var errors <-chan error
	if watcher != nil {
		defer watcher.Close()
		events = watcher.Events
		errors = watcher.Errors
	}
	// The short timer observes injected-clock changes and acts as a fallback if
	// filesystem notifications are lost. Each flight also has its own deadline.
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	flush := time.NewTicker(30 * time.Second)
	defer flush.Stop()
	flushPending := false
	for {
		select {
		case <-g.stop:
			return
		case <-flush.C:
			flushPending = true
		case event, ok := <-events:
			if !ok {
				events = nil
			} else if filepath.Clean(event.Name) != filepath.Clean(g.path) {
				continue
			} else {
				flushPending = true
			}
		case _, ok := <-errors:
			if !ok {
				errors = nil
			}
		case <-tick.C:
		}
		if flushPending {
			if err := registry.FlushGuestCounters(g.path); err == nil {
				flushPending = false
			}
		}
		g.mu.Lock()
		flights := make([]*guestFlight, 0, len(g.flights))
		for f := range g.flights {
			flights = append(flights, f)
		}
		g.mu.Unlock()
		if len(flights) == 0 {
			continue
		}
		grants, err := registry.ReadGuestGrants(g.path, g.svc.Name, g.now())
		if err != nil {
			for _, f := range flights {
				f.end()
			}
		} else {
			g.observeFlights(grants, flights)
		}
	}
}

// Expiry checks may commit a registry latch and its audit receipt even after a
// hijacked socket closes. Join those operations before shutdown releases state.
func (g *guestGate) grantState(read func() (registry.GuestView, string)) (registry.GuestView, string) {
	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		return registry.GuestView{}, "unavailable"
	}
	g.stateReads.Add(1)
	g.mu.Unlock()
	defer g.stateReads.Done()
	return read()
}

func (g *guestGate) Close() error {
	g.closeOnce.Do(func() {
		close(g.stop)
		g.unsubscribe()
		g.mu.Lock()
		g.closed = true
		flights := make([]*guestFlight, 0, len(g.flights))
		for f := range g.flights {
			flights = append(flights, f)
		}
		g.mu.Unlock()
		for _, f := range flights {
			f.end()
		}
		<-g.done
		g.stateReads.Wait()
	})
	return registry.FlushGuestCounters(g.path)
}

func (g *guestGate) release(flight *guestFlight) {
	flight.mu.Lock()
	timer := flight.timer
	flight.mu.Unlock()
	if timer != nil {
		timer.Stop()
	}
	g.mu.Lock()
	delete(g.flights, flight)
	g.mu.Unlock()
	flight.cancel()
}

func (g *guestGate) serveApp(w http.ResponseWriter, r *http.Request, view registry.GuestView) {
	ctx, cancel := context.WithCancel(r.Context())
	flight := &guestFlight{gate: g, id: view.ID, expiry: view.ExpiresAt, cancel: cancel}
	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		cancel()
		g.deny(w, r, view.ID, "unavailable", g.now())
		return
	}
	g.flights[flight] = struct{}{}
	g.mu.Unlock()
	returned := false
	defer func() {
		flight.mu.Lock()
		handedOff := returned && flight.conn != nil && !flight.ended
		flight.mu.Unlock()
		if !handedOff {
			flight.end()
		}
		// Panics, including http.ErrAbortHandler, propagate through this defer.
	}()
	// Register before the final state check so no commit can miss this flight.
	_, reason := g.grantState(func() (registry.GuestView, string) {
		return registry.CheckGuest(g.path, g.svc.Name, view.ID, g.now(), false, false)
	})
	if reason != "allowed" {
		flight.end()
		g.deny(w, r, view.ID, reason, g.now())
	} else {
		flight.mu.Lock()
		if !flight.ended {
			flight.timer = time.AfterFunc(view.ExpiresAt.Sub(g.now()), func() {
				// Persist the expiry latch as well as ending the existing connection.
				_, _ = g.grantState(func() (registry.GuestView, string) {
					return registry.CheckGuest(g.path, g.svc.Name, view.ID, g.now(), false, false)
				})
				flight.end()
			})
		}
		flight.mu.Unlock()
		g.app.ServeHTTP(&guestResponse{ResponseWriter: w, gate: g, flight: flight}, stripGuestSecrets(r.WithContext(ctx)))
	}
	returned = true
}

type guestResponse struct {
	http.ResponseWriter
	gate      *guestGate
	flight    *guestFlight
	streaming atomic.Bool
}

func (w *guestResponse) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *guestResponse) WriteHeader(status int) {
	if strings.HasPrefix(strings.ToLower(w.Header().Get("Content-Type")), "text/event-stream") {
		w.streaming.Store(true)
	}
	w.ResponseWriter.WriteHeader(status)
}
func (w *guestResponse) Write(data []byte) (int, error) {
	if w.streaming.Load() {
		if !w.authorized() {
			return 0, context.Canceled
		}
	}
	return w.ResponseWriter.Write(data)
}
func (w *guestResponse) authorized() bool {
	_, reason := w.gate.grantState(func() (registry.GuestView, string) {
		return registry.CheckGuest(w.gate.path, w.gate.svc.Name, w.flight.id, w.gate.now(), false, false)
	})
	if reason != "allowed" {
		w.flight.end()
		return false
	}
	w.flight.mu.Lock()
	ended := w.flight.ended
	w.flight.mu.Unlock()
	return !ended
}
func (w *guestResponse) FlushError() error {
	if w.streaming.Load() && !w.authorized() {
		return context.Canceled
	}
	return http.NewResponseController(w.ResponseWriter).Flush()
}
func (w *guestResponse) Flush() { _ = w.FlushError() }
func (w *guestResponse) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, rw, err := http.NewResponseController(w.ResponseWriter).Hijack()
	if err != nil {
		return nil, nil, err
	}
	tracked := &guestConn{Conn: conn, response: w}
	w.flight.mu.Lock()
	w.flight.conn = tracked
	ended := w.flight.ended
	w.flight.mu.Unlock()
	if ended || !w.authorized() {
		_ = tracked.Close()
		return nil, nil, context.Canceled
	}
	// Preserve bytes read ahead during the upgrade, while routing all subsequent
	// traffic through the per-grant connection and its authorization checks.
	buffered, _ := rw.Reader.Peek(rw.Reader.Buffered())
	reader := bufio.NewReader(io.MultiReader(bytes.NewReader(bytes.Clone(buffered)), tracked))
	return tracked, bufio.NewReadWriter(reader, bufio.NewWriter(tracked)), nil
}

type guestConn struct {
	net.Conn
	response  *guestResponse
	closeOnce sync.Once
	closeErr  error
}

func (c *guestConn) Close() error {
	c.closeOnce.Do(func() {
		c.closeErr = c.Conn.Close()
		c.response.gate.release(c.response.flight)
	})
	return c.closeErr
}

func (c *guestConn) Read(b []byte) (int, error) {
	if !c.response.authorized() {
		return 0, context.Canceled
	}
	n, err := c.Conn.Read(b)
	if !c.response.authorized() {
		return 0, context.Canceled
	}
	return n, err
}
func (c *guestConn) Write(b []byte) (int, error) {
	if !c.response.authorized() {
		return 0, context.Canceled
	}
	return c.Conn.Write(b)
}
