package server

import (
	"context"
	"crypto/tls"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/accesslog"
	"github.com/anydoor7/tslink/internal/registry"
	"github.com/anydoor7/tslink/internal/testwait"
)

const connPanicCode = "connection.panic_recovered"

// requirePanicRecord asserts exactly one recovery record and that its stack
// names the goroutine the panic was injected into.
func requirePanicRecord(t *testing.T, logs *captureHandler, kind, name, frame string, total uint64) {
	t.Helper()
	recs := logs.byCode(connPanicCode)
	if len(recs) != 1 {
		t.Fatalf("want one %s record, got %d: %#v", connPanicCode, len(recs), recs)
	}
	r := recs[0]
	if r["kind"] != kind || r["name"] != name || r["total_recovered"] != total {
		t.Fatalf("record = kind %v name %v total %v, want %s %s %d", r["kind"], r["name"], r["total_recovered"], kind, name, total)
	}
	if p, _ := r["panic"].(string); !strings.Contains(p, "injected") {
		t.Fatalf("panic value = %q, want the injected one", p)
	}
	if stack, _ := r["stack"].(string); !strings.Contains(stack, frame) {
		t.Fatalf("stack does not name %s:\n%s", frame, stack)
	}
}

func requireClosedByServer(t *testing.T, c net.Conn) {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(testwait.Budget(t)))
	if _, err := io.ReadAll(c); err != nil && isTimeout(err) {
		t.Fatal("server did not close the connection whose goroutine panicked")
	}
}

// A panic during one connection's TLS handshake, here from the certificate
// provider, drops that connection only. The capped listener has one slot, so
// the next request succeeds only if the panicking connection released it.
func TestHandshakePanicDropsOnlyThatConnection(t *testing.T) {
	old := slog.Default()
	logs := installCaptureLogger()
	t.Cleanup(func() { slog.SetDefault(old) })
	cert := httptest.NewTLSServer(http.NotFoundHandler())
	defer cert.Close()
	cfg := cert.TLS.Clone()
	// Without a static certificate crypto/tls asks GetCertificate, like the
	// tsnet certificate provider in production.
	leaf := cfg.Certificates[0]
	cfg.Certificates = nil
	var panicked atomic.Bool
	cfg.GetCertificate = func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
		if !panicked.Swap(true) {
			panic("injected certificate provider panic")
		}
		return &leaf, nil
	}
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	limited := newLimitedListener(tls.NewListener(raw, cfg), 1, "http", "photos", nil).(*limitedListener)
	svc := registry.Service{Name: "photos", Type: registry.TypeProxy}
	srv := newHTTPServerFn(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	ln := configureServiceHTTP(srv, svc, limited, nil).(*headerBudgetListener)
	done := make(chan struct{})
	go func() { defer close(done); _ = srv.Serve(ln) }()
	defer func() { srv.Close(); ln.Close(); <-done }()
	before := connectionPanics.Load()

	bad, err := net.Dial("tcp", raw.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer bad.Close()
	_ = bad.SetDeadline(time.Now().Add(30 * time.Second))
	if err := tls.Client(bad, &tls.Config{InsecureSkipVerify: true}).Handshake(); err == nil {
		t.Fatal("handshake on the panicking connection succeeded")
	}
	waitHandshakeCond(t, func() bool { return connectionPanics.Load() == before+1 })
	requireClosedByServer(t, bad)
	waitHandshakeCond(t, func() bool { return len(limited.sem) == 0 })
	ln.mu.Lock()
	pending := len(ln.pending)
	ln.mu.Unlock()
	if pending != 0 {
		t.Fatalf("%d connections left pending after the panic", pending)
	}
	requirePanicRecord(t, logs, "http", "photos", "(*headerBudgetListener).prepare", before+1)

	tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	defer tr.CloseIdleConnections()
	resp, err := (&http.Client{Transport: tr, Timeout: testwait.Budget(t)}).Get("https://" + raw.Addr().String() + "/")
	if err != nil {
		t.Fatalf("listener did not serve after the panic: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}
	tr.CloseIdleConnections()
	joined := make(chan struct{})
	go func() { ln.workers.Wait(); close(joined) }()
	testwait.Recv(t, joined, "header-budget workers were not released after the panic")
}

// A panic in a gateway admission worker closes that connection; the worker's
// slot and WaitGroup are released, so later connections are still admitted
// (here: denied normally) and Close can join the workers.
func TestGatewayAdmissionPanicDropsOnlyThatConnection(t *testing.T) {
	old := slog.Default()
	logs := installCaptureLogger()
	t.Cleanup(func() { slog.SetDefault(old) })
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var panicked atomic.Bool
	gate := &gatewayGate{svc: registry.Service{Name: "photos", Type: registry.TypeProxy}, now: func() time.Time {
		if !panicked.Swap(true) {
			panic("injected admission panic")
		}
		return time.Now()
	}}
	gl := newGatewayListener(t.Context(), raw, gate)
	before := connectionPanics.Load()

	bad, err := net.Dial("tcp", raw.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer bad.Close()
	waitHandshakeCond(t, func() bool { return connectionPanics.Load() == before+1 })
	requireClosedByServer(t, bad)
	requirePanicRecord(t, logs, "gateway", "photos", "(*gatewayListener).admissions.func", before+1)

	next, err := net.Dial("tcp", raw.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	waitHandshakeCond(t, func() bool { return gate.dropped.Load() == 2 })
	requireClosedByServer(t, next)
	closed := make(chan struct{})
	go func() { _ = gl.Close(); close(closed) }()
	testwait.Recv(t, closed, "gateway listener Close did not join its admission workers")
}

type panicReadConn struct{ net.Conn }

func (panicReadConn) Read([]byte) (int, error) { panic("injected backend read panic") }

type discardAccessWriter struct{}

func (discardAccessWriter) Record(accesslog.Event) bool { return true }

// Panics in the TCP proxy's per-connection handler and in its copy goroutines
// drop that connection; serveTCP keeps accepting and still joins its handlers.
func TestTCPProxyPanicDropsOnlyThatConnection(t *testing.T) {
	cases := []struct {
		name  string
		frame string
		// inject returns the access options and backend dialer for the
		// first connection; later connections get the real ones.
		inject func(real func(context.Context, string, string) (net.Conn, error)) (func() time.Time, func(context.Context, string, string) (net.Conn, error))
	}{
		{
			// The access record is written before handleTCPConn defers its
			// own close, so only the recovery closes this connection.
			name:  "handler",
			frame: "server.tcpAccessBegin(",
			inject: func(real func(context.Context, string, string) (net.Conn, error)) (func() time.Time, func(context.Context, string, string) (net.Conn, error)) {
				return func() time.Time { panic("injected access clock panic") }, real
			},
		},
		{
			name:  "copy",
			frame: "server.handleTCPConn.func",
			inject: func(func(context.Context, string, string) (net.Conn, error)) (func() time.Time, func(context.Context, string, string) (net.Conn, error)) {
				return time.Now, func(context.Context, string, string) (net.Conn, error) {
					backend, peer := net.Pipe()
					go func() { _, _ = io.Copy(io.Discard, peer) }()
					return panicReadConn{backend}, nil
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			old := slog.Default()
			logs := installCaptureLogger()
			t.Cleanup(func() { slog.SetDefault(old) })
			echo := startEchoServer(t)
			oldDial := tcpDialContext
			t.Cleanup(func() { tcpDialContext = oldDial })
			var first atomic.Bool
			first.Store(true)
			badNow, badDial := tc.inject(oldDial)
			tcpDialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
				if first.Load() {
					return badDial(ctx, network, address)
				}
				return oldDial(ctx, network, address)
			}
			now := func() time.Time {
				if first.Load() {
					return badNow()
				}
				return time.Now()
			}
			proxyLn, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx = context.WithValue(ctx, tcpAccessKey{}, &tcpAccessOptions{writer: discardAccessWriter{}, now: now})
			serveDone := make(chan struct{})
			go func() { serveTCP(ctx, proxyLn, echo.Addr().String(), "db"); close(serveDone) }()
			before := connectionPanics.Load()

			c, err := net.Dial("tcp", proxyLn.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			_, _ = c.Write([]byte("x"))
			waitHandshakeCond(t, func() bool { return connectionPanics.Load() == before+1 })
			first.Store(false)
			requireClosedByServer(t, c)
			requirePanicRecord(t, logs, "tcp", "db", tc.frame, before+1)

			good, err := net.Dial("tcp", proxyLn.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			defer good.Close()
			msg := []byte("still serving")
			if _, err := good.Write(msg); err != nil {
				t.Fatal(err)
			}
			_ = good.(*net.TCPConn).CloseWrite()
			got, err := io.ReadAll(good)
			if err != nil || string(got) != string(msg) {
				t.Fatalf("proxy after the panic returned %q, %v; want %q", got, err, msg)
			}
			cancel()
			testwait.Recv(t, serveDone, "serveTCP did not join its handlers after the panic")
		})
	}
}
