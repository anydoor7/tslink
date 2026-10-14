package server

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/accesslog"
	"github.com/anydoor7/tslink/internal/health"
	"github.com/anydoor7/tslink/internal/registry"
	"github.com/anydoor7/tslink/internal/testwait"
)

// serverCancelLane is a gateway route or an app node serving one backend, with
// the teardown TSLink applies to it and the reason that teardown records.
type serverCancelLane struct {
	url      string
	client   *http.Client
	teardown func()
	event    func(path string) accesslog.Event
	// control is the reason of a request that ended normally.
	control string
	reason  string
	// check asserts the lane recorded no backend failure or down state.
	check func(t *testing.T)
}

func newServerCancelLane(t *testing.T, lane string, backend http.Handler) serverCancelLane {
	t.Helper()
	if lane == "gateway" {
		f := newGatewayFixture(t, nil)
		port := gatewayAddBackend(t, f, "stream", backend)
		c := f.client(t)
		if code, _, _ := gatewayGet(t, c, strconv.Itoa(port), "/", nil); code != 200 {
			t.Fatal(code)
		}
		f.s.portalRun.routesMu.Lock()
		route := f.s.portalRun.routes["stream"]
		f.s.portalRun.routesMu.Unlock()
		return serverCancelLane{
			url:      "https://home.tailnet.ts.net:" + strconv.Itoa(port),
			client:   c,
			teardown: route.cancel,
			event:    func(path string) accesslog.Event { return gatewayEvent(t, f, "stream", path) },
			control:  gatewayEvent(t, f, "stream", "/").Reason,
			reason:   "route_closed",
			check: func(t *testing.T) {
				f.s.portalRun.routesMu.Lock()
				state := route.state
				f.s.portalRun.routesMu.Unlock()
				if state.Health != health.Healthy || state.OfflineSince != nil {
					t.Fatalf("teardown marked the route down: %+v", state)
				}
			},
		}
	}
	b := httptest.NewServer(backend)
	t.Cleanup(b.Close)
	// Force the close path: graceful shutdown runs out of time.
	oldShutdown := shutdownHTTPServerFn
	shutdownHTTPServerFn = func(context.Context, *http.Server) error { return context.DeadlineExceeded }
	t.Cleanup(func() { shutdownHTTPServerFn = oldShutdown })
	events := &gatewayEvents{}
	who := &stallingWhoIs{entered: make(chan struct{}, 1)}
	svc := registry.Service{Name: "stream", Type: registry.TypeProxy, Target: b.URL, AllowedUsers: []string{"alice@example.com"}}
	s, _, _, base := setupAccessNodeWithClient(t, svc, who.client(), accesslog.Options{}, nil, events)
	logs := captureServerLog(t)
	return serverCancelLane{
		url:      base,
		client:   &http.Client{Timeout: 2 * testwait.Budget(t)},
		teardown: func() { s.mu.Lock(); defer s.mu.Unlock(); s.stopNodeLocked("stream") },
		event: func(path string) accesslog.Event {
			var found accesslog.Event
			testwait.Until(t, path+" access event", func() bool {
				for _, e := range events.all() {
					if e.Path == path {
						found = e
						return true
					}
				}
				return false
			})
			return found
		},
		control: "",
		reason:  "node_closed",
		check: func(t *testing.T) {
			if strings.Contains(logs.String(), "backend_request_failed") {
				t.Fatalf("teardown logged as a backend failure:\n%s", logs)
			}
		},
	}
}

// streamBackend flushes status and "hello" on /stream and then waits for the
// request to end; other paths complete normally.
func streamBackend(status int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/stream" {
			_, _ = io.WriteString(w, "complete")
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, "hello")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
}

// A route teardown or forced node close that cuts a response already started
// keeps the forwarded status, as the visitor received it, and records that
// TSLink ended it: route_closed or node_closed, no error class, no down state.
// The visitor stays connected throughout.
func TestServerCancelAfterResponseStartKeepsForwardedStatus(t *testing.T) {
	for _, lane := range []string{"gateway", "node"} {
		for _, status := range []int{http.StatusOK, http.StatusInternalServerError} {
			t.Run(lane+"/"+strconv.Itoa(status), func(t *testing.T) {
				l := newServerCancelLane(t, lane, streamBackend(status))
				visitor := context.Background()
				req, _ := http.NewRequestWithContext(visitor, http.MethodGet, l.url+"/stream", nil)
				resp, err := l.client.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				defer resp.Body.Close()
				first := make([]byte, 5)
				if _, err := io.ReadFull(resp.Body, first); err != nil || resp.StatusCode != status || string(first) != "hello" {
					t.Fatalf("response %d %q %v", resp.StatusCode, first, err)
				}
				l.teardown()
				_, _ = io.Copy(io.Discard, resp.Body)
				e := l.event("/stream")
				if visitor.Err() != nil || e.Decision != "allowed" || e.Status != status || e.Reason != l.reason || e.ErrorClass != "" {
					t.Fatalf("server cut after response start: %+v", e)
				}
				l.check(t)
			})
		}
	}
}

// A response that completed before a later teardown keeps its normal reason.
func TestServerTeardownAfterCompletedResponseKeepsReason(t *testing.T) {
	for _, lane := range []string{"gateway", "node"} {
		t.Run(lane, func(t *testing.T) {
			l := newServerCancelLane(t, lane, streamBackend(http.StatusOK))
			resp, err := l.client.Get(l.url + "/done")
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			if err != nil || resp.StatusCode != http.StatusOK || string(body) != "complete" {
				t.Fatalf("response %d %q %v", resp.StatusCode, body, err)
			}
			l.teardown()
			if e := l.event("/done"); e.Decision != "allowed" || e.Status != http.StatusOK || e.Reason != l.control || e.ErrorClass != "" {
				t.Fatalf("completed response event %+v, want reason %q", e, l.control)
			}
			l.check(t)
		})
	}
}

// The access middleware applies a server cancellation reason only when the
// handler did not return, so ordering is controlled here: a teardown that
// lands after the response completed, but before the event is recorded,
// keeps the normal reason; a cut response gets the server reason; a client
// that leaves keeps its normal reason; a denial keeps its reason.
func TestAccessEventServerReasonOnlyForCutResponses(t *testing.T) {
	for _, tc := range []struct {
		name     string
		status   int
		cut      bool
		client   bool
		deny     bool
		reason   string
		decision string
	}{
		{name: "completed then teardown", status: http.StatusOK, reason: "", decision: "allowed"},
		{name: "cut 200", status: http.StatusOK, cut: true, reason: "route_closed", decision: "allowed"},
		{name: "cut 500", status: http.StatusInternalServerError, cut: true, reason: "route_closed", decision: "allowed"},
		{name: "client left", status: http.StatusOK, client: true, reason: "", decision: "allowed"},
		{name: "denied then cut", status: http.StatusForbidden, cut: true, deny: true, reason: "people", decision: "denied"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base, cancelBase := context.WithCancelCause(context.Background())
			defer cancelBase(nil)
			started := make(chan struct{})
			events := &gatewayEvents{}
			svc := registry.Service{Name: "app", Type: registry.TypeProxy}
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.deny {
					accessDeny(r, "people")
				}
				if !tc.cut && !tc.client {
					w.Header().Set("Content-Length", "8")
				}
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, "complete")
				w.(http.Flusher).Flush()
				close(started)
				if !tc.client {
					cancelBase(errRouteClosed)
				}
				<-r.Context().Done()
				if tc.cut || tc.client {
					panic(http.ErrAbortHandler)
				}
			})
			srv := httptest.NewUnstartedServer(AccessEventMiddleware(svc, accesslog.Options{}, events, nil, func() time.Time { return accessTestTime }, next))
			srv.Config.BaseContext = func(net.Listener) context.Context { return base }
			srv.Start()
			defer srv.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/x", nil)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			<-started
			if tc.client {
				cancel()
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode != tc.status {
				t.Fatalf("wire status %d", resp.StatusCode)
			}
			var got []accesslog.Event
			testwait.Until(t, "access event", func() bool { got = events.all(); return len(got) > 0 })
			if e := got[0]; len(got) != 1 || e.Decision != tc.decision || e.Status != tc.status || e.Reason != tc.reason || e.ErrorClass != "" {
				t.Fatalf("events %+v, want %s/%d/%q", got, tc.decision, tc.status, tc.reason)
			}
		})
	}
}
