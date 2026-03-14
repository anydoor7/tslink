package server

import (
	"io"
	"log/slog"
	"net"
	"sync"
)

// serveTCP accepts connections on ln and forwards them to target via bidirectional io.Copy.
func serveTCP(ln net.Listener, target, name string) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			if isClosedListenerError(err) {
				return
			}
			slog.Error("tcp accept error", "name", name, "error", err)
			continue
		}
		go handleTCPConn(conn, target, name)
	}
}

func handleTCPConn(clientConn net.Conn, target, name string) {
	defer clientConn.Close()

	backendConn, err := net.Dial("tcp", target)
	if err != nil {
		slog.Error("tcp dial backend", "name", name, "target", target, "error", err)
		return
	}
	defer backendConn.Close()

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
