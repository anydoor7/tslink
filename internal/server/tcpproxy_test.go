package server

import (
	"errors"
	"io"
	"net"
	"testing"
	"time"
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
		handleTCPConn(proxyConn, echo.Addr().String(), "test")
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
		serveTCP(ln, "127.0.0.1:1", "test")
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

	go serveTCP(proxyLn, echo.Addr().String(), "test-fwd")

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

	conn.Close()
	proxyLn.Close()
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
		serveTCP(ln, "127.0.0.1:1", "test") // target doesn't matter, handleTCPConn will fail
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
		serveTCP(el, "127.0.0.1:1", "test-err")
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

func TestHandleTCPConn_UnreachableBackend(t *testing.T) {
	// Use a port that is almost certainly not listening.
	clientConn, proxyConn := net.Pipe()
	defer clientConn.Close()

	done := make(chan struct{})
	go func() {
		handleTCPConn(proxyConn, "127.0.0.1:1", "test")
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("handleTCPConn should return quickly for unreachable backend")
	}
}
