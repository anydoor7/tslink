package server

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAccessLogProxyPreservesUpgradeTunnel(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			http.Error(w, "upgrade missing", http.StatusBadRequest)
			return
		}
		conn, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = fmt.Fprint(rw, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
		if err := rw.Flush(); err != nil {
			return
		}
		message := make([]byte, 4)
		if _, err := io.ReadFull(rw, message); err != nil {
			return
		}
		_, _ = rw.Write(message)
		_ = rw.Flush()
	}))
	t.Cleanup(backend.Close)
	proxy, err := NewProxyHandler(backend.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	chain := AccessLogMiddleware("upgrade", nil, proxy)
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(done)
		chain.ServeHTTP(w, r)
	}))
	t.Cleanup(front.Close)
	conn, err := net.DialTimeout("tcp", strings.TrimPrefix(front.URL, "http://"), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		conn.Close()
		// Server.Close does not join hijacked connections. Join the handler
		// before another test can replace the process-wide logger.
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("upgrade handler did not finish after connection close")
		}
	})
	if err := conn.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	_, _ = fmt.Fprint(conn, "GET / HTTP/1.1\r\nHost: example.test\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
	reader := bufio.NewReader(conn)
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(line, "101 Switching Protocols") {
		t.Fatalf("upgrade response = %q, want 101", line)
	}
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if line == "\r\n" {
			break
		}
	}
	if _, err := conn.Write([]byte("PING")); err != nil {
		t.Fatal(err)
	}
	echo := make([]byte, 4)
	if _, err := io.ReadFull(reader, echo); err != nil {
		t.Fatal(err)
	}
	if string(echo) != "PING" {
		t.Fatalf("upgraded tunnel echoed %q, want PING", echo)
	}
}
