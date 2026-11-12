package server

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"runtime/debug"
	"sync"
	"syscall"
	"testing"
	"time"
	"weak"

	"github.com/anydoor7/tslink/internal/registry"
	"tailscale.com/tsnet"
)

// fakeTimers is a manual scheduler: nothing fires until the test says so.
type fakeTimers struct {
	mu      sync.Mutex
	pending []func()
	armed   int
}

func (f *fakeTimers) after(_ time.Duration, fn func()) func() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.armed++
	f.pending = append(f.pending, fn)
	return func() bool { return true }
}

func (f *fakeTimers) fireAll() {
	f.mu.Lock()
	p := f.pending
	f.pending = nil
	f.mu.Unlock()
	for _, fn := range p {
		fn()
	}
}

func newTestMonitor(timers *fakeTimers) *handshakeMonitor {
	m := newHandshakeMonitor("photos", nil)
	m.after = timers.after
	return m
}

func (h *captureHandler) byMessage(msg string) []slog.Record {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []slog.Record
	for _, r := range h.records {
		if r.Message == msg {
			out = append(out, r)
		}
	}
	return out
}

func (h *captureHandler) byCode(code string) []map[string]any {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []map[string]any
	for _, r := range h.records {
		m := map[string]any{"msg": r.Message}
		r.Attrs(func(a slog.Attr) bool { m[a.Key] = a.Value.Any(); return true })
		if m["code"] == code {
			out = append(out, m)
		}
	}
	return out
}

func TestClassifyHandshakeError(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		class      string
		detail     string
		isTeardown bool
	}{
		{"deadline", context.DeadlineExceeded, "timeout", "", false},
		{"net timeout", &net.OpError{Op: "read", Err: timeoutErr{}}, "timeout", "", false},
		{"eof", io.EOF, "eof", "", false},
		{"unexpected eof", fmt.Errorf("tls: %w", io.ErrUnexpectedEOF), "eof", "", false},
		{"reset", &net.OpError{Op: "read", Err: syscall.ECONNRESET}, "reset", "", false},
		// Built exactly as gonet builds them: errors.New(tcpip error String()).
		{"gonet reset", &net.OpError{Op: "read", Net: "tcp", Err: errors.New("connection reset by peer")}, "reset", "", false},
		{"gonet aborted", &net.OpError{Op: "read", Err: errors.New("connection aborted")}, "reset", "", false},
		{"gonet broken pipe", &net.OpError{Op: "write", Err: errors.New("broken pipe")}, "reset", "", false},
		{"gonet closed for send", &net.OpError{Op: "write", Err: errors.New("endpoint is closed for send")}, "reset", "", false},
		{"gonet timeout", &net.OpError{Op: "read", Err: &gonetTimeout{}}, "timeout", "", false},
		{"bad record", tls.RecordHeaderError{Msg: "first record does not look like a TLS handshake"}, "bad_record", "", false},
		{"client alert", &net.OpError{Op: "remote error", Err: errors.New("tls: bad certificate")}, "client_alert", "tls: bad certificate", false},
		{"protocol", errors.New("tls: client offered only unsupported versions: [301]"), "protocol", "", false},
		{"other", errors.New("boom"), "other", "", false},
		{"closed", net.ErrClosed, "other", "", true},
		{"canceled", context.Canceled, "other", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			class, detail := classifyHandshakeError(tc.err)
			if class != tc.class || detail != tc.detail {
				t.Fatalf("classify = %q,%q want %q,%q", class, detail, tc.class, tc.detail)
			}
			if handshakeTeardown(tc.err) != tc.isTeardown {
				t.Fatalf("teardown = %v want %v", !tc.isTeardown, tc.isTeardown)
			}
		})
	}
}

type gonetTimeout struct{}

func (*gonetTimeout) Error() string   { return "i/o timeout" }
func (*gonetTimeout) Timeout() bool   { return true }
func (*gonetTimeout) Temporary() bool { return true }

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return false }

func TestHandshakeMonitorAggregatesPerWindow(t *testing.T) {
	old := slog.Default()
	logs := installCaptureLogger()
	t.Cleanup(func() { slog.SetDefault(old) })
	timers := &fakeTimers{}
	m := newTestMonitor(timers)

	for i := 0; i < 500; i++ { // a scanner flood
		m.observe("100.64.0.9:5000", 30*time.Millisecond, io.EOF, helloUntracked)
	}
	m.observe("100.64.0.9:5000", 2*time.Second, &net.OpError{Op: "read", Err: timeoutErr{}}, helloUntracked)
	if timers.armed != 1 {
		t.Fatalf("one window must arm one timer, armed %d", timers.armed)
	}
	if got := len(logs.byCode("tls.handshake.failed")); got != 0 {
		t.Fatalf("nothing may be logged before the window ends, got %d", got)
	}
	timers.fireAll()
	recs := logs.byCode("tls.handshake.failed")
	if len(recs) != 1 {
		t.Fatalf("want exactly one aggregated record for 501 failures, got %d", len(recs))
	}
	r := recs[0]
	if r["count"] != int64(501) || r["first_class"] != "eof" || r["classes"] != "eof=500,timeout=1" || r["name"] != "photos" || r["max_duration_ms"] != int64(2000) || r["total_failed"] != int64(501) {
		t.Fatalf("unexpected record: %#v", r)
	}
	if r["first_client"] != "unknown" {
		t.Fatalf("no resolver must yield unknown, got %v", r["first_client"])
	}
	for k, v := range r {
		if s, ok := v.(string); ok && (s == "100.64.0.9:5000" || s == "100.64.0.9") {
			t.Fatalf("record leaks the client address in %s", k)
		}
	}

	// A new window starts on the next event and logs again.
	m.observe("100.64.0.9:5001", time.Millisecond, io.EOF, helloUntracked)
	timers.fireAll()
	if got := len(logs.byCode("tls.handshake.failed")); got != 2 {
		t.Fatalf("second window must produce a second record, got %d total", got)
	}
}

func TestHandshakeMonitorSlowAndTeardown(t *testing.T) {
	old := slog.Default()
	logs := installCaptureLogger()
	t.Cleanup(func() { slog.SetDefault(old) })
	timers := &fakeTimers{}
	m := newTestMonitor(timers)

	m.observe("a:1", 100*time.Millisecond, nil, helloUntracked) // fast: silent
	m.observe("a:1", 2*time.Second, nil, helloUntracked)        // exactly at threshold: slow
	m.observe("a:1", 5*time.Second, nil, helloUntracked)
	m.observe("a:1", time.Second, net.ErrClosed, helloUntracked)    // teardown: not counted
	m.observe("a:1", time.Second, context.Canceled, helloUntracked) // teardown: not counted
	timers.fireAll()
	if got := len(logs.byCode("tls.handshake.failed")); got != 0 {
		t.Fatalf("teardown errors must not be logged as failures, got %d", got)
	}
	slow := logs.byCode("tls.handshake.slow")
	if len(slow) != 1 || slow[0]["count"] != int64(2) || slow[0]["max_duration_ms"] != int64(5000) || slow[0]["threshold_ms"] != int64(2000) {
		t.Fatalf("unexpected slow records: %#v", slow)
	}
	if m.Succeeded.Load() != 3 || m.Failed.Load() != 0 || m.Slow.Load() != 2 {
		t.Fatalf("counters = %d/%d/%d", m.Succeeded.Load(), m.Failed.Load(), m.Slow.Load())
	}
}

func TestHandshakeMonitorCloseFlushesPending(t *testing.T) {
	old := slog.Default()
	logs := installCaptureLogger()
	t.Cleanup(func() { slog.SetDefault(old) })
	m := newTestMonitor(&fakeTimers{})
	m.observe("a:1", time.Millisecond, io.EOF, helloUntracked)
	m.close()
	m.close()
	m.observe("a:1", time.Millisecond, io.EOF, helloUntracked) // after close: counted, not logged
	if got := len(logs.byCode("tls.handshake.failed")); got != 1 {
		t.Fatalf("close must emit the pending window once, got %d", got)
	}
}

func TestHandshakeMonitorNilSafe(t *testing.T) {
	var m *handshakeMonitor
	m.observe("a:1", time.Hour, io.EOF, helloUntracked)
	m.close()
}

// A failed handshake against the real listener chain (limited listener under
// configureServiceHTTP, served by net/http) yields one aggregated record, and a
// good handshake yields none.
func TestFailedHandshakeProducesOneAggregatedRecord(t *testing.T) {
	old := slog.Default()
	logs := installCaptureLogger()
	t.Cleanup(func() { slog.SetDefault(old) })

	cert := httptest.NewTLSServer(http.NotFoundHandler())
	defer cert.Close()
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	limited := newLimitedListener(tls.NewListener(raw, cert.TLS.Clone()), 8, "http", "photos", nil).(*limitedListener)
	timers := &fakeTimers{}
	limited.handshakes = newTestMonitor(timers)
	svc := registry.Service{Name: "photos", Type: registry.TypeProxy}
	srv := newHTTPServerFn(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	ln := configureServiceHTTP(srv, svc, limited, nil)
	done := make(chan struct{})
	go func() { defer close(done); _ = srv.Serve(ln) }()
	defer func() { srv.Close(); ln.Close(); <-done }()

	// Good request first: no records.
	tr := &http.Transport{TLSClientConfig: cert.Client().Transport.(*http.Transport).TLSClientConfig.Clone()}
	defer tr.CloseIdleConnections()
	resp, err := (&http.Client{Transport: tr}).Get("https://" + raw.Addr().String() + "/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	waitHandshakeCond(t, func() bool { return limited.handshakes.Succeeded.Load() == 1 })

	// Three plaintext clients fail the handshake.
	for i := 0; i < 3; i++ {
		c, err := net.Dial("tcp", raw.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprint(c, "GET / HTTP/1.1\r\nHost: x\r\n\r\n")
		_, _ = io.Copy(io.Discard, c)
		c.Close()
	}
	waitHandshakeCond(t, func() bool { return limited.handshakes.Failed.Load() == 3 })
	timers.fireAll()
	recs := logs.byCode("tls.handshake.failed")
	if len(recs) != 1 {
		t.Fatalf("want exactly one aggregated record, got %d: %#v", len(recs), recs)
	}
	if recs[0]["count"] != int64(3) || recs[0]["first_class"] != "bad_record" || recs[0]["name"] != "photos" {
		t.Fatalf("unexpected record: %#v", recs[0])
	}
	if got := len(logs.byCode("tls.handshake.slow")); got != 0 {
		t.Fatalf("fast handshakes must not log slow records, got %d", got)
	}
	if got := limited.handshakes.Succeeded.Load(); got != 1 {
		t.Fatalf("one good connection must count as one handshake (net/http and the header budget both call HandshakeContext), got %d", got)
	}
}

func waitHandshakeCond(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in 5s")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestHandshakeMonitorSplitsTimeoutByHello(t *testing.T) {
	old := slog.Default()
	logs := installCaptureLogger()
	t.Cleanup(func() { slog.SetDefault(old) })
	timers := &fakeTimers{}
	m := newTestMonitor(timers)
	to := &net.OpError{Op: "read", Err: timeoutErr{}}
	m.observe("a:1", time.Second, to, helloNotSeen)
	m.observe("a:1", time.Second, to, helloNotSeen)
	m.observe("a:1", time.Second, to, helloSeen)
	m.observe("a:1", time.Second, to, helloUntracked)
	timers.fireAll()
	recs := logs.byCode("tls.handshake.failed")
	if len(recs) != 1 || recs[0]["classes"] != "timeout=1,timeout_after_hello=1,timeout_before_hello=2" || recs[0]["first_class"] != "timeout_before_hello" {
		t.Fatalf("unexpected: %#v", recs)
	}
}

// Real timers, many goroutines observing while the monitor closes: run with
// -race. Every counted failure is accounted for in the emitted records or the
// post-close drop, and nothing deadlocks.
func TestHandshakeMonitorConcurrentObserveAndClose(t *testing.T) {
	old := slog.Default()
	logs := installCaptureLogger()
	t.Cleanup(func() { slog.SetDefault(old) })
	m := newHandshakeMonitor("photos", nil)
	m.window = 2 * time.Millisecond
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				m.observe("100.64.0.1:1", 3*time.Second, io.EOF, helloUntracked)
				m.observe("100.64.0.1:1", time.Millisecond, nil, helloUntracked)
			}
		}()
	}
	time.Sleep(60 * time.Millisecond)
	done := make(chan struct{})
	go func() { m.close(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("close blocked")
	}
	close(stop)
	wg.Wait()
	time.Sleep(20 * time.Millisecond) // a timer that raced close must find nothing
	var logged int64
	for _, r := range logs.byCode("tls.handshake.failed") {
		logged += r["count"].(int64)
	}
	if logged == 0 || logged > m.Failed.Load() {
		t.Fatalf("logged %d of %d counted failures", logged, m.Failed.Load())
	}
}

// The WhoIs lookup must never run on the handshake goroutine, and Close must
// not wait for it.
func TestHandshakeIdentityLookupNeverBlocksHandshakeOrClose(t *testing.T) {
	old := slog.Default()
	installCaptureLogger()
	t.Cleanup(func() { slog.SetDefault(old) })

	entered := make(chan struct{}, 4)
	release := make(chan struct{})
	resolver := NewIdentityResolver(func() (*LocalClient, error) {
		entered <- struct{}{}
		<-release
		return nil, errors.New("released")
	})
	var releaseOnce sync.Once
	releaseResolver := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseResolver()

	cert := httptest.NewTLSServer(http.NotFoundHandler())
	defer cert.Close()
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	limited := newLimitedListener(newHelloTrackingTLSListener(raw, cert.TLS.Clone()), 8, "http", "photos", resolver).(*limitedListener)
	timers := &fakeTimers{}
	limited.handshakes.after = timers.after
	srv := newHTTPServerFn(http.NotFoundHandler())
	ln := configureServiceHTTP(srv, registry.Service{Name: "photos", Type: registry.TypeProxy}, limited, nil)
	done := make(chan struct{})
	go func() { defer close(done); _ = srv.Serve(ln) }()
	defer func() { srv.Close(); ln.Close(); <-done }()

	c, err := net.Dial("tcp", raw.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprint(c, "plaintext")
	_, _ = io.Copy(io.Discard, c) // returns once the server finished the failed handshake
	c.Close()
	waitHandshakeCond(t, func() bool { return limited.handshakes.Failed.Load() == 1 })
	select {
	case <-entered:
		t.Fatal("resolver ran before the window ended: lookup is on the handshake path")
	default:
	}

	flushed := make(chan struct{})
	go func() { timers.fireAll(); close(flushed) }() // lookup blocks here, off the handshake path
	<-entered
	m := limited.handshakes
	m.observe("100.64.0.9:1", time.Millisecond, io.EOF, helloUntracked) // new window while a flush is blocked
	closed := make(chan struct{})
	go func() { m.close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("close waited for the blocked resolver")
	}
	select {
	case <-flushed:
		t.Fatal("flush should still be blocked on the resolver")
	default:
	}
	// Let the blocked flush finish before the test's logger is restored.
	releaseResolver()
	<-flushed
}

// startTrackedChain serves a real chain (hello-tracking TLS listener, capped
// listener, header-budget listener, net/http) and returns its address, monitor
// and fake timers.
func startTrackedChain(t *testing.T, headerTimeout string) (string, *handshakeMonitor, *fakeTimers) {
	t.Helper()
	cert := httptest.NewTLSServer(http.NotFoundHandler())
	t.Cleanup(cert.Close)
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	limited := newLimitedListener(newHelloTrackingTLSListener(raw, cert.TLS.Clone()), 8, "http", "photos", nil).(*limitedListener)
	timers := &fakeTimers{}
	limited.handshakes = newTestMonitor(timers)
	svc := registry.Service{Name: "photos", Type: registry.TypeProxy, RequestLimits: &registry.RequestLimits{HeaderTimeout: headerTimeout}}
	srv := newHTTPServerFn(http.NotFoundHandler())
	ln := configureServiceHTTP(srv, svc, limited, nil)
	done := make(chan struct{})
	go func() { defer close(done); _ = srv.Serve(ln) }()
	t.Cleanup(func() { srv.Close(); ln.Close(); <-done })
	return raw.Addr().String(), limited.handshakes, timers
}

func singleFailedRecord(t *testing.T, logs *captureHandler, timers *fakeTimers) map[string]any {
	t.Helper()
	timers.fireAll()
	recs := logs.byCode("tls.handshake.failed")
	if len(recs) != 1 {
		t.Fatalf("want one record, got %#v", recs)
	}
	return recs[0]
}

// captureClientHello returns the bytes of one genuine TLS ClientHello.
func captureClientHello(t *testing.T) []byte {
	t.Helper()
	clientSide, serverSide := net.Pipe()
	hello := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 4096)
		n, _ := serverSide.Read(buf)
		hello <- buf[:n]
		serverSide.Close()
	}()
	_ = tls.Client(clientSide, &tls.Config{ServerName: "photos.example.ts.net", InsecureSkipVerify: true}).Handshake()
	return <-hello
}

func TestRealChainHalfClientHelloThenCloseIsEOF(t *testing.T) {
	old := slog.Default()
	logs := installCaptureLogger()
	t.Cleanup(func() { slog.SetDefault(old) })
	addr, m, timers := startTrackedChain(t, "5s")

	h := captureClientHello(t)
	if len(h) < 20 {
		t.Fatalf("captured hello too short: %d", len(h))
	}
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = c.Write(h[:len(h)/2])
	c.Close()
	waitHandshakeCond(t, func() bool { return m.Failed.Load() == 1 })
	r := singleFailedRecord(t, logs, timers)
	if r["first_class"] != "eof" && r["first_class"] != "reset" {
		t.Fatalf("half a ClientHello then close must be eof (or reset), got %v", r["first_class"])
	}
}

func TestRealChainSilentClientIsTimeoutBeforeHello(t *testing.T) {
	old := slog.Default()
	logs := installCaptureLogger()
	t.Cleanup(func() { slog.SetDefault(old) })
	addr, m, timers := startTrackedChain(t, "1s")
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	waitHandshakeCond(t, func() bool { return m.Failed.Load() == 1 })
	r := singleFailedRecord(t, logs, timers)
	if r["first_class"] != "timeout_before_hello" {
		t.Fatalf("silent client must be timeout_before_hello, got %v", r["first_class"])
	}
}

// A client that sent its ClientHello and then went quiet before finishing the
// handshake is timeout_after_hello.
func TestRealChainStalledAfterHelloIsTimeoutAfterHello(t *testing.T) {
	old := slog.Default()
	logs := installCaptureLogger()
	t.Cleanup(func() { slog.SetDefault(old) })
	addr, m, timers := startTrackedChain(t, "1s")
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Write(captureClientHello(t)); err != nil { // whole hello, then silence
		t.Fatal(err)
	}
	waitHandshakeCond(t, func() bool { return m.Failed.Load() == 1 })
	r := singleFailedRecord(t, logs, timers)
	if r["first_class"] != "timeout_after_hello" {
		t.Fatalf("hello sent then silence must be timeout_after_hello, got %v", r["first_class"])
	}
}

// unwrapTSNetServer returns the tsnet.Server behind the production constructor,
// which wraps it to hook ListenTLS.
func unwrapTSNetServer(s tsnetServer) (*tsnet.Server, bool) {
	if w, ok := s.(*helloTrackingServer); ok {
		return w.Server, true
	}
	return nil, false
}

// The ListenTLS mirror was copied from one tailscale.com version. A bump must
// re-check tsnet's ListenTLS and then update tsnetMirrorVersion.
func TestListenTLSMirrorMatchesTailscaleVersion(t *testing.T) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		t.Skip("no build info")
	}
	for _, dep := range info.Deps {
		if dep.Path == "tailscale.com" {
			if dep.Version != tsnetMirrorVersion {
				t.Fatalf("go.mod has tailscale.com %s but helloTrackingServer.ListenTLS mirrors %s: diff tsnet.Server.ListenTLS, update the mirror, then tsnetMirrorVersion", dep.Version, tsnetMirrorVersion)
			}
			return
		}
	}
	t.Fatal("tailscale.com not found in build info")
}

// weakRecorder records a weak pointer to every raw connection it accepts, so a
// test can prove nothing retains them (no global registry, no leak).
type weakRecorder struct {
	net.Listener
	mu   sync.Mutex
	refs []weak.Pointer[net.TCPConn]
}

func (w *weakRecorder) Accept() (net.Conn, error) {
	c, err := w.Listener.Accept()
	if err != nil {
		return nil, err
	}
	if tc, ok := c.(*net.TCPConn); ok {
		w.mu.Lock()
		w.refs = append(w.refs, weak.Make(tc))
		w.mu.Unlock()
	}
	return c, nil
}

func (w *weakRecorder) live() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := 0
	for _, r := range w.refs {
		if r.Value() != nil {
			n++
		}
	}
	return n
}

func (w *weakRecorder) count() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.refs)
}

// gcUntil runs GC until live() <= want or the deadline passes.
func gcUntil(t *testing.T, rec *weakRecorder, want int, what string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for rec.live() > want {
		runtime.GC()
		if time.Now().After(deadline) {
			t.Fatalf("%s: %d connections still retained (want <= %d)", what, rec.live(), want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func serveChain(t *testing.T, ln net.Listener, svc registry.Service) {
	t.Helper()
	srv := newHTTPServerFn(http.NotFoundHandler())
	chain := configureServiceHTTP(srv, svc, ln, nil)
	done := make(chan struct{})
	go func() { defer close(done); _ = srv.Serve(chain) }()
	t.Cleanup(func() { srv.Close(); chain.Close(); <-done })
}

// Cap reject: the first connection holds the only slot (silent, handshake
// budget long), so every one of the other 49 is rejected by the cap, every
// time. None of the rejected connections may be retained, with the listener
// still open.
func TestHelloTrackingRetainsNothingForCapRejectedConnections(t *testing.T) {
	old := slog.Default()
	logs := installCaptureLogger()
	t.Cleanup(func() { slog.SetDefault(old) })
	cert := httptest.NewTLSServer(http.NotFoundHandler())
	defer cert.Close()
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	rec := &weakRecorder{Listener: raw}
	limited := newLimitedListener(newHelloTrackingTLSListener(rec, cert.TLS.Clone()), 1, "http", "photos", nil).(*limitedListener)
	serveChain(t, limited, registry.Service{Name: "photos", Type: registry.TypeProxy, RequestLimits: &registry.RequestLimits{HeaderTimeout: "5s"}})

	holder, err := net.Dial("tcp", raw.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	waitHandshakeCond(t, func() bool { return len(limited.sem) == 1 })
	for i := 0; i < 49; i++ {
		c, err := net.Dial("tcp", raw.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		c.Close()
	}
	waitHandshakeCond(t, func() bool { return rec.count() == 50 })
	waitHandshakeCond(t, func() bool {
		return len(logs.byMessage("connection limit exceeded; closing accepted connection")) == 49
	})
	if got := len(logs.byMessage("connection limit exceeded; closing accepted connection")); got != 49 {
		t.Fatalf("cap rejected %d connections, want 49", got)
	}
	gcUntil(t, rec, 1, "cap-rejected connections (only the slot holder may remain)")
	holder.Close()
	waitHandshakeCond(t, func() bool { return len(limited.sem) == 0 })
}

// Gateway denial: the real gateway listener closes every connection above the
// tracking listener without starting TLS. None may be retained while the
// listener is still open.
func TestHelloTrackingRetainsNothingForGatewayDeniedConnections(t *testing.T) {
	old := slog.Default()
	installCaptureLogger()
	t.Cleanup(func() { slog.SetDefault(old) })
	cert := httptest.NewTLSServer(http.NotFoundHandler())
	defer cert.Close()
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	rec := &weakRecorder{Listener: raw}
	svc := registry.Service{Name: "photos", Type: registry.TypeProxy}
	gate := &gatewayGate{svc: svc, now: time.Now} // nil LocalClient: admit denies everything
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	gl := newGatewayListener(ctx, newHelloTrackingTLSListener(rec, cert.TLS.Clone()), gate)
	serveChain(t, newLimitedListener(gl, 64, "http", "photos", nil), svc)

	for i := 0; i < 50; i++ {
		c, err := net.Dial("tcp", raw.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		c.Close()
	}
	waitHandshakeCond(t, func() bool { return gate.dropped.Load() == 50 })
	gcUntil(t, rec, 0, "gateway-denied connections")
}

// Host sockets on Windows report Winsock's own errno values, which are not
// syscall.ECONNRESET/ECONNABORTED there; they must still classify as reset.
func TestClassifyHandshakeErrorWinsockErrnos(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Winsock errno values only mean reset/abort on Windows")
	}
	for _, errno := range []syscall.Errno{10054, 10053} {
		err := &net.OpError{Op: "read", Net: "tcp", Err: os.NewSyscallError("wsarecv", errno)}
		if class, _ := classifyHandshakeError(err); class != handshakeClassReset {
			t.Fatalf("errno %d classified %q, want %q", errno, class, handshakeClassReset)
		}
	}
}

// A reader that sees Failed or Slow must also find the event in the pending
// window: the counter moves under the same lock as the window. Before, observe
// counted first and recorded later, so a test (or flush) that waited on the
// counter could fire the timers before the window existed and see no record.
func TestHandshakeCounterNeverPrecedesItsWindow(t *testing.T) {
	timers := &fakeTimers{}
	m := newTestMonitor(timers)
	m.mu.Lock()
	done := make(chan struct{})
	go func() {
		defer close(done)
		m.observe("100.64.0.9:1234", time.Millisecond, io.EOF, helloNotSeen)
	}()
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		if m.Failed.Load() != 0 {
			m.mu.Unlock()
			t.Fatal("Failed counted before the event reached the pending window")
		}
		time.Sleep(time.Millisecond)
	}
	m.mu.Unlock()
	<-done
	if m.Failed.Load() != 1 || timers.armed != 1 {
		t.Fatalf("Failed = %d, armed timers = %d, want 1 and 1", m.Failed.Load(), timers.armed)
	}
}
