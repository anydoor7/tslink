package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/anydoor7/tslink/internal/filelock"
	"github.com/anydoor7/tslink/internal/registry"
)

func awaitGuestRecovery(request func() (int, error)) (int, error) {
	deadline := time.Now().Add(5 * time.Second)
	for {
		status, err := request()
		if err != nil || status != http.StatusServiceUnavailable || !time.Now().Before(deadline) {
			return status, err
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestGuestRecoveryRetriesOnlyTemporaryUnavailable(t *testing.T) {
	requestErr := errors.New("request failed")
	for _, tc := range []struct {
		name        string
		statuses    []int
		err         error
		wantCalls   int
		wantElapsed time.Duration
	}{
		{name: "recovered", statuses: []int{503, 503, 204}, wantCalls: 3, wantElapsed: 20 * time.Millisecond},
		{name: "revoked", statuses: []int{401, 204}, wantCalls: 1},
		{name: "transport-error", statuses: []int{503, 204}, err: requestErr, wantCalls: 1},
		{name: "bounded", statuses: []int{503}, wantCalls: 501, wantElapsed: 5 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				calls := 0
				start := time.Now()
				status, err := awaitGuestRecovery(func() (int, error) {
					index := min(calls, len(tc.statuses)-1)
					calls++
					return tc.statuses[index], tc.err
				})
				wantStatus := tc.statuses[min(tc.wantCalls-1, len(tc.statuses)-1)]
				if status != wantStatus || !errors.Is(err, tc.err) || calls != tc.wantCalls || time.Since(start) != tc.wantElapsed {
					t.Fatalf("status=%d err=%v calls=%d elapsed=%s; want %d %v %d %s", status, err, calls, time.Since(start), wantStatus, tc.err, tc.wantCalls, tc.wantElapsed)
				}
			})
		})
	}
}

func TestGuestReadFailureVirtualBoundAndSessionRecovery(t *testing.T) {
	for _, fault := range []string{"invalid-json", "held-writer"} {
		t.Run(fault, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "registry.json")
				svc := registry.Service{Name: "photos", Type: registry.TypeProxy, Target: "http://127.0.0.1:1"}
				if _, err := registry.Add(path, svc); err != nil {
					t.Fatal(err)
				}
				view, _, err := registry.CreateGuest(path, registry.CreateGuestOptions{App: "photos", Value: "2h", PublicAck: true, Now: accessTestTime})
				if err != nil {
					t.Fatal(err)
				}
				nonce := strings.Repeat("s", 43)
				hits := 0
				g := &guestGate{
					path: path, svc: svc, now: func() time.Time { return accessTestTime },
					sessions: map[[32]byte]guestSession{guestKey(nonce): {id: view.ID, expiry: view.ExpiresAt}},
					flights:  make(map[*guestFlight]struct{}),
					app: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						hits++
						w.WriteHeader(http.StatusNoContent)
					}),
				}
				request := func() *httptest.ResponseRecorder {
					r := httptest.NewRequest(http.MethodGet, "https://photos.test/", nil)
					r = r.WithContext(context.WithValue(r.Context(), accessFunnelKey{}, true))
					r.AddCookie(&http.Cookie{Name: guestCookie, Value: nonce})
					w := httptest.NewRecorder()
					g.ServeHTTP(w, r)
					return w
				}
				if w := request(); w.Code != http.StatusNoContent || hits != 1 {
					t.Fatalf("valid-session control: %d, hits=%d", w.Code, hits)
				}
				var restore func()
				wantElapsed := time.Duration(0)
				if fault == "invalid-json" {
					raw, err := os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(path, []byte(`{"guests":`), 0600); err != nil {
						t.Fatal(err)
					}
					restore = func() {
						if err := os.WriteFile(path, raw, 0600); err != nil {
							t.Fatal(err)
						}
					}
				} else {
					lock, err := os.OpenFile(path+".lock", os.O_RDWR, 0600)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { lock.Close() })
					if err := filelock.Lock(lock); err != nil {
						t.Fatal(err)
					}
					wantElapsed = 100 * time.Millisecond
					restore = func() {
						if err := filelock.Unlock(lock); err != nil {
							t.Fatal(err)
						}
					}
				}
				start := time.Now()
				w := request()
				if w.Code != http.StatusServiceUnavailable || w.Header().Get("Retry-After") != "1" || hits != 1 {
					t.Fatalf("fault escaped the gate: %d, hits=%d", w.Code, hits)
				}
				if elapsed := time.Since(start); elapsed != wantElapsed {
					t.Fatalf("fault response took %s virtual time, want %s", elapsed, wantElapsed)
				}
				restore()
				if w := request(); w.Code != http.StatusNoContent || hits != 2 {
					t.Fatalf("read failure discarded the original session: %d, hits=%d", w.Code, hits)
				}
			})
		})
	}
}
