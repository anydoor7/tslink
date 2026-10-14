package server

import (
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/accesslog"
	"github.com/anydoor7/tslink/internal/registry"
	"github.com/anydoor7/tslink/internal/testwait"
)

// guestEvents waits until the stored log holds the http event of path and
// returns every stored event.
func guestEvents(t *testing.T, f *guestFixture, path string) (accesslog.Event, []accesslog.Event) {
	t.Helper()
	var found accesslog.Event
	var all []accesslog.Event
	testwait.Until(t, "stored "+path+" event", func() bool {
		drainAccess(t, f.store)
		page, err := accesslog.Query(f.dir, accesslog.Filter{})
		if err != nil {
			t.Fatal(err)
		}
		all = page.Events
		for _, e := range all {
			if e.Kind == "http" && e.Path == path {
				found = e
				return true
			}
		}
		return false
	})
	return found, all
}

// guestRequestAsync sends a guest request whose client stays connected and
// reports the status it received, or -1 for a transport error.
func guestRequestAsync(f *guestFixture, path string, cookies []*http.Cookie) <-chan int {
	statuses := make(chan int, 1)
	go func() {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, f.base+path, nil)
		for _, c := range cookies {
			req.AddCookie(c)
		}
		resp, err := f.client.Do(req)
		if err != nil {
			statuses <- -1
			return
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		statuses <- resp.StatusCode
	}()
	return statuses
}

// Stopping a guest-gated app ends its active guest flights. A valid guest
// whose request was in flight was cut by TSLink, not denied: allowed and
// node_closed, with 503 before the response started and the forwarded status
// after. No denial event, no guest denial audit entry, and the grant is
// untouched.
func TestGuestNodeStopIsNotADenial(t *testing.T) {
	for _, tc := range []struct {
		name   string
		stream bool
		status int
	}{
		{"before response start", false, http.StatusServiceUnavailable},
		{"after response start", true, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newGuestFixture(t, "", false, true)
			entered := make(chan struct{}, 1)
			replaceGuestBackend(t, f, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.stream {
					w.Header().Set("Content-Type", "text/event-stream")
					w.WriteHeader(http.StatusOK)
					_, _ = io.WriteString(w, "hello")
					w.(http.Flusher).Flush()
				}
				entered <- struct{}{}
				<-r.Context().Done()
			}))
			cookies := f.login()
			release := f.holdCounterFlush()
			var statuses <-chan int
			var resp *http.Response
			if tc.stream {
				req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, f.base+"/slow", nil)
				for _, c := range cookies {
					req.AddCookie(c)
				}
				var err error
				if resp, err = f.client.Do(req); err != nil {
					t.Fatal(err)
				}
				defer resp.Body.Close()
				first := make([]byte, 5)
				if _, err := io.ReadFull(resp.Body, first); err != nil || string(first) != "hello" {
					t.Fatalf("stream start %q %v", first, err)
				}
			} else {
				statuses = guestRequestAsync(f, "/slow", cookies)
			}
			testwait.Recv(t, entered, "guest backend entered")
			release()
			f.s.mu.Lock()
			f.s.stopNodeLocked("photos")
			f.s.mu.Unlock()
			got := 0
			if tc.stream {
				_, _ = io.Copy(io.Discard, resp.Body)
				got = resp.StatusCode
			} else {
				got = testwait.Recv(t, statuses, "guest response")
			}
			if got != tc.status {
				t.Fatalf("guest received %d, want %d", got, tc.status)
			}
			e, all := guestEvents(t, f, "/slow")
			if e.Decision != "allowed" || e.Status != tc.status || e.Reason != "node_closed" || e.ErrorClass != "" {
				t.Fatalf("node stop event %+v", e)
			}
			for _, other := range all {
				if other.Decision == "denied" {
					t.Fatalf("node stop recorded a denial: %+v", other)
				}
			}
			grant, err := registry.ShowGuest(f.path, f.grant.ID, accessTestTime)
			if err != nil {
				t.Fatal(err)
			}
			if grant.Revoked || grant.Expired {
				t.Fatalf("node stop changed the grant: %+v", grant)
			}
		})
	}
}

// A grant that expires or is revoked while its request is in flight keeps the
// denial path: 401, denied with the grant's reason and a guest denial event,
// never node_closed.
func TestGuestGrantEndInFlightStaysADenial(t *testing.T) {
	for _, reason := range []string{"expired", "revoked"} {
		t.Run(reason, func(t *testing.T) {
			f := newGuestFixture(t, "", false, true)
			entered := make(chan struct{}, 1)
			replaceGuestBackend(t, f, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				entered <- struct{}{}
				<-r.Context().Done()
			}))
			cookies := f.login()
			release := f.holdCounterFlush()
			statuses := guestRequestAsync(f, "/slow", cookies)
			testwait.Recv(t, entered, "guest backend entered")
			release()
			if reason == "expired" {
				// The expiry denial persists its latch, so it must not hold a
				// read window.
				f.now.Store(accessTestTime.Add(2*time.Hour + time.Second).UnixNano())
			} else {
				if _, err := registry.RevokeGuest(f.path, f.grant.ID, accessTestTime); err != nil {
					t.Fatal(err)
				}
				// The revoked denial only reads: own its read window.
				defer f.holdCounterFlush()()
			}
			if got := testwait.Recv(t, statuses, "guest response"); got != http.StatusUnauthorized {
				t.Fatalf("guest received %d, want 401", got)
			}
			e, all := guestEvents(t, f, "/slow")
			if e.Decision != "denied" || e.Status != http.StatusUnauthorized || e.Reason != reason {
				t.Fatalf("grant end event %+v", e)
			}
			audited := false
			for _, other := range all {
				if other.Reason == "node_closed" {
					t.Fatalf("grant end recorded as node_closed: %+v", other)
				}
				if other.Kind == "guest" && other.Guest != nil && other.Guest.Decision == "denied" && other.Guest.Reason == reason {
					audited = true
				}
			}
			if !audited {
				t.Fatalf("no guest denial event for %s: %+v", reason, all)
			}
		})
	}
}
