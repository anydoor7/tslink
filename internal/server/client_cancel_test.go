package server

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/accesslog"
	"github.com/anydoor7/tslink/internal/registry"
	"github.com/anydoor7/tslink/internal/testwait"
	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/tailcfg"
)

// stallingWhoIs answers WhoIs for alice. While stalled it holds the call until
// the caller's context ends, as when a phone drops a request mid-lookup; while
// failing it returns an error at once with the caller still connected.
type stallingWhoIs struct {
	stall, fail atomic.Bool
	entered     chan struct{}
}

func (s *stallingWhoIs) client() *LocalClient {
	return &LocalClient{OmitAuth: true, Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		if s.stall.Load() {
			select {
			case s.entered <- struct{}{}:
			default:
			}
			<-r.Context().Done()
			return nil, r.Context().Err()
		}
		if s.fail.Load() {
			return nil, errors.New("localapi unavailable")
		}
		body, _ := json.Marshal(&apitype.WhoIsResponse{UserProfile: &tailcfg.UserProfile{LoginName: "alice@example.com"}, Node: &tailcfg.Node{ComputedName: "phone"}})
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(body))}, nil
	})}
}

type lockedLog struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *lockedLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}
func (l *lockedLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

func captureServerLog(t *testing.T) *lockedLog {
	t.Helper()
	logs := &lockedLog{}
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(old) })
	return logs
}

// cancelNode assembles the real app-node handler chain (access log, people or
// ACL middleware, request limits, reverse proxy) and serves it from a real
// HTTP server. done receives once per finished request, so a test can join a
// canceled request's handler before reading the access log.
func cancelNode(t *testing.T, svc registry.Service, edit func(string)) (url string, done chan struct{}, who *stallingWhoIs, events func() []accesslog.Event) {
	t.Helper()
	who = &stallingWhoIs{entered: make(chan struct{}, 1)}
	s, store, dir, _ := setupAccessNodeWithClient(t, svc, who.client(), accesslog.Options{}, edit, nil)
	s.mu.Lock()
	handler := s.nodes[svc.Name].httpSrv.Handler
	s.mu.Unlock()
	done = make(chan struct{}, 16)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() { done <- struct{}{} }()
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	events = func() []accesslog.Event {
		t.Helper()
		drainAccess(t, store)
		r, err := accesslog.Query(dir, accesslog.Filter{})
		if err != nil {
			t.Fatal(err)
		}
		var out []accesslog.Event
		for _, e := range r.Events {
			if e.Kind == "http" {
				out = append(out, e)
			}
		}
		return out
	}
	return srv.URL, done, who, events
}

// cancelAfter sends a request and cancels it once entered fires.
func cancelAfter(t *testing.T, url string, entered <-chan struct{}) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		resp, err := http.DefaultClient.Do(req)
		if resp != nil {
			resp.Body.Close()
		}
		result <- err
	}()
	testwait.Recv(t, entered, "request reached the stalled step")
	cancel()
	if err := testwait.Recv(t, result, "client cancellation"); !errors.Is(err, context.Canceled) {
		t.Fatalf("client request was not canceled: %v", err)
	}
}

func getStatus(t *testing.T, url string) int {
	t.Helper()
	resp, err := (&http.Client{Timeout: testwait.Budget(t)}).Get(url)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp.StatusCode
}

func TestClientCancelDuringWhoIsIsNotADenial(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok") }))
	defer backend.Close()
	for _, tc := range []struct {
		name string
		svc  registry.Service
		edit func(string)
	}{
		{"acl", registry.Service{Name: "app", Type: registry.TypeProxy, Target: backend.URL, AllowedUsers: []string{"alice@example.com"}}, nil},
		{"people", registry.Service{Name: "app", Type: registry.TypeProxy, Target: backend.URL}, func(path string) {
			if _, err := registry.ChangePerson(path, "alice@example.com", []string{"app"}, nil, true, false); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			url, done, who, events := cancelNode(t, tc.svc, tc.edit)
			logs := captureServerLog(t)
			who.stall.Store(true)
			cancelAfter(t, url+"/canceled", who.entered)
			testwait.Recv(t, done, "canceled request handler")
			who.stall.Store(false)
			// Control: a WhoIs failure while the client is still connected
			// remains a denial, so the log can show one.
			who.fail.Store(true)
			if code := getStatus(t, url+"/unidentified"); code != http.StatusForbidden {
				t.Fatalf("unidentified status=%d", code)
			}
			testwait.Recv(t, done, "unidentified request handler")
			who.fail.Store(false)
			if code := getStatus(t, url+"/allowed"); code != http.StatusOK {
				t.Fatalf("allowed status=%d", code)
			}
			testwait.Recv(t, done, "allowed request handler")
			got := events()
			if len(got) != 2 {
				t.Fatalf("want the unidentified and allowed events only, got %+v", got)
			}
			paths := map[string]accesslog.Event{}
			for _, e := range got {
				paths[e.Path] = e
			}
			if e, ok := paths["/unidentified"]; !ok || e.Decision != "denied" || e.Status != http.StatusForbidden {
				t.Fatalf("control denial missing: %+v", got)
			}
			if e, ok := paths["/allowed"]; !ok || e.Decision != "allowed" || e.Status != http.StatusOK {
				t.Fatalf("allowed event missing: %+v", got)
			}
			if _, ok := paths["/canceled"]; ok {
				t.Fatalf("a client cancellation was recorded: %+v", paths["/canceled"])
			}
			if strings.Contains(logs.String(), "context canceled") {
				t.Fatalf("cancellation logged as an identification error:\n%s", logs)
			}
		})
	}
}

func TestClientCancelDuringProxyIsNot5xx(t *testing.T) {
	entered := make(chan struct{}, 1)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slow" {
			entered <- struct{}{}
			<-r.Context().Done()
			return
		}
		io.WriteString(w, "ok")
	}))
	defer backend.Close()
	url, done, _, events := cancelNode(t, registry.Service{Name: "app", Type: registry.TypeProxy, Target: backend.URL, AllowedUsers: []string{"alice@example.com"}}, nil)
	logs := captureServerLog(t)
	cancelAfter(t, url+"/slow", entered)
	testwait.Recv(t, done, "canceled request handler")
	got := events()
	if len(got) != 1 {
		t.Fatalf("events %+v", got)
	}
	e := got[0]
	if e.Decision != "allowed" || e.Status != 499 || e.Reason != "client_canceled" || e.ErrorClass != "" || e.Path != "/slow" {
		t.Fatalf("cancellation event %+v", e)
	}
	if strings.Contains(logs.String(), "backend_request_failed") {
		t.Fatalf("cancellation logged as a backend failure:\n%s", logs)
	}
}

func TestBackendFailuresKeep502WithErrorClass(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	refused := "http://" + ln.Addr().String()
	ln.Close()
	reset := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			conn.Close()
		}
	}))
	defer reset.Close()
	for _, tc := range []struct {
		name, target, class string
	}{
		{"refused", refused, "connection_refused"},
		{"reset", reset.URL, "connection_reset"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			url, done, _, events := cancelNode(t, registry.Service{Name: "app", Type: registry.TypeProxy, Target: tc.target, AllowedUsers: []string{"alice@example.com"}}, nil)
			logs := captureServerLog(t)
			if code := getStatus(t, url+"/"); code != http.StatusBadGateway {
				t.Fatalf("status=%d", code)
			}
			testwait.Recv(t, done, "request handler")
			got := events()
			if len(got) != 1 || got[0].Status != http.StatusBadGateway || got[0].Decision != "allowed" || got[0].Reason != "" || got[0].ErrorClass != tc.class {
				t.Fatalf("events %+v", got)
			}
			line := logs.String()
			if !strings.Contains(line, "error_class="+tc.class) || strings.Contains(line, "127.0.0.1") {
				t.Fatalf("log line:\n%s", line)
			}
			b, _ := json.Marshal(got[0])
			if strings.Contains(string(b), "127.0.0.1") {
				t.Fatalf("event leaks the backend address: %s", b)
			}
		})
	}
}

func TestBackendErrorClass(t *testing.T) {
	timeout := &net.OpError{Op: "dial", Err: &timeoutError{timeout: true}}
	for _, tc := range []struct {
		err   error
		class string
	}{
		{timeout, "timeout"},
		{context.DeadlineExceeded, "timeout"},
		{&net.OpError{Op: "dial", Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)}, "connection_refused"},
		{&net.OpError{Op: "read", Err: os.NewSyscallError("read", syscall.ECONNRESET)}, "connection_reset"},
		{io.ErrUnexpectedEOF, "connection_reset"},
		{errors.New("tls: " + "bad record"), "other"},
		{fmt.Errorf("handshake: %w", tls.AlertError(42)), "tls"},
		{&tls.CertificateVerificationError{Err: errors.New("unknown authority")}, "tls"},
		{errors.New("something else"), "other"},
	} {
		if got := backendErrorClass(tc.err); got != tc.class {
			t.Errorf("%v: %s want %s", tc.err, got, tc.class)
		}
	}
	// Winsock numbers count only on Windows.
	winsock := backendErrorClass(&net.OpError{Op: "dial", Err: os.NewSyscallError("connectex", syscall.Errno(10061))})
	if want := map[bool]string{true: "connection_refused", false: "other"}[runtime.GOOS == "windows"]; winsock != want {
		t.Errorf("WSAECONNREFUSED: %s want %s", winsock, want)
	}
}

// The MCP control plane identifies every request too. A caller that cancels
// during WhoIs is refused without being served and without the identification
// warning; only a value-free Debug line remains. A WhoIs failure with the
// caller still connected keeps its 403 and warning.
func TestMCPControlPlaneCancelDuringWhoIsIsNotAWarning(t *testing.T) {
	who := &stallingWhoIs{entered: make(chan struct{}, 1)}
	logs := &lockedLog{}
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(old) })
	var served atomic.Int64
	h := MCPAuthMiddleware([]string{"alice@example.com"}, who.client(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { served.Add(1) }))
	done := make(chan struct{}, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() { done <- struct{}{} }()
		h.ServeHTTP(w, r)
	}))
	defer srv.Close()

	who.stall.Store(true)
	cancelAfter(t, srv.URL, who.entered)
	testwait.Recv(t, done, "canceled MCP request handler")
	canceled := logs.String()
	if served.Load() != 0 {
		t.Fatalf("canceled unidentified MCP caller served %d times", served.Load())
	}
	if strings.Contains(canceled, "level=WARN") || strings.Contains(canceled, "failed to identify caller") {
		t.Fatalf("cancellation logged as an identification failure:\n%s", canceled)
	}
	if !strings.Contains(canceled, "level=DEBUG") || strings.Contains(canceled, "127.0.0.1") || strings.Contains(canceled, "context canceled") {
		t.Fatalf("want one value-free debug line:\n%s", canceled)
	}

	who.stall.Store(false)
	who.fail.Store(true)
	if code := getStatus(t, srv.URL); code != http.StatusForbidden {
		t.Fatalf("live WhoIs failure status=%d", code)
	}
	testwait.Recv(t, done, "live MCP failure handler")
	who.fail.Store(false)
	if code := getStatus(t, srv.URL); code != http.StatusOK {
		t.Fatalf("identified caller status=%d", code)
	}
	testwait.Recv(t, done, "identified MCP request handler")
	if served.Load() != 1 {
		t.Fatalf("served %d times, want only the identified caller", served.Load())
	}
	if n := strings.Count(logs.String(), "mcp control plane denied: failed to identify caller"); n != 1 {
		t.Fatalf("identification warnings=%d, want the live failure only:\n%s", n, logs)
	}
}

// Suppressing the access event is not enough: an unidentified request must
// never reach the handler behind the identification boundary. The downstream
// handler here ignores cancellation and counts invocations, as a file handler
// would, so it cannot rely on a proxy transport noticing the canceled context.
// Both a canceled and a live identification failure must leave it uncalled.
func TestUnidentifiedRequestIsNeverServed(t *testing.T) {
	for _, boundary := range []string{"acl", "people", "gateway"} {
		t.Run(boundary, func(t *testing.T) {
			who := &stallingWhoIs{entered: make(chan struct{}, 1)}
			var served atomic.Int64
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				served.Add(1)
				io.WriteString(w, "private file")
			})
			var h http.Handler
			switch boundary {
			case "acl":
				h = ACLMiddleware([]string{"alice@example.com"}, who.client())(next)
			case "people":
				path := t.TempDir() + "/registry.json"
				svc := registry.Service{Name: "app", Type: registry.TypeProxy, Target: "http://localhost:9", PeopleScoped: true}
				if _, err := registry.Add(path, svc); err != nil {
					t.Fatal(err)
				}
				h = peopleMiddleware(path, svc, func() (*LocalClient, error) { return who.client(), nil }, time.Now)(next)
			case "gateway":
				h = (&gatewayGate{lc: who.client()}).handler(next)
			}
			done := make(chan struct{}, 4)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer func() { done <- struct{}{} }()
				h.ServeHTTP(w, r)
			}))
			defer srv.Close()

			who.stall.Store(true)
			cancelAfter(t, srv.URL, who.entered)
			testwait.Recv(t, done, "canceled identification handler")
			if n := served.Load(); n != 0 {
				t.Fatalf("canceled unidentified request served %d times", n)
			}
			who.stall.Store(false)
			who.fail.Store(true)
			if code := getStatus(t, srv.URL); code != http.StatusForbidden {
				t.Fatalf("live identification failure status=%d", code)
			}
			testwait.Recv(t, done, "live identification failure handler")
			if n := served.Load(); n != 0 {
				t.Fatalf("live unidentified request served %d times", n)
			}
		})
	}
}

// Stopping or reloading an app node waits for in-flight requests; when that
// graceful shutdown runs out of time TSLink cancels them. Those requests are
// recorded as allowed, 503 and node_closed: not client_canceled, not a
// backend failure.
func TestNodeForcedCloseIsNotAClientCancellation(t *testing.T) {
	entered := make(chan struct{}, 1)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		<-r.Context().Done()
	}))
	defer backend.Close()
	oldShutdown := shutdownHTTPServerFn
	shutdownHTTPServerFn = func(context.Context, *http.Server) error { return context.DeadlineExceeded }
	t.Cleanup(func() { shutdownHTTPServerFn = oldShutdown })
	who := &stallingWhoIs{entered: make(chan struct{}, 1)}
	events := &gatewayEvents{}
	svc := registry.Service{Name: "app", Type: registry.TypeProxy, Target: backend.URL, AllowedUsers: []string{"alice@example.com"}}
	s, _, _, url := setupAccessNodeWithClient(t, svc, who.client(), accesslog.Options{}, nil, events)
	logs := captureServerLog(t)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		if resp, err := http.Get(url + "/slow"); err == nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
	}()
	testwait.Recv(t, entered, "backend entered")
	s.mu.Lock()
	s.stopNodeLocked(svc.Name)
	s.mu.Unlock()
	testwait.Recv(t, finished, "request ended by the forced close")
	var got []accesslog.Event
	testwait.Until(t, "access event", func() bool { got = events.all(); return len(got) > 0 })
	if len(got) != 1 {
		t.Fatalf("events %+v", got)
	}
	if e := got[0]; e.Decision != "allowed" || e.Status != http.StatusServiceUnavailable || e.Reason != "node_closed" || e.ErrorClass != "" || e.Path != "/slow" {
		t.Fatalf("forced close event %+v", e)
	}
	if strings.Contains(logs.String(), "backend_request_failed") {
		t.Fatalf("forced close logged as a backend failure:\n%s", logs)
	}
}

// Once the backend's response headers are forwarded, the status on the wire is
// committed. A client that leaves during the body keeps that status, including
// a forwarded 5xx: no 499, no client_canceled, no error class and no backend
// failure log.
func TestClientCancelAfterResponseStartKeepsForwardedStatus(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusInternalServerError} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(status)
				io.WriteString(w, "hello")
				w.(http.Flusher).Flush()
				<-r.Context().Done()
			}))
			defer backend.Close()
			url, done, _, events := cancelNode(t, registry.Service{Name: "app", Type: registry.TypeProxy, Target: backend.URL, AllowedUsers: []string{"alice@example.com"}}, nil)
			logs := captureServerLog(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url+"/stream", nil)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			first := make([]byte, 5)
			if _, err := io.ReadFull(resp.Body, first); err != nil || resp.StatusCode != status || string(first) != "hello" {
				t.Fatalf("response %d %q %v", resp.StatusCode, first, err)
			}
			cancel()
			testwait.Recv(t, done, "streaming request handler")
			got := events()
			if len(got) != 1 {
				t.Fatalf("events %+v", got)
			}
			if e := got[0]; e.Decision != "allowed" || e.Status != status || e.Reason != "" || e.ErrorClass != "" || e.Path != "/stream" {
				t.Fatalf("post-header cancellation event %+v", e)
			}
			if strings.Contains(logs.String(), "backend_request_failed") {
				t.Fatalf("post-header cancellation logged as a backend failure:\n%s", logs)
			}
		})
	}
}
