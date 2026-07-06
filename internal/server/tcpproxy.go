package server

import (
	"context"
	"io"
	"log/slog"
	"net"
	"sync"
	"time"
)

const tcpBackendDialTimeout = 10 * time.Second
const tcpIdleTimeout = 5 * time.Minute

var tcpDialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
	var dialer net.Dialer
	return dialer.DialContext(ctx, network, address)
}

type idleDeadlineConn struct {
	net.Conn
	timeout time.Duration
}

func (c idleDeadlineConn) Read(p []byte) (int, error) {
	_ = c.Conn.SetReadDeadline(time.Now().Add(c.timeout))
	return c.Conn.Read(p)
}

// serveTCP accepts connections on ln and forwards them to target via bidirectional io.Copy.
func serveTCP(ctx context.Context, ln net.Listener, target, name string) {
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			if isClosedListenerError(err) || ctx.Err() != nil {
				return
			}
			slog.Error("tcp accept error", "name", name, "error", err)
			continue
		}
		go handleTCPConn(ctx, conn, target, name)
	}
}

func handleTCPConn(ctx context.Context, clientConn net.Conn, target, name string) {
	defer clientConn.Close()

	dialCtx, cancel := context.WithTimeout(ctx, tcpBackendDialTimeout)
	defer cancel()

	backendConn, err := tcpDialContext(dialCtx, "tcp", target)
	if err != nil {
		slog.Error("tcp dial backend", "name", name, "target", target, "error", err)
		return
	}
	defer backendConn.Close()

	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = clientConn.Close()
			_ = backendConn.Close()
		case <-done:
		}
	}()
	defer close(done)

	var wg sync.WaitGroup
	wg.Add(2)

	// client → backend
	go func() {
		defer wg.Done()
		_, _ = io.Copy(backendConn, idleDeadlineConn{Conn: clientConn, timeout: tcpIdleTimeout})
		// Signal backend that client is done writing
		if tc, ok := backendConn.(*net.TCPConn); ok {
			tc.CloseWrite()
		}
	}()

	// backend → client
	go func() {
		defer wg.Done()
		_, _ = io.Copy(clientConn, idleDeadlineConn{Conn: backendConn, timeout: tcpIdleTimeout})
		// Signal client that backend is done writing
		if tc, ok := clientConn.(*net.TCPConn); ok {
			tc.CloseWrite()
		}
	}()

	wg.Wait()
}
