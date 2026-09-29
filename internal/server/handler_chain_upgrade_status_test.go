package server

import (
	"bufio"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/monody0007/tslink/internal/metrics"
)

// ReverseProxy hijacks a WebSocket upgrade and writes the backend's 101
// itself, without calling WriteHeader. Through the production chain the
// tunnel must keep working and both the access log and metrics must record
// 101 rather than the wrappers' implicit 200 default.
func TestServiceHandlerChainHijackedUpgradeRecordedAs101(t *testing.T) {
	oldLogger := slog.Default()
	logs := installCaptureLogger()
	t.Cleanup(func() { slog.SetDefault(oldLogger) })

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	m := metrics.New()
	chain := instrumentServiceHandler(m, "ws", nil, proxy)
	done := make(chan struct{})
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(done)
		chain.ServeHTTP(w, r)
	}))
	t.Cleanup(front.Close)

	conn, err := net.DialTimeout("tcp", strings.TrimPrefix(front.URL, "http://"), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
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
		header, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if header == "\r\n" {
			break
		}
	}
	if _, err := conn.Write([]byte("PING")); err != nil {
		t.Fatal(err)
	}
	echo := make([]byte, 4)
	if _, err := io.ReadFull(reader, echo); err != nil || string(echo) != "PING" {
		t.Fatalf("upgraded tunnel echoed %q (err %v), want PING", echo, err)
	}
	conn.Close()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("production chain did not return after the tunnel closed")
	}

	if got := logs.attrMap(t, 0)["status"]; got != int64(http.StatusSwitchingProtocols) {
		t.Fatalf("logged upgrade status = %v, want 101", got)
	}
	want := `tslink_requests_total{method="GET",service="ws",status="101"} 1`
	if got := requestsTotalLines(t, m); len(got) != 1 || got[0] != want {
		t.Fatalf("request metrics = %q, want only %q", got, want)
	}
}
