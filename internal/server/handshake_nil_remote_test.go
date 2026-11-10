package server

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/registry"
)

// gonetLikeConn models the address behaviour of gVisor's gonet.TCPConn, the
// connection type tsnet hands to TSLink: RemoteAddr returns a nil net.Addr as
// soon as the endpoint is no longer connected (gvisor
// pkg/tcpip/adapters/gonet/gonet.go TCPConn.RemoteAddr, which returns nil when
// tcp.Endpoint.GetRemoteAddress reports ErrNotConnected). Two events take the
// endpoint there during a handshake: a peer reset, seen by the server as a
// failed read, and a local close, which is how crypto/tls interrupts a
// handshake whose context expired.
type gonetLikeConn struct {
	net.Conn
	gone atomic.Bool
}

func (c *gonetLikeConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	var netErr net.Error
	if err != nil && !(errors.As(err, &netErr) && netErr.Timeout()) {
		c.gone.Store(true)
	}
	return n, err
}

func (c *gonetLikeConn) Close() error {
	c.gone.Store(true)
	return c.Conn.Close()
}

func (c *gonetLikeConn) RemoteAddr() net.Addr {
	if c.gone.Load() {
		return nil
	}
	return c.Conn.RemoteAddr()
}

type gonetLikeListener struct{ net.Listener }

func (l gonetLikeListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return &gonetLikeConn{Conn: c}, nil
}

// startGonetLikeChain serves the production listener chain (hello-tracking TLS
// listener, capped listener, header-budget listener, net/http) over
// connections that lose their remote address when they die.
func startGonetLikeChain(t *testing.T, headerTimeout string) (string, *limitedListener, *fakeTimers, *http.Transport) {
	t.Helper()
	cert := httptest.NewTLSServer(http.NotFoundHandler())
	t.Cleanup(cert.Close)
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	limited := newLimitedListener(newHelloTrackingTLSListener(gonetLikeListener{raw}, cert.TLS.Clone()), 8, "http", "photos", nil).(*limitedListener)
	timers := &fakeTimers{}
	limited.handshakes = newTestMonitor(timers)
	svc := registry.Service{Name: "photos", Type: registry.TypeProxy, RequestLimits: &registry.RequestLimits{HeaderTimeout: headerTimeout}}
	srv := newHTTPServerFn(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	ln := configureServiceHTTP(srv, svc, limited, nil)
	done := make(chan struct{})
	go func() { defer close(done); _ = srv.Serve(ln) }()
	t.Cleanup(func() { srv.Close(); ln.Close(); <-done })
	tr := &http.Transport{TLSClientConfig: cert.Client().Transport.(*http.Transport).TLSClientConfig.Clone()}
	t.Cleanup(tr.CloseIdleConnections)
	return raw.Addr().String(), limited, timers, tr
}

// pendingFirstAddr is the address the monitor attributed the first failure of
// the open window to.
func pendingFirstAddr(t *testing.T, m *handshakeMonitor) string {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pending == nil {
		t.Fatal("no pending handshake window")
	}
	return m.pending.firstAddr
}

func requireChainServes(t *testing.T, addr string, tr *http.Transport) {
	t.Helper()
	resp, err := (&http.Client{Transport: tr, Timeout: 30 * time.Second}).Get("https://" + addr + "/")
	if err != nil {
		t.Fatalf("listener stopped serving after the failed handshake: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}
}

// Regression for W0072: a connection whose transport dies during the TLS
// handshake made RemoteAddr return nil, and recording the failure called
// String on it inside headerBudgetListener.prepare. That goroutine has no
// recover, so the whole daemon exited with SIGSEGV at addr=0x20.
func TestHandshakeFailureAfterTransportDiesIsRecordedNotFatal(t *testing.T) {
	cases := []struct {
		name          string
		headerTimeout string
		kill          func(t *testing.T, c *net.TCPConn)
		classes       []string
	}{
		{
			// The peer sends half a ClientHello and resets: the server's read
			// fails and the endpoint is gone before observe runs.
			name:          "peer reset mid-hello",
			headerTimeout: "30s",
			kill: func(t *testing.T, c *net.TCPConn) {
				if _, err := c.Write(captureClientHello(t)[:20]); err != nil {
					t.Fatal(err)
				}
				if err := c.SetLinger(0); err != nil {
					t.Fatal(err)
				}
				c.Close()
			},
			classes: []string{handshakeClassReset, handshakeClassEOF},
		},
		{
			// The peer goes silent: the handshake budget expires and
			// crypto/tls closes the transport to interrupt the read.
			name:          "handshake budget expires",
			headerTimeout: "1s",
			kill:          func(t *testing.T, c *net.TCPConn) {},
			classes:       []string{handshakeClassTimeoutBeforeHello},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			old := slog.Default()
			logs := installCaptureLogger()
			t.Cleanup(func() { slog.SetDefault(old) })
			addr, limited, timers, tr := startGonetLikeChain(t, tc.headerTimeout)

			c, err := net.Dial("tcp", addr)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			client := c.LocalAddr().String()
			tc.kill(t, c.(*net.TCPConn))

			m := limited.handshakes
			waitHandshakeCond(t, func() bool { return m.Failed.Load() == 1 })
			// The failure is attributed to the peer read at accept time.
			if got := pendingFirstAddr(t, m); got != client {
				t.Fatalf("failure attributed to %q, want the client %q", got, client)
			}
			r := singleFailedRecord(t, logs, timers)
			class, _ := r["first_class"].(string)
			matched := false
			for _, want := range tc.classes {
				matched = matched || class == want
			}
			if !matched {
				t.Fatalf("first_class = %q, want one of %v", class, tc.classes)
			}
			// The connection's slot is free and the listener keeps serving.
			waitHandshakeCond(t, func() bool { return len(limited.sem) == 0 })
			requireChainServes(t, addr, tr)
		})
	}
}

func TestRemoteAddrStringHandlesMissingAddresses(t *testing.T) {
	live, peer := net.Pipe()
	defer live.Close()
	defer peer.Close()
	gone := &gonetLikeConn{Conn: live}
	gone.gone.Store(true)
	cases := []struct {
		name string
		conn net.Conn
		want string
	}{
		{"nil conn", nil, ""},
		{"nil addr", gone, ""},
		{"typed nil addr", fixedAddrConn{Conn: live, addr: (*net.TCPAddr)(nil)}, ""},
		{"address", fixedAddrConn{Conn: live, addr: &net.TCPAddr{IP: net.IPv4(100, 64, 0, 7), Port: 443}}, "100.64.0.7:443"},
	}
	for _, tc := range cases {
		if got := remoteAddrString(tc.conn); got != tc.want {
			t.Errorf("%s: remoteAddrString = %q, want %q", tc.name, got, tc.want)
		}
	}
}

type fixedAddrConn struct {
	net.Conn
	addr net.Addr
}

func (c fixedAddrConn) RemoteAddr() net.Addr { return c.addr }

// The capped listener reads the peer at accept time even for a connection
// whose address is already gone, and the access helpers treat "" as unknown.
func TestLimitedListenerCapturesRemoteAtAccept(t *testing.T) {
	live, peer := net.Pipe()
	defer peer.Close()
	want := &net.TCPAddr{IP: net.IPv4(100, 64, 0, 9), Port: 50123}
	conn := &gonetLikeConn{Conn: fixedAddrConn{Conn: live, addr: want}}
	ln := newLimitedListener(&oneConnListener{conn: tls.Server(conn, &tls.Config{})}, 1, "http", "photos", nil)
	accepted, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer accepted.Close()
	capped := accepted.(*limitedTLSConn)
	conn.gone.Store(true)
	if capped.remote != want.String() {
		t.Fatalf("captured remote = %q, want %q", capped.remote, want.String())
	}
	if got := remoteAddrString(capped); got != "" {
		t.Fatalf("live RemoteAddr after the transport died = %q, want empty", got)
	}
}

type oneConnListener struct {
	conn net.Conn
	used atomic.Bool
}

func (l *oneConnListener) Accept() (net.Conn, error) {
	if l.used.Swap(true) {
		return nil, io.EOF
	}
	return l.conn, nil
}
func (l *oneConnListener) Close() error   { return nil }
func (l *oneConnListener) Addr() net.Addr { return &net.TCPAddr{} }

// goneConnListener hands out connections whose transport is already gone, so
// RemoteAddr returns nil as gonet does after a reset.
type goneConnListener struct {
	net.Listener
}

func (l goneConnListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	gone := &gonetLikeConn{Conn: c}
	gone.gone.Store(true)
	return gone, nil
}

// The gateway admission path read c.RemoteAddr().String() in both the accept
// loop and the per-connection worker.
func TestGatewayAdmissionSurvivesConnectionWithoutRemoteAddr(t *testing.T) {
	old := slog.Default()
	installCaptureLogger()
	t.Cleanup(func() { slog.SetDefault(old) })
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	gate := &gatewayGate{svc: registry.Service{Name: "photos", Type: registry.TypeProxy}, now: time.Now} // nil LocalClient: admit denies
	gl := newGatewayListener(t.Context(), goneConnListener{raw}, gate)
	defer gl.Close()
	for i := 1; i <= 2; i++ {
		c, err := net.Dial("tcp", raw.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		waitHandshakeCond(t, func() bool { return gate.dropped.Load() == uint64(i) })
		_ = c.SetReadDeadline(time.Now().Add(10 * time.Second))
		if _, err := c.Read(make([]byte, 1)); err == nil {
			t.Fatal("denied connection was not closed")
		}
		c.Close()
	}
}

// tcpAccessBegin read conn.RemoteAddr().String() for the access record.
func TestTCPAccessRecordSurvivesConnectionWithoutRemoteAddr(t *testing.T) {
	left, right := net.Pipe()
	defer right.Close()
	gone := &gonetLikeConn{Conn: left}
	gone.gone.Store(true)
	capture := &accessEventCapture{}
	opts := &tcpAccessOptions{writer: capture, now: func() time.Time { return accessTestTime }}
	ctx := context.WithValue(t.Context(), tcpAccessKey{}, opts)
	_, finish := tcpAccessBegin(ctx, gone, "db", "")
	finish()
	if len(capture.events) != 2 || capture.events[0].Kind != "tcp_open" || capture.events[1].Kind != "tcp_close" {
		t.Fatalf("events = %+v, want tcp_open then tcp_close", capture.events)
	}
	if remote := capture.events[0].Identity.Remote; remote != "" {
		t.Fatalf("unknown peer recorded as %q, want empty", remote)
	}
}
