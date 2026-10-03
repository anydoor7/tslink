package server

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"testing/synctest"
	"time"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/inspect"
	"github.com/anydoor7/tslink/internal/registry"
	tsRuntime "github.com/anydoor7/tslink/internal/runtime"
	"tailscale.com/client/local"
)

func uploadFront(t *testing.T, service registry.Service, report func(inspect.WarningView), handler http.Handler) *httptest.Server {
	t.Helper()
	front := httptest.NewUnstartedServer(AccessLogMiddleware(service.Name, nil, RequestLimitsMiddleware(service, report, handler)))
	front.Config = newHTTPServerFn(front.Config.Handler)
	front.Listener = configureServiceHTTP(front.Config, service, front.Listener, report)
	front.Start()
	t.Cleanup(front.Close)
	return front
}
func uploadProxy(t *testing.T, backend *httptest.Server) http.Handler {
	t.Helper()
	h, err := NewProxyHandler(backend.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// Exercise the same TLS -> connection cap -> header budget -> access log ->
// request limits chain as startNodeLocked, without any tailnet or owner state.
func uploadTLSFront(t *testing.T, svc registry.Service, handler http.Handler, h2 bool) (string, *limitedListener) {
	return uploadTLSFrontWithReport(t, svc, handler, h2, nil)
}

func uploadTLSFrontWithReport(t *testing.T, svc registry.Service, handler http.Handler, h2 bool, report func(inspect.WarningView)) (string, *limitedListener) {
	t.Helper()
	addr, limited, _ := uploadTLSFrontServer(t, svc, handler, h2, report, 1)
	return addr, limited
}

func uploadTLSFrontServer(t *testing.T, svc registry.Service, handler http.Handler, h2 bool, report func(inspect.WarningView), cap int, prepare ...func(*http.Server)) (string, *limitedListener, *http.Server) {
	t.Helper()
	cert := httptest.NewTLSServer(http.NotFoundHandler())
	t.Cleanup(cert.Close)
	tlsConfig := cert.TLS.Clone()
	if h2 {
		tlsConfig.NextProtos = []string{"h2", "http/1.1"}
	}
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	limited := newLimitedListener(tls.NewListener(raw, tlsConfig), cap, "http", svc.Name).(*limitedListener)
	srv := newHTTPServerFn(AccessLogMiddleware(svc.Name, nil, RequestLimitsMiddleware(svc, report, handler)))
	ln := configureServiceHTTP(srv, svc, limited, report)
	for _, setup := range prepare {
		setup(srv)
	}
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ln) }()
	t.Cleanup(func() {
		_ = srv.Close()
		if err := <-done; err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Error(err)
		}
	})
	return raw.Addr().String(), limited, srv
}

func TestRequestLimitsTLSHandshakeLifecycle(t *testing.T) {
	t.Run("handshake_deadline", assertTLSHandshakeDeadline)
	t.Run("configured_timeout", func(t *testing.T) {
		svc := registry.Service{Name: "handshake-timeout", Type: registry.TypeFile, RequestLimits: &registry.RequestLimits{HeaderTimeout: "1s"}}
		addr, limited, _ := uploadTLSFrontServer(t, svc, http.NotFoundHandler(), false, nil, 1)
		client, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		defer client.Close()
		deadline := time.Now().Add(5 * time.Second)
		for len(limited.sem) != 1 && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		if len(limited.sem) != 1 {
			t.Fatal("stalled handshake did not acquire its connection slot")
		}
		_ = client.SetReadDeadline(deadline)
		if _, err := client.Read(make([]byte, 1)); !errors.Is(err, io.EOF) && !errors.Is(err, syscall.ECONNRESET) {
			t.Fatalf("configured header timeout did not close stalled TLS handshake with EOF or reset: %v", err)
		}
		uploadRelease(t, limited, deadline)
	})
	for _, path := range []string{"client_close", "invalid", "close", "shutdown"} {
		t.Run(path, func(t *testing.T) {
			// The virtual subtest pins the short inner timer. Real socket
			// recovery uses the service policy, including healthy handshakes.
			svc := registry.Service{Name: "handshake", Type: registry.TypeFile}
			addr, limited, srv := uploadTLSFrontServer(t, svc, http.NotFoundHandler(), true, nil, 1)
			client, err := net.Dial("tcp", addr)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			deadline := time.Now().Add(5 * time.Second)
			for len(limited.sem) != 1 && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			if len(limited.sem) != 1 {
				t.Fatal("stalled handshake did not acquire its connection slot")
			}
			switch path {
			case "client_close":
				_ = client.Close()
			case "invalid":
				_, _ = fmt.Fprint(client, "not a TLS record")
			case "close":
				if err := srv.Close(); err != nil {
					t.Fatal(err)
				}
			case "shutdown":
				ctx, cancel := context.WithDeadline(context.Background(), deadline)
				defer cancel()
				if err := srv.Shutdown(ctx); err != nil {
					t.Fatal(err)
				}
			}
			if path != "client_close" {
				_ = client.SetReadDeadline(deadline)
				if _, err := client.Read(make([]byte, 1)); err == nil || isTimeout(err) {
					t.Fatalf("unserved handshake was not closed before the hang guard: %v", err)
				}
			}
			uploadRelease(t, limited, deadline)
			if path == "client_close" || path == "invalid" {
				// A failed handshake must not stop the accept loop.
				tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, ForceAttemptHTTP2: true}
				defer tr.CloseIdleConnections()
				resp, err := (&http.Client{Transport: tr, Timeout: 5 * time.Second}).Get("https://" + addr)
				if err != nil {
					t.Fatal(err)
				}
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
				if resp.ProtoMajor != 2 || resp.StatusCode != 404 {
					t.Fatalf("handshake recovery response=%s %d", resp.Proto, resp.StatusCode)
				}
				tr.CloseIdleConnections()
				uploadRelease(t, limited, time.Now().Add(5*time.Second), tr)
			}
		})
	}
}

// Joining preparation is test cleanup, not part of listener Close. The caller
// must close the listener first so acceptLoop cannot add another worker.
func uploadJoinPreparation(t *testing.T, ln *headerBudgetListener) {
	t.Helper()
	done := make(chan struct{})
	go func() { ln.workers.Wait(); close(done) }()
	uploadWait(t, done, 5*time.Second, "preparation workers did not exit")
}

type uploadJoinedAcceptListener struct {
	*headerBudgetListener
	acceptDone chan struct{}
}

func (l *uploadJoinedAcceptListener) Accept() (net.Conn, error) {
	// Serve registers its listener before calling Accept. Starting here keeps
	// that production ordering while allowing the test to join the accept loop.
	l.once.Do(func() { go func() { l.acceptLoop(); close(l.acceptDone) }() })
	return l.headerBudgetListener.Accept()
}

// Port the review's stop controls through the actual pinned SDK callback and a
// loopback LocalAPI. GetCertificate owns a background context, so closing the
// TLS connection and expiring HandshakeContext cannot release this dependency.
func TestRequestLimitsTLSCertificateLookupStop(t *testing.T) {
	for _, capped := range []bool{false, true} {
		for _, stop := range []string{"listener_close", "server_close", "shutdown_100ms", "shutdown_production_budget"} {
			t.Run(fmt.Sprintf("capped=%v/%s", capped, stop), func(t *testing.T) {
				entered, release, lookupDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
				var once sync.Once
				unblock := func() { once.Do(func() { close(release) }) }
				backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path != "/localapi/v0/cert/share.example.invalid" {
						t.Errorf("unexpected LocalAPI path: %s", r.URL.Path)
					}
					close(entered)
					<-release
					http.Error(w, "injected delayed certificate lookup", http.StatusServiceUnavailable)
				}))
				defer backend.Close()
				defer unblock()
				tr := &http.Transport{DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
					return (&net.Dialer{}).DialContext(ctx, "tcp", backend.Listener.Addr().String())
				}}
				defer tr.CloseIdleConnections()
				lc := &local.Client{Transport: tr, OmitAuth: true}
				cfg := &tls.Config{GetCertificate: func(hi *tls.ClientHelloInfo) (*tls.Certificate, error) {
					defer close(lookupDone)
					return lc.GetCertificate(hi)
				}}
				raw, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				var transport net.Listener = tls.NewListener(raw, cfg)
				var limited *limitedListener
				if capped {
					limited = newLimitedListener(transport, 1, "http", "certificate").(*limitedListener)
					transport = limited
				}
				srv := newHTTPServerFn(http.NotFoundHandler())
				ln := configureServiceHTTP(srv, registry.Service{Type: registry.TypeFile, RequestLimits: &registry.RequestLimits{HeaderTimeout: "100ms"}}, transport, nil).(*headerBudgetListener)
				acceptDone := make(chan struct{})
				served := make(chan error, 1)
				go func() {
					served <- srv.Serve(&uploadJoinedAcceptListener{headerBudgetListener: ln, acceptDone: acceptDone})
				}()
				connected := make(chan error, 1)
				connectedDone := make(chan struct{})
				go func() {
					defer close(connectedDone)
					c, err := tls.DialWithDialer(&net.Dialer{Timeout: 10 * time.Second}, "tcp", raw.Addr().String(), &tls.Config{InsecureSkipVerify: true, ServerName: "share.example.invalid"})
					if c != nil {
						_ = c.Close()
					}
					connected <- err
				}()
				defer func() {
					unblock()
					_ = srv.Close()
					select {
					case err := <-served:
						if !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
							t.Error(err)
						}
					case <-time.After(5 * time.Second):
						t.Error("Serve did not exit")
					}
					uploadWait(t, acceptDone, 5*time.Second, "accept loop did not exit")
					uploadWait(t, connectedDone, 5*time.Second, "TLS client did not exit")
					uploadJoinPreparation(t, ln)
				}()
				uploadWait(t, entered, 5*time.Second, "actual GetCertificate was not reached")
				if capped && len(limited.sem) != 1 {
					t.Fatal("handshake did not acquire cap")
				}
				// Completion while the provider is still blocked is the contract.
				// Socket scheduling is bounded only by a loose hang guard.
				budget, bound := 100*time.Millisecond, 5*time.Second
				if stop == "shutdown_production_budget" {
					budget, bound = httpShutdownTimeout, 6*time.Second
				}
				ctx, cancel := context.WithTimeout(context.Background(), budget)
				defer cancel()
				stopped := make(chan error, 1)
				stopDone := make(chan struct{})
				defer func() { unblock(); uploadWait(t, stopDone, 5*time.Second, "stop goroutine did not exit") }()
				start := time.Now()
				go func() {
					defer close(stopDone)
					switch stop {
					case "listener_close":
						stopped <- ln.Close()
					case "server_close":
						stopped <- srv.Close()
					default:
						stopped <- srv.Shutdown(ctx)
					}
				}()
				select {
				case err := <-stopped:
					if err != nil {
						t.Errorf("stop returned %v", err)
					}
					t.Logf("stop returned in %s with provider still blocked (budget %s)", time.Since(start), budget)
				case <-time.After(bound):
					t.Errorf("stop blocked for %s behind GetCertificate; shutdown context=%v", time.Since(start), ctx.Err())
					unblock()
					select {
					case <-stopped:
					case <-time.After(5 * time.Second):
						t.Fatal("stop did not finish after provider release")
					}
				}
				select {
				case err := <-connected:
					if err == nil {
						t.Error("closed raw transport completed TLS")
					}
				case <-time.After(5 * time.Second):
					t.Error("raw transport survived stop")
					unblock()
					<-connected
				}
				if capped {
					if len(limited.sem) != 0 {
						t.Fatal("stop retained the connection cap")
					}
					// Reuse the released slot before the worker finishes. A second
					// release would consume this token or block forever.
					limited.sem <- struct{}{}
				}
				unblock()
				uploadWait(t, lookupDone, 5*time.Second, "certificate provider did not exit")
				uploadJoinPreparation(t, ln)
				if capped && len(limited.sem) != 1 {
					t.Error("late worker released the cap twice")
				}
				if capped && len(limited.sem) == 1 {
					<-limited.sem // remove the test's replacement token
				}
				ln.mu.Lock()
				pending := len(ln.pending)
				ln.mu.Unlock()
				tracked := 0
				ln.tlsSlots.Range(func(_, _ any) bool { tracked++; return true })
				if pending != 0 || tracked != 0 {
					t.Errorf("late preparation leaked: pending=%d tracked=%d", pending, tracked)
				}
				if c, err := ln.Accept(); c != nil || !errors.Is(err, net.ErrClosed) {
					t.Errorf("closed listener returned late handoff: %v, %v", c, err)
				}
			})
		}
	}
}

func TestRequestLimitsTLSLateHandoff(t *testing.T) {
	for _, capped := range []bool{false, true} {
		t.Run(fmt.Sprintf("capped=%v", capped), func(t *testing.T) {
			cert := httptest.NewTLSServer(http.NotFoundHandler())
			defer cert.Close()
			cfg := cert.TLS.Clone()
			cfg.NextProtos = []string{"h2"}
			cfg.MaxVersion, cfg.SessionTicketsDisabled = tls.VersionTLS12, true
			raw, peer := net.Pipe()
			defer raw.Close()
			defer peer.Close()
			native := tls.Server(raw, cfg)
			client := tls.Client(peer, &tls.Config{InsecureSkipVerify: true, NextProtos: []string{"h2"}, MaxVersion: tls.VersionTLS12})
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			handshaken := make(chan error, 1)
			go func() { handshaken <- native.HandshakeContext(ctx) }()
			if err := client.HandshakeContext(ctx); err != nil {
				t.Fatal(err)
			}
			if err := <-handshaken; err != nil {
				t.Fatal(err)
			}
			if native.ConnectionState().NegotiatedProtocol != "h2" {
				t.Fatal("late-handoff control did not negotiate h2")
			}
			var original net.Conn = native
			slots := make(chan struct{}, 1)
			if capped {
				slots <- struct{}{}
				original = &limitedTLSConn{limitedConn: &limitedConn{Conn: native, release: func() { <-slots }}, tlsConn: native}
			}
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			ln := configureServiceHTTP(newHTTPServerFn(http.NotFoundHandler()), registry.Service{Type: registry.TypeFile}, listener, nil).(*headerBudgetListener)
			ln.once.Do(func() {})
			// Model a worker admitted before Close whose handshake is already
			// complete, but which resumes the handoff only after Close returns.
			ln.pending[original] = struct{}{}
			ln.workers.Add(1)
			resume := make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(resume) }) }
			go func() { <-resume; ln.prepare(original) }()
			defer func() { unblock(); _ = ln.Close(); uploadJoinPreparation(t, ln) }()
			closed := make(chan error, 1)
			go func() { closed <- ln.Close() }()
			select {
			case err := <-closed:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Error("Close waited for a worker before its late handoff")
				unblock()
				<-closed
			}
			unblock()
			uploadJoinPreparation(t, ln)
			ln.mu.Lock()
			pending := len(ln.pending)
			ln.mu.Unlock()
			tracked := 0
			ln.tlsSlots.Range(func(_, _ any) bool { tracked++; return true })
			if pending != 0 || tracked != 0 || len(slots) != 0 {
				t.Errorf("late h2 handoff leaked: pending=%d tracked=%d slots=%d", pending, tracked, len(slots))
			}
			if c, err := ln.Accept(); c != nil || !errors.Is(err, net.ErrClosed) {
				t.Errorf("closed listener delivered late h2 handoff: %v, %v", c, err)
			}
		})
	}
}

func TestRequestLimitsTLSHandshakeDoesNotBlockAccept(t *testing.T) {
	svc := registry.Service{Name: "parallel-handshake", Type: registry.TypeFile, RequestLimits: &registry.RequestLimits{HeaderTimeout: "1m"}}
	addr, limited, _ := uploadTLSFrontServer(t, svc, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }), true, nil, 2)
	stalled, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer stalled.Close()
	deadline := time.Now().Add(5 * time.Second)
	for len(limited.sem) != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(limited.sem) != 1 {
		t.Fatal("stalled handshake did not acquire its connection slot")
	}
	tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, ForceAttemptHTTP2: true}
	defer tr.CloseIdleConnections()
	resp, err := (&http.Client{Transport: tr, Timeout: 5 * time.Second}).Get("https://" + addr)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 204 || resp.ProtoMajor != 2 || len(limited.sem) != 2 {
		t.Fatalf("parallel response=%s %d active=%d", resp.Proto, resp.StatusCode, len(limited.sem))
	}
}

func TestRequestLimitsTLSHTTP2ServerLifecycle(t *testing.T) {
	for _, shutdown := range []bool{false, true} {
		t.Run(fmt.Sprintf("shutdown=%v", shutdown), func(t *testing.T) {
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			defer unblock()
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				close(entered)
				select {
				case <-release:
					w.WriteHeader(204)
				case <-r.Context().Done():
				}
			})
			addr, limited, srv := uploadTLSFrontServer(t, registry.Service{Name: "h2-lifecycle", Type: registry.TypeFile}, handler, true, nil, 1)
			tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, ForceAttemptHTTP2: true}
			defer tr.CloseIdleConnections()
			response := make(chan error, 1)
			go func() {
				resp, err := (&http.Client{Transport: tr, Timeout: 5 * time.Second}).Get("https://" + addr)
				if err == nil {
					_ = resp.Body.Close()
					if resp.StatusCode != 204 || resp.ProtoMajor != 2 {
						err = fmt.Errorf("response=%s %d", resp.Proto, resp.StatusCode)
					}
				}
				response <- err
			}()
			uploadWait(t, entered, 5*time.Second, "HTTP/2 handler was not reached")
			if len(limited.sem) != 1 {
				t.Fatal("HTTP/2 connection did not retain its cap")
			}
			if shutdown {
				stopped := make(chan error, 1)
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				go func() { stopped <- srv.Shutdown(ctx) }()
				select {
				case err := <-response:
					t.Fatalf("Shutdown aborted an active stream: %v", err)
				case <-time.After(30 * time.Millisecond):
				}
				unblock()
				if err := <-response; err != nil {
					t.Fatal(err)
				}
				if err := <-stopped; err != nil {
					t.Fatal(err)
				}
			} else {
				if err := srv.Close(); err != nil {
					t.Fatal(err)
				}
				if err := <-response; err == nil {
					t.Fatal("Close did not interrupt the active HTTP/2 stream")
				}
			}
			uploadRelease(t, limited, time.Now().Add(5*time.Second), tr)
		})
	}
}

func TestRequestLimitsTLSPendingHandoffClosed(t *testing.T) {
	for _, capped := range []bool{false, true} {
		t.Run(fmt.Sprintf("capped=%v", capped), func(t *testing.T) {
			cert := httptest.NewTLSServer(http.NotFoundHandler())
			defer cert.Close()
			tlsConfig := cert.TLS.Clone()
			tlsConfig.NextProtos = []string{"h2", "http/1.1"}
			raw, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			var transport net.Listener = tls.NewListener(raw, tlsConfig)
			var limited *limitedListener
			if capped {
				limited = newLimitedListener(transport, 1, "http", "handoff").(*limitedListener)
				transport = limited
			}
			srv := newHTTPServerFn(http.NotFoundHandler())
			ln := configureServiceHTTP(srv, registry.Service{Type: registry.TypeFile}, transport, nil).(*headerBudgetListener)
			defer ln.Close()
			// Start accepting while the HTTP server has not requested the next
			// prepared connection yet. The completed TLS handoff must remain
			// owned by the listener until Accept returns it.
			ln.once.Do(func() { go ln.acceptLoop() })
			peer, err := tls.Dial("tcp", raw.Addr().String(), &tls.Config{InsecureSkipVerify: true, NextProtos: []string{"h2"}})
			if err != nil {
				t.Fatal(err)
			}
			defer peer.Close()
			deadline := time.Now().Add(5 * time.Second)
			var tracked int
			for tracked == 0 && time.Now().Before(deadline) {
				ln.tlsSlots.Range(func(_, _ any) bool { tracked++; return true })
				if tracked == 0 {
					time.Sleep(time.Millisecond)
				}
			}
			if tracked != 1 || (limited != nil && len(limited.sem) != 1) {
				t.Fatalf("completed TLS handoff was not tracked: connections=%d", tracked)
			}
			if err := ln.Close(); err != nil {
				t.Fatal(err)
			}
			uploadJoinPreparation(t, ln)
			_ = peer.SetReadDeadline(time.Now().Add(5 * time.Second))
			if _, err := peer.Read(make([]byte, 1)); err == nil || isTimeout(err) {
				t.Fatalf("completed but unserved TLS connection survived Close: %v", err)
			}
			ln.mu.Lock()
			pending := len(ln.pending)
			ln.mu.Unlock()
			tracked = 0
			ln.tlsSlots.Range(func(_, _ any) bool { tracked++; return true })
			if pending != 0 || tracked != 0 || (limited != nil && len(limited.sem) != 0) {
				t.Fatalf("unserved TLS handoff leaked: pending=%d tracked=%d", pending, tracked)
			}
		})
	}
}

type uploadTemporaryAcceptError struct{}

func (uploadTemporaryAcceptError) Error() string   { return "temporary accept failure" }
func (uploadTemporaryAcceptError) Timeout() bool   { return false }
func (uploadTemporaryAcceptError) Temporary() bool { return true }

type uploadAcceptControl struct {
	net.Listener
	once       sync.Once
	firstError error
	accepted   chan struct{}
	released   chan struct{}
}

func (l *uploadAcceptControl) Accept() (net.Conn, error) {
	var err error
	l.once.Do(func() { err = l.firstError })
	if err != nil {
		return nil, err
	}
	c, err := l.Listener.Accept()
	if err == nil && l.accepted != nil {
		close(l.accepted)
		<-l.released
	}
	return c, err
}

func (l *uploadAcceptControl) Close() error {
	if l.released != nil {
		close(l.released)
	}
	return l.Listener.Close()
}

func TestRequestLimitsTLSAcceptLifecycle(t *testing.T) {
	for _, path := range []string{"temporary", "closed_during_accept", "closed_handoff", "closed_error"} {
		t.Run(path, func(t *testing.T) {
			raw, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer raw.Close()
			limited := newLimitedListener(raw, 1, "http", "accept").(*limitedListener)
			control := &uploadAcceptControl{Listener: limited}
			if path == "temporary" {
				control.firstError = uploadTemporaryAcceptError{}
			}
			if path == "closed_during_accept" {
				control.accepted, control.released = make(chan struct{}), make(chan struct{})
			}
			srv := newHTTPServerFn(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
			ln := configureServiceHTTP(srv, registry.Service{Type: registry.TypeFile}, control, nil).(*headerBudgetListener)
			defer ln.Close()
			if path == "temporary" {
				done := make(chan error, 1)
				go func() { done <- srv.Serve(ln) }()
				defer func() { _ = srv.Close(); <-done }()
				resp, err := (&http.Client{Timeout: 5 * time.Second}).Get("http://" + raw.Addr().String())
				if err != nil {
					t.Fatal(err)
				}
				_ = resp.Body.Close()
				if resp.StatusCode != 204 {
					t.Fatalf("temporary accept failure stopped serving: %d", resp.StatusCode)
				}
				return
			}
			if path == "closed_during_accept" {
				ln.once.Do(func() { go ln.acceptLoop() })
			}
			peer, err := net.Dial("tcp", raw.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			defer peer.Close()
			if path == "closed_during_accept" {
				uploadWait(t, control.accepted, 5*time.Second, "accept did not acquire the connection")
				if len(limited.sem) != 1 {
					t.Fatal("accept did not acquire the slot")
				}
				_ = ln.Close()
			} else {
				original, err := limited.Accept()
				if err != nil {
					t.Fatal(err)
				}
				defer original.Close()
				if len(limited.sem) != 1 {
					t.Fatal("handoff did not acquire the slot")
				}
				// Model the schedule in which the ready send wins the select,
				// but Close owns the listener before Accept checks ownership.
				ln.once.Do(func() {})
				ln.ready = make(chan preparedHTTPConn, 1)
				if path == "closed_handoff" {
					ln.ready <- preparedHTTPConn{conn: original, original: original}
				} else {
					ln.ready <- preparedHTTPConn{err: net.ErrClosed}
				}
				ln.closed = true
				if got, err := ln.Accept(); got != nil || !errors.Is(err, net.ErrClosed) {
					t.Fatalf("closed listener returned connection=%v error=%v", got, err)
				}
				if path == "closed_error" {
					_ = original.Close()
				}
			}
			_ = peer.SetReadDeadline(time.Now().Add(5 * time.Second))
			if _, err := peer.Read(make([]byte, 1)); err == nil || isTimeout(err) {
				t.Fatalf("connection survived the closed listener: %v", err)
			}
			uploadRelease(t, limited, time.Now().Add(5*time.Second))
		})
	}
}

func TestRequestLimitsTLSUnconsumedBodyDrain(t *testing.T) {
	t.Run("disposal_deadline", assertUploadDisposalDeadline)
	for _, path := range []string{"file", "file_flush", "acl_denied", "body_limit", "body_close"} {
		for _, chunked := range []bool{false, true} {
			for _, complete := range []bool{true, false} {
				t.Run(fmt.Sprintf("%s/chunked=%v/complete=%v", path, chunked, complete), func(t *testing.T) {
					t.Setenv(config.ConfigDirEnv, t.TempDir())
					// Allow Go's additional 500ms TCP reset-avoidance close
					// grace after our 100ms drain deadline has expired.
					const bound = 5 * time.Second // hang guard; virtual subtest asserts the disposal policy
					svc := registry.Service{Name: "drain", Type: registry.TypeFile, RequestLimits: &registry.RequestLimits{ReadTimeout: "100ms", IdleTimeout: "5s"}}
					dir := t.TempDir()
					urlPath := "/"
					if path == "file_flush" {
						if err := os.WriteFile(filepath.Join(dir, "large.txt"), []byte(strings.Repeat("x", 16<<10)), 0600); err != nil {
							t.Fatal(err)
						}
						urlPath = "/large.txt"
					}
					file, err := NewFileHandler(dir)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { _ = file.Close() })
					var handler http.Handler = file
					want := http.StatusOK
					switch path {
					case "acl_denied":
						handler = ACLMiddleware([]string{"blocked@example.invalid"}, nil)(handler)
						want = http.StatusForbidden
					case "body_limit":
						svc.RequestLimits.MaxBody = "8B"
						want = http.StatusRequestEntityTooLarge
						// A chunked body has no known length: it must cross the
						// streaming cap before 413, then stall before its terminator.
						backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
							_, _ = io.Copy(io.Discard, r.Body)
							w.WriteHeader(http.StatusNoContent)
						}))
						t.Cleanup(backend.Close)
						handler = uploadProxy(t, backend)
					case "body_close":
						want = http.StatusNoContent
						handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
							_, _ = r.Body.Read(make([]byte, 1))
							_ = r.Body.Close()
							w.WriteHeader(want)
						})
					}
					closed := make(chan struct{})
					addr, limited, srv := uploadTLSFrontServer(t, svc, handler, false, nil, 1, func(srv *http.Server) {
						prior := srv.ConnState
						srv.ConnState = func(conn net.Conn, state http.ConnState) {
							prior(conn, state)
							if state == http.StateClosed {
								close(closed)
							}
						}
					})
					conn, err := tls.Dial("tcp", addr, &tls.Config{InsecureSkipVerify: true})
					if err != nil {
						t.Fatal(err)
					}
					defer conn.Close()
					// Prove that the slot was acquired; a zero-only assertion
					// could otherwise pass without observing the limited listener.
					if got := len(limited.sem); got != 1 {
						t.Fatalf("active connections=%d, want 1", got)
					}
					start := time.Now()
					deadline := start.Add(bound)
					_ = conn.SetDeadline(deadline)
					body := "x"
					if complete || (path == "body_limit" && chunked) {
						body = "123456789"
					}
					framing := "Content-Length: 9\r\n"
					if chunked {
						framing = "Transfer-Encoding: chunked\r\n"
						body = fmt.Sprintf("%x\r\n%s\r\n", len(body), body)
						if complete {
							body += "0\r\n\r\n"
						}
					}
					if _, err := fmt.Fprintf(conn, "POST %s HTTP/1.1\r\nHost: drain\r\n%s\r\n%s", urlPath, framing, body); err != nil {
						t.Fatal(err)
					}
					resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
					if err != nil {
						if complete || !errors.Is(err, io.EOF) {
							t.Errorf("no response or EOF within %s: %v", bound, err)
						}
					} else {
						if resp.StatusCode != want {
							t.Errorf("status=%d, want %d", resp.StatusCode, want)
						}
						if _, err := io.Copy(io.Discard, resp.Body); err != nil {
							t.Errorf("incomplete response: %v", err)
						}
						_ = resp.Body.Close()
					}
					if complete {
						_ = conn.Close()
					}
					// For partial bodies the client stays open: server-side
					// disposal, rather than test cleanup, must release the slot.
					// StateClosed follows connection close and the production
					// slot release. Join that event instead of polling a clock.
					select {
					case <-closed:
					case <-time.After(time.Until(deadline)):
						_ = conn.Close()
						_ = srv.Close()
						select {
						case <-closed:
						case <-time.After(bound):
							t.Fatal("connection-close join did not finish after forced transport shutdown")
						}
						t.Fatal("server did not dispose of the request body and close the connection before the hang guard")
					}
					if got := len(limited.sem); got != 0 {
						t.Errorf("closed connection still holds its slot: active=%d", got)
					}
					t.Logf("response/EOF and slot check after %s; complete=%v", time.Since(start), complete)
				})
			}
		}
	}
}

func TestRequestLimitsTLSProgressAndStall(t *testing.T) {
	for _, h2 := range []bool{false, true} {
		for _, progress := range []bool{false, true} {
			t.Run(fmt.Sprintf("http2=%v/progress=%v", h2, progress), func(t *testing.T) {
				backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if _, err := io.Copy(io.Discard, r.Body); err != nil {
						return
					}
					w.WriteHeader(http.StatusNoContent)
				}))
				t.Cleanup(backend.Close)
				svc := registry.Service{Name: "tls-upload", Type: registry.TypeProxy, RequestLimits: &registry.RequestLimits{ReadTimeout: "200ms"}}
				if progress {
					svc.RequestLimits.ReadTimeout = "5s"
				}
				addr, _ := uploadTLSFront(t, svc, uploadProxy(t, backend), h2)
				tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, ForceAttemptHTTP2: h2}
				defer tr.CloseIdleConnections()
				reader, writer := io.Pipe()
				done := make(chan struct{})
				go func() {
					defer close(done)
					defer writer.Close()
					_, _ = writer.Write([]byte("a"))
					if progress {
						for i := 0; i < 9; i++ {
							if _, err := writer.Write([]byte("a")); err != nil {
								return
							}
						}
					} else {
						time.Sleep(700 * time.Millisecond)
					}
				}()
				defer func() { reader.Close(); writer.Close(); <-done }()
				req, _ := http.NewRequest("POST", "https://"+addr, reader)
				req.ContentLength = 10
				client := &http.Client{Transport: tr, Timeout: 5 * time.Second}
				resp, err := client.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				defer resp.Body.Close()
				body, _ := io.ReadAll(resp.Body)
				want := http.StatusRequestTimeout
				if progress {
					want = http.StatusNoContent
				}
				if resp.StatusCode != want || (!progress && !strings.Contains(string(body), "--request-read-timeout")) {
					t.Fatalf("status=%d body=%q, want %d", resp.StatusCode, body, want)
				}
				if h2 && resp.ProtoMajor != 2 {
					t.Fatalf("HTTP/2 control negotiated %s", resp.Proto)
				}
			})
		}
	}
}

func TestRequestLimitsTLSDisposalAbsoluteDeadline(t *testing.T) {
	t.Run("disposal_deadline", assertUploadDisposalDeadline)
	for _, idle := range []string{"100ms", "2m"} {
		t.Run(idle, func(t *testing.T) {
			file, err := NewFileHandler(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = file.Close() })
			svc := registry.Service{Name: "drip", Type: registry.TypeFile, RequestLimits: &registry.RequestLimits{ReadTimeout: idle, IdleTimeout: "5s"}}
			addr, limited := uploadTLSFront(t, svc, file, false)
			conn, err := tls.Dial("tcp", addr, &tls.Config{InsecureSkipVerify: true})
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			if len(limited.sem) != 1 {
				t.Fatal("connection slot was not acquired")
			}
			// Keep sending chunked bytes every 25ms. This is disposal after
			// an unused body, so progress must not renew the absolute deadline.
			const bound = 5 * time.Second // hang guard; virtual subtest asserts the disposal policy
			deadline := time.Now().Add(bound)
			_ = conn.SetDeadline(deadline)
			if _, err := fmt.Fprint(conn, "POST / HTTP/1.1\r\nHost: drip\r\nTransfer-Encoding: chunked\r\n\r\n1\r\nx\r\n"); err != nil {
				t.Fatal(err)
			}
			stop := make(chan struct{})
			done := make(chan struct{})
			go func() {
				defer close(done)
				ticker := time.NewTicker(25 * time.Millisecond)
				defer ticker.Stop()
				for {
					select {
					case <-stop:
						return
					case <-ticker.C:
						if _, err := fmt.Fprint(conn, "1\r\nx\r\n"); err != nil {
							return
						}
					}
				}
			}()
			defer func() { close(stop); <-done }()
			resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
			if err != nil {
				t.Errorf("no bounded response: %v", err)
			} else {
				_, err := io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
				if resp.StatusCode != http.StatusOK || err != nil {
					t.Errorf("status=%d body read=%v", resp.StatusCode, err)
				}
			}
			deadline = time.Now().Add(5 * time.Second)
			for len(limited.sem) != 0 && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			if len(limited.sem) != 0 {
				t.Errorf("dripping client retained slot beyond %s with read timeout %s", bound, idle)
			}
		})
	}
}

type uploadZeros struct{}

func (uploadZeros) Read(p []byte) (int, error) { clear(p); return len(p), nil }

func TestRequestLimitsLargeUploadStreamsWithBoundedHeap(t *testing.T) {
	const size int64 = 512 << 20
	var baseline uint64
	var peak atomic.Uint64
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 64<<10)
		var count int64
		for {
			n, err := r.Body.Read(buf)
			count += int64(n)
			if count%(4<<20) < int64(n) {
				var mem runtime.MemStats
				runtime.ReadMemStats(&mem)
				if mem.HeapAlloc > peak.Load() {
					peak.Store(mem.HeapAlloc)
				}
			}
			if err == io.EOF {
				break
			}
			if err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
		}
		w.Header().Set("Upload-Bytes", strconv.FormatInt(count, 10))
		w.WriteHeader(204)
	}))
	defer backend.Close()
	svc := registry.Service{Name: "photos", Type: registry.TypeProxy, RequestLimits: &registry.RequestLimits{MaxBody: "600MiB"}}
	front := uploadFront(t, svc, nil, uploadProxy(t, backend))
	runtime.GC()
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	baseline = mem.HeapAlloc
	req, _ := http.NewRequest("POST", front.URL, io.LimitReader(uploadZeros{}, size))
	req.ContentLength = size
	resp, err := front.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 204 || resp.Header.Get("Upload-Bytes") != strconv.FormatInt(size, 10) {
		t.Fatalf("status=%d bytes=%s", resp.StatusCode, resp.Header.Get("Upload-Bytes"))
	}
	if peak.Load() > baseline+(64<<20) {
		t.Fatalf("heap grew by %d bytes for 512MiB upload; budget 64MiB", peak.Load()-baseline)
	}
	t.Logf("streamed %d bytes; baseline=%d peak=%d heap budget=64MiB", size, baseline, peak.Load())
}

func TestRequestLimitsRejectKnownAndChunkedWith413(t *testing.T) {
	old := slog.Default()
	logs := installCaptureLogger()
	defer slog.SetDefault(old)
	warnings := &serviceLimitWarnings{}
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			return
		}
		w.WriteHeader(204)
	}))
	defer backend.Close()
	front := uploadFront(t, registry.Service{Name: "photos", Type: registry.TypeProxy, RequestLimits: &registry.RequestLimits{MaxBody: "8B"}}, func(w inspect.WarningView) { warnings.add(w) }, uploadProxy(t, backend))
	for _, tc := range []struct {
		name    string
		size    int
		chunked bool
		status  int
	}{{"at_limit", 8, false, 204}, {"known", 9, false, 413}, {"chunked", 9, true, 413}} {
		t.Run(tc.name, func(t *testing.T) {
			var body io.Reader = strings.NewReader(strings.Repeat("x", tc.size))
			if tc.chunked {
				body = io.LimitReader(uploadZeros{}, int64(tc.size))
			}
			resp, err := front.Client().Post(front.URL, "application/octet-stream", body)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			message, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != tc.status {
				t.Fatalf("status=%d body=%q want %d", resp.StatusCode, message, tc.status)
			}
			if tc.status == 413 && !strings.Contains(string(message), "--max-request-body") {
				t.Fatalf("unclear error %s", message)
			}
		})
	}
	got := warnings.snapshot()
	if len(got) != 1 || got[0].Code != registry.CodeRequestBodyLimit || !strings.Contains(got[0].Message, "photos") {
		t.Fatalf("warnings=%+v", got)
	}
	found := false
	logs.mu.Lock()
	count := len(logs.records)
	logs.mu.Unlock()
	for i := 0; i < count; i++ {
		attrs := logs.attrMap(t, i)
		if attrs["code"] == registry.CodeRequestBodyLimit && attrs["name"] == "photos" && attrs["limit"] == "8" {
			found = true
		}
	}
	if !found {
		t.Fatal("missing structured service limit log")
	}
}

func TestRequestLimitsSlowProgressAndStall(t *testing.T) {
	for _, progress := range []bool{true, false} {
		t.Run(fmt.Sprint(progress), func(t *testing.T) {
			warnings := &serviceLimitWarnings{}
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if _, err := io.Copy(io.Discard, r.Body); err != nil {
					return
				}
				w.WriteHeader(204)
			}))
			defer backend.Close()
			svc := registry.Service{Name: "phone", Type: registry.TypeProxy, RequestLimits: &registry.RequestLimits{ReadTimeout: "200ms"}}
			if progress {
				svc.RequestLimits.ReadTimeout = "5s"
			}
			front := uploadFront(t, svc, func(w inspect.WarningView) { warnings.add(w) }, uploadProxy(t, backend))
			conn, err := net.Dial("tcp", strings.TrimPrefix(front.URL, "http://"))
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			conn.SetDeadline(time.Now().Add(5 * time.Second))
			fmt.Fprint(conn, "POST / HTTP/1.1\r\nHost: phone\r\nContent-Length: 10\r\n\r\na")
			if progress {
				for i := 0; i < 9; i++ {
					if _, err := conn.Write([]byte("a")); err != nil {
						t.Fatal(err)
					}
				}
			}
			resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			message, _ := io.ReadAll(resp.Body)
			if progress {
				if resp.StatusCode != 204 {
					t.Fatalf("progress upload status=%d body=%s", resp.StatusCode, message)
				}
				if len(warnings.snapshot()) != 0 {
					t.Fatal("progress produced warning")
				}
			} else {
				if resp.StatusCode != 408 || !strings.Contains(string(message), "--request-read-timeout") {
					t.Fatalf("stalled status=%d body=%s", resp.StatusCode, message)
				}
				got := warnings.snapshot()
				if len(got) != 1 || got[0].Code != registry.CodeRequestReadTimeout {
					t.Fatalf("warnings=%+v", got)
				}
			}
		})
	}
}

type virtualProgressReader struct{}

func (virtualProgressReader) Read(p []byte) (int, error) {
	time.Sleep(50 * time.Millisecond)
	p[0] = 'a'
	return 1, nil
}

func (virtualProgressReader) Close() error { return nil }

// Network fixtures above verify real protocol/streaming behavior. Pin the
// per-read idle deadline without spending it on OS scheduling or TLS setup.
func TestRequestReadBudgetRenewsAtEveryRead(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		writer := &uploadDeadlineWriter{ResponseRecorder: httptest.NewRecorder()}
		body := &progressBody{ReadCloser: virtualProgressReader{}, ctl: http.NewResponseController(writer), idle: 200 * time.Millisecond, state: &requestBudgetState{}, reject: func(*requestLimitError) { t.Error("progress rejected") }}
		started := time.Now()
		for i := 0; i < 10; i++ {
			before := time.Now()
			if n, err := body.Read(make([]byte, 1)); n != 1 || err != nil {
				t.Fatalf("progress read %d = %d, %v", i, n, err)
			}
			deadlines := writer.snapshot()
			if len(deadlines) != 2*(i+1) || deadlines[len(deadlines)-2] != before.Add(200*time.Millisecond) || !deadlines[len(deadlines)-1].IsZero() {
				t.Fatalf("read %d deadlines = %v, want independent 200ms budget then cleared", i, deadlines)
			}
		}
		if elapsed := time.Since(started); elapsed != 500*time.Millisecond {
			t.Fatalf("virtual upload duration = %v, want 500ms across renewed 200ms reads", elapsed)
		}
	})
}

func TestRequestLimitsHeader408AndKeepAliveIdle(t *testing.T) {
	oldLogger := slog.Default()
	logs := installCaptureLogger()
	defer slog.SetDefault(oldLogger)
	warnings := &serviceLimitWarnings{}
	svc := registry.Service{Name: "phone", Type: registry.TypeProxy, RequestLimits: &registry.RequestLimits{HeaderTimeout: "100ms", IdleTimeout: "100ms"}}
	front := uploadFront(t, svc, func(w inspect.WarningView) { warnings.add(w) }, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	conn, err := net.Dial("tcp", strings.TrimPrefix(front.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	fmt.Fprint(conn, "GET / HTTP/1.1\r\nHost:")
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 408 || !strings.Contains(string(body), "--request-header-timeout") {
		t.Fatalf("header response %d %s", resp.StatusCode, body)
	}
	if got := warnings.snapshot(); len(got) != 1 || got[0].Code != registry.CodeRequestHeaderTimeout {
		t.Fatalf("warnings=%+v", got)
	}
	// The first request's header budget also applies when no header byte arrives.
	blank, err := net.Dial("tcp", strings.TrimPrefix(front.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer blank.Close()
	blank.SetDeadline(time.Now().Add(5 * time.Second))
	blankResponse, err := http.ReadResponse(bufio.NewReader(blank), nil)
	if err != nil {
		t.Fatalf("silent initial request did not receive 408: %v", err)
	}
	blankResponse.Body.Close()
	if blankResponse.StatusCode != 408 {
		t.Fatalf("silent initial status=%d", blankResponse.StatusCode)
	}
	// Positive control: an idle keep-alive is closed without a request warning.
	clean := &serviceLimitWarnings{}
	idle := uploadFront(t, svc, func(w inspect.WarningView) { clean.add(w) }, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	c, err := net.Dial("tcp", strings.TrimPrefix(idle.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(5 * time.Second))
	fmt.Fprint(c, "GET / HTTP/1.1\r\nHost: phone\r\n\r\n")
	rd := bufio.NewReader(c)
	ok, err := http.ReadResponse(rd, nil)
	if err != nil {
		t.Fatal(err)
	}
	ok.Body.Close()
	if _, err := rd.ReadByte(); err != io.EOF {
		t.Fatalf("idle close=%v want EOF", err)
	}
	if len(clean.snapshot()) != 0 {
		t.Fatal("idle keep-alive reported a request limit")
	}
	logs.mu.Lock()
	records := append([]slog.Record(nil), logs.records...)
	logs.mu.Unlock()
	headerHits := 0
	for _, record := range records {
		record.Attrs(func(attr slog.Attr) bool {
			if attr.Key == "code" && attr.Value.String() == registry.CodeRequestHeaderTimeout {
				headerHits++
			}
			return true
		})
	}
	if headerHits != 2 {
		t.Fatalf("header limit log count=%d; want one each for partial and blank request", headerHits)
	}
}

func TestRequestLimitsWarningsAreBounded(t *testing.T) {
	var absent *serviceLimitWarnings
	if absent.snapshot() != nil {
		t.Fatal("legacy nodes without warnings need an empty snapshot")
	}
	s := &serviceLimitWarnings{}
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); s.add(inspect.WarningView{Code: registry.CodeRequestBodyLimit}) }()
	}
	wg.Wait()
	if len(s.snapshot()) != 1 {
		t.Fatal("duplicate warnings")
	}
	copy := s.snapshot()
	copy[0].Code = "mutated"
	if s.snapshot()[0].Code != registry.CodeRequestBodyLimit {
		t.Fatal("snapshot aliases warning storage")
	}
}

func TestRequestLimitsUnlimitedAndSlowResponse(t *testing.T) {
	// Allow scheduling of a large race-instrumented upload. Backend pauses
	// still exceed the read-idle budget, so they cannot count as client stalls.
	const idle = time.Second
	for _, h2 := range []bool{false, true} {
		t.Run(fmt.Sprintf("http2=%v", h2), func(t *testing.T) {
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Pause consumption after upload begins, filling the proxy's
				// backend socket while the client still has much more to send.
				if _, err := io.ReadFull(r.Body, make([]byte, 1)); err != nil {
					return
				}
				time.Sleep(5 * idle / 2)
				n, err := io.Copy(io.Discard, r.Body)
				if err != nil {
					return
				}
				time.Sleep(2 * idle)
				w.Header().Set("Upload-Bytes", strconv.FormatInt(n+1, 10))
				w.WriteHeader(204)
			}))
			defer backend.Close()
			svc := registry.Service{Name: "video", Type: registry.TypeProxy, RequestLimits: &registry.RequestLimits{MaxBody: "unlimited", UnlimitedAck: true, ReadTimeout: idle.String()}}
			var front *httptest.Server
			if h2 {
				front = httptest.NewUnstartedServer(AccessLogMiddleware(svc.Name, nil, RequestLimitsMiddleware(svc, nil, uploadProxy(t, backend))))
				front.EnableHTTP2 = true
				front.Config = newHTTPServerFn(front.Config.Handler)
				front.StartTLS()
				t.Cleanup(front.Close)
			} else {
				front = uploadFront(t, svc, nil, uploadProxy(t, backend))
			}
			const size int64 = 33 << 20
			resp, err := front.Client().Post(front.URL, "application/octet-stream", io.LimitReader(uploadZeros{}, size))
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != 204 || resp.Header.Get("Upload-Bytes") != strconv.FormatInt(size, 10) {
				t.Fatalf("unlimited upload/slow response status=%d count=%s", resp.StatusCode, resp.Header.Get("Upload-Bytes"))
			}
			if h2 && resp.ProtoMajor != 2 {
				t.Fatalf("HTTP/2 control negotiated %s", resp.Proto)
			}
		})
	}
}

type rejectedDeadlineWriter struct {
	*httptest.ResponseRecorder
	err error
}

func (w rejectedDeadlineWriter) SetReadDeadline(time.Time) error { return w.err }
func TestRequestLimitsDeadlineAndCancellationErrors(t *testing.T) {
	sentinel := errors.New("read deadline unavailable")
	w := rejectedDeadlineWriter{httptest.NewRecorder(), sentinel}
	body := &progressBody{ReadCloser: io.NopCloser(strings.NewReader("x")), ctl: http.NewResponseController(w), idle: time.Second, state: &requestBudgetState{}}
	if _, err := body.Read(make([]byte, 1)); !errors.Is(err, sentinel) {
		t.Fatalf("deadline error=%v", err)
	}
	expected := &requestLimitError{code: registry.CodeRequestReadTimeout, status: 408}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := httptest.NewRequest("POST", "/", nil).WithContext(context.WithValue(ctx, requestBudgetKey{}, &requestBudgetState{failure: expected}))
	if requestFailure(r, context.Canceled) != expected {
		t.Fatal("cancellation masked request read timeout")
	}
	if requestFailure(r, fmt.Errorf("wrapped: %w", expected)) != expected {
		t.Fatal("wrapped limit lost")
	}
	if requestFailure(httptest.NewRequest("POST", "/", nil), sentinel) != nil {
		t.Fatal("backend error reclassified as client limit")
	}
	srv := newHTTPServerFn(http.NotFoundHandler())
	called := false
	srv.ConnState = func(net.Conn, http.ConnState) { called = true }
	type contextKey struct{}
	srv.ConnContext = func(ctx context.Context, c net.Conn) context.Context {
		return context.WithValue(ctx, contextKey{}, "retained")
	}
	ln := configureServiceHTTP(srv, registry.Service{Type: registry.TypeProxy}, &fakeListener{}, nil)
	srv.ConnState(nil, http.StateNew)
	if !called {
		t.Fatal("existing ConnState hook lost")
	}
	conn := &headerBudgetConn{}
	ctx = srv.ConnContext(context.Background(), conn)
	if ctx.Value(contextKey{}) != "retained" || ctx.Value(requestConnKey{}) != conn {
		t.Fatal("existing ConnContext or disposal connection lost")
	}
	ln.Close()
}

func TestRequestLimitsChangesReloadService(t *testing.T) {
	base := registry.Service{Name: "web", Type: registry.TypeProxy}
	custom := base
	custom.RequestLimits = &registry.RequestLimits{MaxBody: "1GiB"}
	if !serviceChanged(base, custom) {
		t.Fatal("limits change did not reload")
	}
	defaults := base
	defaults.RequestLimits = &registry.RequestLimits{MaxBody: "32MiB", ReadTimeout: "30s", HeaderTimeout: "10s", IdleTimeout: "60s"}
	if serviceChanged(base, defaults) {
		t.Fatal("equivalent defaults restarted service")
	}
}

func TestRequestLimitsProductionNodePersistsWarningWithoutCompletingPartialSnapshot(t *testing.T) {
	t.Setenv(config.ConfigDirEnv, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	old := newTSNetServerFn
	newTSNetServerFn = func(registry.Service, string, string, string) tsnetServer { return &listenerTSNetServer{ln: ln} }
	defer func() { newTSNetServerFn = old }()
	s, err := New("key", "")
	if err != nil {
		t.Fatal(err)
	}
	svc := registry.Service{Name: "photos", Type: registry.TypeFile, Path: t.TempDir(), RequestLimits: &registry.RequestLimits{MaxBody: "8B", HeaderTimeout: "150ms", ReadTimeout: "200ms", IdleTimeout: "300ms"}}
	if err := s.startNodeLocked(context.Background(), svc); err != nil {
		t.Fatal(err)
	}
	defer s.stopNodeLocked(svc.Name)
	s.lastRegistryFingerprint = "fixture-fingerprint"
	s.mu.Lock()
	s.writeRuntimeSnapshotLocked(s.lastRegistryFingerprint, false)
	s.mu.Unlock()
	resp, err := http.Post("http://"+ln.Addr().String(), "application/octet-stream", strings.NewReader("123456789"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 413 {
		t.Fatalf("production handler status=%d", resp.StatusCode)
	}
	path, err := config.RuntimeSnapshotPath()
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := tsRuntime.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.Partial || len(snapshot.Services) != 1 || len(snapshot.Services[0].Warnings) != 1 || snapshot.Services[0].Warnings[0].Code != registry.CodeRequestBodyLimit {
		t.Fatalf("persisted snapshot=%+v", snapshot)
	}
	node := s.nodes[svc.Name]
	if node.httpSrv.ReadTimeout != 0 || node.httpSrv.ReadHeaderTimeout != 150*time.Millisecond || node.httpSrv.IdleTimeout != 300*time.Millisecond {
		t.Fatalf("production HTTP configuration=%+v", node.httpSrv)
	}
}

func TestRequestLimitsInvalidMiddlewareFailsClosed(t *testing.T) {
	handler := RequestLimitsMiddleware(registry.Service{RequestLimits: &registry.RequestLimits{MaxBody: "unlimited"}}, nil, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("invalid limit reached backend") }))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("POST", "/", nil))
	if w.Code != 503 || !strings.Contains(w.Body.String(), "ack-unlimited-request-body") {
		t.Fatalf("invalid response=%d %s", w.Code, w.Body.String())
	}
}

// Signal the proxy transport's second underlying Read. Limiting the first Read
// to one byte makes both complete and partial uploads exercise another Read.
type uploadReadSignal struct {
	io.ReadCloser
	reads     int
	entered   chan struct{}
	finished  chan struct{}
	closed    chan time.Duration
	closeOnce sync.Once
}

func (b *uploadReadSignal) Read(p []byte) (int, error) {
	b.reads++
	if b.reads == 1 && b.entered != nil && len(p) > 1 {
		p = p[:1]
	}
	if b.reads == 2 && b.entered != nil {
		close(b.entered)
	}
	n, err := b.ReadCloser.Read(p)
	if b.reads == 2 && b.finished != nil {
		close(b.finished)
	}
	return n, err
}

func (b *uploadReadSignal) Close() error {
	start := time.Now()
	err := b.ReadCloser.Close()
	if b.closed != nil {
		b.closeOnce.Do(func() { b.closed <- time.Since(start) })
	}
	return err
}

func uploadWait(t *testing.T, done <-chan struct{}, bound time.Duration, message string) {
	t.Helper()
	bound = max(bound, 5*time.Second)
	select {
	case <-done:
	case <-time.After(bound):
		t.Fatal(message)
	}
}

func uploadRelease(t *testing.T, listener *limitedListener, deadline time.Time, transport ...*http.Transport) {
	t.Helper()
	for len(listener.sem) != 0 && time.Now().Before(deadline) {
		// HTTP/2's client marks a just-completed stream idle asynchronously.
		// Close only idle connections, after response and server Read completion.
		for _, tr := range transport {
			if tr != nil {
				tr.CloseIdleConnections()
			}
		}
		time.Sleep(time.Millisecond)
	}
	if got := len(listener.sem); got != 0 {
		t.Errorf("server retained %d connection slots past the disposal bound", got)
	}
}

func TestRequestLimitsTLSProxyEarlyRejection(t *testing.T) {
	t.Run("disposal_deadline", assertUploadDisposalDeadline)
	for _, h2 := range []bool{false, true} {
		for _, complete := range []bool{false, true} {
			t.Run(fmt.Sprintf("http2=%v/complete=%v", h2, complete), func(t *testing.T) {
				t.Setenv(config.ConfigDirEnv, t.TempDir())
				entered, readFinished, handlerFinished := make(chan struct{}), make(chan struct{}), make(chan struct{})
				closeFinished := make(chan time.Duration, 1)
				backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method == "GET" {
						w.WriteHeader(http.StatusNoContent)
						return
					}
					_ = http.NewResponseController(w).EnableFullDuplex() // HTTP/1 backend.
					if _, err := io.ReadFull(r.Body, make([]byte, 1)); err != nil {
						t.Errorf("backend did not receive the partial upload: %v", err)
						return
					}
					select {
					case <-entered:
					case <-r.Context().Done():
						return
					}
					w.Header().Set("Connection", "close")
					http.Error(w, "denied", http.StatusForbidden)
					w.(http.Flusher).Flush()
				}))
				t.Cleanup(backend.Close)
				proxy := uploadProxy(t, backend)
				handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method == "POST" {
						// Observe the real Read below progressBody and its completion
						// above it, including warning classification and deadline changes.
						body := r.Body.(*progressBody)
						// Go 1.26's ReverseProxy wraps its outbound body in a
						// noopCloseReader. Observe the actual underlying disposal,
						// which our middleware performs on both toolchains.
						body.ReadCloser = &uploadReadSignal{ReadCloser: body.ReadCloser, entered: entered, closed: closeFinished}
						r.Body = &uploadReadSignal{ReadCloser: body, finished: readFinished}
						defer close(handlerFinished)
					}
					proxy.ServeHTTP(w, r)
				})
				warnings := &serviceLimitWarnings{}
				svc := registry.Service{Name: "early-rejection", Type: registry.TypeProxy, RequestLimits: &registry.RequestLimits{ReadTimeout: "1m", IdleTimeout: "5s"}}
				addr, listener := uploadTLSFrontWithReport(t, svc, handler, h2, func(w inspect.WarningView) { warnings.add(w) })
				start := time.Now()
				const bound = 5 * time.Second // real-network hang guard
				deadline := start.Add(bound)
				var resp *http.Response
				var err error
				var conn *tls.Conn
				var transport *http.Transport
				var client *http.Client
				var buffered *bufio.Reader
				if h2 {
					transport = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, ForceAttemptHTTP2: true}
					defer transport.CloseIdleConnections()
					client = &http.Client{Transport: transport, Timeout: 5 * time.Second}
					reader, writer := io.Pipe()
					written := make(chan struct{})
					go func() {
						defer close(written)
						body := "x"
						if complete {
							body = strings.Repeat("x", 1000)
						}
						_, _ = io.WriteString(writer, body)
						if complete {
							_ = writer.Close()
						}
					}()
					defer func() { _ = reader.Close(); _ = writer.Close(); <-written }()
					req, _ := http.NewRequest("POST", "https://"+addr, reader)
					req.ContentLength = 1000
					resp, err = client.Do(req)
				} else {
					conn, err = tls.Dial("tcp", addr, &tls.Config{InsecureSkipVerify: true})
					if err != nil {
						t.Fatal(err)
					}
					defer conn.Close()
					_ = conn.SetDeadline(start.Add(5 * time.Second))
					body := "x"
					if complete {
						body = strings.Repeat("x", 1000)
					}
					if _, err = fmt.Fprintf(conn, "POST / HTTP/1.1\r\nHost: early\r\nContent-Length: 1000\r\n\r\n%s", body); err != nil {
						t.Fatal(err)
					}
					buffered = bufio.NewReader(conn)
					resp, err = http.ReadResponse(buffered, nil)
				}
				if err != nil {
					t.Fatal(err)
				}
				data, err := io.ReadAll(resp.Body)
				_ = resp.Body.Close()
				elapsed := time.Since(start)
				if resp.StatusCode != http.StatusForbidden || string(data) != "denied\n" || err != nil {
					t.Errorf("early rejection response=%d body=%q error=%v", resp.StatusCode, data, err)
				}
				wantProtocol := 1
				if h2 {
					wantProtocol = 2
				}
				if resp.ProtoMajor != wantProtocol {
					t.Fatalf("negotiated %s, want HTTP/%d", resp.Proto, wantProtocol)
				}
				uploadWait(t, handlerFinished, 5*time.Second, "proxy handler retained rejected stream")
				uploadWait(t, readFinished, 5*time.Second, "proxy body Read was not interrupted")
				var closeElapsed time.Duration
				select {
				case closeElapsed = <-closeFinished:
				case <-time.After(5 * time.Second):
					t.Fatal("underlying request body was not closed")
				}
				if got := warnings.snapshot(); len(got) != 0 {
					t.Errorf("backend rejection generated spurious upload warnings: %+v", got)
				}
				if h2 || complete {
					if got := len(listener.sem); got != 1 {
						t.Fatalf("single-connection control acquired %d slots, want 1", got)
					}
					// A new stream/request must work on the same one-slot connection.
					if h2 {
						resp, err = client.Get("https://" + addr + "/after")
					} else {
						_, err = fmt.Fprint(conn, "GET /after HTTP/1.1\r\nHost: early\r\n\r\n")
						if err == nil {
							resp, err = http.ReadResponse(buffered, nil)
						}
					}
					if err != nil {
						t.Fatal(err)
					}
					data, err = io.ReadAll(resp.Body)
					_ = resp.Body.Close()
					if resp.StatusCode != http.StatusNoContent || resp.ProtoMajor != wantProtocol || len(data) != 0 || err != nil {
						t.Errorf("connection reuse status=%d protocol=%s body=%q error=%v", resp.StatusCode, resp.Proto, data, err)
					}
					if h2 {
						transport.CloseIdleConnections()
					} else {
						_ = conn.Close()
					}
				}
				// A partial HTTP/1 upload remains open on the client until this
				// assertion proves that the server released its connection slot.
				uploadRelease(t, listener, deadline, transport)
				t.Logf("response complete in %s; proxy Close in %s; slot released after %s", elapsed, closeElapsed, time.Since(start))
			})
		}
	}
}

type uploadDeadlineWriter struct {
	*httptest.ResponseRecorder
	mu        sync.Mutex
	deadlines []time.Time
}

func (w *uploadDeadlineWriter) SetReadDeadline(deadline time.Time) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.deadlines = append(w.deadlines, deadline)
	return nil
}

func (w *uploadDeadlineWriter) snapshot() []time.Time {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]time.Time(nil), w.deadlines...)
}

// A read may have accepted input before Close but return afterward. The barrier
// pins that ordering without relying on socket scheduling or the race detector.
type uploadBarrierBody struct {
	entered, release, closed chan struct{}
	closeOnce                sync.Once
	enterOnce                sync.Once
	result                   error
}

func (b *uploadBarrierBody) Read(p []byte) (int, error) {
	b.enterOnce.Do(func() { close(b.entered) })
	<-b.release
	if b.result == context.DeadlineExceeded {
		return 0, b.result
	}
	p[0] = 'x'
	return 1, b.result
}

func (b *uploadBarrierBody) Close() error {
	b.closeOnce.Do(func() { close(b.closed) })
	return nil
}

func TestRequestLimitsClosingDeadline(t *testing.T) {
	for _, result := range []error{nil, io.EOF, context.DeadlineExceeded} {
		t.Run(fmt.Sprint(result), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				underlying := &uploadBarrierBody{entered: make(chan struct{}), release: make(chan struct{}), closed: make(chan struct{}), result: result}
				writer := &uploadDeadlineWriter{ResponseRecorder: httptest.NewRecorder()}
				warnings := atomic.Int32{}
				body := &progressBody{ReadCloser: underlying, ctl: http.NewResponseController(writer), idle: 3 * time.Second, state: &requestBudgetState{}, reject: func(*requestLimitError) { warnings.Add(1) }}
				readDone := make(chan struct{})
				go func() { defer close(readDone); _, _ = body.Read(make([]byte, 1)) }()
				<-underlying.entered
				closeDone := make(chan struct{})
				go func() { defer close(closeDone); _ = body.Close() }()
				// Record the old implementation's liveness failure, then release the
				// barrier so even a RED run joins all goroutines before returning.
				select {
				case <-closeDone:
				case <-time.After(200 * time.Millisecond):
					t.Error("Close waited for the in-flight Read before calling underlying Close")
				}
				select {
				case <-underlying.closed:
				default:
					t.Error("Close did not reach the underlying body while Read was in flight")
				}
				deadlines := writer.snapshot()
				if len(deadlines) != 2 || deadlines[1].IsZero() || deadlines[1] != time.Now().Add(time.Second) {
					t.Errorf("Close did not install its <=1s disposal deadline: %v", deadlines)
				}
				close(underlying.release)
				<-readDone
				<-closeDone
				_ = body.Close()
				body.disposalDeadline()
				if got := writer.snapshot(); len(got) != len(deadlines) {
					t.Errorf("a concurrent Read or repeated Close changed the disposal deadline: before=%v after=%v", deadlines, got)
				}
				if got := warnings.Load(); got != 0 {
					t.Errorf("disposal generated %d upload warnings", got)
				}
				if len(deadlines) == 2 {
					if _, err := body.Read(make([]byte, 1)); !errors.Is(err, http.ErrBodyReadAfterClose) {
						t.Errorf("Read after Close=%v, want ErrBodyReadAfterClose", err)
					}
					if got := writer.snapshot(); len(got) != len(deadlines) {
						t.Errorf("Read after Close renewed deadline: %v", got)
					}
				}
			})
		})
	}
}

func TestRequestLimitsCanceledReadJoin(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		underlying := &uploadBarrierBody{entered: make(chan struct{}), release: make(chan struct{}), closed: make(chan struct{}), result: context.DeadlineExceeded}
		state := &requestBudgetState{}
		body := &progressBody{ReadCloser: underlying, ctl: http.NewResponseController(httptest.NewRecorder()), idle: time.Second, state: state, reject: func(*requestLimitError) {}}
		readDone := make(chan struct{})
		go func() { defer close(readDone); _, _ = body.Read(make([]byte, 1)) }()
		<-underlying.entered
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		r := httptest.NewRequest("POST", "/", nil).WithContext(context.WithValue(ctx, requestBudgetKey{}, state))
		failure := make(chan *requestLimitError, 1)
		go func() { failure <- requestFailure(r, context.Canceled) }()
		select {
		case <-failure:
			t.Error("cancellation classification did not join the in-flight read")
			close(underlying.release)
			<-readDone
			return
		case <-time.After(30 * time.Millisecond):
			synctest.Wait()
		}
		close(underlying.release)
		<-readDone
		if got := <-failure; got == nil || got.status != 408 || got.code != registry.CodeRequestReadTimeout {
			t.Errorf("cancellation masked upload timeout: %+v", got)
		}
	})
}

func TestRequestLimitsTLSConcurrentReadClose(t *testing.T) {
	t.Run("disposal_deadline", assertUploadDisposalDeadline)
	for _, h2 := range []bool{false, true} {
		for _, complete := range []bool{false, true} {
			t.Run(fmt.Sprintf("http2=%v/complete=%v", h2, complete), func(t *testing.T) {
				t.Setenv(config.ConfigDirEnv, t.TempDir())
				readStarted, readDone, closeDone := make(chan struct{}), make(chan struct{}), make(chan time.Duration, 1)
				warnings := &serviceLimitWarnings{}
				handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					body := r.Body.(*progressBody)
					body.ReadCloser = &uploadReadSignal{ReadCloser: body.ReadCloser, entered: readStarted}
					if _, err := io.ReadFull(r.Body, make([]byte, 1)); err != nil {
						t.Errorf("initial read failed: %v", err)
						return
					}
					go func() {
						defer close(readDone)
						_, _ = io.Copy(io.Discard, r.Body)
					}()
					<-readStarted
					if complete {
						uploadWait(t, readDone, 5*time.Second, "complete body did not reach EOF")
					}
					select {
					case <-readDone:
						if !complete {
							t.Error("unfinished-body control did not keep Read in flight")
						}
					default:
						if complete {
							t.Error("complete-body control did not reach EOF")
						}
					}
					start := time.Now()
					_ = r.Body.Close()
					closeDone <- time.Since(start)
					<-readDone
					w.WriteHeader(http.StatusNoContent)
				})
				svc := registry.Service{Name: "concurrent-close", Type: registry.TypeProxy, RequestLimits: &registry.RequestLimits{ReadTimeout: "1m"}}
				addr, listener := uploadTLSFrontWithReport(t, svc, handler, h2, func(w inspect.WarningView) { warnings.add(w) })
				start := time.Now()
				var transport *http.Transport
				if h2 {
					transport = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, ForceAttemptHTTP2: true}
					defer transport.CloseIdleConnections()
					reader, writer := io.Pipe()
					written := make(chan struct{})
					go func() {
						defer close(written)
						body := "x"
						if complete {
							body = "123456789"
						}
						_, _ = io.WriteString(writer, body)
						if complete {
							_ = writer.Close()
						}
					}()
					defer func() { _ = reader.Close(); _ = writer.Close(); <-written }()
					req, _ := http.NewRequest("POST", "https://"+addr, reader)
					req.ContentLength = 9
					resp, err := (&http.Client{Transport: transport, Timeout: 5 * time.Second}).Do(req)
					if err != nil {
						t.Fatal(err)
					}
					_, err = io.Copy(io.Discard, resp.Body)
					_ = resp.Body.Close()
					if err != nil || resp.StatusCode != 204 || resp.ProtoMajor != 2 {
						t.Errorf("response=%d protocol=%s error=%v", resp.StatusCode, resp.Proto, err)
					}
					transport.CloseIdleConnections()
				} else {
					conn, err := tls.Dial("tcp", addr, &tls.Config{InsecureSkipVerify: true})
					if err != nil {
						t.Fatal(err)
					}
					defer conn.Close()
					_ = conn.SetDeadline(start.Add(5 * time.Second))
					body := "x"
					if complete {
						body = "123456789"
					}
					if _, err := fmt.Fprintf(conn, "POST / HTTP/1.1\r\nHost: close\r\nContent-Length: 9\r\n\r\n%s", body); err != nil {
						t.Fatal(err)
					}
					resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
					if err != nil {
						t.Fatal(err)
					}
					_, err = io.Copy(io.Discard, resp.Body)
					_ = resp.Body.Close()
					if err != nil || resp.StatusCode != 204 {
						t.Errorf("response=%d error=%v", resp.StatusCode, err)
					}
					if complete {
						_ = conn.Close()
					}
				}
				duration := <-closeDone
				if got := warnings.snapshot(); len(got) != 0 {
					t.Errorf("disposal generated upload warnings: %+v", got)
				}
				uploadRelease(t, listener, time.Now().Add(5*time.Second), transport)
				t.Logf("concurrent Close returned in %s", duration)
			})
		}
	}
}

// Keep the disposal policy separate from real network latency. An unfinished
// Read cannot be joined before Close: the deadline must be installed while it
// is blocked, and a subsequent successful Read cannot renew that deadline.
func assertUploadDisposalDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		underlying := &uploadBarrierBody{entered: make(chan struct{}), release: make(chan struct{}), closed: make(chan struct{})}
		writer := &uploadDeadlineWriter{ResponseRecorder: httptest.NewRecorder()}
		body := &progressBody{ReadCloser: underlying, ctl: http.NewResponseController(writer), idle: 3 * time.Second, state: &requestBudgetState{}, reject: func(*requestLimitError) { t.Error("disposal was classified as upload timeout") }}
		done := make(chan struct{})
		go func() { defer close(done); _, _ = body.Read(make([]byte, 1)) }()
		<-underlying.entered
		closed := make(chan struct{})
		go func() { _ = body.Close(); close(closed) }()
		synctest.Wait()
		select {
		case <-closed:
		default:
			t.Error("Close waited behind Read")
		}
		deadlines := writer.snapshot()
		if len(deadlines) != 2 || deadlines[1] != time.Now().Add(time.Second) {
			t.Errorf("disposal deadline = %v, want independent 1s bound", deadlines)
		}
		close(underlying.release)
		<-done
		<-closed
		if got := writer.snapshot(); len(got) != 2 {
			t.Errorf("Read renewed disposal deadline: %v", got)
		}
	})
}

func assertTLSHandshakeDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		serverConn, clientConn := net.Pipe()
		defer clientConn.Close()
		native := tls.Server(serverConn, &tls.Config{})
		svc := registry.Service{Name: "handshake-deadline", Type: registry.TypeFile, RequestLimits: &registry.RequestLimits{HeaderTimeout: "200ms"}}
		ln := configureServiceHTTP(&http.Server{}, svc, nil, nil).(*headerBudgetListener)
		if ln.timeout != svc.RequestLimits.HeaderTimeout {
			t.Errorf("listener header timeout=%q, want configured %q", ln.timeout, svc.RequestLimits.HeaderTimeout)
		}
		ln.pending[native] = struct{}{}
		ln.workers.Add(1)
		finished := make(chan struct{})
		started := time.Now()
		go func() { ln.prepare(native); close(finished) }()
		synctest.Wait()
		time.Sleep(200*time.Millisecond - time.Nanosecond)
		synctest.Wait()
		select {
		case <-finished:
			t.Error("handshake deadline fired early")
		default:
		}
		time.Sleep(time.Nanosecond)
		synctest.Wait()
		select {
		case <-finished:
		default:
			t.Error("handshake did not expire at its inner deadline")
		}
		if time.Since(started) != 200*time.Millisecond {
			t.Error("incorrect virtual handshake budget")
		}
		_ = serverConn.Close()
		<-finished
		if len(ln.pending) != 0 {
			t.Error("failed handshake retained pending connection")
		}
	})
}
