package server

import (
	"bufio"
	"fmt"
	"github.com/anydoor7/tslink/internal/registry"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ReverseProxy hijacks a WebSocket upgrade and writes the backend's 101
// itself, without calling WriteHeader. Through the production chain the
// tunnel must keep working and the access log must record 101 rather than the
// wrapper's implicit 200 default.
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
	svc := registry.Service{Name: "ws", Type: registry.TypeProxy, RequestLimits: &registry.RequestLimits{MaxBody: "1GiB", ReadTimeout: "30ms"}}
	chain := AccessLogMiddleware("ws", nil, RequestLimitsMiddleware(svc, nil, proxy))
	done := make(chan struct{})
	front := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(done)
		chain.ServeHTTP(w, r)
	}))
	front.Config = newHTTPServerFn(front.Config.Handler)
	front.Listener = configureServiceHTTP(front.Config, svc, front.Listener, nil)
	front.Start()
	t.Cleanup(front.Close)

	conn, err := net.DialTimeout("tcp", strings.TrimPrefix(front.URL, "http://"), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
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
	time.Sleep(80 * time.Millisecond) // Beyond the upload idle window; upgrade must survive.
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
	case <-time.After(5 * time.Second):
		t.Fatal("production chain did not return after the tunnel closed")
	}

	if got := logs.attrMap(t, 0)["status"]; got != int64(http.StatusSwitchingProtocols) {
		t.Fatalf("logged upgrade status = %v, want 101", got)
	}
}
