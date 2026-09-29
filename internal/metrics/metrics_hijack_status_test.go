package metrics

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// A handler that hijacks the connection writes its status line itself and
// never calls WriteHeader; ReverseProxy does exactly that for a WebSocket
// upgrade. The request must be counted as 101, not as the implicit 200.
func TestMetricsMiddlewareHijackedUpgradeCountedAs101(t *testing.T) {
	m := New()
	const svc = "ws"
	instrumented := m.Middleware(svc, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Errorf("hijack: %v", err)
			return
		}
		defer conn.Close()
		_, _ = fmt.Fprint(rw, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
		_ = rw.Flush()
	}))
	done := make(chan struct{})
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(done)
		instrumented.ServeHTTP(w, r)
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
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(line, "101 Switching Protocols") {
		t.Fatalf("status line = %q, want 101", line)
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("instrumented handler did not return")
	}
	if got := getCounterValue(t, m.RequestsTotal, svc, http.MethodGet, "101"); got != 1 {
		t.Fatalf("requests counted as 101 = %v, want 1", got)
	}
	if got := getCounterValue(t, m.RequestsTotal, svc, http.MethodGet, "200"); got != 0 {
		t.Fatalf("requests counted as 200 = %v, want 0", got)
	}
}
