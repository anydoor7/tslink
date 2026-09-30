package server

import (
	"context"
	"errors"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/monody0007/tslink/internal/registry"
	"github.com/monody0007/tslink/internal/testenv"
)

// startEchoServer starts a TCP server that echoes back everything it receives.
func startEchoServer(t *testing.T) net.Listener {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				_, _ = io.Copy(conn, conn)
			}()
		}
	}()

	return ln
}

func TestHandleTCPConn_Bidirectional(t *testing.T) {
	echo := startEchoServer(t)

	// Create a real TCP listener to accept a connection that handleTCPConn will use.
	proxyLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	defer proxyLn.Close()

	// Accept in background.
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := proxyLn.Accept()
		if err != nil {
			return
		}
		accepted <- conn
	}()

	// Dial to get a real TCP connection pair.
	clientConn, err := net.Dial("tcp", proxyLn.Addr().String())
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer clientConn.Close()

	proxyConn := <-accepted

	done := make(chan struct{})
	go func() {
		handleTCPConn(context.Background(), proxyConn, echo.Addr().String(), "test")
		close(done)
	}()

	// Write data and read it back.
	msg := []byte("hello tcp proxy")
	if _, err := clientConn.Write(msg); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	// Close write side so the echo server gets EOF and echoes back.
	clientConn.(*net.TCPConn).CloseWrite()

	buf, err := io.ReadAll(clientConn)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if string(buf) != string(msg) {
		t.Fatalf("got %q, want %q", buf, msg)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handleTCPConn did not return")
	}
}

func TestServeTCP_ClosedListener(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}

	// Close the listener immediately so serveTCP returns.
	ln.Close()

	done := make(chan struct{})
	go func() {
		serveTCP(context.Background(), ln, "127.0.0.1:1", "test")
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("serveTCP did not return for closed listener")
	}
}

func TestServeTCP_ForwardsToBackend(t *testing.T) {
	echo := startEchoServer(t)

	// Create a listener for serveTCP
	proxyLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	serveDone := make(chan struct{})
	go func() {
		serveTCP(ctx, proxyLn, echo.Addr().String(), "test-fwd")
		close(serveDone)
	}()
	t.Cleanup(func() {
		cancel()
		_ = proxyLn.Close()
		select {
		case <-serveDone:
		case <-time.After(2 * time.Second):
			t.Error("serveTCP did not stop during cleanup")
		}
	})

	// Connect to proxy and send data
	conn, err := net.Dial("tcp", proxyLn.Addr().String())
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}

	msg := []byte("test forward via serveTCP")
	if _, err := conn.Write(msg); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	conn.(*net.TCPConn).CloseWrite()

	buf, err := io.ReadAll(conn)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if string(buf) != string(msg) {
		t.Fatalf("got %q, want %q", buf, msg)
	}

	if err := conn.Close(); err != nil {
		t.Fatalf("client Close() error = %v", err)
	}
	cancel()
	if err := proxyLn.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		t.Fatalf("proxy listener Close() error = %v", err)
	}
	select {
	case <-serveDone:
	case <-time.After(2 * time.Second):
		t.Fatal("serveTCP did not wait for its connection handler")
	}
}

func TestStopNodeLocked_ClosesInFlightTCPConnection(t *testing.T) {
	testenv.SetHome(t, t.TempDir())

	backendLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("backend Listen() error = %v", err)
	}
	defer backendLn.Close()

	backendAccepted := make(chan net.Conn, 1)
	go func() {
		conn, err := backendLn.Accept()
		if err == nil {
			backendAccepted <- conn
		}
	}()

	proxyLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("proxy Listen() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	s.nodes["db"] = &ServiceNode{
		service:  registry.Service{Name: "db", Type: registry.TypeTCP, Target: backendLn.Addr().String()},
		listener: proxyLn,
		tsnetSrv: &fakeTSNetServer{},
		cancel:   cancel,
	}

	serveDone := make(chan struct{})
	go func() {
		serveTCP(ctx, proxyLn, backendLn.Addr().String(), "db")
		close(serveDone)
	}()

	clientConn, err := net.Dial("tcp", proxyLn.Addr().String())
	if err != nil {
		t.Fatalf("client Dial() error = %v", err)
	}
	defer clientConn.Close()

	select {
	case backendConn := <-backendAccepted:
		defer backendConn.Close()
	case <-time.After(2 * time.Second):
		t.Fatal("backend did not receive in-flight proxy connection")
	}

	s.stopNodeLocked("db")

	if err := clientConn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline() error = %v", err)
	}
	if _, err := clientConn.Read(make([]byte, 1)); err == nil {
		t.Fatal("client connection remained open after node stop")
	} else {
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			t.Fatalf("client connection read timed out after node stop; connection remained open: %v", err)
		}
	}

	select {
	case <-serveDone:
	case <-time.After(2 * time.Second):
		t.Fatal("serveTCP did not return after node stop")
	}
}

func TestServeTCP_NonFatalAcceptError(t *testing.T) {
	// Use a listener that returns a temporary error then closes.
	// We use a real listener, accept one connection, then close the listener.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}

	// Connect to trigger an accept, then close listener to stop the loop
	go func() {
		conn, err := net.Dial("tcp", ln.Addr().String())
		if err == nil {
			conn.Close()
		}
		time.Sleep(50 * time.Millisecond)
		ln.Close()
	}()

	done := make(chan struct{})
	go func() {
		serveTCP(context.Background(), ln, "127.0.0.1:1", "test") // target doesn't matter, handleTCPConn will fail
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("serveTCP did not return")
	}
}

// errorListener is a net.Listener that returns configurable errors from Accept.
type errorListener struct {
	errors chan error
	closed chan struct{}
}

func (l *errorListener) Accept() (net.Conn, error) {
	select {
	case err := <-l.errors:
		return nil, err
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *errorListener) Close() error {
	select {
	case <-l.closed:
	default:
		close(l.closed)
	}
	return nil
}

func (l *errorListener) Addr() net.Addr { return &net.TCPAddr{} }

func TestServeTCP_AcceptError_NonClosed(t *testing.T) {
	el := &errorListener{
		errors: make(chan error, 1),
		closed: make(chan struct{}),
	}

	// Send a non-closed error; serveTCP should log it and continue.
	el.errors <- errors.New("temporary accept failure")

	done := make(chan struct{})
	go func() {
		serveTCP(context.Background(), el, "127.0.0.1:1", "test-err")
		close(done)
	}()

	// Give serveTCP time to process the error and loop back to Accept.
	time.Sleep(50 * time.Millisecond)

	// Close the listener to stop the loop.
	el.Close()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("serveTCP did not return after closing errorListener")
	}
}

type deadlineRecordingConn struct {
	readDeadlineCalls atomic.Int32
}

func (c *deadlineRecordingConn) Read([]byte) (int, error)         { return 0, io.EOF }
func (c *deadlineRecordingConn) Write(p []byte) (int, error)      { return len(p), nil }
func (c *deadlineRecordingConn) Close() error                     { return nil }
func (c *deadlineRecordingConn) LocalAddr() net.Addr              { return &net.TCPAddr{} }
func (c *deadlineRecordingConn) RemoteAddr() net.Addr             { return &net.TCPAddr{} }
func (c *deadlineRecordingConn) SetDeadline(time.Time) error      { return nil }
func (c *deadlineRecordingConn) SetWriteDeadline(time.Time) error { return nil }
func (c *deadlineRecordingConn) SetReadDeadline(time.Time) error {
	c.readDeadlineCalls.Add(1)
	return nil
}

func TestHandleTCPConn_DoesNotSetHardIdleReadDeadline(t *testing.T) {
	clientConn := &deadlineRecordingConn{}
	backendConn := &deadlineRecordingConn{}

	oldDial := tcpDialContext
	t.Cleanup(func() { tcpDialContext = oldDial })
	tcpDialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		return backendConn, nil
	}

	handleTCPConn(context.Background(), clientConn, "127.0.0.1:1", "test")

	if got := clientConn.readDeadlineCalls.Load(); got != 0 {
		t.Fatalf("client read deadline calls = %d, want 0", got)
	}
	if got := backendConn.readDeadlineCalls.Load(); got != 0 {
		t.Fatalf("backend read deadline calls = %d, want 0", got)
	}
}

func TestHandleTCPConn_UnreachableBackend(t *testing.T) {
	// Use a port that is almost certainly not listening.
	clientConn, proxyConn := net.Pipe()
	defer clientConn.Close()

	done := make(chan struct{})
	go func() {
		handleTCPConn(context.Background(), proxyConn, "127.0.0.1:1", "test")
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("handleTCPConn should return quickly for unreachable backend")
	}
}

func TestHandleTCPConn_DialsBackendWithTimeoutContext(t *testing.T) {
	oldDial := tcpDialContext
	t.Cleanup(func() { tcpDialContext = oldDial })

	seenDeadline := false
	tcpDialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if network != "tcp" {
			t.Fatalf("network = %q, want tcp", network)
		}
		if address != "127.0.0.1:1" {
			t.Fatalf("address = %q, want 127.0.0.1:1", address)
		}
		deadline, ok := ctx.Deadline()
		if !ok {
			t.Fatal("dial context has no deadline")
		}
		remaining := time.Until(deadline)
		if remaining <= 0 || remaining > tcpBackendDialTimeout {
			t.Fatalf("deadline remaining = %v, want within %v", remaining, tcpBackendDialTimeout)
		}
		seenDeadline = true
		return nil, errors.New("dial stopped")
	}

	clientConn, proxyConn := net.Pipe()
	defer clientConn.Close()

	done := make(chan struct{})
	go func() {
		handleTCPConn(context.Background(), proxyConn, "127.0.0.1:1", "test")
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handleTCPConn did not return after dial error")
	}
	if !seenDeadline {
		t.Fatal("dial context was not observed")
	}
}
