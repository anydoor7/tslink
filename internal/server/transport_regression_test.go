package server

// Transport regressions use loopback TLS and TCP peers only.

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestLimitedTLSConnectionPreservesForwardedHTTPS(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, r.Header.Get("X-Forwarded-Proto"))
	}))
	defer backend.Close()
	certServer := httptest.NewTLSServer(http.NotFoundHandler())
	defer certServer.Close()
	for _, limited := range []bool{false, true} {
		t.Run(fmt.Sprintf("limited=%v", limited), func(t *testing.T) {
			h, err := NewProxyHandler(backend.URL, nil)
			if err != nil {
				t.Fatal(err)
			}
			seenTLS := make(chan bool, 1)
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				seenTLS <- r.TLS != nil && r.TLS.HandshakeComplete
				h.ServeHTTP(w, r)
			})
			raw, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			var ln net.Listener = tls.NewListener(raw, certServer.TLS.Clone())
			if limited {
				ln = newLimitedListener(ln, 256, "http", "audit")
			}
			srv := newHTTPServerFn(handler)
			done := make(chan struct{})
			go func() { defer close(done); _ = srv.Serve(ln) }()
			defer func() { _ = srv.Close(); _ = ln.Close(); <-done }()
			client := certServer.Client()
			client.Timeout = 2 * time.Second
			resp, err := client.Get("https://" + raw.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("actual TLS request: limited=%v upstream X-Forwarded-Proto=%q", limited, body)
			if string(body) != "https" {
				t.Errorf("TLS identity lost before proxy: got %q, want https", body)
			}
			if !<-seenTLS {
				t.Error("HTTP server lost completed TLS transport state")
			}
		})
	}
}

func TestLimitedTLSConnectionRetainsCapAndReleasesSlot(t *testing.T) {
	certServer := httptest.NewTLSServer(http.NotFoundHandler())
	defer certServer.Close()
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	limited := newLimitedListener(tls.NewListener(raw, certServer.TLS.Clone()), 1, "http", "cap").(*limitedListener)
	defer limited.Close()
	accepted := make(chan net.Conn, 2)
	acceptErr := make(chan error, 2)
	go func() {
		for i := 0; i < 2; i++ {
			conn, err := limited.Accept()
			if err != nil {
				acceptErr <- err
				return
			}
			accepted <- conn
		}
	}()
	firstClient, err := net.Dial("tcp", raw.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer firstClient.Close()
	var firstServer net.Conn
	select {
	case firstServer = <-accepted:
	case err := <-acceptErr:
		t.Fatal(err)
	case <-time.After(2 * time.Second):
		t.Fatal("first accept timed out")
	}
	if _, ok := firstServer.(*limitedTLSConn); !ok {
		t.Fatalf("accepted %T, want TLS-aware capped connection", firstServer)
	}
	if got := len(limited.sem); got != 1 {
		t.Fatalf("occupied slots = %d, want 1", got)
	}
	secondClient, err := net.Dial("tcp", raw.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer secondClient.Close()
	_ = secondClient.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 1)
	if _, err := secondClient.Read(buf); err == nil {
		t.Fatal("over-cap connection remained open")
	} else if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
		t.Fatal("over-cap connection was not closed")
	}
	if err := firstServer.Close(); err != nil {
		t.Fatal(err)
	}
	_ = firstServer.Close()
	if got := len(limited.sem); got != 0 {
		t.Fatalf("released slots = %d, want 0", got)
	}
	thirdClient, err := net.Dial("tcp", raw.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer thirdClient.Close()
	select {
	case thirdServer := <-accepted:
		_ = thirdServer.Close()
	case err := <-acceptErr:
		t.Fatal(err)
	case <-time.After(2 * time.Second):
		t.Fatal("slot was not reusable")
	}
}

// Models the relevant tsnet *gonet.TCPConn contract: net.Conn + CloseWrite,
// without being the host kernel's concrete *net.TCPConn.
type auditHalfCloseConn struct{ *net.TCPConn }

func TestProxyResponseStreamOutlivesRequestReadBudget(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "first\n")
		w.(http.Flusher).Flush()
		time.Sleep(100 * time.Millisecond)
		_, _ = io.WriteString(w, "last\n")
	}))
	defer backend.Close()
	certServer := httptest.NewTLSServer(http.NotFoundHandler())
	defer certServer.Close()
	for _, useTLS := range []bool{false, true} {
		t.Run(fmt.Sprintf("tls_limited=%v", useTLS), func(t *testing.T) {
			h, err := NewProxyHandler(backend.URL, nil)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			var ln net.Listener = raw
			scheme := "http://"
			client := &http.Client{Timeout: 2 * time.Second}
			if useTLS {
				ln = newLimitedListener(tls.NewListener(raw, certServer.TLS.Clone()), 256, "http", "stream")
				scheme = "https://"
				client = certServer.Client()
				client.Timeout = 2 * time.Second
			}
			srv := newHTTPServerFn(ResourceBudgetMiddleware(h))
			// Scale the request read budget down; never cap response duration.
			srv.ReadTimeout = 25 * time.Millisecond
			done := make(chan struct{})
			go func() { defer close(done); _ = srv.Serve(ln) }()
			defer func() { _ = srv.Close(); _ = ln.Close(); <-done }()
			resp, err := client.Get(scheme + raw.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil || string(body) != "first\nlast\n" {
				t.Fatalf("stream truncated: %q error=%v", body, err)
			}
		})
	}
}

func TestTCPBackendHalfCloseReachesTsnetShapedClient(t *testing.T) {
	for _, wrapped := range []bool{false, true} {
		t.Run(fmt.Sprintf("tsnet_shape=%v", wrapped), func(t *testing.T) {
			backend, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer backend.Close()
			backendDone := make(chan struct{})
			go func() {
				defer close(backendDone)
				c, err := backend.Accept()
				if err != nil {
					return
				}
				defer c.Close()
				_, _ = io.WriteString(c, "response")
				_ = c.(*net.TCPConn).CloseWrite()
				_, _ = io.Copy(io.Discard, c)
			}()
			front, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer front.Close()
			client, err := net.Dial("tcp", front.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			conn, err := front.Accept()
			if err != nil {
				t.Fatal(err)
			}
			if wrapped {
				conn = &auditHalfCloseConn{conn.(*net.TCPConn)}
			}
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			go func() { defer close(done); handleTCPConn(ctx, conn, backend.Addr().String(), "audit") }()
			defer func() { cancel(); _ = client.Close(); <-done; <-backendDone }()
			_ = client.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
			body, err := io.ReadAll(client)
			t.Logf("backend sent FIN; wrapped=%v body=%q readError=%v", wrapped, body, err)
			if string(body) != "response" {
				t.Fatalf("fixture did not forward payload: %q", body)
			}
			if err != nil {
				t.Errorf("backend EOF was not forwarded to client: %v", err)
			}
			_ = client.(*net.TCPConn).CloseWrite()
		})
	}
}

func TestTCPClientHalfCloseReachesWrappedBackend(t *testing.T) {
	backend, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	backendResult := make(chan string, 1)
	go func() {
		conn, err := backend.Accept()
		if err != nil {
			backendResult <- err.Error()
			return
		}
		defer conn.Close()
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		body, err := io.ReadAll(conn)
		if err != nil {
			backendResult <- err.Error()
			return
		}
		backendResult <- string(body)
		_, _ = io.WriteString(conn, "ack")
		_ = conn.(*net.TCPConn).CloseWrite()
	}()
	oldDial := tcpDialContext
	tcpDialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		conn, err := oldDial(ctx, network, address)
		if err != nil {
			return nil, err
		}
		return &auditHalfCloseConn{conn.(*net.TCPConn)}, nil
	}
	defer func() { tcpDialContext = oldDial }()
	front, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer front.Close()
	client, err := net.Dial("tcp", front.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	conn, err := front.Accept()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); handleTCPConn(ctx, conn, backend.Addr().String(), "wrapped-backend") }()
	defer func() { cancel(); _ = client.Close(); <-done }()
	if _, err := io.WriteString(client, "request"); err != nil {
		t.Fatal(err)
	}
	if err := client.(*net.TCPConn).CloseWrite(); err != nil {
		t.Fatal(err)
	}
	_ = client.SetReadDeadline(time.Now().Add(2 * time.Second))
	response, err := io.ReadAll(client)
	if err != nil || string(response) != "ack" {
		t.Fatalf("response = %q, error = %v, want ack and EOF", response, err)
	}
	if got := <-backendResult; got != "request" {
		t.Fatalf("backend received %q, want request and EOF", got)
	}
}
