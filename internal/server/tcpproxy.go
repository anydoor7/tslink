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
const tcpKeepAlivePeriod = 2 * time.Minute
const tcpMaxActiveConnections = 128

var tcpDialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
	var dialer net.Dialer
	return dialer.DialContext(ctx, network, address)
}

// serveTCP accepts connections on ln and forwards them to target via bidirectional io.Copy.
func serveTCP(ctx context.Context, ln net.Listener, target, name string) {
	sem := make(chan struct{}, tcpMaxActiveConnections)

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
		select {
		case sem <- struct{}{}:
			go func() {
				defer func() { <-sem }()
				handleTCPConn(ctx, conn, target, name)
			}()
		default:
			slog.Warn("tcp connection limit exceeded; closing accepted connection", "name", name, "limit", tcpMaxActiveConnections)
			_ = conn.Close()
		}
	}
}

func handleTCPConn(ctx context.Context, clientConn net.Conn, target, name string) {
	defer clientConn.Close()
	enableTCPKeepAlive(clientConn)

	dialCtx, cancel := context.WithTimeout(ctx, tcpBackendDialTimeout)
	defer cancel()

	backendConn, err := tcpDialContext(dialCtx, "tcp", target)
	if err != nil {
		slog.Error("tcp dial backend", "name", name, "target", target, "error", err)
		return
	}
	defer backendConn.Close()
	enableTCPKeepAlive(backendConn)

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
		_, _ = io.Copy(backendConn, clientConn)
		// Signal backend that client is done writing
		if tc, ok := backendConn.(*net.TCPConn); ok {
			tc.CloseWrite()
		}
	}()

	// backend → client
	go func() {
		defer wg.Done()
		_, _ = io.Copy(clientConn, backendConn)
		// Signal client that backend is done writing
		if tc, ok := clientConn.(*net.TCPConn); ok {
			tc.CloseWrite()
		}
	}()

	wg.Wait()
}

func enableTCPKeepAlive(conn net.Conn) {
	tc, ok := conn.(*net.TCPConn)
	if !ok {
		return
	}
	_ = tc.SetKeepAlive(true)
	_ = tc.SetKeepAlivePeriod(tcpKeepAlivePeriod)
}
