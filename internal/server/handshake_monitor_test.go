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
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/registry"
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
		m.observe("100.64.0.9:5000", 30*time.Millisecond, io.EOF)
	}
	m.observe("100.64.0.9:5000", 2*time.Second, &net.OpError{Op: "read", Err: timeoutErr{}})
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
	m.observe("100.64.0.9:5001", time.Millisecond, io.EOF)
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

	m.observe("a:1", 100*time.Millisecond, nil) // fast: silent
	m.observe("a:1", 2*time.Second, nil)        // exactly at threshold: slow
	m.observe("a:1", 5*time.Second, nil)
	m.observe("a:1", time.Second, net.ErrClosed)    // teardown: not counted
	m.observe("a:1", time.Second, context.Canceled) // teardown: not counted
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
	m.observe("a:1", time.Millisecond, io.EOF)
	m.close()
	m.close()
	m.observe("a:1", time.Millisecond, io.EOF) // after close: counted, not logged
	if got := len(logs.byCode("tls.handshake.failed")); got != 1 {
		t.Fatalf("close must emit the pending window once, got %d", got)
	}
}

func TestHandshakeMonitorNilSafe(t *testing.T) {
	var m *handshakeMonitor
	m.observe("a:1", time.Hour, io.EOF)
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
	limited := newLimitedListener(tls.NewListener(raw, cert.TLS.Clone()), 8, "http", "photos").(*limitedListener)
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
