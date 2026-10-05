package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/anydoor7/tslink/internal/filelock"
	"github.com/anydoor7/tslink/internal/registry"
	"github.com/anydoor7/tslink/internal/testwait"
)

// Exercise the same restore notification as the Unix special-file fixture,
// through its real TLS listener, OS watcher and counter monitor.
func TestGuestListenerRestoreFlushOwnership(t *testing.T) {
	f := newGuestFixture(t, "", false, true)
	// Subscribe and prepare the notification before any pending login usage
	// can be consumed. The login helper's own read scope ends on return.
	prepared := sync.OnceFunc(f.holdCounterFlush())
	t.Cleanup(prepared)
	cookies := f.login()
	raw, err := os.ReadFile(f.path)
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unsubscribe := registry.WatchGuestCommits(f.path, func(grants []registry.GuestGrant) {
		once.Do(func() {
			if len(grants) != 1 || grants[0].Sessions != 1 {
				t.Error("restore monitor did not persist the login counter")
			}
			close(entered)
			<-release
		})
	})
	defer unsubscribe()
	defer close(release)
	if err := os.Remove(f.path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	prepared()
	testwait.Recv(t, entered, "restore notification reached the real counter monitor")
	lock, err := os.OpenFile(f.path+".lock", os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if ok, err := filelock.TryReadLock(lock); err != nil || ok {
		t.Fatalf("monitor writer ownership: acquired=%t err=%v", ok, err)
	}
	for _, path := range []string{"/guest/" + f.token, "/"} {
		r, _ := f.request("GET", path, "", cookies)
		if r.StatusCode != 503 || r.Header.Get("Retry-After") != "1" || f.hits.Load() != 0 {
			t.Fatalf("held restore flush: status=%d hits=%d", r.StatusCode, f.hits.Load())
		}
	}
	t.Log("restore monitor owns writer: bearer=503 original-session=503 backend-hits=0")
	owned := make(chan func(), 1)
	go func() { owned <- f.holdCounterFlush() }()
	release <- struct{}{}
	unlock := testwait.Recv(t, owned, "fixture joined released restore flush")
	defer unlock()
	if ok, err := filelock.TryLock(lock); err != nil || ok {
		t.Fatalf("fixture ownership: writer acquired=%t err=%v", ok, err)
	}
	r, _ := f.request("GET", "/", "", cookies)
	if r.StatusCode != 204 || f.hits.Load() != 1 {
		t.Fatalf("restore lost original session: status=%d hits=%d", r.StatusCode, f.hits.Load())
	}
	f.login()
	t.Log("joined restore flush: original-session=204 backend-hits=1 fresh-bearer=303")
}

func TestGuestHealthyRequestFlushOwnership(t *testing.T) {
	f := newGuestFixture(t, "", true, true)
	prepared := sync.OnceFunc(f.holdCounterFlush())
	t.Cleanup(prepared)
	gate := cleanupGate(f)
	app := gate.app
	gate.app = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lock, err := os.OpenFile(f.path+".lock", os.O_RDWR, 0600)
		if err != nil {
			t.Error(err)
			return
		}
		defer lock.Close()
		if acquired, err := filelock.TryLock(lock); err != nil || acquired {
			if acquired {
				filelock.Unlock(lock)
			}
			t.Errorf("healthy request did not retain read ownership through backend: acquired=%t err=%v", acquired, err)
		}
		app.ServeHTTP(w, r)
	})
	cookies := f.login()
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unsubscribe := registry.WatchGuestCommits(f.path, func(grants []registry.GuestGrant) {
		once.Do(func() {
			if len(grants) != 1 || grants[0].Sessions != 1 {
				t.Error("flush did not publish login")
			}
			close(entered)
			<-release
		})
	})
	defer unsubscribe()
	defer close(release)
	raw, err := os.ReadFile(f.path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	prepared()
	testwait.Recv(t, entered, "real monitor flush entered")
	// The raw control reproduces the healthy session's 503, not a latency
	// failure or revocation. The same session recovers without a retry.
	r, _ := f.request("GET", "/control", "", cookies)
	if r.StatusCode != 503 || r.Header.Get("Retry-After") != "1" || f.hits.Load() != 0 {
		t.Fatalf("held flush control: status=%d hits=%d", r.StatusCode, f.hits.Load())
	}
	result := make(chan *http.Response, 1)
	go func() { r, _ := f.healthyRequest("/control", cookies); result <- r }()
	select {
	case r := <-result:
		t.Fatalf("healthy request escaped held writer: %d", r.StatusCode)
	case <-time.After(150 * time.Millisecond):
	}
	release <- struct{}{}
	if r := testwait.Recv(t, result, "healthy request joined released flush"); r.StatusCode != 204 || f.hits.Load() != 1 {
		t.Fatalf("original session: status=%d hits=%d", r.StatusCode, f.hits.Load())
	}
	// A subsequent real mutation must succeed; helper ownership cannot leak
	// into revoke. Its denial still uses the unmodified raw request path.
	if _, err := registry.RevokeGuest(f.path, f.grant.ID, accessTestTime); err != nil {
		t.Fatal(err)
	}
	r, _ = f.healthyRequest("/", nil)
	if r.StatusCode != 401 || f.hits.Load() != 1 {
		t.Fatal("healthy helper masked denial", r.StatusCode)
	}
	defer f.holdCounterFlush()()
	r, _ = f.request("GET", "/", "", cookies)
	if r.StatusCode != 401 || f.hits.Load() != 1 {
		t.Fatal("revoke did not deny original session", r.StatusCode)
	}
	t.Log("real flush: raw control=503 hits=0; scoped same-session control=204 hits=1; released-scope revoke=401 hits=1")
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

// The real monitor flush owns the writer until synchronous commit observers
// return. Hold that boundary to distinguish counter-writer contention from
// cookie loss, a stale grant, or a backend response failure.
func TestGuestMonitorFlushOwnershipAndSessionRecovery(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "registry.json")
		svc := registry.Service{Name: "photos", Type: registry.TypeProxy, Target: "http://127.0.0.1:1"}
		if _, err := registry.Add(path, svc); err != nil {
			t.Fatal(err)
		}
		view, _, err := registry.CreateGuest(path, registry.CreateGuestOptions{App: svc.Name, Value: "2h", PublicAck: true, Now: accessTestTime})
		if err != nil {
			t.Fatal(err)
		}
		nonce := strings.Repeat("s", 43)
		hits := 0
		g := &guestGate{
			path: path, svc: svc, now: func() time.Time { return accessTestTime },
			sessions: map[[32]byte]guestSession{guestKey(nonce): {id: view.ID, expiry: view.ExpiresAt}},
			flights:  make(map[*guestFlight]struct{}), stop: make(chan struct{}), done: make(chan struct{}),
			app: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { hits++; w.WriteHeader(http.StatusNoContent) }),
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
			t.Fatalf("initial session: status=%d hits=%d", w.Code, hits)
		}
		entered, release := make(chan struct{}), make(chan struct{})
		g.unsubscribe = registry.WatchGuestCommits(path, func(grants []registry.GuestGrant) {
			if len(grants) != 1 || grants[0].Uses != 1 {
				t.Error("monitor did not publish the pending usage")
			}
			close(entered)
			<-release
		})
		// Keep the production monitor and its periodic flush, without an OS
		// watcher whose external readiness cannot be driven by virtual time.
		go g.monitor(nil)
		defer func() { g.unsubscribe(); close(g.stop); <-g.done }()
		defer close(release)
		time.Sleep(30 * time.Second)
		synctest.Wait()
		select {
		case <-entered:
		default:
			t.Fatal("real monitor never entered the counter commit")
		}
		lock, err := os.OpenFile(path+".lock", os.O_RDWR, 0600)
		if err != nil {
			t.Fatal(err)
		}
		defer lock.Close()
		if acquired, err := filelock.TryReadLock(lock); err != nil || acquired {
			t.Fatalf("flush ownership control: shared acquired=%t err=%v", acquired, err)
		}
		begin := time.Now()
		_, reason := registry.CheckGuest(path, svc.Name, view.ID, accessTestTime, false, false)
		if reason != "unavailable" || time.Since(begin) != 100*time.Millisecond {
			t.Fatalf("held flush decision=%s elapsed=%s", reason, time.Since(begin))
		}
		begin = time.Now()
		w := request()
		if w.Code != http.StatusServiceUnavailable || w.Header().Get("Retry-After") != "1" || hits != 1 || time.Since(begin) != 100*time.Millisecond {
			t.Fatalf("held flush: status=%d hits=%d elapsed=%s", w.Code, hits, time.Since(begin))
		}
		t.Log("owner=monitor-counter-flush shared-lock=busy reason=unavailable status=503 backend-hits=1 virtual-wait=100ms")
		owned := make(chan func(), 1)
		go func() { owned <- (&guestFixture{t: t, path: path, holdBudget: 5 * time.Second}).holdCounterFlush() }()
		synctest.Wait()
		select {
		case unlock := <-owned:
			unlock()
			t.Fatal("fixture ownership barrier returned while flush still owned writer")
		default:
		}
		// Send rather than close: the deferred close also unblocks failure paths.
		release <- struct{}{}
		time.Sleep(time.Millisecond)
		synctest.Wait()
		var unlock func()
		select {
		case unlock = <-owned:
		default:
			t.Fatal("fixture failed to acquire ownership after flush release")
		}
		defer unlock()
		if acquired, err := filelock.TryLock(lock); err != nil || acquired {
			t.Fatalf("fixture did not retain shared ownership: writer acquired=%t err=%v", acquired, err)
		}
		if w := request(); w.Code != http.StatusNoContent || hits != 2 {
			t.Fatalf("original session after flush: status=%d hits=%d", w.Code, hits)
		}
		t.Log("owner=released shared-lock=acquired original-session-status=204 backend-hits=2")
	})
}
