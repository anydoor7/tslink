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
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"

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
