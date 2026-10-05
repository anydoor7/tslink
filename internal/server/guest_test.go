package server

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/accesslog"
	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/duration"
	"github.com/anydoor7/tslink/internal/filelock"
	"github.com/anydoor7/tslink/internal/registry"
	"github.com/anydoor7/tslink/internal/testwait"
	"tailscale.com/ipn"
)

type guestFixture struct {
	t                      *testing.T
	path, dir, token, base string
	grant                  registry.GuestView
	svc                    registry.Service
	s                      *Server
	store                  *accesslog.Store
	fake                   *accessListenerFake
	client                 *http.Client
	now                    atomic.Int64
	tlsConfig              *tls.Config
	public                 bool
	hits                   atomic.Int64
	received               chan *http.Request
	logPath                string
	// holdBudget bounds holdCounterFlush. It is computed outside any synctest
	// bubble, where t.Deadline panics; bubble fixtures set a virtual duration.
	holdBudget time.Duration
}

func newGuestFixture(t *testing.T, pin string, h2, public bool) *guestFixture {
	t.Helper()
	f := &guestFixture{t: t, public: public, received: make(chan *http.Request, 1000), holdBudget: testwait.Budget(t)}
	f.now.Store(accessTestTime.UnixNano())
	clock := func() time.Time { return time.Unix(0, f.now.Load()).UTC() }
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.hits.Add(1)
		f.received <- r.Clone(context.Background())
		w.Header().Add("Set-Cookie", "app_cookie=unchanged; Path=/; HttpOnly")
		w.Header().Add("Set-Cookie", guestCookie+"=backend-forgery; Secure; Path=/")
		w.Header().Set("Referrer-Policy", "unsafe-url")
		w.WriteHeader(204)
	}))
	t.Cleanup(backend.Close)
	t.Setenv(config.ConfigDirEnv, t.TempDir())
	if e := config.EnsureDir(); e != nil {
		t.Fatal(e)
	}
	f.path, _ = config.RegistryPath()
	f.dir = filepath.Dir(f.path)
	f.svc = registry.Service{Name: "photos", Type: registry.TypeProxy, Target: backend.URL, PreserveHost: true, AllowedUsers: []string{"alice"}}
	if _, e := registry.Add(f.path, f.svc); e != nil {
		t.Fatal(e)
	}
	if _, e := registry.ChangePerson(f.path, "alice", []string{"photos"}, nil, false, false); e != nil {
		t.Fatal(e)
	}
	var e error
	f.grant, f.token, e = registry.CreateGuest(f.path, registry.CreateGuestOptions{App: "photos", Label: "Aunt May", Value: "2h", PIN: pin, PublicAck: true, Now: clock(), Policy: duration.Policy{}})
	if e != nil {
		t.Fatal(e)
	}
	reg, _, e := registry.Preflight(f.path)
	if e != nil {
		t.Fatal(e)
	}
	f.svc = reg.Services[0]
	cert := httptest.NewTLSServer(http.NotFoundHandler())
	t.Cleanup(cert.Close)
	f.tlsConfig = cert.TLS.Clone()
	if h2 {
		f.tlsConfig.NextProtos = []string{"h2", "http/1.1"}
	} else {
		f.tlsConfig.NextProtos = []string{"http/1.1"}
	}
	// The client timeout is only a backstop for a hung synchronous request.
	// It must outlast every testwait guard in the test, or it would end a
	// stream the product failed to end and let that guard pass vacuously.
	f.client = &http.Client{Transport: &http.Transport{TLSClientConfig: cert.Client().Transport.(*http.Transport).TLSClientConfig.Clone(), ForceAttemptHTTP2: h2}, Timeout: 2 * testwait.Budget(t), CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	t.Cleanup(f.client.CloseIdleConnections)
	f.fake = &accessListenerFake{fakeTSNetServer: &fakeTSNetServer{localClient: fakeWhoIsClient(t, whoIsUser("alice", "laptop"), nil), status: funnelEnabledStatus("app.tailnet.ts.net.")}}
	originalNew, originalNow := newTSNetServerFn, serverNowFn
	newTSNetServerFn = func(registry.Service, string, string, string) tsnetServer { return f.fake }
	serverNowFn = clock
	t.Cleanup(func() { newTSNetServerFn = originalNew; serverNowFn = originalNow })
	f.logPath = filepath.Join(f.dir, "daemon-test.log")
	logFile, e := os.Create(f.logPath)
	if e != nil {
		t.Fatal(e)
	}
	oldLog := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(logFile, nil)))
	t.Cleanup(func() { slog.SetDefault(oldLog); logFile.Close() })
	f.s, e = New("test-key", "")
	if e != nil {
		t.Fatal(e)
	}
	f.store, e = accesslog.New(f.dir, accesslog.Options{}, clock)
	if e != nil {
		t.Fatal(e)
	}
	f.s.accessWriter = f.store
	f.start()
	t.Cleanup(func() { f.s.stopNodeLocked("photos"); drainAccess(t, f.store) })
	return f
}
func (f *guestFixture) start() {
	f.t.Helper()
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		f.t.Fatal(e)
	}
	f.base = "https://" + ln.Addr().String()
	var transport net.Listener = ln
	if f.public {
		transport = &accessPublicListener{ln}
	}
	f.fake.ln = tls.NewListener(transport, f.tlsConfig)
	if e = f.s.startNodeLocked(f.t.Context(), f.svc); e != nil {
		f.t.Fatal(e)
	}
}
func (f *guestFixture) request(method, path, body string, cookies []*http.Cookie) (*http.Response, string) {
	f.t.Helper()
	r, e := http.NewRequest(method, f.base+path, strings.NewReader(body))
	if e != nil {
		f.t.Fatal(e)
	}
	for _, c := range cookies {
		r.AddCookie(c)
	}
	if method == "POST" {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	r.Header.Set("Referer", f.base+"/guest/"+f.token)
	r.Header.Set("X-TSLink-Guest-Token", f.token)
	r.Header.Set("X-TSLink-Guest-PIN", "975310")
	response, e := f.client.Do(r)
	if e != nil {
		f.t.Fatal(e)
	}
	raw, e := io.ReadAll(response.Body)
	response.Body.Close()
	if e != nil {
		f.t.Fatal(e)
	}
	return response, string(raw)
}
func (f *guestFixture) login() []*http.Cookie {
	f.t.Helper()
	// This helper expects a healthy, unexpired grant. Establishing its session
	// only queues counters; own the read window instead of racing their flush.
	// Tests of denial or writer contention use request directly.
	defer f.holdCounterFlush()()
	r, _ := f.request("GET", "/guest/"+f.token, "", nil)
	if r.StatusCode != 303 || r.Header.Get("Location") != "/" || r.Header.Get("Referrer-Policy") != "no-referrer" {
		f.t.Fatalf("healthy link: status=%d location=%q retry-after=%q", r.StatusCode, r.Header.Get("Location"), r.Header.Get("Retry-After"))
	}
	cookies := r.Cookies()
	found := false
	for _, c := range cookies {
		if c.Name == guestCookie {
			found = true
			if !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteLaxMode || c.Domain != "" || c.Expires.After(f.grant.ExpiresAt) {
				f.t.Fatalf("cookie attributes: %+v", c)
			}
		}
	}
	if !found {
		f.t.Fatal("no session cookie")
	}
	return cookies
}

// healthyRequest owns only an unexpired GET's read window, including its body.
// It does not retry or change the response. Use request for writer contention,
// expiry latching and PIN mutations; use holdCounterFlush around stream setup
// and release it before revoke, advancing the clock, or Close.
func (f *guestFixture) healthyRequest(path string, cookies []*http.Cookie) (*http.Response, string) {
	f.t.Helper()
	defer f.holdCounterFlush()()
	return f.request("GET", path, "", cookies)
}

// Acquire and retain a shared registry lock: this joins an active counter
// writer and excludes future flushes throughout a read-only assertion window.
// The real monitor keeps running, and requests still perform every grant read.
// Release before PIN writes, expiry latching, revoke, node shutdown or any
// registry API mutation. Direct fault injection is safe while readers own it.
func (f *guestFixture) holdCounterFlush() func() {
	f.t.Helper()
	lock, err := os.OpenFile(f.path+".lock", os.O_RDWR, 0600)
	if err != nil {
		f.t.Fatal(err)
	}
	// Also called inside synctest bubbles and from helper goroutines, where
	// testwait.Until is unavailable; the budget alone bounds a hung writer.
	deadline := time.After(f.holdBudget)
	for {
		acquired, err := filelock.TryReadLock(lock)
		if err != nil {
			lock.Close()
			f.t.Fatal(err)
		}
		if acquired {
			return func() { filelock.Unlock(lock); lock.Close() }
		}
		select {
		case <-deadline:
			lock.Close()
			f.t.Fatal("counter writer did not release the fixture registry")
		case <-time.After(time.Millisecond):
		}
	}
}

// persistedGuestExpired reads the fixture grant's durable expiry latch.
func persistedGuestExpired(t *testing.T, f *guestFixture) bool {
	t.Helper()
	reg, _, err := registry.Preflight(f.path)
	if err != nil {
		t.Fatal(err)
	}
	for _, grant := range reg.Guests {
		if grant.ID == f.grant.ID {
			return grant.Expired
		}
	}
	t.Fatal("fixture grant missing from the registry")
	return false
}
func TestGuestListenerProtocolsAndIsolation(t *testing.T) {
	for _, h2 := range []bool{false, true} {
		t.Run(fmt.Sprintf("http2=%t", h2), func(t *testing.T) {
			f := newGuestFixture(t, "", h2, true)
			for _, path := range []string{"/", "/private-details", "/guest/" + strings.Repeat("x", 43)} {
				r, body := f.request("GET", path, "", nil)
				if r.StatusCode != 401 || !strings.Contains(body, "Reopen the original link") {
					t.Fatalf("ungated %d %s", r.StatusCode, body)
				}
			}
			if f.hits.Load() != 0 {
				t.Fatal("unauthorized backend hits")
			}
			cookies := f.login()
			cookies = append(cookies, &http.Cookie{Name: "app_session", Value: "app-value"})
			r, _ := f.healthyRequest("/?token="+f.token+"&pin=975310&keep=yes", cookies)
			if r.StatusCode != 204 || r.ProtoMajor != map[bool]int{true: 2, false: 1}[h2] || r.Header.Get("Referrer-Policy") != "no-referrer" {
				t.Fatalf("app response: %+v", r)
			}
			backend := <-f.received
			if backend.Host != "app.tailnet.ts.net" || backend.Header.Get("X-Forwarded-Host") != "app.tailnet.ts.net" {
				t.Fatalf("canonical Host: %v", backend.Header)
			}
			if backend.Header.Get("Cookie") != "app_session=app-value" || backend.URL.RawQuery != "keep=yes" || backend.Header.Get("Referer") != "" || backend.Header.Get("X-TSLink-Guest-Token") != "" || backend.Header.Get("X-TSLink-Guest-PIN") != "" || backend.Header.Get("X-Tailscale-User-Login") != "" {
				t.Fatalf("backend secrets: %v %v", backend.Header, backend.URL)
			}
			for _, c := range r.Cookies() {
				if c.Name == guestCookie || c.Name == guestPINCookie {
					t.Fatal("backend overwrote reserved cookie")
				}
			}
			if !strings.Contains(r.Header.Get("Set-Cookie"), "app_cookie=unchanged") {
				t.Fatal("app cookie removed")
			}
			view, e := registry.ShowGuest(f.path, f.grant.ID, accessTestTime)
			if e != nil || view.Uses != 1 || view.Sessions != 1 {
				t.Fatalf("counters %+v %v", view, e)
			}
			f.s.stopNodeLocked("photos")
			f.start()
			r, _ = f.request("GET", "/", "", cookies)
			if r.StatusCode != 401 {
				t.Fatal("old session survived restart")
			}
			f.login() // Persisted grant still works after actual node restart.
			drainAccess(t, f.store)
			log, e := accesslog.Query(f.dir, accesslog.Filter{Limit: 10000})
			if e != nil || log.Summary.Count == 0 {
				t.Fatal("no access log", e)
			}
			allowed := false
			for _, event := range log.Events {
				if event.Kind == "guest" && event.Guest.LinkID == f.grant.ID && event.Guest.Reason == "allowed" {
					allowed = true
				}
			}
			if !allowed {
				t.Fatal("no guest allow event")
			}
			for _, pattern := range []string{"access-log/*.jsonl", "daemon-test.log"} {
				files, _ := filepath.Glob(filepath.Join(f.dir, pattern))
				if len(files) == 0 {
					t.Fatal("privacy probe has no input")
				}
				for _, p := range files {
					b, e := os.ReadFile(p)
					if e != nil || len(b) == 0 {
						t.Fatal("empty privacy control", e)
					}
					for _, secret := range []string{f.token, "975310", cookies[0].Value} {
						if strings.Contains(string(b), secret) {
							t.Fatalf("secret in log %s", p)
						}
					}
				}
			}
		})
	}
}
func TestGuestExpiryAndRevokeMidSession(t *testing.T) {
	for _, reason := range []string{"expired", "revoked"} {
		t.Run(reason, func(t *testing.T) {
			f := newGuestFixture(t, "", false, true)
			cookies := f.login()
			r, _ := f.healthyRequest("/", cookies)
			if r.StatusCode != 204 {
				t.Fatal("control denied")
			}
			hits := f.hits.Load()
			if reason == "expired" {
				f.now.Store(accessTestTime.Add(2 * time.Hour).UnixNano())
			} else {
				if _, e := registry.RevokeGuest(f.path, f.grant.ID, accessTestTime); e != nil {
					t.Fatal(e)
				}
			}
			// Every committed registry write wakes the gate monitor, which then
			// flushes the queued session counters as a registry writer. A read
			// that meets that flush waits 100ms and then fails closed with 503.
			// The revoked denial and the rollback check below are read-only, so
			// they own a read window as login does. The expiry denial must not:
			// it takes the writer lock to persist the rollback latch.
			release := func() {}
			if reason == "revoked" {
				release = f.holdCounterFlush()
			}
			r, _ = f.request("GET", "/", "", cookies)
			release()
			if r.StatusCode != 401 || f.hits.Load() != hits {
				t.Fatalf("dead grant reached backend: status=%d retry-after=%q backend hits %d -> %d", r.StatusCode, r.Header.Get("Retry-After"), hits, f.hits.Load())
			}
			// A rollback cannot resurrect an expired grant.
			if reason == "expired" {
				release = f.holdCounterFlush()
				// The expiry denial is only allowed after its latch is durable.
				if !persistedGuestExpired(t, f) {
					t.Fatal("expiry denial did not persist the rollback latch")
				}
				f.now.Store(accessTestTime.UnixNano())
				r, _ = f.request("GET", "/guest/"+f.token, "", nil)
				release()
				if r.StatusCode != 401 {
					t.Fatalf("rollback revived expiry: status=%d retry-after=%q", r.StatusCode, r.Header.Get("Retry-After"))
				}
			}
			drainAccess(t, f.store)
			log, e := accesslog.Query(f.dir, accesslog.Filter{Decision: "denied"})
			if e != nil {
				t.Fatal(e)
			}
			found := false
			for _, event := range log.Events {
				if event.Kind == "guest" && event.Guest.LinkID == f.grant.ID && event.Guest.Reason == reason {
					found = true
				}
			}
			if !found {
				t.Fatalf("missing %s event", reason)
			}
		})
	}
}
func TestGuestPINListener(t *testing.T) {
	f := newGuestFixture(t, "975310", true, true)
	form, body := f.healthyRequest("/guest/"+f.token, nil)
	if form.StatusCode != 200 || strings.Contains(body, f.token) || form.Header.Get("Referrer-Policy") != "no-referrer" {
		t.Fatal("invalid PIN form")
	}
	csrf := regexp.MustCompile(`name="csrf" value="([^"]+)"`).FindStringSubmatch(body)
	if len(csrf) != 2 {
		t.Fatal(body)
	}
	cookies := form.Cookies()
	for _, body := range []string{"pin=975310", "csrf=wrong&pin=975310", strings.Repeat("x", 2048)} {
		r, _ := f.request("POST", "/guest/pin", body, cookies)
		if r.StatusCode != 401 {
			t.Fatal("CSRF/body limit bypass")
		}
	}
	wrong := url.Values{"csrf": {csrf[1]}, "pin": {"111111"}}.Encode()
	for i := range 5 {
		want := 200
		if i == 4 {
			want = 401
		}
		r, _ := f.request("POST", "/guest/pin", wrong, cookies)
		if r.StatusCode != want {
			t.Fatal("bad PIN allowed")
		}
	}
	good := url.Values{"csrf": {csrf[1]}, "pin": {"975310"}}.Encode()
	r, _ := f.request("POST", "/guest/pin", good, cookies)
	if r.StatusCode != 401 {
		t.Fatal("PIN lockout bypass")
	}
	f.now.Store(accessTestTime.Add(16 * time.Minute).UnixNano())
	form, body = f.healthyRequest("/guest/"+f.token, nil)
	csrf = regexp.MustCompile(`name="csrf" value="([^"]+)"`).FindStringSubmatch(body)
	if len(csrf) != 2 {
		t.Fatal(body)
	}
	good = url.Values{"csrf": {csrf[1]}, "pin": {"975310"}}.Encode()
	r, _ = f.request("POST", "/guest/pin", good, form.Cookies())
	if r.StatusCode != 303 {
		t.Fatal("correct PIN denied after lockout", r.StatusCode)
	}
	release := f.holdCounterFlush()
	defer release()
	r, _ = f.request("GET", "/", "", r.Cookies())
	if r.StatusCode != 204 || f.hits.Load() != 1 {
		t.Fatalf("PIN session denied: status=%d backend-hits=%d", r.StatusCode, f.hits.Load())
	}
	drainAccess(t, f.store)
	log, e := accesslog.Query(f.dir, accesslog.Filter{})
	if e != nil {
		t.Fatal(e)
	}
	reasons := map[string]bool{}
	for _, event := range log.Events {
		if event.Kind == "guest" {
			reasons[event.Guest.Reason] = true
		}
	}
	for _, reason := range []string{"bad_pin", "rate_limited", "csrf", "allowed"} {
		if !reasons[reason] {
			t.Fatal("missing reason", reason)
		}
	}
	files, _ := filepath.Glob(filepath.Join(f.dir, "access-log", "*.jsonl"))
	for _, p := range append(files, f.logPath) {
		raw, e := os.ReadFile(p)
		if e != nil || len(raw) == 0 {
			t.Fatal("empty privacy input", e)
		}
		for _, secret := range []string{f.token, "975310", "111111", csrf[1]} {
			if strings.Contains(string(raw), secret) {
				t.Fatal("PIN privacy leak")
			}
		}
	}
}
func TestGuestTailnetUnaffected(t *testing.T) {
	f := newGuestFixture(t, "975310", false, false)
	r, _ := f.request("GET", "/", "", []*http.Cookie{{Name: guestCookie, Value: "ignored"}, {Name: "app_session", Value: "app-value"}})
	if r.StatusCode != 204 || f.hits.Load() != 1 {
		t.Fatal("tailnet denied")
	}
	backend := <-f.received
	if backend.Header.Get("X-Tailscale-User-Login") != "alice" || backend.Header.Get("Cookie") != "app_session=app-value" {
		t.Fatal("tailnet behavior changed", backend.Header)
	}
	if _, e := registry.RemovePerson(f.path, "alice"); e != nil {
		t.Fatal(e)
	}
	r, _ = f.request("GET", "/", "", nil)
	if r.StatusCode != 403 || f.hits.Load() != 1 {
		t.Fatal("tailnet tombstone bypass")
	}
}
func TestGuestConcurrentRevokeRequests(t *testing.T) {
	f := newGuestFixture(t, "", true, true)
	cookies := f.login()
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			r, e := http.NewRequest("GET", f.base+"/", nil)
			if e != nil {
				t.Error(e)
				return
			}
			for _, c := range cookies {
				r.AddCookie(c)
			}
			response, e := f.client.Do(r)
			if e != nil {
				t.Error(e)
				return
			}
			io.Copy(io.Discard, response.Body)
			response.Body.Close()
			if response.StatusCode != 204 && response.StatusCode != 401 && response.StatusCode != 503 {
				t.Error(response.StatusCode)
			}
		})
	}
	if _, e := registry.RevokeGuest(f.path, f.grant.ID, accessTestTime); e != nil {
		t.Fatal(e)
	}
	wg.Wait()
	hits := f.hits.Load()
	for range 3 {
		r, _ := f.request("GET", "/", "", cookies)
		if r.StatusCode != 401 {
			t.Fatal("revoked concurrent session authorized")
		}
	}
	if f.hits.Load() != hits {
		t.Fatal("post-revoke backend access")
	}
}
func TestGuestListenerCorruptAndMissingRegistry(t *testing.T) {
	f := newGuestFixture(t, "", false, true)
	cookies := f.login()
	defer f.holdCounterFlush()()
	raw, e := os.ReadFile(f.path)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(f.path, []byte(`{"guests":`), 0600); e != nil {
		t.Fatal(e)
	}
	r, _ := f.request("GET", "/", "", cookies)
	if r.StatusCode != 503 || r.Header.Get("Retry-After") != "1" || f.hits.Load() != 0 {
		t.Fatal("corrupt registry fail-open")
	}
	if e = os.Remove(f.path); e != nil {
		t.Fatal(e)
	}
	r, _ = f.request("GET", "/", "", cookies)
	if r.StatusCode != 503 || r.Header.Get("Retry-After") != "1" || f.hits.Load() != 0 {
		t.Fatal("missing registry fail-open")
	}
	if e = os.WriteFile(f.path, raw, 0600); e != nil {
		t.Fatal(e)
	}
	// Temporary unavailability preserves the original session; no re-login.
	r, _ = f.request("GET", "/", "", cookies)
	if r.StatusCode != 204 || f.hits.Load() != 1 {
		t.Fatalf("restored original session denied: status=%d backend-hits=%d", r.StatusCode, f.hits.Load())
	}
}

func TestGuestSourceLimitAcrossGrants(t *testing.T) {
	f := newGuestFixture(t, "975310", false, true)
	for i := range 3 {
		if i > 0 {
			_, token, e := registry.CreateGuest(f.path, registry.CreateGuestOptions{App: "photos", Value: "2h", PIN: "975310", Now: accessTestTime, Policy: duration.Policy{}})
			if e != nil {
				t.Fatal(e)
			}
			f.token = token
		}
		form, body := f.healthyRequest("/guest/"+f.token, nil)
		csrf := regexp.MustCompile(`name="csrf" value="([^"]+)"`).FindStringSubmatch(body)
		if len(csrf) != 2 {
			t.Fatal(body)
		}
		attempts := 5
		if i == 2 {
			attempts = 1
		}
		for attempt := range attempts {
			pin := "111111"
			if i == 2 {
				pin = "975310"
			}
			r, _ := f.request("POST", "/guest/pin", url.Values{"csrf": {csrf[1]}, "pin": {pin}}.Encode(), form.Cookies())
			want := 200
			if i == 2 || attempt == 4 {
				want = 401
			}
			if r.StatusCode != want {
				t.Fatal("source limit bypass")
			}
		}
	}
	if f.hits.Load() != 0 {
		t.Fatal("PIN denial reached backend")
	}
	drainAccess(t, f.store)
	log, e := accesslog.Query(f.dir, accesslog.Filter{})
	if e != nil {
		t.Fatal(e)
	}
	limited := false
	for _, event := range log.Events {
		if event.Kind == "guest" && event.Guest.Reason == "rate_limited" {
			limited = true
		}
	}
	if !limited {
		t.Fatal("source limit not audited")
	}
}

func TestGuestWriterContentionDeniesPromptly(t *testing.T) {
	f := newGuestFixture(t, "", false, true)
	cookies := f.login()
	lock, e := os.OpenFile(f.path+".lock", os.O_RDWR, 0600)
	if e != nil {
		t.Fatal(e)
	}
	defer lock.Close()
	if e = filelock.Lock(lock); e != nil {
		t.Fatal(e)
	}
	// The writer stays held across the request, so a reader that waited for
	// it would hang here. The 100ms reader bound itself is pinned in virtual
	// time by TestGuestReadFailureVirtualBoundAndSessionRecovery/held-writer.
	r, _ := f.request("GET", "/", "", cookies)
	if r.StatusCode != 503 || r.Header.Get("Retry-After") != "1" || f.hits.Load() != 0 {
		t.Fatal("registry writer stalled or fail-open", r.StatusCode)
	}
	if e = filelock.Unlock(lock); e != nil {
		t.Fatal(e)
	}
	defer f.holdCounterFlush()()
	r, _ = f.request("GET", "/", "", cookies)
	if r.StatusCode != 204 || f.hits.Load() != 1 {
		t.Fatalf("writer release lost original session: status=%d hits=%d", r.StatusCode, f.hits.Load())
	}
}
func TestGuestOriginAndChallengeExpiry(t *testing.T) {
	f := newGuestFixture(t, "975310", false, true)
	form, body := f.healthyRequest("/guest/"+f.token, nil)
	csrf := regexp.MustCompile(`name="csrf" value="([^"]+)"`).FindStringSubmatch(body)
	if len(csrf) != 2 {
		t.Fatal(body)
	}
	for _, origin := range []string{"https://attacker.example", "null", f.base + "/bad"} {
		r, e := http.NewRequest("POST", f.base+"/guest/pin", strings.NewReader(url.Values{"csrf": {csrf[1]}, "pin": {"975310"}}.Encode()))
		if e != nil {
			t.Fatal(e)
		}
		r.Header.Set("Origin", origin)
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		for _, c := range form.Cookies() {
			r.AddCookie(c)
		}
		response, e := f.client.Do(r)
		if e != nil {
			t.Fatal(e)
		}
		io.Copy(io.Discard, response.Body)
		response.Body.Close()
		if response.StatusCode != 401 {
			t.Fatal("cross-origin form accepted")
		}
	}
	f.now.Store(accessTestTime.Add(6 * time.Minute).UnixNano())
	r, _ := f.request("POST", "/guest/pin", url.Values{"csrf": {csrf[1]}, "pin": {"975310"}}.Encode(), form.Cookies())
	if r.StatusCode != 401 || f.hits.Load() != 0 {
		t.Fatal("expired challenge allowed")
	}
	r, _ = f.request("POST", "/guest/"+f.token, "pin=975310", nil)
	if r.StatusCode != 401 {
		t.Fatal("unexpected link method allowed")
	}
}

func TestGuestUnauthenticatedPartialUploadLanding(t *testing.T) {
	f := newGuestFixture(t, "", false, true)
	addr := strings.TrimPrefix(f.base, "https://")
	c, e := tls.Dial("tcp", addr, f.client.Transport.(*http.Transport).TLSClientConfig)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	if e = c.SetDeadline(time.Now().Add(testwait.Budget(t))); e != nil {
		t.Fatal(e)
	}
	// No body byte is ever sent, so a landing that waited for the declared
	// 32 MiB would never respond; that hang, not elapsed time, is the failure.
	if _, e = fmt.Fprintf(c, "POST / HTTP/1.1\r\nHost: %s\r\nContent-Length: 33554433\r\n\r\n", addr); e != nil {
		t.Fatal(e)
	}
	r, e := http.ReadResponse(bufio.NewReader(c), nil)
	if e != nil {
		t.Fatal(e)
	}
	defer r.Body.Close()
	raw, e := io.ReadAll(r.Body)
	if e != nil || r.StatusCode != 401 || !strings.Contains(string(raw), "Reopen the original link") || f.hits.Load() != 0 {
		t.Fatal("unauthenticated upload exposed backend/limits or stalled", r.StatusCode, e)
	}
}

// Capacity controls use the same HTTP middleware/listener functions as node
// startup. The grant, proxy, TLS and audit store are real local objects; only
// the bounded server-side map contents are seeded to reach exhaustion.
func guestMemoryListener(t *testing.T, f *guestFixture) *guestGate {
	t.Helper()
	f.s.stopNodeLocked("photos")
	clock := func() time.Time { return time.Unix(0, f.now.Load()).UTC() }
	identity := NewStaticIdentityResolver(f.fake.localClient)
	app, e := NewProxyHandlerWithOptions(f.svc.Target, identity, ProxyOptions{PreserveHost: true, CanonicalHost: func() string { return "app.tailnet.ts.net" }})
	if e != nil {
		t.Fatal(e)
	}
	private := RequestLimitsMiddleware(f.svc, nil, peopleMiddleware(f.path, f.svc, f.fake.LocalClient, clock)(app))
	gate := newGuestGate(f.path, f.svc, clock, f.store, private, RequestLimitsMiddleware(f.svc, nil, app)).(*guestGate)
	t.Cleanup(func() { _ = gate.Close() })
	handler := AccessEventMiddleware(f.svc, accesslog.Options{}, f.store, identity, clock, gate)
	srv := newHTTPServerFn(handler)
	configureAccessHTTP(srv)
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	f.base = "https://" + ln.Addr().String()
	var listener net.Listener = newLimitedListener(tls.NewListener(&guestAttestedSourceListener{ln}, f.tlsConfig), httpMaxActiveConns, "http", "photos")
	listener = configureServiceHTTP(srv, f.svc, listener, nil)
	done := make(chan error, 1)
	go func() { done <- srv.Serve(listener) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), testwait.Budget(t))
		defer cancel()
		if e := srv.Shutdown(ctx); e != nil {
			t.Error(e)
		}
		if e := <-done; e != nil && e != http.ErrServerClosed {
			t.Error(e)
		}
	})
	return gate
}
func TestGuestMemoryCapacityAndRecovery(t *testing.T) {
	for _, kind := range []string{"sessions", "challenges", "sources"} {
		t.Run(kind, func(t *testing.T) {
			pin := ""
			if kind != "sessions" {
				pin = "975310"
			}
			f := newGuestFixture(t, pin, true, true)
			g := guestMemoryListener(t, f)
			g.mu.Lock()
			for i := range 4096 {
				k := guestKey(fmt.Sprintf("synthetic-map-%d", i))
				entry := guestSession{id: f.grant.ID, expiry: accessTestTime.Add(time.Hour)}
				switch kind {
				case "sessions":
					g.sessions[k] = entry
				case "challenges":
					g.challenges[k] = entry
				case "sources":
					g.sources[fmt.Sprintf("synthetic-source-%d", i)] = guestSource{until: entry.expiry}
				}
			}
			g.mu.Unlock()
			form, body := f.healthyRequest("/guest/"+f.token, nil)
			if kind == "sources" {
				csrf := regexp.MustCompile(`name="csrf" value="([^"]+)"`).FindStringSubmatch(body)
				if len(csrf) != 2 {
					t.Fatal(body)
				}
				form, _ = f.request("POST", "/guest/pin", url.Values{"csrf": {csrf[1]}, "pin": {"975310"}}.Encode(), form.Cookies())
			}
			if form.StatusCode != 401 || f.hits.Load() != 0 {
				t.Fatal("map exhaustion fail-open", kind)
			}
			view, e := registry.ShowGuest(f.path, f.grant.ID, accessTestTime)
			if e != nil || view.Sessions != 0 || view.PINFailures != 0 {
				t.Fatal("failed capacity consumed successful session or PIN", view, e)
			}
			f.now.Store(accessTestTime.Add(61 * time.Minute).UnixNano())
			form, body = f.healthyRequest("/guest/"+f.token, nil)
			if kind == "sessions" {
				if form.StatusCode != 303 {
					t.Fatal("expired sessions did not prune")
				}
			} else {
				csrf := regexp.MustCompile(`name="csrf" value="([^"]+)"`).FindStringSubmatch(body)
				if len(csrf) != 2 {
					t.Fatal("expired map did not prune")
				}
				form, _ = f.request("POST", "/guest/pin", url.Values{"csrf": {csrf[1]}, "pin": {"975310"}}.Encode(), form.Cookies())
				if form.StatusCode != 303 {
					t.Fatal("capacity recovery denied")
				}
			}
			r, _ := f.healthyRequest("/", form.Cookies())
			if r.StatusCode != 204 || f.hits.Load() != 1 {
				t.Fatal("capacity recovery backend control")
			}
		})
	}
}

type guestAttestedSourceListener struct{ net.Listener }

func (l *guestAttestedSourceListener) Accept() (net.Conn, error) {
	c, e := l.Listener.Accept()
	if e != nil {
		return nil, e
	}
	return &ipn.FunnelConn{Conn: c, Src: netip.MustParseAddrPort("203.0.113.25:50000")}, nil
}
func TestGuestSourceUsesTrustedFunnelAddress(t *testing.T) {
	f := newGuestFixture(t, "975310", true, true)
	g := guestMemoryListener(t, f)
	form, body := f.healthyRequest("/guest/"+f.token, nil)
	csrf := regexp.MustCompile(`name="csrf" value="([^"]+)"`).FindStringSubmatch(body)
	if len(csrf) != 2 {
		t.Fatal(body)
	}
	r, e := http.NewRequest("POST", f.base+"/guest/pin", strings.NewReader(url.Values{"csrf": {csrf[1]}, "pin": {"111111"}}.Encode()))
	if e != nil {
		t.Fatal(e)
	}
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("X-Forwarded-For", "192.0.2.99")
	r.Header.Set("Tailscale-Ingress-Src", "192.0.2.99")
	for _, c := range form.Cookies() {
		r.AddCookie(c)
	}
	response, e := f.client.Do(r)
	if e != nil {
		t.Fatal(e)
	}
	io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal("bad PIN allowed")
	}
	g.mu.Lock()
	source := g.sources["203.0.113.25"]
	count := len(g.sources)
	g.mu.Unlock()
	if count != 1 || source.attempts != 1 {
		t.Fatal("source was not the trusted Funnel client")
	}
	response, _ = f.request("POST", "/guest/pin", url.Values{"csrf": {csrf[1]}, "pin": {"975310"}}.Encode(), form.Cookies())
	if response.StatusCode != 303 {
		t.Fatal("source positive control denied")
	}
}
