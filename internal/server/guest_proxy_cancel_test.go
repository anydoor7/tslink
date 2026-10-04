package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/registry"
)

// Revoke after the real proxy reaches its backend but before response headers.
// A backend cancellation must retain the guest denial, not claim it is offline.
func TestGuestProxyRevocationBeforeHeaders(t *testing.T) {
	for _, h2 := range []bool{false, true} {
		for _, revoke := range []bool{false, true} {
			t.Run(fmt.Sprintf("h2=%t/revoke=%t", h2, revoke), func(t *testing.T) {
				f := newGuestFixture(t, "", h2, true)
				entered, release, canceled := make(chan struct{}), make(chan struct{}), make(chan struct{})
				var hits atomic.Int64
				replaceGuestBackend(t, f, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					hits.Add(1)
					close(entered)
					select {
					case <-release:
						w.WriteHeader(http.StatusNoContent)
					case <-r.Context().Done():
						close(canceled)
					}
				}))
				defer close(release)
				cookies := f.login()
				releaseRead := f.holdCounterFlush()
				defer func() {
					if releaseRead != nil {
						releaseRead()
					}
				}()
				type result struct {
					status int
					err    error
				}
				done := make(chan result, 1)
				go func() {
					status, err := guestRequest(f.client, f.base, "/blocked", cookies)
					done <- result{status, err}
				}()
				select {
				case <-entered:
				case <-time.After(5 * time.Second):
					t.Fatal("request did not reach backend")
				}
				want := http.StatusNoContent
				if revoke {
					releaseRead()
					releaseRead = nil
					if _, err := registry.RevokeGuest(f.path, f.grant.ID, accessTestTime); err != nil {
						t.Fatal(err)
					}
					want = http.StatusUnauthorized
				} else {
					release <- struct{}{}
				}
				select {
				case got := <-done:
					t.Logf("backend entered=%d revoke=%t status=%d error=%v", hits.Load(), revoke, got.status, got.err)
					if got.err != nil || got.status != want {
						t.Errorf("response status=%d error=%v; want %d", got.status, got.err, want)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("request did not finish")
				}
				if revoke {
					select {
					case <-canceled:
					case <-time.After(5 * time.Second):
						t.Fatal("revocation did not cancel the real backend request")
					}
					response, _ := f.request("GET", "/after", "", cookies)
					if response.StatusCode != http.StatusUnauthorized || hits.Load() != 1 {
						t.Fatal("post-revoke request reached backend", response.StatusCode, hits.Load())
					}
				}
			})
		}
	}
}

func TestGuestProxyCancellationKeepsBackendFailures(t *testing.T) {
	for _, tc := range []struct {
		name   string
		end    bool
		revoke bool
		err    error
		want   int
	}{
		{"active cancellation", false, false, context.Canceled, http.StatusBadGateway},
		{"ended offline", true, false, errors.New("backend offline"), http.StatusBadGateway},
		{"ended timeout", true, false, context.DeadlineExceeded, http.StatusGatewayTimeout},
		{"revoked offline", false, true, errors.New("backend offline"), http.StatusBadGateway},
		{"revoked cancellation", false, true, fmt.Errorf("transport: %w", context.Canceled), http.StatusUnauthorized},
		{"ended live grant", true, false, context.Canceled, http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newGuestFixture(t, "", false, true)
			g := cleanupGate(f)
			proxy := mustReverseProxy(t, f.svc.Target, nil)
			g.app = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.revoke {
					if _, err := registry.RevokeGuest(f.path, f.grant.ID, accessTestTime); err != nil {
						t.Fatal(err)
					}
				}
				if tc.end {
					r.Context().Value(guestFlightKey{}).(*guestFlight).end()
				}
				proxy.ErrorHandler(w, r, tc.err)
			})
			w := httptest.NewRecorder()
			g.serveApp(w, httptest.NewRequest("GET", "/", nil), f.grant)
			if w.Code != tc.want {
				t.Fatalf("status=%d, want %d; body=%s", w.Code, tc.want, w.Body.String())
			}
			if tc.want == http.StatusServiceUnavailable && w.Header().Get("Retry-After") != "1" {
				t.Fatal("temporary denial lost Retry-After")
			}
		})
	}
	proxy := mustReverseProxy(t, "http://127.0.0.1:1", nil)
	w := httptest.NewRecorder()
	proxy.ErrorHandler(w, httptest.NewRequest("GET", "/", nil), context.Canceled)
	if w.Code != http.StatusBadGateway {
		t.Fatal("non-guest cancellation was reclassified", w.Code)
	}
}
