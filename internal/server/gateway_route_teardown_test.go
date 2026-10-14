package server

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"testing"

	"github.com/anydoor7/tslink/internal/accesslog"
	"github.com/anydoor7/tslink/internal/health"
	"github.com/anydoor7/tslink/internal/registry"
	"github.com/anydoor7/tslink/internal/testwait"
)

// gatewayEvent waits for the access event of one path.
func gatewayEvent(t *testing.T, f *gatewayFixture, app, path string) accesslog.Event {
	t.Helper()
	var found accesslog.Event
	testwait.Until(t, app+path+" access event", func() bool {
		for _, e := range f.events.all() {
			if e.App == app && e.Path == path {
				found = e
				return true
			}
		}
		return false
	})
	return found
}

// TSLink tearing a route down cancels its in-flight requests. The visitor did
// not leave and the backend did not fail: the event is allowed, 503 and
// route_closed, with no error class, and the route is not marked down.
func TestGatewayRouteTeardownIsNotAClientCancellation(t *testing.T) {
	t.Run("visitor still connected", func(t *testing.T) {
		f := newGatewayFixture(t, nil)
		entered := make(chan struct{}, 1)
		port := gatewayAddBackend(t, f, "teardown", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/slow" {
				entered <- struct{}{}
				<-r.Context().Done()
				return
			}
			io.WriteString(w, "ok")
		}))
		c := f.client(t)
		if code, _, _ := gatewayGet(t, c, strconv.Itoa(port), "/", nil); code != 200 {
			t.Fatal(code)
		}
		visitor := context.Background()
		req, _ := http.NewRequestWithContext(visitor, http.MethodGet, "https://home.tailnet.ts.net:"+strconv.Itoa(port)+"/slow", nil)
		type reply struct {
			status int
			err    error
		}
		replies := make(chan reply, 1)
		go func() {
			resp, err := c.Do(req)
			if err != nil {
				replies <- reply{err: err}
				return
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			replies <- reply{status: resp.StatusCode}
		}()
		testwait.Recv(t, entered, "backend entered")
		f.s.portalRun.routesMu.Lock()
		route := f.s.portalRun.routes["teardown"]
		f.s.portalRun.routesMu.Unlock()
		route.cancel()
		got := testwait.Recv(t, replies, "visitor response")
		if got.err != nil || got.status != http.StatusServiceUnavailable || visitor.Err() != nil {
			t.Fatalf("visitor got %+v", got)
		}
		e := gatewayEvent(t, f, "teardown", "/slow")
		if e.Decision != "allowed" || e.Status != http.StatusServiceUnavailable || e.Reason != "route_closed" || e.ErrorClass != "" {
			t.Fatalf("teardown event %+v", e)
		}
		f.s.portalRun.routesMu.Lock()
		state := route.state
		f.s.portalRun.routesMu.Unlock()
		if state.Health != health.Healthy || state.OfflineSince != nil {
			t.Fatalf("teardown marked the route down: %+v", state)
		}
	})
	t.Run("app removed", func(t *testing.T) {
		f := newGatewayFixture(t, nil)
		entered := make(chan struct{}, 1)
		port := gatewayAddBackend(t, f, "teardown", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			entered <- struct{}{}
			<-r.Context().Done()
		}))
		c := f.client(t)
		finished := make(chan struct{})
		go func() {
			defer close(finished)
			req, _ := http.NewRequest(http.MethodGet, "https://home.tailnet.ts.net:"+strconv.Itoa(port)+"/slow", nil)
			if resp, err := c.Do(req); err == nil {
				io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
			}
		}()
		testwait.Recv(t, entered, "backend entered")
		if removed, err := registry.Remove(f.path, "teardown"); err != nil || !removed {
			t.Fatal(removed, err)
		}
		f.s.syncPortal(context.Background(), f.config)
		testwait.Recv(t, finished, "request ended by the teardown")
		e := gatewayEvent(t, f, "teardown", "/slow")
		if e.Decision != "allowed" || e.Status != http.StatusServiceUnavailable || e.Reason != "route_closed" || e.ErrorClass != "" {
			t.Fatalf("teardown event %+v", e)
		}
	})
}
