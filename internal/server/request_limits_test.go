package server

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/inspect"
	"github.com/anydoor7/tslink/internal/registry"
	tsRuntime "github.com/anydoor7/tslink/internal/runtime"
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
					time.Sleep(50 * time.Millisecond)
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
	conn.SetDeadline(time.Now().Add(3 * time.Second))
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
	blank.SetDeadline(time.Now().Add(3 * time.Second))
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
	c.SetDeadline(time.Now().Add(3 * time.Second))
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
	for _, h2 := range []bool{false, true} {
		t.Run(fmt.Sprintf("http2=%v", h2), func(t *testing.T) {
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Pause consumption after upload begins, filling the proxy's
				// backend socket while the client still has much more to send.
				if _, err := io.ReadFull(r.Body, make([]byte, 1)); err != nil {
					return
				}
				time.Sleep(250 * time.Millisecond)
				n, err := io.Copy(io.Discard, r.Body)
				if err != nil {
					return
				}
				time.Sleep(200 * time.Millisecond)
				w.Header().Set("Upload-Bytes", strconv.FormatInt(n+1, 10))
				w.WriteHeader(204)
			}))
			defer backend.Close()
			svc := registry.Service{Name: "video", Type: registry.TypeProxy, RequestLimits: &registry.RequestLimits{MaxBody: "unlimited", UnlimitedAck: true, ReadTimeout: "100ms"}}
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
	ln := configureServiceHTTP(srv, registry.Service{Type: registry.TypeProxy}, &fakeListener{}, nil)
	srv.ConnState(nil, http.StateNew)
	if !called {
		t.Fatal("existing ConnState hook lost")
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
