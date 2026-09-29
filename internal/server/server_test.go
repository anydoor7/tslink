package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/monody0007/tslink/internal/authmode"
	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/registry"
	runtimesnapshot "github.com/monody0007/tslink/internal/runtime"
	"github.com/monody0007/tslink/internal/tailapi"
	"github.com/monody0007/tslink/internal/testenv"
	"github.com/monody0007/tslink/internal/testenv/localapitest"
	"tailscale.com/ipn"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/tailcfg"
	"tailscale.com/tsnet"
)

func writeRegistry(t *testing.T, services []registry.Service) string {
	t.Helper()

	path, err := config.RegistryPath()
	if err != nil {
		t.Fatalf("RegistryPath() error = %v", err)
	}
	data, err := json.Marshal(registry.Registry{Services: services})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return path
}

func stubConfigDirError(t *testing.T) {
	t.Helper()
	orig := configDirFn
	configDirFn = func() (string, error) { return "", errors.New("synthetic config directory failure") }
	t.Cleanup(func() { configDirFn = orig })
}

func stubRegistryPathError(t *testing.T) {
	t.Helper()
	orig := registryPathFn
	registryPathFn = func() (string, error) { return "", errors.New("synthetic registry path failure") }
	t.Cleanup(func() { registryPathFn = orig })
}

func startRegistryWatcherTest(t *testing.T, s *Server) func() {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.watchRegistry(ctx)
	}()

	stop := func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Errorf("watchRegistry() did not return after context cancellation")
		}
	}
	t.Cleanup(stop)
	return stop
}

func TestHTTPResourceBudgetsConfigured(t *testing.T) {
	srv := newHTTPServerFn(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	if srv.ReadHeaderTimeout != httpReadHeaderTimeout {
		t.Fatalf("ReadHeaderTimeout = %s, want %s", srv.ReadHeaderTimeout, httpReadHeaderTimeout)
	}
	if srv.ReadTimeout != httpReadTimeout {
		t.Fatalf("ReadTimeout = %s, want %s", srv.ReadTimeout, httpReadTimeout)
	}
	if srv.IdleTimeout != httpIdleTimeout {
		t.Fatalf("IdleTimeout = %s, want %s", srv.IdleTimeout, httpIdleTimeout)
	}
	if srv.MaxHeaderBytes != httpMaxHeaderBytes {
		t.Fatalf("MaxHeaderBytes = %d, want %d", srv.MaxHeaderBytes, httpMaxHeaderBytes)
	}
	if srv.WriteTimeout != 0 {
		t.Fatalf("WriteTimeout = %s, want 0 to preserve streaming responses", srv.WriteTimeout)
	}
}

func TestResourceBudgetMiddlewareRejectsOversizedBody(t *testing.T) {
	called := false
	handler := ResourceBudgetMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(""))
	req.ContentLength = httpMaxRequestBytes + 1
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if called {
		t.Fatal("inner handler called for oversized body")
	}
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusRequestEntityTooLarge)
	}
}

func TestResourceBudgetMiddlewarePreservesFlusher(t *testing.T) {
	handler := ResourceBudgetMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := w.(http.Flusher); !ok {
			t.Fatal("ResponseWriter lost http.Flusher support")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
}

func TestLimitedListenerClosesConnectionsOverLimit(t *testing.T) {
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	defer raw.Close()
	limited := newLimitedListener(raw, 1, "test", "svc")

	accepted := make(chan net.Conn, 1)
	acceptErr := make(chan error, 1)
	go func() {
		conn, err := limited.Accept()
		if err != nil {
			acceptErr <- err
			return
		}
		accepted <- conn
	}()
	firstClient, err := net.Dial("tcp", raw.Addr().String())
	if err != nil {
		t.Fatalf("first Dial() error = %v", err)
	}
	defer firstClient.Close()
	var firstServer net.Conn
	select {
	case firstServer = <-accepted:
		defer firstServer.Close()
	case err := <-acceptErr:
		t.Fatalf("first Accept() error = %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("first Accept() timed out")
	}

	secondAccepted := make(chan net.Conn, 1)
	secondAcceptErr := make(chan error, 1)
	go func() {
		conn, err := limited.Accept()
		if err != nil {
			secondAcceptErr <- err
			return
		}
		secondAccepted <- conn
	}()

	secondClient, err := net.Dial("tcp", raw.Addr().String())
	if err != nil {
		t.Fatalf("second Dial() error = %v", err)
	}
	defer secondClient.Close()
	if err := secondClient.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline() error = %v", err)
	}
	buf := make([]byte, 1)
	if _, err := secondClient.Read(buf); err == nil {
		t.Fatal("second connection remained open over listener limit")
	} else {
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			t.Fatalf("second connection read timed out; connection was not closed over limit: %v", err)
		}
	}
	select {
	case conn := <-secondAccepted:
		conn.Close()
		t.Fatal("second Accept returned a connection despite full limit")
	default:
	}
	_ = raw.Close()
	select {
	case <-secondAcceptErr:
	case conn := <-secondAccepted:
		conn.Close()
		t.Fatal("second Accept returned a connection after listener close")
	case <-time.After(2 * time.Second):
		t.Fatal("second Accept did not unblock after listener close")
	}
}

func newNode(t *testing.T, svc registry.Service) *ServiceNode {
	t.Helper()

	return &ServiceNode{
		service: svc,
		cancel:  func() {},
	}
}

func TestInternalServerTestsDoNotImportReflectOrUnsafe(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("ParseFile(%s) error = %v", name, err)
		}
		for _, imp := range file.Imports {
			if imp.Path.Value == `"reflect"` || imp.Path.Value == `"unsafe"` {
				t.Fatalf("%s imports %s; internal/server tests must not mutate third-party private fields", name, imp.Path.Value)
			}
		}
	}
}

type fakeListener struct {
	closed atomic.Bool
}

func (l *fakeListener) Accept() (net.Conn, error) { return nil, net.ErrClosed }
func (l *fakeListener) Addr() net.Addr            { return &net.TCPAddr{} }
func (l *fakeListener) Close() error {
	l.closed.Store(true)
	return nil
}

type countingCloser struct {
	closeCount atomic.Int32
}

func (c *countingCloser) Close() error {
	c.closeCount.Add(1)
	return nil
}

type fakeTSNetServer struct {
	upErr                  error
	upWait                 bool
	status                 *ipnstate.Status
	listenErr              error
	listenTLSErr           error
	listenFunnelErr        error
	localClient            *LocalClient
	closed                 bool
	certDomains            []string
	dnsName                string
	listenCalled           int
	listenTLSCalled        int
	listenFunnelCalled     int
	localClientCalled      int
	closeCount             atomic.Int32
	upContext              context.Context
	nodeContext            context.Context
	requireCanceledOnClose bool
	closeSawActiveContext  atomic.Bool
}

type fakeInteractiveTSNetServer struct {
	fakeTSNetServer
	startCalled bool
	upCalled    bool
	startErr    error
}

func (s *fakeInteractiveTSNetServer) Start() error {
	s.startCalled = true
	return s.startErr
}

func (s *fakeInteractiveTSNetServer) Up(context.Context) (*ipnstate.Status, error) {
	s.upCalled = true
	return nil, errors.New("interactive path must not call Up")
}

type sequenceTSNetStatusClient struct {
	statuses []*ipnstate.Status
	calls    int
}

type gatedInteractiveStatusClient struct {
	ready chan struct{}
	calls atomic.Int32
}

func (c *gatedInteractiveStatusClient) Status(ctx context.Context) (*ipnstate.Status, error) {
	if c.calls.Add(1) == 1 {
		return &ipnstate.Status{BackendState: "NeedsLogin", AuthURL: "https://login.tailscale.com/a/slow-human"}, nil
	}
	select {
	case <-c.ready:
		return &ipnstate.Status{
			BackendState: ipn.Running.String(),
			TailscaleIPs: []netip.Addr{netip.MustParseAddr("100.64.0.2")},
			Self:         &ipnstate.PeerStatus{DNSName: "slow-human.example.ts.net."},
		}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (c *sequenceTSNetStatusClient) Status(context.Context) (*ipnstate.Status, error) {
	if len(c.statuses) == 0 {
		return nil, errors.New("no status configured")
	}
	index := c.calls
	if index >= len(c.statuses) {
		index = len(c.statuses) - 1
	}
	c.calls++
	return c.statuses[index], nil
}

func (s *fakeTSNetServer) Up(ctx context.Context) (*ipnstate.Status, error) {
	s.upContext = ctx
	if s.upWait {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if s.upErr != nil {
		return nil, s.upErr
	}
	if s.status != nil {
		return s.status, nil
	}
	status := &ipnstate.Status{}
	if s.dnsName != "" {
		status.Self = &ipnstate.PeerStatus{DNSName: s.dnsName}
	}
	return status, nil
}

func (s *fakeTSNetServer) Listen(network, addr string) (net.Listener, error) {
	s.listenCalled++
	if s.listenErr != nil {
		return nil, s.listenErr
	}
	return &fakeListener{}, nil
}

func (s *fakeTSNetServer) ListenTLS(network, addr string) (net.Listener, error) {
	s.listenTLSCalled++
	if s.listenTLSErr != nil {
		return nil, s.listenTLSErr
	}
	return &fakeListener{}, nil
}

func (s *fakeTSNetServer) ListenFunnel(network, addr string, opts ...tsnet.FunnelOption) (net.Listener, error) {
	s.listenFunnelCalled++
	if s.listenFunnelErr != nil {
		return nil, s.listenFunnelErr
	}
	return &fakeListener{}, nil
}

func funnelEnabledStatus(dnsName string) *ipnstate.Status {
	return &ipnstate.Status{Self: &ipnstate.PeerStatus{
		DNSName: dnsName,
		CapMap: tailcfg.NodeCapMap{
			tailcfg.CapabilityHTTPS:                      nil,
			tailcfg.NodeAttrFunnel:                       nil,
			tailcfg.CapabilityFunnelPorts + "?ports=443": nil,
		},
	}}
}

func funnelMissingStatus(dnsName string) *ipnstate.Status {
	status := funnelEnabledStatus(dnsName)
	delete(status.Self.CapMap, tailcfg.NodeAttrFunnel)
	return status
}

func (s *fakeTSNetServer) LocalClient() (*LocalClient, error) {
	s.localClientCalled++
	if s.localClient != nil {
		return s.localClient, nil
	}
	return nil, errors.New("local client unavailable")
}

func (s *fakeTSNetServer) CertDomains() []string {
	return append([]string(nil), s.certDomains...)
}

func (s *fakeTSNetServer) Close() error {
	s.closeCount.Add(1)
	if s.requireCanceledOnClose && s.nodeContext != nil {
		select {
		case <-s.nodeContext.Done():
		default:
			s.closeSawActiveContext.Store(true)
		}
	}
	s.closed = true
	return nil
}

type blockingListenerTSNetServer struct {
	fakeTSNetServer
	listenStarted chan struct{}
	unblock       chan struct{}
	unblockOnce   sync.Once
}

type blockingInteractiveListenerTSNetServer struct {
	fakeInteractiveTSNetServer
	listenStarted chan struct{}
	unblock       chan struct{}
	unblockOnce   sync.Once
}

func (s *blockingInteractiveListenerTSNetServer) ListenTLS(string, string) (net.Listener, error) {
	close(s.listenStarted)
	<-s.unblock
	return &fakeListener{}, nil
}

func (s *blockingInteractiveListenerTSNetServer) Close() error {
	s.closeCount.Add(1)
	s.closed = true
	s.unblockOnce.Do(func() { close(s.unblock) })
	return nil
}

func (s *blockingListenerTSNetServer) ListenFunnel(string, string, ...tsnet.FunnelOption) (net.Listener, error) {
	close(s.listenStarted)
	<-s.unblock
	return nil, net.ErrClosed
}

func (s *blockingListenerTSNetServer) Close() error {
	s.closeCount.Add(1)
	s.closed = true
	s.unblockOnce.Do(func() { close(s.unblock) })
	return nil
}

type listenerTSNetServer struct {
	ln     net.Listener
	closed atomic.Bool
}

func (s *listenerTSNetServer) Up(context.Context) (*ipnstate.Status, error) {
	return &ipnstate.Status{}, nil
}

func (s *listenerTSNetServer) Listen(network, addr string) (net.Listener, error) {
	return s.ln, nil
}

func (s *listenerTSNetServer) ListenTLS(network, addr string) (net.Listener, error) {
	return s.ln, nil
}

func (s *listenerTSNetServer) ListenFunnel(network, addr string, opts ...tsnet.FunnelOption) (net.Listener, error) {
	return s.ln, nil
}

func (s *listenerTSNetServer) LocalClient() (*LocalClient, error) {
	return nil, errors.New("local client unavailable")
}

func (s *listenerTSNetServer) CertDomains() []string {
	return nil
}

func (s *listenerTSNetServer) Close() error {
	s.closed.Store(true)
	if s.ln != nil {
		return s.ln.Close()
	}
	return nil
}

type funnelWarningTSNetServer struct {
	t        *testing.T
	logBuf   *bytes.Buffer
	listened atomic.Bool
	closed   atomic.Bool
}

func (s *funnelWarningTSNetServer) Up(context.Context) (*ipnstate.Status, error) {
	return funnelEnabledStatus("public-app.tailnet.ts.net."), nil
}

func (s *funnelWarningTSNetServer) Listen(network, addr string) (net.Listener, error) {
	return &fakeListener{}, nil
}

func (s *funnelWarningTSNetServer) ListenTLS(network, addr string) (net.Listener, error) {
	return &fakeListener{}, nil
}

func (s *funnelWarningTSNetServer) ListenFunnel(network, addr string, opts ...tsnet.FunnelOption) (net.Listener, error) {
	s.t.Helper()
	if !strings.Contains(s.logBuf.String(), "funnel.listener.public") {
		s.t.Fatalf("ListenFunnel opened before public Funnel warning was logged; logs: %s", s.logBuf.String())
	}
	s.listened.Store(true)
	return &fakeListener{}, nil
}

func (s *funnelWarningTSNetServer) LocalClient() (*LocalClient, error) {
	return nil, nil
}

func (s *funnelWarningTSNetServer) CertDomains() []string {
	return nil
}

func (s *funnelWarningTSNetServer) Close() error {
	s.closed.Store(true)
	return nil
}

func TestServiceChanged(t *testing.T) {
	base := registry.Service{
		Name:   "a",
		Type:   registry.TypeProxy,
		Target: "http://localhost:3000",
	}

	if serviceChanged(base, base) {
		t.Error("identical services should not be changed")
	}

	changed := base
	changed.Type = registry.TypeFile
	if !serviceChanged(base, changed) {
		t.Error("different type should be changed")
	}

	changed = base
	changed.Target = "http://localhost:4000"
	if !serviceChanged(base, changed) {
		t.Error("different target should be changed")
	}

	changed = base
	changed.Path = "/tmp"
	if !serviceChanged(base, changed) {
		t.Error("different path should be changed")
	}

	changed = base
	changed.Name = "b"
	if serviceChanged(base, changed) {
		t.Error("different name only should not count as changed")
	}
}

func TestIsClosedListenerError(t *testing.T) {
	if !isClosedListenerError(net.ErrClosed) {
		t.Error("net.ErrClosed should be detected")
	}

	if !isClosedListenerError(errors.New("use of closed network connection")) {
		t.Error("closed network connection string should be detected")
	}

	wrapped := errors.Join(errors.New("wrapper"), net.ErrClosed)
	if !isClosedListenerError(wrapped) {
		t.Error("wrapped net.ErrClosed should be detected")
	}

	if isClosedListenerError(errors.New("some other error")) {
		t.Error("random error should not be detected")
	}
}

func TestNew(t *testing.T) {
	testenv.SetHome(t, t.TempDir())

	s, err := New("test-auth-key", "https://headscale.example.com")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if s == nil {
		t.Fatal("New() returned nil")
	}
	if s.authKey != "test-auth-key" {
		t.Fatalf("authKey = %q, want %q", s.authKey, "test-auth-key")
	}
	if s.controlURL != "https://headscale.example.com" {
		t.Fatalf("controlURL = %q, want %q", s.controlURL, "https://headscale.example.com")
	}
	if s.nodes == nil {
		t.Fatal("nodes map should be initialized")
	}
}

func TestSetAuthKeyProviderNilRestoresStaticProvider(t *testing.T) {
	testenv.SetHome(t, t.TempDir())

	s, err := New("static-key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	s.SetAuthKeyProvider(func(context.Context, registry.Service) (string, error) {
		return "dynamic-key", nil
	})
	s.SetAuthKeyProvider(nil)

	got, err := s.authKeyProvider(context.Background(), registry.Service{Name: "svc"})
	if err != nil {
		t.Fatalf("authKeyProvider() error = %v", err)
	}
	if got != "static-key" {
		t.Fatalf("authKeyProvider() = %q, want static key", got)
	}
}

func TestRemoveRuntimeSnapshotUsesConfiguredSeams(t *testing.T) {
	oldPath := runtimeSnapshotPathFn
	oldRemove := runtimeRemoveSnapshotFn
	t.Cleanup(func() {
		runtimeSnapshotPathFn = oldPath
		runtimeRemoveSnapshotFn = oldRemove
	})

	var removedPath string
	runtimeSnapshotPathFn = func() (string, error) {
		return "/tmp/runtime.json", nil
	}
	runtimeRemoveSnapshotFn = func(path string) error {
		removedPath = path
		return nil
	}

	(&Server{}).removeRuntimeSnapshot()
	if removedPath != "/tmp/runtime.json" {
		t.Fatalf("removed path = %q, want seam path", removedPath)
	}

	removedPath = ""
	runtimeSnapshotPathFn = func() (string, error) {
		return "", errors.New("path unavailable")
	}
	(&Server{}).removeRuntimeSnapshot()
	if removedPath != "" {
		t.Fatalf("removed path = %q after path error, want no remove call", removedPath)
	}
}

func TestStopNodeLocked_CAS(t *testing.T) {
	testenv.SetHome(t, t.TempDir())

	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	node := newNode(t, registry.Service{Name: "test"})
	s.nodes["test"] = node

	s.stopNodeLocked("test", false)

	if _, exists := s.nodes["test"]; exists {
		t.Error("node should be removed after stop")
	}
	if !node.closed.Load() {
		t.Error("node.closed should be true after stop")
	}
}

func TestStopNodeLocked_AlreadyClosed(t *testing.T) {
	testenv.SetHome(t, t.TempDir())

	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	node := newNode(t, registry.Service{Name: "test"})
	node.closed.Store(true)
	s.nodes["test"] = node

	s.stopNodeLocked("test", false)

	if _, exists := s.nodes["test"]; exists {
		t.Error("node should be removed from map")
	}
}

func TestStopNodeLocked_RemoveState(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}

	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	nodesDir, err := config.NodesDir()
	if err != nil {
		t.Fatalf("NodesDir() error = %v", err)
	}
	stateDir := filepath.Join(nodesDir, "test")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	node := newNode(t, registry.Service{Name: "test"})
	s.nodes["test"] = node

	s.stopNodeLocked("test", true)

	if _, err := os.Stat(stateDir); !os.IsNotExist(err) {
		t.Fatalf("expected %q to be removed, stat err = %v", stateDir, err)
	}
}

func TestStopNodeLocked_ClosesListenerAndServer(t *testing.T) {
	testenv.SetHome(t, t.TempDir())

	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ln := &fakeListener{}
	ts := &fakeTSNetServer{}

	s.nodes["test"] = &ServiceNode{
		service:  registry.Service{Name: "test"},
		listener: ln,
		tsnetSrv: ts,
		cancel:   func() {},
	}

	s.stopNodeLocked("test", false)

	if !ln.closed.Load() {
		t.Fatal("listener should be closed")
	}
	if !ts.closed {
		t.Fatal("tsnet server should be closed")
	}
}

func TestStopNodeLocked_ClosesPinnedFileHandler(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	closer := &countingCloser{}
	s.nodes["files"] = &ServiceNode{
		service:       registry.Service{Name: "files", Type: registry.TypeFile},
		handlerCloser: closer,
		cancel:        func() {},
	}

	s.stopNodeLocked("files", false)

	if got := closer.closeCount.Load(); got != 1 {
		t.Fatalf("pinned file handler Close calls = %d, want exactly 1", got)
	}
}

func TestStopNodeLocked_HTTPServerShutdownFallsBackToClose(t *testing.T) {
	testenv.SetHome(t, t.TempDir())

	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ln := &fakeListener{}
	fakeTS := &fakeTSNetServer{}

	oldShutdown := shutdownHTTPServerFn
	oldClose := closeHTTPServerFn
	t.Cleanup(func() {
		shutdownHTTPServerFn = oldShutdown
		closeHTTPServerFn = oldClose
	})

	var shutdownCalled atomic.Int32
	var closeCalled atomic.Int32
	shutdownHTTPServerFn = func(ctx context.Context, srv *http.Server) error {
		shutdownCalled.Add(1)
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("shutdown context should have a deadline")
		}
		return context.DeadlineExceeded
	}
	closeHTTPServerFn = func(srv *http.Server) error {
		closeCalled.Add(1)
		return nil
	}

	s.nodes["test"] = &ServiceNode{
		service:  registry.Service{Name: "test"},
		listener: ln,
		httpSrv:  &http.Server{},
		tsnetSrv: fakeTS,
		cancel:   func() {},
	}

	s.stopNodeLocked("test", false)

	if shutdownCalled.Load() != 1 {
		t.Fatalf("shutdown calls = %d, want 1", shutdownCalled.Load())
	}
	if closeCalled.Load() != 1 {
		t.Fatalf("close calls = %d, want 1 fallback close", closeCalled.Load())
	}
	if !ln.closed.Load() {
		t.Fatal("listener should be closed after HTTP shutdown")
	}
	if !fakeTS.closed {
		t.Fatal("tsnet server should be closed")
	}
	if _, exists := s.nodes["test"]; exists {
		t.Fatal("node should be removed after stop")
	}
}

func TestCloseAllNodes(t *testing.T) {
	testenv.SetHome(t, t.TempDir())

	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	s.nodes["one"] = newNode(t, registry.Service{Name: "one"})
	s.nodes["two"] = newNode(t, registry.Service{Name: "two"})

	s.closeAllNodes()

	if len(s.nodes) != 0 {
		t.Fatalf("expected 0 nodes after closeAllNodes(), got %d", len(s.nodes))
	}
}

func TestStartNodeLocked_NodesDirError(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}

	nodesDir, err := config.NodesDir()
	if err != nil {
		t.Fatalf("NodesDir() error = %v", err)
	}
	if err := os.RemoveAll(nodesDir); err != nil {
		t.Fatalf("RemoveAll() error = %v", err)
	}
	if err := os.WriteFile(nodesDir, []byte("not-a-directory"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	err = s.startNodeLocked(context.Background(), registry.Service{Name: "svc", Type: registry.TypeFile, Path: t.TempDir()})
	if err == nil {
		t.Fatal("startNodeLocked() error = nil, want error")
	}
}

func TestStartNodeLocked_ClosesTSNetServerOnUpError(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}

	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	fake := &fakeTSNetServer{upErr: errors.New("up failed")}
	oldNew := newTSNetServerFn
	newTSNetServerFn = func(svc registry.Service, stateDir, authKey, controlURL string) tsnetServer {
		return fake
	}
	t.Cleanup(func() { newTSNetServerFn = oldNew })

	err = s.startNodeLocked(context.Background(), registry.Service{
		Name:   "svc",
		Type:   registry.TypeProxy,
		Target: "http://localhost:3000",
	})
	if err == nil {
		t.Fatal("startNodeLocked() error = nil, want Up error")
	}
	if !fake.closed {
		t.Fatal("tsnet server should be closed after Up error")
	}
	if _, ok := s.nodes["svc"]; ok {
		t.Fatal("failed node should not be registered")
	}
}

func TestNewTSNetServerCredentialTiersPreserveTaggedCompatibility(t *testing.T) {
	svc := registry.Service{
		Name:      "svc",
		Tags:      []string{"tag:tsmain", "tag:shared"},
		Ephemeral: true,
	}

	credentialed, ok := newTSNetServer(svc, t.TempDir(), "tskey-auth-test", "https://control.example.com").(*tsnet.Server)
	if !ok {
		t.Fatal("credentialed constructor did not return *tsnet.Server")
	}
	if credentialed.AuthKey != "tskey-auth-test" {
		t.Fatalf("credentialed AuthKey = %q, want supplied auth key", credentialed.AuthKey)
	}
	if got := strings.Join(credentialed.AdvertiseTags, ","); got != "tag:tsmain,tag:shared" {
		t.Fatalf("credentialed AdvertiseTags = %q, want original service tags", got)
	}
	if !credentialed.Ephemeral || credentialed.ControlURL != "https://control.example.com" {
		t.Fatalf("credentialed constructor changed service options: %+v", credentialed)
	}
	if credentialed.UserLogf == nil {
		t.Fatal("credentialed UserLogf = nil")
	}

	funnelService := svc
	funnelService.Funnel = true
	funnelService.Tags = []string{"tag:tsmain"}
	funnel, ok := newTSNetServer(funnelService, t.TempDir(), "tskey-auth-test", "").(*tsnet.Server)
	if !ok {
		t.Fatal("Funnel constructor did not return *tsnet.Server")
	}
	if got := strings.Join(funnel.AdvertiseTags, ","); got != "tag:tsmain,"+registry.FunnelTag {
		t.Fatalf("Funnel AdvertiseTags = %q, want derived shared tag", got)
	}

	interactive, ok := newTSNetServer(svc, t.TempDir(), "", "").(*tsnet.Server)
	if !ok {
		t.Fatal("interactive constructor did not return *tsnet.Server")
	}
	if interactive.AuthKey != "" {
		t.Fatalf("interactive AuthKey = %q, want empty", interactive.AuthKey)
	}
	if len(interactive.AdvertiseTags) != 0 {
		t.Fatalf("interactive AdvertiseTags = %v, want no tags", interactive.AdvertiseTags)
	}
	if !interactive.Ephemeral {
		t.Fatal("interactive constructor changed Ephemeral=false")
	}
	if interactive.UserLogf == nil {
		t.Fatal("interactive UserLogf = nil")
	}
}

func TestStartNodeLocked_ZeroCredentialUsesStableStatusWithoutUp(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}

	s, err := New("", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(s.closeAllNodes)
	s.SetAuthKeyProvider(func(context.Context, registry.Service) (string, error) { return "", nil })

	fake := &fakeInteractiveTSNetServer{}
	statusClient := &sequenceTSNetStatusClient{statuses: []*ipnstate.Status{
		{BackendState: "NeedsLogin", AuthURL: "https://login.tailscale.com/a/test-auth"},
		{
			BackendState: "Running",
			TailscaleIPs: []netip.Addr{netip.MustParseAddr("100.64.0.1")},
			Self:         &ipnstate.PeerStatus{DNSName: "svc.example.ts.net."},
		},
	}}

	oldNew := newTSNetServerFn
	oldStatusClient := tsnetStatusClientFn
	oldPoll := interactiveStatusPollInterval
	newTSNetServerFn = func(svc registry.Service, stateDir, authKey, controlURL string) tsnetServer {
		if authKey != "" {
			t.Fatalf("interactive auth key = %q, want empty", authKey)
		}
		return fake
	}
	tsnetStatusClientFn = func(tsnetServer) (tsnetStatusClient, error) { return statusClient, nil }
	interactiveStatusPollInterval = time.Millisecond
	t.Cleanup(func() {
		newTSNetServerFn = oldNew
		tsnetStatusClientFn = oldStatusClient
		interactiveStatusPollInterval = oldPoll
	})

	var handoffs []AuthHandoff
	s.SetAuthHandoffFunc(func(_ context.Context, handoff AuthHandoff) error {
		handoffs = append(handoffs, handoff)
		return nil
	})

	err = s.startNodeLocked(context.Background(), registry.Service{
		Name: "svc",
		Type: registry.TypeFile,
		Path: t.TempDir(),
		Tags: []string{"tag:tsmain"},
	})
	if err != nil {
		t.Fatalf("startNodeLocked() error = %v", err)
	}
	if !fake.startCalled || fake.upCalled {
		t.Fatalf("interactive calls: Start=%v Up=%v, want Start only", fake.startCalled, fake.upCalled)
	}
	if statusClient.calls < 2 {
		t.Fatalf("Status() calls = %d, want needs-login then running", statusClient.calls)
	}
	if len(handoffs) != 1 || handoffs[0].Service != "svc" || handoffs[0].AuthURL != "https://login.tailscale.com/a/test-auth" {
		t.Fatalf("auth handoffs = %+v, want one stable Status AuthURL", handoffs)
	}
	if node := s.nodes["svc"]; node == nil || node.runtimeHost != "svc.example.ts.net" {
		t.Fatalf("running node = %+v, want authenticated runtime host", node)
	}
}

func TestStartNodeLocked_InteractiveAuthorizationOutlivesTechnicalStartupDeadline(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}

	s, err := New("", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	s.SetAuthKeyProvider(func(context.Context, registry.Service) (string, error) { return "", nil })

	fake := &fakeInteractiveTSNetServer{}
	statusClient := &gatedInteractiveStatusClient{ready: make(chan struct{})}
	oldNew := newTSNetServerFn
	oldStatusClient := tsnetStatusClientFn
	oldPoll := interactiveStatusPollInterval
	oldTimeout := nodeStartupTimeout
	newTSNetServerFn = func(registry.Service, string, string, string) tsnetServer { return fake }
	tsnetStatusClientFn = func(tsnetServer) (tsnetStatusClient, error) { return statusClient, nil }
	interactiveStatusPollInterval = time.Millisecond
	nodeStartupTimeout = 10 * time.Millisecond
	t.Cleanup(func() {
		newTSNetServerFn = oldNew
		tsnetStatusClientFn = oldStatusClient
		interactiveStatusPollInterval = oldPoll
		nodeStartupTimeout = oldTimeout
		s.closeAllNodes()
	})

	handoffPublished := make(chan struct{})
	s.SetAuthHandoffFunc(func(_ context.Context, handoff AuthHandoff) error {
		if handoff.AuthURL != "https://login.tailscale.com/a/slow-human" {
			t.Errorf("auth URL = %q", handoff.AuthURL)
		}
		select {
		case <-handoffPublished:
		default:
			close(handoffPublished)
		}
		return nil
	})

	servicePath := t.TempDir()
	result := make(chan error, 1)
	go func() {
		result <- s.startNodeLocked(context.Background(), registry.Service{Name: "slow-human", Type: registry.TypeFile, Path: servicePath})
	}()

	select {
	case <-handoffPublished:
	case <-time.After(time.Second):
		t.Fatal("interactive auth URL was not published")
	}
	time.Sleep(4 * nodeStartupTimeout)
	select {
	case err := <-result:
		t.Fatalf("interactive startup returned after technical deadline: %v", err)
	default:
	}

	close(statusClient.ready)
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("startNodeLocked() after authorization error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("interactive startup did not resume after authorization")
	}
	if node := s.nodes["slow-human"]; node == nil || node.runtimeHost != "slow-human.example.ts.net" {
		t.Fatalf("running node = %+v, want authorized interactive node", node)
	}
}

func TestStartNodeLocked_InteractiveListenerUsesIndependentTechnicalDeadline(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}
	s, err := New("", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	s.SetAuthKeyProvider(func(context.Context, registry.Service) (string, error) { return "", nil })

	fake := &blockingInteractiveListenerTSNetServer{
		listenStarted: make(chan struct{}),
		unblock:       make(chan struct{}),
	}
	statusClient := &sequenceTSNetStatusClient{statuses: []*ipnstate.Status{{
		BackendState: ipn.Running.String(),
		TailscaleIPs: []netip.Addr{netip.MustParseAddr("100.64.0.3")},
		Self:         &ipnstate.PeerStatus{DNSName: "interactive-listener.example.ts.net."},
	}}}
	oldNew := newTSNetServerFn
	oldStatusClient := tsnetStatusClientFn
	oldPoll := interactiveStatusPollInterval
	oldTimeout := nodeStartupTimeout
	newTSNetServerFn = func(registry.Service, string, string, string) tsnetServer { return fake }
	tsnetStatusClientFn = func(tsnetServer) (tsnetStatusClient, error) { return statusClient, nil }
	interactiveStatusPollInterval = time.Millisecond
	nodeStartupTimeout = 20 * time.Millisecond
	t.Cleanup(func() {
		newTSNetServerFn = oldNew
		tsnetStatusClientFn = oldStatusClient
		interactiveStatusPollInterval = oldPoll
		nodeStartupTimeout = oldTimeout
		s.closeAllNodes()
	})

	parentCtx, cancelParent := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancelParent()
	started := time.Now()
	err = s.startNodeLocked(parentCtx, registry.Service{Name: "interactive-listener", Type: registry.TypeFile, Path: t.TempDir()})
	elapsed := time.Since(started)
	code, _ := registry.ErrorCode(err)
	if code != registry.CodeServiceStartTimeout {
		t.Fatalf("startNodeLocked() error = %v code=%q, want %s", err, code, registry.CodeServiceStartTimeout)
	}
	if elapsed >= 150*time.Millisecond {
		t.Fatalf("interactive listener timeout took %s, want independent %s deadline", elapsed, nodeStartupTimeout)
	}
	select {
	case <-fake.listenStarted:
	default:
		t.Fatal("interactive path did not reach listener activation")
	}
}

func TestStartNodeLocked_UsesPerServiceAuthKeyProvider(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}

	s, err := New("static-key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(s.closeAllNodes)

	var providerService registry.Service
	s.SetAuthKeyProvider(func(ctx context.Context, svc registry.Service) (string, error) {
		providerService = svc
		if len(svc.Tags) != 1 || svc.Tags[0] != "tag:svc" {
			t.Fatalf("provider tags = %v, want [tag:svc]", svc.Tags)
		}
		if !svc.Ephemeral {
			t.Fatal("provider service Ephemeral = false, want true")
		}
		return "per-service-key", nil
	})

	var capturedAuthKey string
	oldNew := newTSNetServerFn
	newTSNetServerFn = func(svc registry.Service, stateDir, authKey, controlURL string) tsnetServer {
		capturedAuthKey = authKey
		return &fakeTSNetServer{}
	}
	t.Cleanup(func() { newTSNetServerFn = oldNew })

	err = s.startNodeLocked(context.Background(), registry.Service{
		Name:      "svc",
		Type:      registry.TypeFile,
		Path:      t.TempDir(),
		Tags:      []string{"tag:svc"},
		Ephemeral: true,
	})
	if err != nil {
		t.Fatalf("startNodeLocked() error = %v", err)
	}
	if providerService.Name != "svc" {
		t.Fatalf("provider service name = %q, want svc", providerService.Name)
	}
	if capturedAuthKey != "per-service-key" {
		t.Fatalf("authKey = %q, want per-service-key", capturedAuthKey)
	}
}

func TestSyncNodes_NewerGenerationCancelsInteractiveStartupBeforeReconcile(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}
	writeRegistry(t, []registry.Service{{Name: "pending", Type: registry.TypeFile, Path: t.TempDir()}})

	fake := &fakeInteractiveTSNetServer{}
	statusClient := &gatedInteractiveStatusClient{ready: make(chan struct{})}
	oldNew := newTSNetServerFn
	oldStatusClient := tsnetStatusClientFn
	oldPoll := interactiveStatusPollInterval
	newTSNetServerFn = func(registry.Service, string, string, string) tsnetServer { return fake }
	tsnetStatusClientFn = func(tsnetServer) (tsnetStatusClient, error) { return statusClient, nil }
	interactiveStatusPollInterval = time.Millisecond
	t.Cleanup(func() {
		newTSNetServerFn = oldNew
		tsnetStatusClientFn = oldStatusClient
		interactiveStatusPollInterval = oldPoll
	})

	s, err := New("", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	s.SetAuthKeyProvider(func(context.Context, registry.Service) (string, error) { return "", nil })
	handoff := make(chan struct{})
	s.SetAuthHandoffFunc(func(context.Context, AuthHandoff) error {
		select {
		case <-handoff:
		default:
			close(handoff)
		}
		return nil
	})

	firstDone := make(chan error, 1)
	go func() { firstDone <- s.syncNodes(context.Background()) }()
	select {
	case <-handoff:
	case <-time.After(time.Second):
		t.Fatal("interactive authorization URL was not published")
	}

	lockAvailable := make(chan struct{})
	go func() {
		// Taking the lock and immediately releasing it IS the assertion: it
		// proves the interactive authorization wait does not hold s.mu while it
		// blocks. The critical section is empty on purpose.
		s.mu.Lock()
		//lint:ignore SA2001 the empty critical section is the assertion, see above
		s.mu.Unlock()
		close(lockAvailable)
	}()
	select {
	case <-lockAvailable:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("interactive authorization wait held s.mu")
	}

	writeRegistry(t, nil)
	secondDone := make(chan error, 1)
	go func() { secondDone <- s.syncNodes(context.Background()) }()
	select {
	case err := <-secondDone:
		if err != nil {
			t.Fatalf("superseding syncNodes() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("registry removal did not supersede interactive startup")
	}
	select {
	case err := <-firstDone:
		if err != nil {
			t.Fatalf("superseded syncNodes() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("superseded interactive sync did not return")
	}
	if fake.closeCount.Load() != 1 {
		t.Fatalf("superseded interactive node Close calls = %d, want 1", fake.closeCount.Load())
	}
	if s.nodeRunning("pending") {
		t.Fatal("removed pending service committed after supersession")
	}
}

func TestSecuritySemantics_FileNoAllowStartsWithoutWhoIsDependency(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}

	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(s.closeAllNodes)

	fake := &fakeTSNetServer{}
	oldNew := newTSNetServerFn
	newTSNetServerFn = func(svc registry.Service, stateDir, authKey, controlURL string) tsnetServer {
		return fake
	}
	t.Cleanup(func() { newTSNetServerFn = oldNew })

	err = s.startNodeLocked(context.Background(), registry.Service{
		Name: "files", Type: registry.TypeFile, Path: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("startNodeLocked() error = %v", err)
	}
	if fake.localClientCalled != 0 {
		t.Fatalf("LocalClient called %d times, want none for file/no-allow", fake.localClientCalled)
	}
	if fake.listenTLSCalled != 1 {
		t.Fatalf("ListenTLS called %d times, want one HTTPS file listener", fake.listenTLSCalled)
	}
}

func TestSecuritySemantics_FileAllowFailsClosedWhenWhoIsUnavailable(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}

	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	fake := &fakeTSNetServer{}
	oldNew := newTSNetServerFn
	newTSNetServerFn = func(svc registry.Service, stateDir, authKey, controlURL string) tsnetServer {
		return fake
	}
	t.Cleanup(func() { newTSNetServerFn = oldNew })

	err = s.startNodeLocked(context.Background(), registry.Service{
		Name: "files", Type: registry.TypeFile, Path: t.TempDir(), AllowedUsers: []string{"alice@example.com"},
	})
	if err == nil {
		t.Fatal("startNodeLocked() error = nil, want LocalClient failure before listener")
	}
	if !strings.Contains(err.Error(), "local client") {
		t.Fatalf("error = %v, want local client context", err)
	}
	if fake.listenTLSCalled != 0 {
		t.Fatalf("ListenTLS called %d times, want no listener on file/allow identity failure", fake.listenTLSCalled)
	}
}

func TestSecuritySemantics_RawTCPBypassesHTTPIdentityAndTLSMiddleware(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}

	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	fake := &fakeTSNetServer{}
	oldNew := newTSNetServerFn
	newTSNetServerFn = func(svc registry.Service, stateDir, authKey, controlURL string) tsnetServer {
		return fake
	}
	t.Cleanup(func() { newTSNetServerFn = oldNew })

	err = s.startNodeLocked(context.Background(), registry.Service{
		Name: "db", Type: registry.TypeTCP, Target: "localhost:5432", Port: 5432,
	})
	if err != nil {
		t.Fatalf("startNodeLocked() error = %v", err)
	}
	if fake.listenCalled != 1 || fake.listenTLSCalled != 0 || fake.localClientCalled != 0 {
		t.Fatalf("raw tcp calls: Listen=%d ListenTLS=%d LocalClient=%d, want raw Listen only", fake.listenCalled, fake.listenTLSCalled, fake.localClientCalled)
	}
}

func TestStartNodeLocked_UsesEffectiveControlURL(t *testing.T) {
	tests := []struct {
		name              string
		serviceControlURL string
		serverControlURL  string
		wantControlURL    string
	}{
		{
			name:              "per service control URL wins",
			serviceControlURL: "https://service-control.example.com",
			serverControlURL:  "https://server-control.example.com",
			wantControlURL:    "https://service-control.example.com",
		},
		{
			name:             "server control URL fallback",
			serverControlURL: "https://server-control.example.com",
			wantControlURL:   "https://server-control.example.com",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			testenv.SetHome(t, t.TempDir())
			if err := config.EnsureDir(); err != nil {
				t.Fatalf("EnsureDir() error = %v", err)
			}

			s, err := New("key", tc.serverControlURL)
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			t.Cleanup(s.closeAllNodes)

			var capturedControlURL string
			oldNew := newTSNetServerFn
			newTSNetServerFn = func(svc registry.Service, stateDir, authKey, controlURL string) tsnetServer {
				capturedControlURL = controlURL
				return &fakeTSNetServer{}
			}
			t.Cleanup(func() { newTSNetServerFn = oldNew })

			err = s.startNodeLocked(context.Background(), registry.Service{
				Name:       "svc",
				Type:       registry.TypeFile,
				Path:       t.TempDir(),
				ControlURL: tc.serviceControlURL,
			})
			if err != nil {
				t.Fatalf("startNodeLocked() error = %v", err)
			}
			if capturedControlURL != tc.wantControlURL {
				t.Fatalf("controlURL = %q, want %q", capturedControlURL, tc.wantControlURL)
			}
		})
	}
}

func TestStartNodeLocked_HTTPServerHasTimeouts(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}

	oldNew := newTSNetServerFn
	newTSNetServerFn = func(svc registry.Service, stateDir, authKey, controlURL string) tsnetServer {
		return &fakeTSNetServer{}
	}
	t.Cleanup(func() { newTSNetServerFn = oldNew })

	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	err = s.startNodeLocked(context.Background(), registry.Service{
		Name: "files",
		Type: registry.TypeFile,
		Path: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("startNodeLocked() error = %v", err)
	}
	t.Cleanup(func() {
		s.stopNodeLocked("files", false)
	})

	node := s.nodes["files"]
	if node.httpSrv == nil {
		t.Fatal("HTTP service should store configured http.Server")
	}
	if node.httpSrv.ReadHeaderTimeout <= 0 {
		t.Fatalf("ReadHeaderTimeout = %v, want non-zero", node.httpSrv.ReadHeaderTimeout)
	}
	if node.httpSrv.IdleTimeout <= 0 {
		t.Fatalf("IdleTimeout = %v, want non-zero", node.httpSrv.IdleTimeout)
	}
	if node.httpSrv.WriteTimeout != 0 {
		t.Fatalf("WriteTimeout = %v, want zero to avoid breaking long streams", node.httpSrv.WriteTimeout)
	}
}

func TestStartNodeLocked_HTTPServerReadHeaderTimeoutClosesSlowClient(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}

	oldNew := newTSNetServerFn
	newTSNetServerFn = func(svc registry.Service, stateDir, authKey, controlURL string) tsnetServer {
		return &listenerTSNetServer{ln: ln}
	}
	oldHTTP := newHTTPServerFn
	newHTTPServerFn = func(handler http.Handler) *http.Server {
		return &http.Server{
			Handler:           handler,
			ReadHeaderTimeout: 25 * time.Millisecond,
			IdleTimeout:       time.Second,
		}
	}
	t.Cleanup(func() {
		newTSNetServerFn = oldNew
		newHTTPServerFn = oldHTTP
	})

	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	err = s.startNodeLocked(context.Background(), registry.Service{
		Name: "files",
		Type: registry.TypeFile,
		Path: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("startNodeLocked() error = %v", err)
	}
	t.Cleanup(func() {
		s.stopNodeLocked("files", false)
	})

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer conn.Close()

	if _, err := conn.Write([]byte("GET / HTTP/1.1\r\nHost: localhost\r\n")); err != nil {
		t.Fatalf("partial Write() error = %v", err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond)); err != nil {
		t.Fatalf("SetReadDeadline() error = %v", err)
	}

	var b [1]byte
	_, err = conn.Read(b[:])
	if err == nil {
		t.Fatal("partial request unexpectedly received data; want timeout-driven close")
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		t.Fatalf("connection remained open until client read deadline; http.Server ReadHeaderTimeout was not applied: %v", err)
	}
}

func TestStartNodeLocked_AuthKeyProviderErrorIncludesService(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}

	s, err := New("static-key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	s.SetAuthKeyProvider(func(ctx context.Context, svc registry.Service) (string, error) {
		return "", errors.New("derive failed")
	})

	err = s.startNodeLocked(context.Background(), registry.Service{
		Name: "svc", Type: registry.TypeFile, Path: t.TempDir(), Tags: []string{"tag:svc"},
	})
	if err == nil {
		t.Fatal("startNodeLocked() error = nil, want auth provider error")
	}
	if !strings.Contains(err.Error(), `auth key for service "svc"`) {
		t.Fatalf("error = %v, want service context", err)
	}
}

func TestStartNodeLocked_InvalidTagIncludesServiceContext(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}

	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	err = s.startNodeLocked(context.Background(), registry.Service{
		Name: "legacy", Type: registry.TypeFile, Path: t.TempDir(), Tags: []string{"tag:Bad"},
	})
	if err == nil {
		t.Fatal("startNodeLocked() error = nil, want invalid tag error")
	}
	if !strings.Contains(err.Error(), `service "legacy": invalid tag "tag:Bad"`) {
		t.Fatalf("error = %v, want service/tag context", err)
	}
	if !strings.Contains(err.Error(), "tag:<lowercase-hyphen-name>") || !strings.Contains(err.Error(), "edit registry.json") {
		t.Fatalf("error = %v, want grammar and registry remediation", err)
	}
}

func TestStartNodeLocked_RejectsTCPAllowedUsers(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}

	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	err = s.startNodeLocked(context.Background(), registry.Service{
		Name:         "db",
		Type:         registry.TypeTCP,
		Target:       "localhost:5432",
		Port:         5432,
		AllowedUsers: []string{"alice@example.com"},
	})
	if err == nil {
		t.Fatal("startNodeLocked() error = nil, want tcp allowed_users error")
	}
	if !strings.Contains(err.Error(), "tcp services do not support allowed_users") {
		t.Fatalf("error = %v, want tcp allowed_users error", err)
	}
}

func TestSyncNodes_RejectsHandEditedTCPAllowedUsers(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}

	writeRegistry(t, []registry.Service{{
		Name:         "db",
		Type:         registry.TypeTCP,
		Target:       "localhost:5432",
		Port:         5432,
		AllowedUsers: []string{"alice@example.com"},
	}})

	oldNew := newTSNetServerFn
	newTSNetServerFn = func(svc registry.Service, stateDir, authKey, controlURL string) tsnetServer {
		t.Fatalf("syncNodes should reject tcp allowed_users before constructing tsnet server")
		return &fakeTSNetServer{}
	}
	t.Cleanup(func() { newTSNetServerFn = oldNew })

	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	err = s.syncNodes(context.Background())
	if err != nil {
		t.Fatalf("syncNodes() error = %v, want isolated per-service failure", err)
	}
	if failure := s.serviceFailures["db"]; failure.Error == nil || failure.Error.Code != registry.CodeAllowUnsupportedTCP {
		t.Fatalf("service failure = %+v, want %s", failure, registry.CodeAllowUnsupportedTCP)
	}
	if len(s.nodes) != 0 {
		t.Fatalf("nodes = %+v, want none after rejected hand-edited registry", s.nodes)
	}
}

func TestSyncNodes_RejectsHandEditedFunnelAllowedUsersBeforeListenFunnel(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}

	writeRegistry(t, []registry.Service{{
		Name:         "public-app",
		Type:         registry.TypeProxy,
		Target:       "http://localhost:3000",
		Funnel:       true,
		AllowedUsers: []string{"alice@example.com"},
	}})

	oldNew := newTSNetServerFn
	newTSNetServerFn = func(svc registry.Service, stateDir, authKey, controlURL string) tsnetServer {
		t.Fatalf("syncNodes should reject funnel allowed_users before constructing tsnet server")
		return &fakeTSNetServer{}
	}
	t.Cleanup(func() { newTSNetServerFn = oldNew })

	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	err = s.syncNodes(context.Background())
	if err != nil {
		t.Fatalf("syncNodes() error = %v, want isolated per-service failure", err)
	}
	if failure := s.serviceFailures["public-app"]; failure.Error == nil || failure.Error.Code != registry.CodeFunnelAllowConflict {
		t.Fatalf("service failure = %+v, want %s", failure, registry.CodeFunnelAllowConflict)
	}
	if len(s.nodes) != 0 {
		t.Fatalf("nodes = %+v, want none after rejected hand-edited registry", s.nodes)
	}
}

func TestSyncNodes_SkipsHandEditedFunnelWithoutPublicAckAndStartsValidProxy(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}

	writeRegistry(t, []registry.Service{
		{
			Name:   "public-app",
			Type:   registry.TypeProxy,
			Target: "http://localhost:3000",
			Funnel: true,
		},
		{
			Name:   "valid-app",
			Type:   registry.TypeProxy,
			Target: "http://localhost:3001",
		},
	})

	var logBuf bytes.Buffer
	oldLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, nil)))
	t.Cleanup(func() { slog.SetDefault(oldLogger) })

	oldNew := newTSNetServerFn
	newTSNetServerFn = func(svc registry.Service, stateDir, authKey, controlURL string) tsnetServer {
		if svc.Name == "public-app" {
			t.Fatalf("syncNodes should skip missing public_ack before constructing tsnet server")
		}
		return &funnelWarningTSNetServer{t: t, logBuf: &logBuf}
	}
	t.Cleanup(func() { newTSNetServerFn = oldNew })

	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if err := s.syncNodes(context.Background()); err != nil {
		t.Fatalf("syncNodes() error = %v", err)
	}
	if _, exists := s.nodes["public-app"]; exists {
		t.Fatal("missing-public_ack funnel service should not start")
	}
	if _, exists := s.nodes["valid-app"]; !exists {
		t.Fatal("valid proxy service should start despite skipped legacy funnel service")
	}
	logs := logBuf.String()
	if !strings.Contains(logs, "registry service failed strict load; isolating service") ||
		!strings.Contains(logs, "public-app") ||
		!strings.Contains(logs, registry.CodeFunnelPublicAckRequired) {
		t.Fatalf("logs = %s, want isolated failure warning with service name and code", logs)
	}
}

func TestSyncNodes_RejectsHandEditedFunnelControlURLBeforeListenFunnel(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}

	writeRegistry(t, []registry.Service{{
		Name:       "public-app",
		Type:       registry.TypeProxy,
		Target:     "http://localhost:3000",
		Funnel:     true,
		ControlURL: "https://headscale.example.com",
	}})

	oldNew := newTSNetServerFn
	newTSNetServerFn = func(svc registry.Service, stateDir, authKey, controlURL string) tsnetServer {
		t.Fatalf("syncNodes should reject funnel control_url before constructing tsnet server")
		return &fakeTSNetServer{}
	}
	t.Cleanup(func() { newTSNetServerFn = oldNew })

	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	err = s.syncNodes(context.Background())
	if err != nil {
		t.Fatalf("syncNodes() error = %v, want isolated per-service failure", err)
	}
	if failure := s.serviceFailures["public-app"]; failure.Error == nil || failure.Error.Code != registry.CodeFunnelControlURLConflict {
		t.Fatalf("service failure = %+v, want %s", failure, registry.CodeFunnelControlURLConflict)
	}
	if len(s.nodes) != 0 {
		t.Fatalf("nodes = %+v, want none after rejected hand-edited registry", s.nodes)
	}
}

func TestSyncNodes_RejectsHandEditedFunnelNonProxyTypesBeforeTSNet(t *testing.T) {
	cases := []registry.Service{
		{
			Name:   "public-files",
			Type:   registry.TypeFile,
			Path:   "/tmp/public-files",
			Funnel: true,
		},
		{
			Name:   "public-db",
			Type:   registry.TypeTCP,
			Target: "localhost:5432",
			Port:   5432,
			Funnel: true,
		},
	}
	for _, svc := range cases {
		t.Run(svc.Type, func(t *testing.T) {
			testenv.SetHome(t, t.TempDir())
			if err := config.EnsureDir(); err != nil {
				t.Fatalf("EnsureDir() error = %v", err)
			}

			writeRegistry(t, []registry.Service{svc})

			oldNew := newTSNetServerFn
			newTSNetServerFn = func(svc registry.Service, stateDir, authKey, controlURL string) tsnetServer {
				t.Fatalf("syncNodes should reject funnel type conflict before constructing tsnet server")
				return &fakeTSNetServer{}
			}
			t.Cleanup(func() { newTSNetServerFn = oldNew })

			s, err := New("key", "")
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}

			err = s.syncNodes(context.Background())
			if err != nil {
				t.Fatalf("syncNodes() error = %v, want isolated per-service failure", err)
			}
			if failure := s.serviceFailures[svc.Name]; failure.Error == nil || failure.Error.Code != registry.CodeFunnelTypeConflict {
				t.Fatalf("service failure = %+v, want %s", failure, registry.CodeFunnelTypeConflict)
			}
			if len(s.nodes) != 0 {
				t.Fatalf("nodes = %+v, want none after rejected hand-edited registry", s.nodes)
			}
		})
	}
}

func TestStartNodeLocked_FunnelLogsWarningBeforeListenFunnel(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}

	var logBuf bytes.Buffer
	oldLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, nil)))
	t.Cleanup(func() { slog.SetDefault(oldLogger) })

	funnelSrv := &funnelWarningTSNetServer{t: t, logBuf: &logBuf}
	oldNew := newTSNetServerFn
	newTSNetServerFn = func(svc registry.Service, stateDir, authKey, controlURL string) tsnetServer {
		return funnelSrv
	}
	t.Cleanup(func() { newTSNetServerFn = oldNew })

	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(s.closeAllNodes)

	err = s.startNodeLocked(context.Background(), registry.Service{
		Name:      "public-app",
		Type:      registry.TypeProxy,
		Target:    "http://localhost:3000",
		Funnel:    true,
		PublicAck: true,
	})
	if err != nil {
		t.Fatalf("startNodeLocked() error = %v", err)
	}
	if !funnelSrv.listened.Load() {
		t.Fatal("ListenFunnel was not opened")
	}
	if !strings.Contains(logBuf.String(), "Tailscale Funnel listener exposes this service to the public internet") {
		t.Fatalf("logs = %s, want public Funnel warning message", logBuf.String())
	}
}

func TestStartNodeLockedChecksExpiredFunnelBeforeArmingAnyFunnelListener(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	expires := now.Add(-time.Second)
	oldNow := serverNowFn
	serverNowFn = func() time.Time { return now }
	t.Cleanup(func() { serverNowFn = oldNow })

	fake := &fakeTSNetServer{localClient: localapitest.NewClient(nil), status: &ipnstate.Status{Self: &ipnstate.PeerStatus{
		ID:      tailcfg.StableNodeID("node-expired-fixture"),
		DNSName: "public-app.example.ts.net.",
	}}}
	oldNew := newTSNetServerFn
	newTSNetServerFn = func(svc registry.Service, _ string, _ string, _ string) tsnetServer {
		if svc.Funnel || containsString(svc.Tags, registry.FunnelTag) {
			t.Fatalf("expired service reached tsnet construction as Funnel: %+v", svc)
		}
		return fake
	}
	t.Cleanup(func() { newTSNetServerFn = oldNew })

	s, err := New("key", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.closeAllNodes)
	err = s.startNodeLocked(context.Background(), registry.Service{
		Name: "public-app", Type: registry.TypeProxy, Target: "http://localhost:3000",
		Funnel: true, PublicAck: true, FunnelExpiresAt: &expires,
	})
	if err != nil {
		t.Fatal(err)
	}
	if fake.listenFunnelCalled != 0 || fake.listenTLSCalled != 1 {
		t.Fatalf("listeners: Funnel=%d TLS=%d, want 0/1", fake.listenFunnelCalled, fake.listenTLSCalled)
	}
	ledgerPath, _ := config.NodeOwnershipPath()
	ledger, err := runtimesnapshot.LoadOwnership(ledgerPath)
	if err != nil || len(ledger.Nodes) != 1 || ledger.Nodes[0].ServiceName != "public-app" || ledger.Nodes[0].NodeID != "node-expired-fixture" {
		t.Fatalf("ownership ledger = %+v, err=%v", ledger, err)
	}
}

func TestRunPerformsStartupLifecycleCheckBeforeConstructingListeners(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	expires := now.Add(-time.Second)
	writeRegistry(t, []registry.Service{{
		Name: "startup-expired", Type: registry.TypeProxy, Target: "http://localhost:3000",
		Funnel: true, PublicAck: true, FunnelExpiresAt: &expires,
	}})
	oldNow := serverNowFn
	serverNowFn = func() time.Time { return now }
	t.Cleanup(func() { serverNowFn = oldNow })
	lifecycleChecked := false
	fake := &fakeTSNetServer{localClient: localapitest.NewClient(nil), status: &ipnstate.Status{Self: &ipnstate.PeerStatus{DNSName: "startup-expired.example.ts.net."}}}
	oldNew := newTSNetServerFn
	newTSNetServerFn = func(svc registry.Service, _ string, _ string, _ string) tsnetServer {
		if !lifecycleChecked {
			t.Fatal("tsnet construction occurred before startup lifecycle reconciliation")
		}
		if svc.Funnel {
			t.Fatal("startup constructed an expired Funnel service")
		}
		return fake
	}
	t.Cleanup(func() { newTSNetServerFn = oldNew })
	s, err := New("key", "")
	if err != nil {
		t.Fatal(err)
	}
	s.SetLifecycleReconcileFn(func(context.Context, time.Time) (bool, error) {
		lifecycleChecked = true
		return false, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	s.SetReadyFunc(func() error { cancel(); return nil })
	if err := s.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if !lifecycleChecked || fake.listenFunnelCalled != 0 || fake.listenTLSCalled != 1 {
		t.Fatalf("startup checked=%t Funnel=%d TLS=%d", lifecycleChecked, fake.listenFunnelCalled, fake.listenTLSCalled)
	}
}

func TestLifecycleTickerRereadsWallClockAfterSimulatedSleep(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatal(err)
	}
	writeRegistry(t, nil)
	s, err := New("key", "")
	if err != nil {
		t.Fatal(err)
	}
	beforeSleep := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	afterWake := beforeSleep.Add(8 * time.Hour)
	var nowCalls atomic.Int32
	oldNow, oldInterval := serverNowFn, lifecycleTickerInterval
	serverNowFn = func() time.Time {
		if nowCalls.Add(1) == 1 {
			return beforeSleep
		}
		return afterWake
	}
	lifecycleTickerInterval = 5 * time.Millisecond
	t.Cleanup(func() {
		serverNowFn = oldNow
		lifecycleTickerInterval = oldInterval
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var observed []time.Time
	s.SetLifecycleReconcileFn(func(_ context.Context, now time.Time) (bool, error) {
		observed = append(observed, now)
		if len(observed) == 2 {
			cancel()
		}
		return false, nil
	})
	done := s.startLifecycleTicker(ctx)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("lifecycle ticker did not observe two wall-clock ticks")
	}
	if len(observed) != 2 || !observed[0].Equal(beforeSleep) || !observed[1].Equal(afterWake) {
		t.Fatalf("observed wall clocks = %v, want pre-sleep then wake time", observed)
	}
}

func TestLifecycleTickerSkipsFullSyncWhenReconcileReportsNoChange(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatal(err)
	}
	writeRegistry(t, []registry.Service{{Name: "private", Type: registry.TypeProxy, Target: "http://localhost:3000"}})
	s, err := New("key", "")
	if err != nil {
		t.Fatal(err)
	}
	var constructed atomic.Int32
	oldNew := newTSNetServerFn
	newTSNetServerFn = func(registry.Service, string, string, string) tsnetServer {
		constructed.Add(1)
		return &fakeTSNetServer{localClient: localapitest.NewClient(nil), status: &ipnstate.Status{Self: &ipnstate.PeerStatus{DNSName: "private.example.ts.net."}}}
	}
	oldInterval := lifecycleTickerInterval
	lifecycleTickerInterval = 5 * time.Millisecond
	t.Cleanup(func() {
		newTSNetServerFn = oldNew
		lifecycleTickerInterval = oldInterval
	})
	var ticks atomic.Int32
	s.SetLifecycleReconcileFn(func(context.Context, time.Time) (bool, error) {
		ticks.Add(1)
		return false, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := s.startLifecycleTicker(ctx)
	deadline := time.After(2 * time.Second)
	for ticks.Load() < 3 {
		select {
		case <-deadline:
			t.Fatal("lifecycle ticker did not run")
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	<-done
	if got := constructed.Load(); got != 0 {
		t.Fatalf("tsnet constructions = %d, want no full sync for unchanged lifecycle", got)
	}
}

func probeServer(t *testing.T) *Server {
	t.Helper()
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatal(err)
	}
	writeRegistry(t, []registry.Service{{Name: "private", Type: registry.TypeProxy, Target: "http://localhost:3000"}})
	s, err := New("key", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.closeAllNodes)
	oldNew := newTSNetServerFn
	newTSNetServerFn = func(registry.Service, string, string, string) tsnetServer {
		return &fakeTSNetServer{localClient: localapitest.NewClient(nil), status: &ipnstate.Status{Self: &ipnstate.PeerStatus{ID: "nprobe1CNTRL", DNSName: "private.example.ts.net."}}}
	}
	t.Cleanup(func() { newTSNetServerFn = oldNew })
	return s
}

func TestSyncNodesAuthoritativeTracksLatestFailureState(t *testing.T) {
	s := probeServer(t)
	oldAfter := afterDesiredLoadedFn
	t.Cleanup(func() { afterDesiredLoadedFn = oldAfter })
	afterDesiredLoadedFn = func(context.Context, uint64) error { return errors.New("synthetic initial failure") }
	if err := s.syncNodesAuthoritative(context.Background()); err == nil {
		t.Fatal("syncNodesAuthoritative() error = nil, want injected failure")
	}
	if !s.lastSyncFailed.Load() {
		t.Fatal("authoritative failure did not set lastSyncFailed")
	}
	afterDesiredLoadedFn = func(context.Context, uint64) error { return nil }
	if err := s.syncNodesAuthoritative(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.lastSyncFailed.Load() {
		t.Fatal("authoritative success did not clear lastSyncFailed")
	}
}

// A newer sync failure must not be cleared by an older superseded sync that
// returns nil through the stale-generation early return.
func TestSyncGenerationGuardIsLoadBearing(t *testing.T) {
	s := probeServer(t)
	oldAfter := afterDesiredLoadedFn
	t.Cleanup(func() { afterDesiredLoadedFn = oldAfter })

	release := make(chan struct{})
	entered := make(chan struct{}, 1)
	var calls atomic.Int32
	afterDesiredLoadedFn = func(_ context.Context, gen uint64) error {
		if calls.Add(1) == 1 {
			entered <- struct{}{}
			<-release
			return errors.New("synthetic older-generation failure")
		}
		return errors.New("synthetic newer-generation failure")
	}
	slowDone := make(chan error, 1)
	go func() { slowDone <- s.syncNodes(context.Background()) }()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("older sync never reached the seam")
	}
	newerErr := s.syncNodes(context.Background())
	bitAfterNewerFailure := s.lastSyncFailed.Load()
	close(release)
	olderErr := <-slowDone
	bitFinal := s.lastSyncFailed.Load()
	t.Logf("PROBE R3-25: newer err=%v bit=%t | older(superseded) err=%v | FINAL bit=%t",
		newerErr, bitAfterNewerFailure, olderErr, bitFinal)
	if !bitAfterNewerFailure {
		t.Fatalf("newer failure did not record lastSyncFailed")
	}
	if !bitFinal {
		t.Errorf("REGRESSION: superseded older sync cleared a genuine newer failure (retry lost)")
	}
}

func TestLifecycleTickerRetriesFailedSyncThenReturnsToChangeOnly(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatal(err)
	}
	writeRegistry(t, []registry.Service{{Name: "private", Type: registry.TypeProxy, Target: "http://localhost:3000"}})
	s, err := New("key", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.closeAllNodes)
	oldNew := newTSNetServerFn
	newTSNetServerFn = func(registry.Service, string, string, string) tsnetServer {
		return &fakeTSNetServer{localClient: localapitest.NewClient(nil), status: &ipnstate.Status{Self: &ipnstate.PeerStatus{DNSName: "private.example.ts.net."}}}
	}
	oldAfter := afterDesiredLoadedFn
	var syncAttempts atomic.Int32
	afterDesiredLoadedFn = func(context.Context, uint64) error {
		if syncAttempts.Add(1) == 1 {
			return errors.New("synthetic transient sync failure")
		}
		return nil
	}
	oldInterval := lifecycleTickerInterval
	lifecycleTickerInterval = 5 * time.Millisecond
	t.Cleanup(func() {
		newTSNetServerFn = oldNew
		afterDesiredLoadedFn = oldAfter
		lifecycleTickerInterval = oldInterval
	})
	var ticks atomic.Int32
	s.SetLifecycleReconcileFn(func(context.Context, time.Time) (bool, error) {
		return ticks.Add(1) == 1, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := s.startLifecycleTicker(ctx)
	deadline := time.After(2 * time.Second)
	for ticks.Load() < 6 {
		select {
		case <-deadline:
			t.Fatal("lifecycle ticker did not run")
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	<-done
	if got := syncAttempts.Load(); got != 2 {
		t.Fatalf("sync attempts = %d, want initial failure plus one retry", got)
	}
	if s.lastSyncFailed.Load() {
		t.Fatal("lastSyncFailed remained set after successful retry")
	}
}

func TestRunningFunnelListenerIsTornDownAfterDeadline(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2030, 8, 31, 12, 0, 0, 0, time.UTC)
	expires := base.Add(time.Hour)
	writeRegistry(t, []registry.Service{{
		Name: "public-app", Type: registry.TypeProxy, Target: "http://localhost:3000",
		Funnel: true, PublicAck: true, Tags: []string{"tag:tsmain"}, FunnelExpiresAt: &expires,
	}})
	var current atomic.Int64
	current.Store(base.UnixNano())
	oldNow := serverNowFn
	serverNowFn = func() time.Time { return time.Unix(0, current.Load()).UTC() }
	oldInterval := lifecycleTickerInterval
	lifecycleTickerInterval = 5 * time.Millisecond
	t.Cleanup(func() {
		serverNowFn = oldNow
		lifecycleTickerInterval = oldInterval
	})

	tailnetOnlyConstructed := make(chan struct{}, 1)
	oldNew := newTSNetServerFn
	newTSNetServerFn = func(svc registry.Service, _ string, _ string, _ string) tsnetServer {
		if !svc.Funnel {
			select {
			case tailnetOnlyConstructed <- struct{}{}:
			default:
			}
		}
		return &fakeTSNetServer{
			localClient: localapitest.NewClient(nil),
			status:      funnelEnabledStatus("public-app.tailnet.ts.net."),
			certDomains: []string{"public-app.tailnet.ts.net"},
		}
	}
	t.Cleanup(func() { newTSNetServerFn = oldNew })

	s, err := New("key", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.closeAllNodes)
	s.SetAutoProvisionFunnel(true)
	s.SetEnsureFunnelAttrFn(func(context.Context, tailapi.FunnelPolicyRequest) (tailapi.PolicyMutationResult, error) {
		return tailapi.PolicyMutationResult{WriteOutcome: tailapi.PolicyWriteUnchanged}, nil
	})
	if err := s.syncNodes(context.Background()); err != nil {
		t.Fatalf("initial Funnel sync: %v", err)
	}
	regPath, err := config.RegistryPath()
	if err != nil {
		t.Fatal(err)
	}
	s.SetLifecycleReconcileFn(func(_ context.Context, now time.Time) (bool, error) {
		downgraded, err := registry.DowngradeExpiredFunnels(regPath, now, false)
		return len(downgraded) > 0, err
	})
	current.Store(expires.Add(time.Minute).UnixNano())
	ctx, cancel := context.WithCancel(context.Background())
	done := s.startLifecycleTicker(ctx)
	select {
	case <-tailnetOnlyConstructed:
		cancel()
	case <-time.After(2 * time.Second):
		cancel()
		<-done
		t.Fatal("expired running Funnel was not rebuilt as tailnet-only TLS")
	}
	<-done
}

func TestOwnershipWriteFailureRetriesThenContinuesServiceStartup(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatal(err)
	}
	fake := &fakeTSNetServer{localClient: localapitest.NewClient(nil), status: &ipnstate.Status{Self: &ipnstate.PeerStatus{
		ID: tailcfg.StableNodeID("node-ledger-retry-fixture"), DNSName: "private.example.ts.net.",
	}}}
	oldNew := newTSNetServerFn
	newTSNetServerFn = func(registry.Service, string, string, string) tsnetServer { return fake }
	oldRecord := recordOwnedNodeFn
	oldWait := ownershipRetryWaitFn
	recordCalls := 0
	recordOwnedNodeFn = func(string, string, string, time.Time) error {
		recordCalls++
		return errors.New("synthetic ledger write failure")
	}
	var retryDelays []time.Duration
	ownershipRetryWaitFn = func(_ context.Context, delay time.Duration) error {
		retryDelays = append(retryDelays, delay)
		return nil
	}
	var logBuf bytes.Buffer
	oldLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, nil)))
	t.Cleanup(func() {
		newTSNetServerFn = oldNew
		recordOwnedNodeFn = oldRecord
		ownershipRetryWaitFn = oldWait
		slog.SetDefault(oldLogger)
	})
	s, err := New("key", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.closeAllNodes)
	if err := s.startNodeLocked(context.Background(), registry.Service{Name: "private", Type: registry.TypeProxy, Target: "http://localhost:3000"}); err != nil {
		t.Fatalf("startNodeLocked() error = %v, want service availability", err)
	}
	if recordCalls != 3 || fake.listenTLSCalled != 1 {
		t.Fatalf("record calls=%d TLS=%d, want initial attempt, two retries, and continued listener", recordCalls, fake.listenTLSCalled)
	}
	if len(retryDelays) != 2 || retryDelays[0] != time.Second || retryDelays[1] != 5*time.Second {
		t.Fatalf("retry delays=%v, want [1s 5s]", retryDelays)
	}
	logs := logBuf.String()
	if !strings.Contains(logs, "retrying") || !strings.Contains(logs, "continuing without durable cleanup proof") || strings.Contains(logs, "node-ledger-retry-fixture") {
		t.Fatalf("logs = %q, want actionable warning without NodeID", logs)
	}
}

func TestOwnershipRetryBackoffSuccessAndCancellation(t *testing.T) {
	if err := waitForOwnershipRetry(context.Background(), time.Millisecond); err != nil {
		t.Fatalf("timer wait error = %v", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := waitForOwnershipRetry(canceled, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled wait error = %v, want context.Canceled", err)
	}

	oldRecord, oldWait := recordOwnedNodeFn, ownershipRetryWaitFn
	t.Cleanup(func() {
		recordOwnedNodeFn, ownershipRetryWaitFn = oldRecord, oldWait
	})
	recordCalls := 0
	recordOwnedNodeFn = func(string, string, string, time.Time) error {
		recordCalls++
		if recordCalls == 1 {
			return errors.New("synthetic first-write failure")
		}
		return nil
	}
	var delays []time.Duration
	ownershipRetryWaitFn = func(_ context.Context, delay time.Duration) error {
		delays = append(delays, delay)
		return nil
	}
	if ok := recordOwnedNodeWithBackoff(context.Background(), "ledger", "private", "node-retry-success-fixture"); !ok || recordCalls != 2 || len(delays) != 1 || delays[0] != time.Second {
		t.Fatalf("success retry ok=%t calls=%d delays=%v", ok, recordCalls, delays)
	}

	recordOwnedNodeFn = func(string, string, string, time.Time) error {
		return errors.New("synthetic persistent failure")
	}
	ownershipRetryWaitFn = waitForOwnershipRetry
	if ok := recordOwnedNodeWithBackoff(canceled, "ledger", "private", "node-retry-cancel-fixture"); ok {
		t.Fatal("canceled retry reported durable proof")
	}
}

func TestStartNodeLocked_MiddlewareConfigFailsBeforeTSNet(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}

	var constructed atomic.Int32
	oldNew := newTSNetServerFn
	newTSNetServerFn = func(svc registry.Service, stateDir, authKey, controlURL string) tsnetServer {
		constructed.Add(1)
		return &fakeTSNetServer{}
	}
	t.Cleanup(func() { newTSNetServerFn = oldNew })

	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(s.closeAllNodes)

	err = s.startNodeLocked(context.Background(), registry.Service{
		Name: "docs",
		Type: registry.TypeFile,
		Path: t.TempDir(),
		Middleware: &registry.MiddlewareConfig{
			BasicAuth: "user:pass",
		},
	})
	if err == nil {
		t.Fatal("startNodeLocked() error = nil, want middleware unavailable error")
	}
	if !strings.Contains(err.Error(), "middleware runtime is not wired") {
		t.Fatalf("startNodeLocked() error = %v, want middleware unavailable error", err)
	}
	if got := constructed.Load(); got != 0 {
		t.Fatalf("tsnet constructions = %d, want 0", got)
	}
}

func TestStartNodeLocked_CustomDomainFailsBeforeTSNet(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}

	var constructed atomic.Int32
	oldNew := newTSNetServerFn
	newTSNetServerFn = func(svc registry.Service, stateDir, authKey, controlURL string) tsnetServer {
		constructed.Add(1)
		return &fakeTSNetServer{}
	}
	t.Cleanup(func() { newTSNetServerFn = oldNew })

	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(s.closeAllNodes)

	err = s.startNodeLocked(context.Background(), registry.Service{
		Name:      "web",
		Type:      registry.TypeProxy,
		Target:    "http://localhost:3000",
		Domain:    "app.example.com",
		AcmeEmail: "admin@example.com",
	})
	if err == nil {
		t.Fatal("startNodeLocked() error = nil, want custom-domain/ACME unavailable error")
	}
	if !strings.Contains(err.Error(), "custom-domain/ACME runtime is not wired") {
		t.Fatalf("startNodeLocked() error = %v, want custom-domain/ACME unavailable error", err)
	}
	if got := constructed.Load(); got != 0 {
		t.Fatalf("tsnet constructions = %d, want 0", got)
	}
}

func TestSyncNodes_ContextCancelledPreventsStartingNode(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}

	writeRegistry(t, []registry.Service{
		{Name: "newnode", Type: registry.TypeFile, Path: t.TempDir()},
	})

	var constructed atomic.Int32
	oldNew := newTSNetServerFn
	newTSNetServerFn = func(svc registry.Service, stateDir, authKey, controlURL string) tsnetServer {
		constructed.Add(1)
		return &fakeTSNetServer{}
	}
	t.Cleanup(func() { newTSNetServerFn = oldNew })

	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err = s.syncNodes(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("syncNodes() error = %v, want context.Canceled", err)
	}
	if constructed.Load() != 0 {
		t.Fatalf("constructed tsnet servers = %d, want 0 after cancellation", constructed.Load())
	}
	if len(s.nodes) != 0 {
		t.Fatalf("nodes = %d, want 0", len(s.nodes))
	}
}

func TestSyncNodesRejectsStaleGenerationCommit(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}
	oldRoot := t.TempDir()
	newRoot := t.TempDir()
	oldReg := &registry.Registry{SchemaVersion: registry.CurrentRegistrySchemaVersion, Services: []registry.Service{{
		Name: "old", Type: registry.TypeFile, Path: oldRoot,
	}}}
	newReg := &registry.Registry{SchemaVersion: registry.CurrentRegistrySchemaVersion, Services: []registry.Service{{
		Name: "new", Type: registry.TypeFile, Path: newRoot,
	}}}

	var loadCount atomic.Int32
	oldLoad := registryLoadRuntimeFn
	registryLoadRuntimeFn = func(string) (*registry.Registry, []registry.ServiceIssue, error) {
		if loadCount.Add(1) == 1 {
			return oldReg, nil, nil
		}
		return newReg, nil, nil
	}
	t.Cleanup(func() { registryLoadRuntimeFn = oldLoad })

	firstDesiredLoaded := make(chan struct{})
	releaseFirst := make(chan struct{})
	oldAfterDesired := afterDesiredLoadedFn
	afterDesiredLoadedFn = func(ctx context.Context, generation uint64) error {
		if generation == 1 {
			close(firstDesiredLoaded)
			select {
			case <-releaseFirst:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	}
	t.Cleanup(func() { afterDesiredLoadedFn = oldAfterDesired })

	oldNew := newTSNetServerFn
	newTSNetServerFn = func(svc registry.Service, stateDir, authKey, controlURL string) tsnetServer {
		return &fakeTSNetServer{certDomains: []string{svc.Name + ".tailnet.ts.net"}}
	}
	t.Cleanup(func() { newTSNetServerFn = oldNew })

	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(s.closeAllNodes)

	firstDone := make(chan error, 1)
	go func() { firstDone <- s.syncNodes(context.Background()) }()
	select {
	case <-firstDesiredLoaded:
	case <-time.After(2 * time.Second):
		t.Fatal("first generation did not reach desired-state barrier")
	}

	if err := s.syncNodes(context.Background()); err != nil {
		t.Fatalf("second syncNodes() error = %v", err)
	}
	close(releaseFirst)
	select {
	case err := <-firstDone:
		if err != nil {
			t.Fatalf("first syncNodes() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("first syncNodes() did not finish")
	}

	if _, ok := s.nodes["new"]; !ok {
		t.Fatalf("nodes = %+v, want newer generation service", s.nodes)
	}
	if _, ok := s.nodes["old"]; ok {
		t.Fatalf("nodes = %+v, stale generation committed old service", s.nodes)
	}
}

func TestSyncNodes_CurrentGenerationPreReconcileAndTagErrors(t *testing.T) {
	t.Run("desired-state-hook", func(t *testing.T) {
		testenv.SetHome(t, t.TempDir())
		if err := config.EnsureDir(); err != nil {
			t.Fatal(err)
		}
		writeRegistry(t, nil)
		want := errors.New("desired hook failed")
		oldAfter := afterDesiredLoadedFn
		afterDesiredLoadedFn = func(context.Context, uint64) error { return want }
		t.Cleanup(func() { afterDesiredLoadedFn = oldAfter })
		s, err := New("key", "")
		if err != nil {
			t.Fatal(err)
		}
		if err := s.syncNodes(context.Background()); !errors.Is(err, want) {
			t.Fatalf("syncNodes() error = %v, want %v", err, want)
		}
	})

	t.Run("context-cancels-while-waiting-for-reconcile", func(t *testing.T) {
		testenv.SetHome(t, t.TempDir())
		if err := config.EnsureDir(); err != nil {
			t.Fatal(err)
		}
		writeRegistry(t, nil)
		s, err := New("key", "")
		if err != nil {
			t.Fatal(err)
		}
		s.reconcileGate <- struct{}{}
		t.Cleanup(func() { <-s.reconcileGate })
		ctx, cancel := context.WithCancel(context.Background())
		oldAfter := afterDesiredLoadedFn
		afterDesiredLoadedFn = func(context.Context, uint64) error {
			cancel()
			return nil
		}
		t.Cleanup(func() { afterDesiredLoadedFn = oldAfter })
		if err := s.syncNodes(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("syncNodes() error = %v, want context.Canceled", err)
		}
	})

	t.Run("tag-ensure", func(t *testing.T) {
		testenv.SetHome(t, t.TempDir())
		if err := config.EnsureDir(); err != nil {
			t.Fatal(err)
		}
		writeRegistry(t, []registry.Service{{Name: "tagged", Type: registry.TypeFile, Path: t.TempDir(), Tags: []string{"tag:svc"}}})
		want := errors.New("tag ensure failed")
		s, err := New("key", "")
		if err != nil {
			t.Fatal(err)
		}
		s.SetEnsureTagsFn(func(context.Context, []string) error { return want })
		if err := s.syncNodes(context.Background()); !errors.Is(err, want) {
			t.Fatalf("syncNodes() error = %v, want %v", err, want)
		}
	})
}

func TestSyncNodes_ShutdownStatePreventsStartingNode(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}

	writeRegistry(t, []registry.Service{
		{Name: "newnode", Type: registry.TypeFile, Path: t.TempDir()},
	})

	var constructed atomic.Int32
	oldNew := newTSNetServerFn
	newTSNetServerFn = func(svc registry.Service, stateDir, authKey, controlURL string) tsnetServer {
		constructed.Add(1)
		return &fakeTSNetServer{}
	}
	t.Cleanup(func() { newTSNetServerFn = oldNew })

	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	s.beginShutdown()

	err = s.syncNodes(context.Background())
	if !errors.Is(err, errServerShuttingDown) {
		t.Fatalf("syncNodes() error = %v, want errServerShuttingDown", err)
	}
	if constructed.Load() != 0 {
		t.Fatalf("constructed tsnet servers = %d, want 0 after shutdown begins", constructed.Load())
	}
	if len(s.nodes) != 0 {
		t.Fatalf("nodes = %d, want 0", len(s.nodes))
	}
}

func TestSyncNodes_RemovesDeletedService(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}
	writeRegistry(t, []registry.Service{})

	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	nodesDir, err := config.NodesDir()
	if err != nil {
		t.Fatalf("NodesDir() error = %v", err)
	}
	stateDir := filepath.Join(nodesDir, "old")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	s.nodes["old"] = newNode(t, registry.Service{Name: "old"})

	if err := s.syncNodes(context.Background()); err != nil {
		t.Fatalf("syncNodes() error = %v", err)
	}

	if _, exists := s.nodes["old"]; exists {
		t.Fatal("removed service should no longer have a running node")
	}
	if _, err := os.Stat(stateDir); !os.IsNotExist(err) {
		t.Fatalf("expected %q to be removed, stat err = %v", stateDir, err)
	}
}

func TestSyncNodes_CredentialUpgradeRemovesTierOneStateBeforeAuthKey(t *testing.T) {
	t.Setenv(config.ConfigDirEnv, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}
	writeRegistry(t, []registry.Service{
		{Name: "web", Type: registry.TypeFile, Path: t.TempDir(), Tags: []string{"tag:tsmain"}},
	})

	nodesDir, err := config.NodesDir()
	if err != nil {
		t.Fatalf("NodesDir() error = %v", err)
	}
	stateDir := filepath.Join(nodesDir, "web")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatalf("MkdirAll(state) error = %v", err)
	}
	statePath := filepath.Join(stateDir, "tailscaled.state")
	if err := os.WriteFile(statePath, []byte(`{"tier":"interactive"}`), 0o600); err != nil {
		t.Fatalf("WriteFile(state) error = %v", err)
	}
	if err := authmode.MarkCredentialUpgradePending(); err != nil {
		t.Fatalf("MarkCredentialUpgradePending() error = %v", err)
	}

	oldNew := newTSNetServerFn
	constructorCalled := false
	newTSNetServerFn = func(svc registry.Service, gotStateDir, authKey, controlURL string) tsnetServer {
		constructorCalled = true
		if authKey != "derived-tier-2-key" {
			t.Fatalf("auth key = %q, want derived Tier 2 key", authKey)
		}
		if gotStateDir != stateDir {
			t.Fatalf("state dir = %q, want %q", gotStateDir, stateDir)
		}
		if _, err := os.Stat(statePath); !os.IsNotExist(err) {
			t.Fatalf("Tier 1 state still exists when credentialed tsnet server is constructed: %v", err)
		}
		return &fakeTSNetServer{certDomains: []string{"web.tailnet.ts.net"}}
	}
	t.Cleanup(func() { newTSNetServerFn = oldNew })

	s, err := New("", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(s.closeAllNodes)
	s.SetCredentialed(true)
	s.SetAuthKeyProvider(func(context.Context, registry.Service) (string, error) {
		return "derived-tier-2-key", nil
	})
	if err := s.syncNodes(context.Background()); err != nil {
		t.Fatalf("syncNodes() error = %v", err)
	}
	if !constructorCalled {
		t.Fatal("credentialed tsnet server constructor was not called")
	}
	pending, err := authmode.CredentialUpgradePending()
	if err != nil {
		t.Fatalf("CredentialUpgradePending() error = %v", err)
	}
	if pending {
		t.Fatal("credential upgrade marker remains after Tier 1 state removal")
	}
}

func TestSyncNodes_WritesRuntimeSnapshotAfterServiceStarts(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}

	services := []registry.Service{
		{Name: "files", Type: registry.TypeFile, Path: t.TempDir(), Tags: []string{"tag:files"}},
	}
	regPath := writeRegistry(t, services)

	oldNew := newTSNetServerFn
	newTSNetServerFn = func(svc registry.Service, stateDir, authKey, controlURL string) tsnetServer {
		return &fakeTSNetServer{certDomains: []string{"files.tailnet.ts.net"}}
	}
	t.Cleanup(func() { newTSNetServerFn = oldNew })

	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(s.closeAllNodes)
	if err := s.syncNodes(context.Background()); err != nil {
		t.Fatalf("syncNodes() error = %v", err)
	}

	snapshotPath, err := config.RuntimeSnapshotPath()
	if err != nil {
		t.Fatalf("RuntimeSnapshotPath() error = %v", err)
	}
	snapshot, err := runtimesnapshot.Load(snapshotPath)
	if err != nil {
		t.Fatalf("runtime Load() error = %v", err)
	}
	if snapshot.SchemaVersion != runtimesnapshot.SchemaVersion {
		t.Fatalf("schema_version = %q, want %q", snapshot.SchemaVersion, runtimesnapshot.SchemaVersion)
	}
	if snapshot.DaemonPID != os.Getpid() {
		t.Fatalf("daemon_pid = %d, want %d", snapshot.DaemonPID, os.Getpid())
	}
	if snapshot.DaemonStartedAt.IsZero() || snapshot.UpdatedAt.IsZero() {
		t.Fatalf("snapshot timestamps should be set: %+v", snapshot)
	}
	reg, err := registry.Load(regPath)
	if err != nil {
		t.Fatalf("registry.Load() error = %v", err)
	}
	wantFingerprint, err := runtimesnapshot.RegistryFingerprint(reg)
	if err != nil {
		t.Fatalf("RegistryFingerprint() error = %v", err)
	}
	if snapshot.RegistryFingerprint != wantFingerprint {
		t.Fatalf("registry_fingerprint = %q, want %q", snapshot.RegistryFingerprint, wantFingerprint)
	}
	if len(snapshot.Services) != 1 {
		t.Fatalf("snapshot services = %d, want 1", len(snapshot.Services))
	}
	entry := snapshot.Services[0]
	if entry.Name != "files" || entry.Type != registry.TypeFile {
		t.Fatalf("service entry = %+v, want files file", entry)
	}
	if entry.Endpoint.Display != "https://files.tailnet.ts.net" || entry.Endpoint.State != "exact" {
		t.Fatalf("endpoint = %+v, want exact cert-domain URL", entry.Endpoint)
	}
	if entry.Exposure.Kind != "tailnet" || entry.Exposure.Public {
		t.Fatalf("exposure = %+v, want private tailnet", entry.Exposure)
	}
	if strings.Join(entry.CertDomains, ",") != "files.tailnet.ts.net" {
		t.Fatalf("cert domains = %v, want files.tailnet.ts.net", entry.CertDomains)
	}
}

func TestSyncNodes_PublishesPartialSnapshotBeforeStartingNextService(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}
	writeRegistry(t, []registry.Service{
		{Name: "first", Type: registry.TypeFile, Path: t.TempDir()},
		{Name: "second", Type: registry.TypeFile, Path: t.TempDir()},
	})

	oldNew := newTSNetServerFn
	newTSNetServerFn = func(svc registry.Service, stateDir, authKey, controlURL string) tsnetServer {
		return &fakeTSNetServer{certDomains: []string{svc.Name + ".tailnet.ts.net"}}
	}
	t.Cleanup(func() { newTSNetServerFn = oldNew })

	oldSave := runtimeSaveSnapshotFn
	var snapshots []runtimesnapshot.Snapshot
	runtimeSaveSnapshotFn = func(path string, snapshot runtimesnapshot.Snapshot) error {
		snapshots = append(snapshots, snapshot)
		return nil
	}
	t.Cleanup(func() { runtimeSaveSnapshotFn = oldSave })

	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(s.closeAllNodes)
	if err := s.syncNodes(context.Background()); err != nil {
		t.Fatalf("syncNodes() error = %v", err)
	}
	if len(snapshots) < 3 || len(snapshots[0].Services) != 1 || len(snapshots[len(snapshots)-1].Services) != 2 {
		t.Fatalf("snapshots = %+v, want partial 1 before complete 2", snapshots)
	}
	if !snapshots[0].Partial {
		t.Fatal("first incremental snapshot is not marked partial")
	}
	if snapshots[len(snapshots)-1].Partial {
		t.Fatal("final successful snapshot is still marked partial")
	}
	first := snapshots[0]
	freshness := runtimesnapshot.Classify(&first, nil, runtimesnapshot.ExpectedRuntime{
		DaemonPID:                  first.DaemonPID,
		DaemonStartedAtLowerBound:  first.DaemonStartedAt,
		CurrentRegistryFingerprint: first.RegistryFingerprint,
	})
	if freshness.Exact || freshness.Status != runtimesnapshot.StatusPartial {
		t.Fatalf("partial snapshot freshness = %+v, want non-authoritative partial", freshness)
	}
}

func TestSyncNodes_WritesConcreteTCPRuntimeHostFromStatus(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}

	writeRegistry(t, []registry.Service{
		{Name: "db", Type: registry.TypeTCP, Target: "localhost:5432", Port: 5432},
	})

	oldNew := newTSNetServerFn
	newTSNetServerFn = func(svc registry.Service, stateDir, authKey, controlURL string) tsnetServer {
		return &fakeTSNetServer{status: &ipnstate.Status{Self: &ipnstate.PeerStatus{ID: tailcfg.StableNodeID("n-db-owned"), DNSName: "db.tailnet.ts.net."}}}
	}
	t.Cleanup(func() { newTSNetServerFn = oldNew })

	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := s.syncNodes(context.Background()); err != nil {
		t.Fatalf("syncNodes() error = %v", err)
	}

	snapshotPath, err := config.RuntimeSnapshotPath()
	if err != nil {
		t.Fatalf("RuntimeSnapshotPath() error = %v", err)
	}
	snapshot, err := runtimesnapshot.Load(snapshotPath)
	if err != nil {
		t.Fatalf("runtime Load() error = %v", err)
	}
	if len(snapshot.Services) != 1 {
		t.Fatalf("snapshot services = %d, want 1", len(snapshot.Services))
	}
	endpoint := snapshot.Services[0].Endpoint
	if snapshot.Services[0].NodeID != "n-db-owned" {
		t.Fatalf("snapshot node_id = %q, want stable ID from status.Self.ID", snapshot.Services[0].NodeID)
	}
	if endpoint.Display != "db.tailnet.ts.net:5432" || endpoint.Host != "db.tailnet.ts.net" || endpoint.State != "exact" {
		t.Fatalf("tcp endpoint = %+v, want concrete exact runtime DNS host", endpoint)
	}
	if strings.Contains(endpoint.Display, "<tailnet>") {
		t.Fatalf("tcp endpoint = %+v, must not mark placeholder exact", endpoint)
	}
}

// A TCP service's accept loop must keep running after syncNodesWithOutcome
// returns. The startup generation context is canceled by design on return, so a
// serve loop derived from it would close its listener immediately after "tcp
// node ready" and every connection would be refused.
func TestSyncNodes_TCPListenerSurvivesStartupGenerationCompletion(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}
	writeRegistry(t, []registry.Service{
		{Name: "db", Type: registry.TypeTCP, Target: "localhost:5432", Port: 5432},
	})

	oldNew := newTSNetServerFn
	newTSNetServerFn = func(svc registry.Service, stateDir, authKey, controlURL string) tsnetServer {
		return &fakeTSNetServer{status: &ipnstate.Status{Self: &ipnstate.PeerStatus{DNSName: "db.tailnet.ts.net."}}}
	}
	t.Cleanup(func() { newTSNetServerFn = oldNew })

	oldServe := serveTCPFn
	serveCtxCh := make(chan context.Context, 1)
	serveTCPFn = func(ctx context.Context, ln net.Listener, target, name string) {
		serveCtxCh <- ctx
	}
	t.Cleanup(func() { serveTCPFn = oldServe })

	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(s.closeAllNodes)

	if err := s.syncNodes(context.Background()); err != nil {
		t.Fatalf("syncNodes() error = %v", err)
	}
	if _, ok := s.nodes["db"]; !ok {
		t.Fatal("tcp node was not committed")
	}

	var serveCtx context.Context
	select {
	case serveCtx = <-serveCtxCh:
	case <-time.After(time.Second):
		t.Fatal("serveTCP was never invoked for the tcp node")
	}
	select {
	case <-serveCtx.Done():
		t.Fatal("tcp serve context was canceled when the startup generation completed")
	default:
	}

	s.closeAllNodes()
	select {
	case <-serveCtx.Done():
	case <-time.After(time.Second):
		t.Fatal("tcp serve context did not stop after closeAllNodes")
	}
}

// startNodeLocked must read the serveTCP seam on the caller's goroutine. The
// TCP accept loop it spawns outlives the call, so a seam read from inside that
// goroutine is unordered with respect to every later write of serveTCPFn --
// including the t.Cleanup restore that every serveTCP test performs. Under
// -race the unfixed code reports a data race between the swap below and the
// accept loop's read, and the leaked loop can also invoke the *next* test's
// stub.
func TestStartNode_ReadsServeTCPSeamBeforeSpawningAcceptLoop(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}
	writeRegistry(t, []registry.Service{
		{Name: "db", Type: registry.TypeTCP, Target: "localhost:5432", Port: 5432},
	})

	oldNew := newTSNetServerFn
	newTSNetServerFn = func(svc registry.Service, stateDir, authKey, controlURL string) tsnetServer {
		return &fakeTSNetServer{status: &ipnstate.Status{Self: &ipnstate.PeerStatus{DNSName: "db.tailnet.ts.net."}}}
	}
	t.Cleanup(func() { newTSNetServerFn = oldNew })

	oldServe := serveTCPFn
	t.Cleanup(func() { serveTCPFn = oldServe })

	var once sync.Once
	served := make(chan struct{})
	var variant atomic.Int32
	// Both variants unblock the test, so whichever one the accept loop reads the
	// test still terminates; only the recorded variant differs.
	installedBeforeSync := func(context.Context, net.Listener, string, string) {
		variant.Store(1)
		once.Do(func() { close(served) })
	}
	swappedInAfterSync := func(context.Context, net.Listener, string, string) {
		variant.Store(2)
		once.Do(func() { close(served) })
	}
	serveTCPFn = installedBeforeSync

	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(s.closeAllNodes)
	if err := s.syncNodes(context.Background()); err != nil {
		t.Fatalf("syncNodes() error = %v", err)
	}

	// Swap the seam the way the next test would.
	serveTCPFn = swappedInAfterSync

	select {
	case <-served:
	case <-time.After(5 * time.Second):
		t.Fatal("tcp accept loop never invoked the serveTCP seam")
	}
	if got := variant.Load(); got != 1 {
		t.Fatalf("accept loop invoked seam variant %d, want 1 (the value installed before syncNodes)", got)
	}
}

func TestSyncNodes_RecoverableAgentFailuresAreBoundedAndVisible(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}
	writeRegistry(t, []registry.Service{
		{Name: "public-app", Type: registry.TypeProxy, Target: "http://localhost:3000", Tags: []string{"tag:tsmain", registry.FunnelTag}, Funnel: true, PublicAck: true},
		{Name: "listen-fail", Type: registry.TypeProxy, Target: "http://localhost:3001", Tags: []string{"tag:tsmain", registry.FunnelTag}, Funnel: true, PublicAck: true},
		{Name: "stuck", Type: registry.TypeFile, Path: t.TempDir()},
		{Name: "healthy", Type: registry.TypeFile, Path: t.TempDir()},
	})

	servers := map[string]*fakeTSNetServer{}
	nodeContexts := map[string]context.Context{}
	oldObserveNodeContext := observeNodeContextFn
	observeNodeContextFn = func(name string, ctx context.Context) {
		nodeContexts[name] = ctx
		if fake := servers[name]; fake != nil {
			fake.nodeContext = ctx
		}
	}
	t.Cleanup(func() { observeNodeContextFn = oldObserveNodeContext })
	oldNew := newTSNetServerFn
	newTSNetServerFn = func(svc registry.Service, stateDir, authKey, controlURL string) tsnetServer {
		fake := &fakeTSNetServer{}
		switch svc.Name {
		case "public-app":
			fake.status = funnelMissingStatus("public-app.tailnet.ts.net.")
			fake.requireCanceledOnClose = true
		case "stuck":
			fake.upWait = true
		case "listen-fail":
			fake.status = funnelEnabledStatus("listen-fail.tailnet.ts.net.")
			fake.localClient = localapitest.NewClient(nil)
			fake.listenFunnelErr = errors.New("synthetic Funnel bind failure")
		case "healthy":
			fake.certDomains = []string{"healthy.tailnet.ts.net"}
		}
		servers[svc.Name] = fake
		return fake
	}
	t.Cleanup(func() { newTSNetServerFn = oldNew })

	oldTimeout := nodeStartupTimeout
	nodeStartupTimeout = 20 * time.Millisecond
	t.Cleanup(func() { nodeStartupTimeout = oldTimeout })

	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(s.closeAllNodes)
	s.SetEnsureFunnelAttrFn(func(context.Context, tailapi.FunnelPolicyRequest) (tailapi.PolicyMutationResult, error) {
		return tailapi.PolicyMutationResult{WriteOutcome: tailapi.PolicyWriteUnchanged}, nil
	})

	started := time.Now()
	if err := s.syncNodes(context.Background()); err != nil {
		t.Fatalf("syncNodes() error = %v, want recoverable per-service failures", err)
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("syncNodes() took %s, want bounded startup", elapsed)
	}
	if _, ok := s.nodes["healthy"]; !ok {
		t.Fatal("healthy service did not start after prior service failures")
	}
	if _, ok := s.nodes["public-app"]; ok {
		t.Fatal("Funnel service without capability unexpectedly started")
	}
	if servers["public-app"].listenFunnelCalled != 0 {
		t.Fatal("ListenFunnel was called before the node-specific capability preflight passed")
	}
	for _, name := range []string{"public-app", "listen-fail", "stuck"} {
		if got := servers[name].closeCount.Load(); got != 1 {
			t.Fatalf("%s Close calls = %d, want exactly 1", name, got)
		}
	}
	select {
	case <-nodeContexts["public-app"].Done():
	default:
		t.Fatal("capability failure left its node context active")
	}
	if servers["public-app"].closeSawActiveContext.Load() {
		t.Fatal("capability failure closed tsnet before canceling its context")
	}

	snapshotPath, err := config.RuntimeSnapshotPath()
	if err != nil {
		t.Fatalf("RuntimeSnapshotPath() error = %v", err)
	}
	snapshot, err := runtimesnapshot.Load(snapshotPath)
	if err != nil {
		t.Fatalf("runtime Load() error = %v", err)
	}
	if snapshot.Partial || len(snapshot.Services) != 4 {
		t.Fatalf("snapshot = %+v, want exact state for all four services", snapshot)
	}
	byName := map[string]runtimesnapshot.ServiceSnapshot{}
	for _, service := range snapshot.Services {
		byName[service.Name] = service
	}
	publicApp := byName["public-app"]
	if !publicApp.FunnelRequested || publicApp.FunnelActive || publicApp.FunnelState != runtimesnapshot.FunnelStateCapabilityMissing {
		t.Fatalf("public-app Funnel state = %+v", publicApp)
	}
	if publicApp.RuntimeState != runtimesnapshot.ServiceRuntimeFailed || publicApp.Error == nil || publicApp.Error.Code != registry.CodeFunnelCapabilityMissing || len(publicApp.Error.Next) == 0 {
		t.Fatalf("public-app failure = %+v, want actionable stable error", publicApp)
	}
	listenFail := byName["listen-fail"]
	if !listenFail.FunnelRequested || listenFail.FunnelActive || listenFail.FunnelState != runtimesnapshot.FunnelStateListenFailed || listenFail.Error == nil || listenFail.Error.Code != registry.CodeFunnelListenFailed {
		t.Fatalf("listen-fail Funnel state = %+v", listenFail)
	}
	stuck := byName["stuck"]
	if stuck.RuntimeState != runtimesnapshot.ServiceRuntimeFailed || stuck.Error == nil || stuck.Error.Code != registry.CodeServiceStartTimeout {
		t.Fatalf("stuck failure = %+v, want startup timeout", stuck)
	}
	healthy := byName["healthy"]
	if healthy.RuntimeState != runtimesnapshot.ServiceRuntimeRunning || healthy.FunnelRequested || healthy.FunnelActive || healthy.FunnelState != runtimesnapshot.FunnelStateNotRequested {
		t.Fatalf("healthy runtime state = %+v", healthy)
	}
}

func TestStartNodeLocked_VerifiesPreparedFunnelPolicyAndWaitsForNetmap(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}

	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	fake := &fakeTSNetServer{
		status:      funnelMissingStatus("public-app.tailnet.ts.net."),
		localClient: localapitest.NewClient(nil),
	}
	statusClient := &sequenceTSNetStatusClient{statuses: []*ipnstate.Status{
		funnelMissingStatus("public-app.tailnet.ts.net."),
		funnelEnabledStatus("public-app.tailnet.ts.net."),
	}}

	oldNew := newTSNetServerFn
	oldStatusClient := tsnetStatusClientFn
	oldPoll := funnelCapabilityPollInterval
	oldTimeout := funnelCapabilityWaitTimeout
	newTSNetServerFn = func(registry.Service, string, string, string) tsnetServer { return fake }
	tsnetStatusClientFn = func(tsnetServer) (tsnetStatusClient, error) { return statusClient, nil }
	funnelCapabilityPollInterval = time.Millisecond
	funnelCapabilityWaitTimeout = 100 * time.Millisecond
	t.Cleanup(func() {
		newTSNetServerFn = oldNew
		tsnetStatusClientFn = oldStatusClient
		funnelCapabilityPollInterval = oldPoll
		funnelCapabilityWaitTimeout = oldTimeout
		s.closeAllNodes()
	})

	s.SetEnsureFunnelAttrFn(func(context.Context, tailapi.FunnelPolicyRequest) (tailapi.PolicyMutationResult, error) {
		t.Fatal("policy mutation must run in the ensure phase before startNodeLocked")
		return tailapi.PolicyMutationResult{}, nil
	})

	svc := registry.Service{
		Name: "public-app", Type: registry.TypeProxy, Target: "http://localhost:3000",
		Tags: []string{"tag:public", registry.FunnelTag}, Funnel: true, PublicAck: true,
	}
	provision := registry.ProvisionOutcome{
		Attempted: true, Target: registry.FunnelTag, Changed: true,
		Reason: registry.ProvisionReasonPolicyUpdated, WriteOutcome: tailapi.PolicyWriteChanged,
	}
	if err := s.startNodeLocked(context.Background(), svc, provision); err != nil {
		t.Fatalf("startNodeLocked() error = %v", err)
	}
	if statusClient.calls != 2 {
		t.Fatalf("netmap Status() calls = %d, want missing then enabled", statusClient.calls)
	}
	if fake.listenFunnelCalled != 1 || !s.nodeRunning("public-app") {
		t.Fatalf("Funnel activation = listen calls:%d running:%v, want active listener", fake.listenFunnelCalled, s.nodeRunning("public-app"))
	}
}

func TestStartNodeLocked_RuntimeHostComesFromTheNetmapTheFunnelWaitObserved(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}

	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	// tsnet.Up returns as soon as the backend is Running, which can be before
	// the netmap carries this node's DNS name. The Funnel capability wait polls
	// a later netmap; the host it observed is the one the node must publish.
	upStatus := funnelMissingStatus("")
	fake := &fakeTSNetServer{status: upStatus, localClient: localapitest.NewClient(nil)}
	statusClient := &sequenceTSNetStatusClient{statuses: []*ipnstate.Status{
		funnelMissingStatus(""),
		funnelEnabledStatus("public-late.tailnet.ts.net."),
	}}

	oldNew := newTSNetServerFn
	oldStatusClient := tsnetStatusClientFn
	oldPoll := funnelCapabilityPollInterval
	oldTimeout := funnelCapabilityWaitTimeout
	newTSNetServerFn = func(registry.Service, string, string, string) tsnetServer { return fake }
	tsnetStatusClientFn = func(tsnetServer) (tsnetStatusClient, error) { return statusClient, nil }
	funnelCapabilityPollInterval = time.Millisecond
	funnelCapabilityWaitTimeout = 100 * time.Millisecond
	t.Cleanup(func() {
		newTSNetServerFn = oldNew
		tsnetStatusClientFn = oldStatusClient
		funnelCapabilityPollInterval = oldPoll
		funnelCapabilityWaitTimeout = oldTimeout
		s.closeAllNodes()
	})

	svc := registry.Service{
		Name: "public-late", Type: registry.TypeProxy, Target: "http://localhost:3000",
		Tags: []string{"tag:public", registry.FunnelTag}, Funnel: true, PublicAck: true,
	}
	provision := registry.ProvisionOutcome{
		Attempted: true, Target: registry.FunnelTag, Changed: true,
		Reason: registry.ProvisionReasonPolicyUpdated, WriteOutcome: tailapi.PolicyWriteChanged,
	}
	if err := s.startNodeLocked(context.Background(), svc, provision); err != nil {
		t.Fatalf("startNodeLocked() error = %v", err)
	}
	node := s.nodes["public-late"]
	if node == nil {
		t.Fatal("node was not registered")
	}
	if node.runtimeHost != "public-late.tailnet.ts.net" {
		t.Fatalf("runtimeHost = %q, want the host from the netmap the Funnel wait observed; deriving it from the tsnet.Up status leaves it empty and every consumer falls back to the <tailnet> placeholder", node.runtimeHost)
	}
	// A later netmap must never downgrade a host that was already known.
	if upStatus.Self.DNSName != "" {
		t.Fatalf("test setup drifted: the Up status must not carry a DNS name, got %q", upStatus.Self.DNSName)
	}
}

func TestStartNodeLocked_AutoProvisionTimeoutIsBoundedAndActionable(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}

	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	fake := &fakeTSNetServer{status: funnelMissingStatus("public-app.tailnet.ts.net.")}
	statusClient := &sequenceTSNetStatusClient{statuses: []*ipnstate.Status{funnelMissingStatus("public-app.tailnet.ts.net.")}}

	oldNew := newTSNetServerFn
	oldStatusClient := tsnetStatusClientFn
	oldPoll := funnelCapabilityPollInterval
	oldTimeout := funnelCapabilityWaitTimeout
	newTSNetServerFn = func(registry.Service, string, string, string) tsnetServer { return fake }
	tsnetStatusClientFn = func(tsnetServer) (tsnetStatusClient, error) { return statusClient, nil }
	funnelCapabilityPollInterval = 2 * time.Millisecond
	funnelCapabilityWaitTimeout = 15 * time.Millisecond
	t.Cleanup(func() {
		newTSNetServerFn = oldNew
		tsnetStatusClientFn = oldStatusClient
		funnelCapabilityPollInterval = oldPoll
		funnelCapabilityWaitTimeout = oldTimeout
	})
	started := time.Now()
	err = s.startNodeLocked(context.Background(), registry.Service{
		Name: "public-app", Type: registry.TypeProxy, Target: "http://localhost:3000",
		Tags: []string{"tag:public", registry.FunnelTag}, Funnel: true, PublicAck: true,
	}, registry.ProvisionOutcome{
		Attempted: true, Target: registry.FunnelTag, Changed: true,
		Reason: registry.ProvisionReasonPolicyUpdated, WriteOutcome: tailapi.PolicyWriteChanged,
	})
	if elapsed := time.Since(started); elapsed > 250*time.Millisecond {
		t.Fatalf("Funnel capability wait took %s, want bounded 15ms wait", elapsed)
	}
	if code, ok := registry.ErrorCode(err); !ok || code != registry.CodeFunnelCapabilityMissing {
		t.Fatalf("startNodeLocked() error = %v code=%q, want %s", err, code, registry.CodeFunnelCapabilityMissing)
	}
	var recovery interface{ NextCommands() []string }
	if !errors.As(err, &recovery) {
		t.Fatalf("error %T does not expose NextCommands", err)
	}
	next := strings.Join(recovery.NextCommands(), "\n")
	if !strings.Contains(err.Error(), "policy is prepared for \"tag:tslink-funnel\"") || !strings.Contains(err.Error(), "actual") {
		t.Fatalf("error = %q, want prepared-policy and actual-budget evidence", err)
	}
	if !strings.Contains(next, "policy is already prepared") || strings.Contains(next, "admin console") {
		t.Fatalf("next = %q, want wait/restart remedy without an ACL rewrite instruction", next)
	}
}

func TestStartNodeLocked_NoAutoProvisionSkipsMutationAndExplainsOptOut(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}
	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	fake := &fakeTSNetServer{status: funnelMissingStatus("public-app.tailnet.ts.net.")}
	oldNew := newTSNetServerFn
	newTSNetServerFn = func(registry.Service, string, string, string) tsnetServer { return fake }
	t.Cleanup(func() { newTSNetServerFn = oldNew })
	err = s.startNodeLocked(context.Background(), registry.Service{
		Name: "public-app", Type: registry.TypeProxy, Target: "http://localhost:3000",
		Tags: []string{"tag:public", registry.FunnelTag}, Funnel: true, PublicAck: true, NoAutoProvision: true,
	}, registry.ProvisionOutcome{Target: registry.FunnelTag, Reason: registry.ProvisionReasonServiceDisabled, WriteOutcome: tailapi.PolicyWriteUnchanged})
	var recovery interface{ NextCommands() []string }
	var coded registry.CodedError
	if !errors.As(err, &recovery) || !errors.As(err, &coded) || coded.Provision == nil || coded.Provision.Reason != registry.ProvisionReasonServiceDisabled {
		t.Fatalf("startNodeLocked() error = %v next=%v provision=%+v, want structured service opt-out evidence", err, recovery, coded.Provision)
	}
}

func TestEnsureFunnelPolicyBeforeRestart_FailurePreservesRecoveryAndCause(t *testing.T) {
	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	s.SetEnsureFunnelAttrFn(func(context.Context, tailapi.FunnelPolicyRequest) (tailapi.PolicyMutationResult, error) {
		return tailapi.PolicyMutationResult{WriteOutcome: tailapi.PolicyWriteRejected}, tailapi.ErrNoAPIClient
	})
	svc := registry.Service{
		Name: "public-app", Type: registry.TypeProxy, Target: "http://localhost:3000",
		Tags: []string{"tag:public", registry.FunnelTag}, Funnel: true, PublicAck: true,
	}
	failures, outcomes, called := s.ensureFunnelPolicyBeforeRestart(
		context.Background(), map[string]registry.Service{svc.Name: svc}, nil, svc.Tags,
	)
	if !called || len(failures) != 1 {
		t.Fatalf("called=%v failures=%+v, want isolated pre-start failure", called, failures)
	}
	got := outcomes[svc.Name]
	if !got.Attempted || got.Target != registry.FunnelTag || got.Reason != registry.ProvisionReasonEnsureFailed || got.WriteOutcome != tailapi.PolicyWriteRejected {
		t.Fatalf("outcome = %+v, want rejected structured provisioning result", got)
	}
	failure := failures[svc.Name]
	if failure.Error == nil || !strings.Contains(failure.Error.Message, tailapi.ErrNoAPIClient.Error()) || failure.Error.Provision == nil {
		t.Fatalf("failure = %+v, want preserved cause and provisioning object", failure)
	}
}

func TestEnsureFunnelPolicyBeforeRestart_FusesSharedTagAndDerivedOwners(t *testing.T) {
	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	services := map[string]registry.Service{
		"alpha": {
			Name: "alpha", Type: registry.TypeProxy, Target: "http://localhost:3000",
			Tags: []string{"tag:owner-a", registry.FunnelTag}, Funnel: true, PublicAck: true,
		},
		"beta": {
			Name: "beta", Type: registry.TypeProxy, Target: "http://localhost:3001",
			Tags: []string{"tag:owner-b", registry.FunnelTag}, Funnel: true, PublicAck: true,
		},
	}
	var calls int
	var captured tailapi.FunnelPolicyRequest
	s.SetEnsureFunnelAttrFn(func(ctx context.Context, request tailapi.FunnelPolicyRequest) (tailapi.PolicyMutationResult, error) {
		calls++
		captured = request
		return tailapi.PolicyMutationResult{WriteOutcome: tailapi.PolicyWriteUnchanged}, nil
	})
	failures, outcomes, fused := s.ensureFunnelPolicyBeforeRestart(
		context.Background(), services, nil,
		[]string{"tag:owner-a", registry.FunnelTag, "tag:owner-b"},
	)
	if !fused || calls != 1 || len(failures) != 0 {
		t.Fatalf("fused=%v calls=%d failures=%+v, want one fused transaction", fused, calls, failures)
	}
	if captured.Target != registry.FunnelTag || strings.Join(captured.Owners, ",") != "tag:owner-a,tag:owner-b" {
		t.Fatalf("request = %+v, want one shared target with service-derived owners", captured)
	}
	if strings.Join(captured.Tags, ",") != "tag:owner-a,tag:owner-b" {
		t.Fatalf("fused ordinary tags = %v", captured.Tags)
	}
	for _, name := range []string{"alpha", "beta"} {
		outcome := outcomes[name]
		if !outcome.Attempted || outcome.Target != registry.FunnelTag || outcome.Changed || outcome.Reason != registry.ProvisionReasonPolicySatisfied {
			t.Fatalf("%s outcome = %+v, want shared idempotent policy outcome", name, outcome)
		}
	}
}

func TestEnsureFunnelPolicyBeforeRestart_NoUsableOwnerIsStructuredAndDoesNotCallWriter(t *testing.T) {
	cases := []struct {
		name       string
		tags       []string
		wantReason string
	}{
		{name: "no existing owner tag", tags: []string{registry.FunnelTag}, wantReason: registry.ProvisionReasonNoUsableOwner},
		{name: "tagless service", tags: nil, wantReason: registry.ProvisionReasonNoUsableOwner},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, err := New("key", "")
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			s.SetEnsureFunnelAttrFn(func(context.Context, tailapi.FunnelPolicyRequest) (tailapi.PolicyMutationResult, error) {
				t.Fatal("writer called without a usable caller-derived owner")
				return tailapi.PolicyMutationResult{}, nil
			})
			svc := registry.Service{
				Name: "public-app", Type: registry.TypeProxy, Target: "http://localhost:3000",
				Tags: tc.tags, Funnel: true, PublicAck: true,
			}
			failures, outcomes, fused := s.ensureFunnelPolicyBeforeRestart(
				context.Background(), map[string]registry.Service{svc.Name: svc}, nil, svc.Tags,
			)
			if fused || len(failures) != 1 {
				t.Fatalf("fused=%v failures=%+v, want pre-start isolated owner failure", fused, failures)
			}
			outcome := outcomes[svc.Name]
			if outcome.Attempted || outcome.Reason != tc.wantReason || outcome.WriteOutcome != tailapi.PolicyWriteNotAttempted {
				t.Fatalf("outcome = %+v, want reason %s and no attempted write", outcome, tc.wantReason)
			}
			// deriveFunnelTagOwner skips the Funnel tag when hunting for an owner,
			// so telling an agent to add that tag cannot resolve this failure. The
			// recovery steps must name a tag the OAuth client already holds.
			failure := failures[svc.Name].Error
			if failure == nil || len(failure.Next) == 0 {
				t.Fatalf("failure = %+v, want recovery steps an agent can act on", failures[svc.Name])
			}
			for _, step := range failure.Next {
				if strings.Contains(step, registry.FunnelTag) {
					t.Fatalf("recovery step %q tells the agent to add the derived Funnel tag, which cannot fix a missing owner", step)
				}
			}
		})
	}
}

func TestSyncNodes_FunnelPolicyFailurePrecedesStopAndStateWipe(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}
	oldSvc := registry.Service{
		Name: "app", Type: registry.TypeProxy, Target: "http://localhost:3000", Tags: []string{"tag:tsmain"},
	}
	newSvc := oldSvc
	newSvc.Funnel = true
	newSvc.PublicAck = true
	newSvc.Tags = []string{"tag:tsmain", registry.FunnelTag}
	writeRegistry(t, []registry.Service{newSvc})

	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	oldNode := newNode(t, oldSvc)
	s.nodes[oldSvc.Name] = oldNode
	wipes := 0
	oldRemove := removeServiceStateDirFn
	removeServiceStateDirFn = func(name string) error {
		wipes++
		return nil
	}
	t.Cleanup(func() { removeServiceStateDirFn = oldRemove })
	oldNew := newTSNetServerFn
	newTSNetServerFn = func(registry.Service, string, string, string) tsnetServer {
		t.Fatal("replacement node started after failed policy preflight")
		return nil
	}
	t.Cleanup(func() { newTSNetServerFn = oldNew })
	s.SetEnsureFunnelAttrFn(func(context.Context, tailapi.FunnelPolicyRequest) (tailapi.PolicyMutationResult, error) {
		if oldNode.closed.Load() {
			t.Fatal("old node was stopped before policy preflight")
		}
		return tailapi.PolicyMutationResult{WriteOutcome: tailapi.PolicyWriteRejected}, errors.New("synthetic ACL rejection")
	})

	if err := s.syncNodes(context.Background()); err != nil {
		t.Fatalf("syncNodes() error = %v, want isolated Funnel failure", err)
	}
	if oldNode.closed.Load() || s.nodes[oldSvc.Name] != oldNode {
		t.Fatal("failed preflight did not preserve the existing node and identity")
	}
	if wipes != 0 {
		t.Fatalf("state wipe count = %d, want 0 before successful policy preflight", wipes)
	}
	failure := s.serviceFailures[oldSvc.Name]
	if failure.Error == nil || failure.Error.Provision == nil || failure.Error.Provision.Reason != registry.ProvisionReasonEnsureFailed {
		t.Fatalf("service failure = %+v, want structured preflight failure", failure)
	}
}

func TestEnsureFunnelPolicyBeforeRestart_DaemonKillSwitchWins(t *testing.T) {
	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	s.SetAutoProvisionFunnel(false)
	s.SetEnsureFunnelAttrFn(func(context.Context, tailapi.FunnelPolicyRequest) (tailapi.PolicyMutationResult, error) {
		t.Fatal("writer called while daemon kill switch was disabled")
		return tailapi.PolicyMutationResult{}, nil
	})
	svc := registry.Service{
		Name: "public-app", Type: registry.TypeProxy, Target: "http://localhost:3000",
		Tags: []string{"tag:tsmain", registry.FunnelTag}, Funnel: true, PublicAck: true,
	}
	failures, outcomes, fused := s.ensureFunnelPolicyBeforeRestart(
		context.Background(), map[string]registry.Service{svc.Name: svc}, nil, svc.Tags,
	)
	if fused || len(failures) != 0 {
		t.Fatalf("fused=%v failures=%+v, want disabled policy phase", fused, failures)
	}
	if outcome := outcomes[svc.Name]; outcome.Attempted || outcome.Reason != registry.ProvisionReasonDaemonDisabled {
		t.Fatalf("outcome = %+v, want daemon-disabled reason", outcome)
	}
}

func TestEnsureFunnelPolicyBeforeRestart_HTTPSDisabledIsStructuredWithoutPolicyWriteClaim(t *testing.T) {
	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	s.SetEnsureFunnelAttrFn(func(context.Context, tailapi.FunnelPolicyRequest) (tailapi.PolicyMutationResult, error) {
		return tailapi.PolicyMutationResult{WriteOutcome: tailapi.PolicyWriteNotAttempted}, tailapi.ErrTailnetHTTPSDisabled
	})
	svc := registry.Service{
		Name: "public-app", Type: registry.TypeProxy, Target: "http://localhost:3000",
		Tags: []string{"tag:tsmain", registry.FunnelTag}, Funnel: true, PublicAck: true,
	}
	failures, outcomes, tagsEnsured := s.ensureFunnelPolicyBeforeRestart(
		context.Background(), map[string]registry.Service{svc.Name: svc}, nil, svc.Tags,
	)
	if tagsEnsured || len(failures) != 1 {
		t.Fatalf("tagsEnsured=%v failures=%+v, want pre-policy HTTPS prerequisite failure", tagsEnsured, failures)
	}
	outcome := outcomes[svc.Name]
	if outcome.Reason != registry.ProvisionReasonHTTPSDisabled || outcome.WriteOutcome != tailapi.PolicyWriteNotAttempted || outcome.Changed {
		t.Fatalf("outcome = %+v, want HTTPS-disabled/no-policy-write", outcome)
	}
	next := strings.Join(failures[svc.Name].Error.Next, "\n")
	for _, want := range []string{"PATCH /api/v2/tailnet/{tailnet}/settings", "httpsEnabled", "networking_settings"} {
		if !strings.Contains(next, want) {
			t.Fatalf("next = %q, want %q", next, want)
		}
	}
}

func TestEnsureFunnelPolicyBeforeRestart_LogsMutationPlanOnlyForActualSetAttempt(t *testing.T) {
	cases := []struct {
		name      string
		result    tailapi.PolicyMutationResult
		err       error
		wantLog   bool
		wantFused bool
	}{
		{
			name: "HTTPS prerequisite stops before policy read", result: tailapi.PolicyMutationResult{WriteOutcome: tailapi.PolicyWriteNotAttempted},
			err: tailapi.ErrTailnetHTTPSDisabled,
		},
		{
			name: "idempotent policy is not a mutation attempt", result: tailapi.PolicyMutationResult{WriteOutcome: tailapi.PolicyWriteUnchanged},
			wantFused: true,
		},
		{
			name: "policy Set attempt is auditable", result: tailapi.PolicyMutationResult{Changed: true, WriteOutcome: tailapi.PolicyWriteChanged},
			wantLog: true, wantFused: true,
		},
		{
			name: "rejected policy Set attempt is auditable", result: tailapi.PolicyMutationResult{WriteOutcome: tailapi.PolicyWriteRejected},
			err: errors.New("synthetic policy rejection"), wantLog: true, wantFused: true,
		},
		{
			name: "unknown policy Set outcome is auditable", result: tailapi.PolicyMutationResult{WriteOutcome: tailapi.PolicyWriteUnknown},
			err: errors.New("synthetic transport loss"), wantLog: true, wantFused: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var logBuf bytes.Buffer
			oldLogger := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, nil)))
			t.Cleanup(func() { slog.SetDefault(oldLogger) })

			s, err := New("key", "")
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			s.SetEnsureFunnelAttrFn(func(context.Context, tailapi.FunnelPolicyRequest) (tailapi.PolicyMutationResult, error) {
				return tc.result, tc.err
			})
			svc := registry.Service{
				Name: "public-app", Type: registry.TypeProxy, Target: "http://localhost:3000",
				Tags: []string{"tag:tsmain"}, Funnel: true, PublicAck: true,
			}
			_, _, fused := s.ensureFunnelPolicyBeforeRestart(
				context.Background(), map[string]registry.Service{svc.Name: svc}, nil, svc.Tags,
			)
			if fused != tc.wantFused {
				t.Fatalf("fused tag ensure = %v, want %v", fused, tc.wantFused)
			}
			logged := strings.Contains(logBuf.String(), "remote ACL mutation plan")
			if logged != tc.wantLog {
				t.Fatalf("mutation-plan logged=%v want=%v; log=%q", logged, tc.wantLog, logBuf.String())
			}
		})
	}
}

func TestEnsureFunnelPolicyBeforeRestart_SettingsUnavailableIsStructured(t *testing.T) {
	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	s.SetEnsureFunnelAttrFn(func(context.Context, tailapi.FunnelPolicyRequest) (tailapi.PolicyMutationResult, error) {
		return tailapi.PolicyMutationResult{WriteOutcome: tailapi.PolicyWriteNotAttempted}, tailapi.ErrTailnetSettingsUnavailable
	})
	svc := registry.Service{
		Name: "public-app", Type: registry.TypeProxy, Target: "http://localhost:3000",
		Tags: []string{"tag:tsmain", registry.FunnelTag}, Funnel: true, PublicAck: true,
	}
	failures, outcomes, _ := s.ensureFunnelPolicyBeforeRestart(
		context.Background(), map[string]registry.Service{svc.Name: svc}, nil, svc.Tags,
	)
	if outcome := outcomes[svc.Name]; outcome.Reason != registry.ProvisionReasonSettingsUnavailable || outcome.WriteOutcome != tailapi.PolicyWriteNotAttempted {
		t.Fatalf("outcome = %+v, want settings_unavailable without policy write", outcome)
	}
	next := strings.Join(failures[svc.Name].Error.Next, "\n")
	if !strings.Contains(next, "networking_settings") || strings.Contains(next, "Access controls > Funnel") {
		t.Fatalf("next = %q, want settings scope remedy without ACL-console advice", next)
	}
}

func TestEnsureFunnelPolicyBeforeRestart_UnknownWriteOutcomeIsMachineReadable(t *testing.T) {
	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	s.SetEnsureFunnelAttrFn(func(context.Context, tailapi.FunnelPolicyRequest) (tailapi.PolicyMutationResult, error) {
		return tailapi.PolicyMutationResult{WriteOutcome: tailapi.PolicyWriteUnknown}, context.DeadlineExceeded
	})
	svc := registry.Service{
		Name: "public-app", Type: registry.TypeProxy, Target: "http://localhost:3000",
		Tags: []string{"tag:tsmain", registry.FunnelTag}, Funnel: true, PublicAck: true,
	}
	failures, outcomes, _ := s.ensureFunnelPolicyBeforeRestart(
		context.Background(), map[string]registry.Service{svc.Name: svc}, nil, svc.Tags,
	)
	outcome := outcomes[svc.Name]
	if outcome.Reason != registry.ProvisionReasonWriteUnknown || outcome.WriteOutcome != tailapi.PolicyWriteUnknown || outcome.Changed {
		t.Fatalf("outcome = %+v, want unknown server-side write outcome", outcome)
	}
	provision := failures[svc.Name].Error.Provision
	if provision == nil || *provision != outcome {
		t.Fatalf("failure provision = %+v, want %+v", provision, outcome)
	}
	if strings.Contains(strings.Join(failures[svc.Name].Error.Next, "\n"), "admin console") {
		t.Fatalf("unknown-write recovery incorrectly asks for another policy mutation: %+v", failures[svc.Name].Error.Next)
	}
}

func TestEnsureFunnelPolicyBeforeRestart_ReportsActualParentLimitedRequestBudget(t *testing.T) {
	oldTimeout := funnelCapabilityWaitTimeout
	funnelCapabilityWaitTimeout = 2 * time.Second
	t.Cleanup(func() { funnelCapabilityWaitTimeout = oldTimeout })
	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	s.SetEnsureFunnelAttrFn(func(ctx context.Context, _ tailapi.FunnelPolicyRequest) (tailapi.PolicyMutationResult, error) {
		<-ctx.Done()
		return tailapi.PolicyMutationResult{WriteOutcome: tailapi.PolicyWriteUnknown}, ctx.Err()
	})
	svc := registry.Service{
		Name: "public-app", Type: registry.TypeProxy, Target: "http://localhost:3000",
		Tags: []string{"tag:tsmain", registry.FunnelTag}, Funnel: true, PublicAck: true,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	started := time.Now()
	failures, _, _ := s.ensureFunnelPolicyBeforeRestart(ctx, map[string]registry.Service{svc.Name: svc}, nil, svc.Tags)
	if elapsed := time.Since(started); elapsed > 200*time.Millisecond {
		t.Fatalf("policy preflight elapsed = %s, want parent-limited budget", elapsed)
	}
	message := failures[svc.Name].Error.Message
	if !strings.Contains(message, "actual") || strings.Contains(message, "2s request budget") {
		t.Fatalf("policy failure message = %q, want actual parent-limited budget", message)
	}
}

func TestSetEnsureFunnelAttrFnNilRestoresDefault(t *testing.T) {
	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	s.SetEnsureFunnelAttrFn(func(context.Context, tailapi.FunnelPolicyRequest) (tailapi.PolicyMutationResult, error) {
		t.Fatal("stale injected writer called after nil reset")
		return tailapi.PolicyMutationResult{}, nil
	})
	s.SetEnsureFunnelAttrFn(nil)
	result, err := s.ensureFunnelAttrFn(context.Background(), tailapi.FunnelPolicyRequest{})
	if err == nil || result.WriteOutcome != tailapi.PolicyWriteNotAttempted {
		t.Fatalf("default writer result=%+v error=%v, want safe empty-target rejection", result, err)
	}
}

func TestVerifyFunnelAccessClassifiesNonPolicyPrerequisitesWithoutPolling(t *testing.T) {
	oldStatusClient := tsnetStatusClientFn
	tsnetStatusClientFn = func(tsnetServer) (tsnetStatusClient, error) {
		t.Fatal("status polling started for an immediately classified prerequisite")
		return nil, nil
	}
	t.Cleanup(func() { tsnetStatusClientFn = oldStatusClient })

	httpsMissing := funnelEnabledStatus("public-app.tailnet.ts.net.")
	delete(httpsMissing.Self.CapMap, tailcfg.CapabilityHTTPS)
	portMissing := funnelEnabledStatus("public-app.tailnet.ts.net.")
	delete(portMissing.Self.CapMap, tailcfg.CapabilityFunnelPorts+"?ports=443")
	cases := []struct {
		name       string
		status     *ipnstate.Status
		wantReason string
		wantNext   []string
	}{
		{
			name: "HTTPS is checked before node attr", status: httpsMissing,
			wantReason: registry.ProvisionReasonHTTPSDisabled,
			wantNext:   []string{"PATCH /api/v2/tailnet/{tailnet}/settings", "httpsEnabled", "networking_settings"},
		},
		{
			name: "unsupported port", status: portMissing,
			wantReason: registry.ProvisionReasonPortUnsupported,
			wantNext:   []string{"443, 8443, and 10000", "per node DNS name"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &Server{}
			svc := registry.Service{Name: "public-app", Funnel: true}
			provision := registry.ProvisionOutcome{
				Attempted: true, Target: registry.FunnelTag, Changed: true,
				Reason: registry.ProvisionReasonPolicyUpdated, WriteOutcome: tailapi.PolicyWriteChanged,
			}
			_, err := s.verifyFunnelAccess(context.Background(), &fakeTSNetServer{}, svc, tc.status, provision)
			var coded registry.CodedError
			if !errors.As(err, &coded) || coded.Provision == nil || coded.Provision.Reason != tc.wantReason {
				t.Fatalf("verifyFunnelAccess() error=%v provision=%+v, want reason %s", err, coded.Provision, tc.wantReason)
			}
			next := strings.Join(coded.Next, "\n")
			for _, want := range tc.wantNext {
				if !strings.Contains(next, want) {
					t.Fatalf("next = %q, want %q", next, want)
				}
			}
		})
	}
}

func TestVerifyFunnelAccessReportsActualParentLimitedBudget(t *testing.T) {
	oldStatusClient := tsnetStatusClientFn
	oldPoll := funnelCapabilityPollInterval
	oldTimeout := funnelCapabilityWaitTimeout
	tsnetStatusClientFn = func(tsnetServer) (tsnetStatusClient, error) {
		return &sequenceTSNetStatusClient{statuses: []*ipnstate.Status{funnelMissingStatus("public-app.tailnet.ts.net.")}}, nil
	}
	funnelCapabilityPollInterval = time.Millisecond
	funnelCapabilityWaitTimeout = 2 * time.Second
	t.Cleanup(func() {
		tsnetStatusClientFn = oldStatusClient
		funnelCapabilityPollInterval = oldPoll
		funnelCapabilityWaitTimeout = oldTimeout
	})

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := (&Server{}).verifyFunnelAccess(ctx, &fakeTSNetServer{}, registry.Service{Name: "public-app", Funnel: true},
		funnelMissingStatus("public-app.tailnet.ts.net."), registry.ProvisionOutcome{
			Attempted: true, Target: registry.FunnelTag, Reason: registry.ProvisionReasonPolicyUpdated, WriteOutcome: tailapi.PolicyWriteChanged,
		})
	if elapsed := time.Since(started); elapsed > 200*time.Millisecond {
		t.Fatalf("verifyFunnelAccess() elapsed = %s, want parent-limited wait", elapsed)
	}
	if err == nil || !strings.Contains(err.Error(), "actual") || strings.Contains(err.Error(), "2s wait budget") {
		t.Fatalf("verifyFunnelAccess() error = %v, want actual parent-limited budget instead of configured 2s", err)
	}
	var coded registry.CodedError
	if !errors.As(err, &coded) || coded.Provision == nil || coded.Provision.Reason != registry.ProvisionReasonNetmapTimeout {
		t.Fatalf("verifyFunnelAccess() provision = %+v, want netmap_timeout", coded.Provision)
	}
}

func TestSyncNodes_FunnelActiveRequiresSuccessfulFunnelListener(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}
	writeRegistry(t, []registry.Service{{
		Name: "public-ok", Type: registry.TypeProxy, Target: "http://localhost:3000", Tags: []string{"tag:tsmain"}, Funnel: true, PublicAck: true,
	}})

	fake := &fakeTSNetServer{
		status:      funnelEnabledStatus("public-ok.tailnet.ts.net."),
		localClient: localapitest.NewClient(nil),
		certDomains: []string{"public-ok.tailnet.ts.net"},
	}
	oldNew := newTSNetServerFn
	var constructorTags []string
	newTSNetServerFn = func(svc registry.Service, _ string, _ string, _ string) tsnetServer {
		constructorTags = append([]string(nil), svc.Tags...)
		return fake
	}
	t.Cleanup(func() { newTSNetServerFn = oldNew })

	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(s.closeAllNodes)
	var authProviderTags []string
	s.SetAuthKeyProvider(func(_ context.Context, svc registry.Service) (string, error) {
		authProviderTags = append([]string(nil), svc.Tags...)
		return "key", nil
	})
	s.SetEnsureFunnelAttrFn(func(context.Context, tailapi.FunnelPolicyRequest) (tailapi.PolicyMutationResult, error) {
		return tailapi.PolicyMutationResult{WriteOutcome: tailapi.PolicyWriteUnchanged}, nil
	})
	if err := s.syncNodes(context.Background()); err != nil {
		t.Fatalf("syncNodes() error = %v", err)
	}
	if fake.listenFunnelCalled != 1 {
		t.Fatalf("ListenFunnel calls = %d, want 1", fake.listenFunnelCalled)
	}
	wantEffectiveTags := "tag:tsmain," + registry.FunnelTag
	if got := strings.Join(authProviderTags, ","); got != wantEffectiveTags {
		t.Fatalf("auth provider tags = %q, want runtime-derived %q", got, wantEffectiveTags)
	}
	if got := strings.Join(constructorTags, ","); got != wantEffectiveTags {
		t.Fatalf("tsnet constructor tags = %q, want runtime-derived %q", got, wantEffectiveTags)
	}
	if node := s.nodes["public-ok"]; node == nil || !node.funnelListenerActive || node.runtimeHost != "public-ok.tailnet.ts.net" {
		t.Fatalf("running node = %+v, want Funnel listener and runtime host from Up status", node)
	} else if got := strings.Join(node.service.Tags, ","); got != "tag:tsmain" {
		t.Fatalf("stored node service tags = %q, want original registry tags without derived state", got)
	}

	snapshotPath, err := config.RuntimeSnapshotPath()
	if err != nil {
		t.Fatalf("RuntimeSnapshotPath() error = %v", err)
	}
	snapshot, err := runtimesnapshot.Load(snapshotPath)
	if err != nil {
		t.Fatalf("runtime Load() error = %v", err)
	}
	if len(snapshot.Services) != 1 || !snapshot.Services[0].FunnelActive || snapshot.Services[0].FunnelState != runtimesnapshot.FunnelStateActive {
		t.Fatalf("snapshot services = %+v, want active Funnel backed by successful listener", snapshot.Services)
	}
}

// A concurrent ACL editor makes the first fused write return 412 and the retry
// commit. The retry's bookkeeping is what tells the caller that (a) the tag
// ensure already happened, so the legacy path must not write again, and (b) a
// tailnet-wide mutation really committed, so the audit line must be emitted.
// Dropping either OR would leave a committed ACL mutation unlogged.
func TestSyncNodes_FunnelPolicyConflictRetryRecordsEnsureAndAudit(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}
	writeRegistry(t, []registry.Service{{
		Name: "public-retry", Type: registry.TypeProxy, Target: "http://localhost:3000", Tags: []string{"tag:tsmain"}, Funnel: true, PublicAck: true,
	}})

	fake := &fakeTSNetServer{
		status:      funnelEnabledStatus("public-retry.tailnet.ts.net."),
		localClient: localapitest.NewClient(nil),
		certDomains: []string{"public-retry.tailnet.ts.net"},
	}
	oldNew := newTSNetServerFn
	newTSNetServerFn = func(registry.Service, string, string, string) tsnetServer { return fake }
	t.Cleanup(func() { newTSNetServerFn = oldNew })

	var logBuf bytes.Buffer
	oldLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, nil)))
	t.Cleanup(func() { slog.SetDefault(oldLogger) })

	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(s.closeAllNodes)

	var ensureCalls int
	s.SetEnsureFunnelAttrFn(func(context.Context, tailapi.FunnelPolicyRequest) (tailapi.PolicyMutationResult, error) {
		ensureCalls++
		if ensureCalls == 1 {
			// The shape tailapi actually produces: acl.go returns
			// PolicyWriteRejected alongside ErrPolicyConflict, because a 412 means
			// the POST was sent and refused. Both flags are therefore already true
			// before the retry, which is what makes the accumulation load-bearing.
			return tailapi.PolicyMutationResult{WriteOutcome: tailapi.PolicyWriteRejected}, tailapi.ErrPolicyConflict
		}
		// The retry does not reach the write: a fresh policy read fails. Only the
		// first call's outcome records that a mutation reached the tailnet, so
		// overwriting instead of accumulating would lose the audit record.
		return tailapi.PolicyMutationResult{WriteOutcome: tailapi.PolicyWriteNotAttempted}, errors.New("fresh policy read failed")
	})
	var ensureTagsCalls int
	s.SetEnsureTagsFn(func(context.Context, []string) error {
		ensureTagsCalls++
		return nil
	})

	if err := s.syncNodes(context.Background()); err != nil {
		t.Fatalf("syncNodes() error = %v", err)
	}
	if ensureCalls != 2 {
		t.Fatalf("fused policy calls = %d, want one conflict plus one retry", ensureCalls)
	}
	if ensureTagsCalls != 0 {
		t.Fatalf("legacy EnsureTags calls = %d, want 0; the retry already ensured the tags", ensureTagsCalls)
	}
	if !strings.Contains(logBuf.String(), "remote ACL mutation plan") {
		t.Fatalf("committed ACL mutation was not audited after the conflict retry:\n%s", logBuf.String())
	}
}

func TestSyncNodes_HTTPSDisabledFallsBackToOrdinaryTagEnsureAndSkipsBlockedFunnelStart(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}
	writeRegistry(t, []registry.Service{
		{Name: "public", Type: registry.TypeProxy, Target: "http://localhost:3000", Tags: []string{"tag:tsmain"}, Funnel: true, PublicAck: true},
		{Name: "ordinary", Type: registry.TypeFile, Path: t.TempDir(), Tags: []string{"tag:brandnew"}},
	})

	ordinary := &fakeTSNetServer{certDomains: []string{"ordinary.tailnet.ts.net"}}
	oldNew := newTSNetServerFn
	newTSNetServerFn = func(svc registry.Service, _ string, _ string, _ string) tsnetServer {
		if svc.Name == "public" {
			t.Fatal("policy-blocked Funnel service reached node construction")
		}
		return ordinary
	}
	t.Cleanup(func() { newTSNetServerFn = oldNew })

	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(s.closeAllNodes)
	s.SetEnsureFunnelAttrFn(func(_ context.Context, request tailapi.FunnelPolicyRequest) (tailapi.PolicyMutationResult, error) {
		if got := strings.Join(request.Tags, ","); got != "tag:brandnew,tag:tsmain" {
			t.Fatalf("fused ordinary tags = %q, want both non-Funnel tags", got)
		}
		return tailapi.PolicyMutationResult{WriteOutcome: tailapi.PolicyWriteNotAttempted}, tailapi.ErrTailnetHTTPSDisabled
	})
	var ensureTagsCalls int
	var ensuredTags []string
	s.SetEnsureTagsFn(func(_ context.Context, tags []string) error {
		ensureTagsCalls++
		ensuredTags = append([]string(nil), tags...)
		return nil
	})

	if err := s.syncNodes(context.Background()); err != nil {
		t.Fatalf("syncNodes() error = %v, want isolated Funnel prerequisite failure", err)
	}
	if ensureTagsCalls != 1 || strings.Join(ensuredTags, ",") != "tag:brandnew,tag:tsmain" {
		t.Fatalf("ordinary EnsureTags calls=%d tags=%v, want one fallback without shared Funnel tag", ensureTagsCalls, ensuredTags)
	}
	if !s.nodeRunning("ordinary") {
		t.Fatal("ordinary service did not start after pre-policy Funnel failure")
	}
	if s.nodeRunning("public") {
		t.Fatal("policy-blocked Funnel service unexpectedly started")
	}
	failure := s.serviceFailures["public"]
	if failure.Error == nil || failure.Error.Provision == nil || failure.Error.Provision.Reason != registry.ProvisionReasonHTTPSDisabled {
		t.Fatalf("Funnel failure = %+v, want structured HTTPS prerequisite failure", failure)
	}
}

func TestSyncNodes_FunnelOptOutNeverSendsSharedTagToLegacyEnsureTags(t *testing.T) {
	cases := []struct {
		name          string
		daemonEnabled bool
		serviceOptOut bool
	}{
		{name: "daemon kill switch", daemonEnabled: false},
		{name: "service kill switch", daemonEnabled: true, serviceOptOut: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			testenv.SetHome(t, t.TempDir())
			if err := config.EnsureDir(); err != nil {
				t.Fatalf("EnsureDir() error = %v", err)
			}
			writeRegistry(t, []registry.Service{{
				Name: "public", Type: registry.TypeProxy, Target: "http://localhost:3000",
				Tags: []string{"tag:tsmain", registry.FunnelTag}, Funnel: true, PublicAck: true, NoAutoProvision: tc.serviceOptOut,
			}})

			fake := &fakeTSNetServer{
				status: funnelEnabledStatus("public.tailnet.ts.net."), localClient: localapitest.NewClient(nil),
				certDomains: []string{"public.tailnet.ts.net"},
			}
			oldNew := newTSNetServerFn
			newTSNetServerFn = func(registry.Service, string, string, string) tsnetServer { return fake }
			t.Cleanup(func() { newTSNetServerFn = oldNew })

			s, err := New("key", "")
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			t.Cleanup(s.closeAllNodes)
			s.SetAutoProvisionFunnel(tc.daemonEnabled)
			s.SetEnsureFunnelAttrFn(func(context.Context, tailapi.FunnelPolicyRequest) (tailapi.PolicyMutationResult, error) {
				t.Fatal("Funnel policy writer called despite explicit opt-out")
				return tailapi.PolicyMutationResult{}, nil
			})
			var ensuredTags []string
			s.SetEnsureTagsFn(func(_ context.Context, tags []string) error {
				ensuredTags = append([]string(nil), tags...)
				return nil
			})

			if err := s.syncNodes(context.Background()); err != nil {
				t.Fatalf("syncNodes() error = %v", err)
			}
			if got := strings.Join(ensuredTags, ","); got != "tag:tsmain" {
				t.Fatalf("legacy EnsureTags input = %q, want only ordinary owner tag", got)
			}
			if !s.nodeRunning("public") {
				t.Fatal("opted-out Funnel service did not start with pre-existing capability")
			}
		})
	}
}

func TestSyncNodes_PolicyAccessDeniedDegradesWithoutBlockingServices(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}
	writeRegistry(t, []registry.Service{{Name: "app", Type: registry.TypeFile, Path: t.TempDir(), Tags: []string{"tag:tsmain"}}})

	fake := &fakeTSNetServer{certDomains: []string{"app.tailnet.ts.net"}}
	oldNew := newTSNetServerFn
	newTSNetServerFn = func(registry.Service, string, string, string) tsnetServer { return fake }
	t.Cleanup(func() { newTSNetServerFn = oldNew })
	var logBuf bytes.Buffer
	oldLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, nil)))
	t.Cleanup(func() { slog.SetDefault(oldLogger) })

	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(s.closeAllNodes)
	s.SetEnsureTagsFn(func(context.Context, []string) error {
		return fmt.Errorf("synthetic OAuth scope failure: %w", tailapi.ErrPolicyAccessDenied)
	})

	if err := s.syncNodes(context.Background()); err != nil {
		t.Fatalf("syncNodes() error = %v, want forbidden policy access to degrade", err)
	}
	if !s.nodeRunning("app") {
		t.Fatal("policy access denial blocked service startup")
	}
	if got := logBuf.String(); !strings.Contains(got, "policy access was forbidden") || !strings.Contains(got, "degraded_mode=true") {
		t.Fatalf("degraded log = %q, want explicit forbidden policy classification", got)
	}
}

func TestSyncNodes_ListenerActivationTimeoutClosesAndUnblocksLaterService(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}
	writeRegistry(t, []registry.Service{
		{Name: "blocked-funnel", Type: registry.TypeProxy, Target: "http://localhost:3000", Tags: []string{"tag:tsmain", registry.FunnelTag}, Funnel: true, PublicAck: true},
		{Name: "healthy", Type: registry.TypeFile, Path: t.TempDir()},
	})

	blocked := &blockingListenerTSNetServer{
		fakeTSNetServer: fakeTSNetServer{status: funnelEnabledStatus("blocked-funnel.tailnet.ts.net."), localClient: localapitest.NewClient(nil)},
		listenStarted:   make(chan struct{}),
		unblock:         make(chan struct{}),
	}
	healthy := &fakeTSNetServer{certDomains: []string{"healthy.tailnet.ts.net"}}
	oldNew := newTSNetServerFn
	newTSNetServerFn = func(svc registry.Service, _ string, _ string, _ string) tsnetServer {
		if svc.Name == "blocked-funnel" {
			return blocked
		}
		return healthy
	}
	t.Cleanup(func() { newTSNetServerFn = oldNew })
	oldTimeout := nodeStartupTimeout
	nodeStartupTimeout = 20 * time.Millisecond
	t.Cleanup(func() { nodeStartupTimeout = oldTimeout })

	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(s.closeAllNodes)
	s.SetEnsureFunnelAttrFn(func(context.Context, tailapi.FunnelPolicyRequest) (tailapi.PolicyMutationResult, error) {
		return tailapi.PolicyMutationResult{WriteOutcome: tailapi.PolicyWriteUnchanged}, nil
	})
	started := time.Now()
	if err := s.syncNodes(context.Background()); err != nil {
		t.Fatalf("syncNodes() error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("listener-bounded sync took %s", elapsed)
	}
	select {
	case <-blocked.listenStarted:
	default:
		t.Fatal("test did not block inside ListenFunnel")
	}
	if got := blocked.closeCount.Load(); got != 1 {
		t.Fatalf("blocked listener Close calls = %d, want exactly 1", got)
	}
	if !s.nodeRunning("healthy") {
		t.Fatal("healthy service did not start after listener-stage timeout")
	}
	failure := s.serviceFailures["blocked-funnel"]
	if failure.Error == nil || failure.Error.Code != registry.CodeServiceStartTimeout {
		t.Fatalf("blocked listener failure = %+v, want %s", failure, registry.CodeServiceStartTimeout)
	}
}

func TestActivateListenerCancellationJoinsWorkerBeforeReturn(t *testing.T) {
	listenerCtx, cancel := context.WithCancel(context.Background())
	workerMayFinish := make(chan struct{})
	workerDone := make(chan struct{})
	closeResources := func() {
		cancel()
	}
	returned := make(chan struct{})
	go func() {
		defer close(returned)
		_, _, _ = activateListener(listenerCtx, context.Background(), closeResources, func() (net.Listener, error) {
			<-workerMayFinish
			close(workerDone)
			return nil, net.ErrClosed
		})
	}()

	cancel()
	select {
	case <-returned:
		t.Fatal("activateListener returned before the listener worker completed")
	case <-time.After(20 * time.Millisecond):
	}
	close(workerMayFinish)
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("activateListener did not return after the listener worker completed")
	}
	select {
	case <-workerDone:
	default:
		t.Fatal("listener worker was not joined before return")
	}
}

func TestActivateListenerCancellationClosesLateListener(t *testing.T) {
	listenerCtx, cancel := context.WithCancel(context.Background())
	late := &fakeListener{}
	listenerStarted := make(chan struct{})
	listenerMayReturn := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		_, _, err := activateListener(listenerCtx, context.Background(), func() {
			cancel()
			close(listenerMayReturn)
		}, func() (net.Listener, error) {
			close(listenerStarted)
			<-listenerMayReturn
			return late, nil
		})
		result <- err
	}()
	<-listenerStarted
	cancel()
	select {
	case <-result:
	case <-time.After(time.Second):
		t.Fatal("activateListener did not return after cancellation")
	}
	if !late.closed.Load() {
		t.Fatal("listener returned after cancellation was not closed")
	}
}

func TestLoadRegistryForRuntimeSettledRereadsAfterTransientFailure(t *testing.T) {
	oldLoad := registryLoadRuntimeFn
	oldDelay := registrySettleDelay
	t.Cleanup(func() {
		registryLoadRuntimeFn = oldLoad
		registrySettleDelay = oldDelay
	})
	registrySettleDelay = time.Millisecond
	calls := 0
	registryLoadRuntimeFn = func(string) (*registry.Registry, []registry.ServiceIssue, error) {
		calls++
		if calls == 1 {
			return nil, nil, io.ErrUnexpectedEOF
		}
		return &registry.Registry{SchemaVersion: registry.CurrentRegistrySchemaVersion, Services: []registry.Service{}}, nil, nil
	}

	reg, issues, err := loadRegistryForRuntimeSettled(context.Background(), "/isolated/registry.json")
	if err != nil || reg == nil || len(reg.Services) != 0 || len(issues) != 0 || calls != 2 {
		t.Fatalf("settled load = reg:%+v issues:%+v err:%v calls:%d, want successful second read", reg, issues, err, calls)
	}
}

func TestSyncNodes_MissingFilePathIsIsolatedFromHealthyServices(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}
	missingPath := filepath.Join(t.TempDir(), "removed")
	writeRegistry(t, []registry.Service{
		{Name: "gone", Type: registry.TypeFile, Path: missingPath},
		{Name: "healthy", Type: registry.TypeFile, Path: t.TempDir()},
	})

	var started []string
	oldNew := newTSNetServerFn
	newTSNetServerFn = func(svc registry.Service, stateDir, authKey, controlURL string) tsnetServer {
		started = append(started, svc.Name)
		return &fakeTSNetServer{certDomains: []string{svc.Name + ".tailnet.ts.net"}}
	}
	t.Cleanup(func() { newTSNetServerFn = oldNew })

	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(s.closeAllNodes)
	if err := s.syncNodes(context.Background()); err != nil {
		t.Fatalf("syncNodes() error = %v, want per-service validation isolation", err)
	}
	if strings.Join(started, ",") != "healthy" {
		t.Fatalf("started services = %v, want only healthy", started)
	}
	if _, ok := s.nodes["healthy"]; !ok {
		t.Fatal("healthy service did not start")
	}
	if _, ok := s.nodes["gone"]; ok {
		t.Fatal("missing-path service unexpectedly started")
	}

	snapshotPath, err := config.RuntimeSnapshotPath()
	if err != nil {
		t.Fatalf("RuntimeSnapshotPath() error = %v", err)
	}
	snapshot, err := runtimesnapshot.Load(snapshotPath)
	if err != nil {
		t.Fatalf("runtime Load() error = %v", err)
	}
	if snapshot.Partial || len(snapshot.Services) != 2 {
		t.Fatalf("snapshot = %+v, want exact state for failed and healthy services", snapshot)
	}
	byName := map[string]runtimesnapshot.ServiceSnapshot{}
	for _, service := range snapshot.Services {
		byName[service.Name] = service
	}
	gone := byName["gone"]
	if gone.RuntimeState != runtimesnapshot.ServiceRuntimeFailed || gone.Error == nil || gone.Error.Code != registry.CodePathNotFound || len(gone.Error.Next) == 0 {
		t.Fatalf("gone service = %+v, want actionable path_not_found failure", gone)
	}
	if healthy := byName["healthy"]; healthy.RuntimeState != runtimesnapshot.ServiceRuntimeRunning {
		t.Fatalf("healthy service = %+v, want running", healthy)
	}
}

func TestRecoverableServiceFailureIncludesFilePathValidationCodes(t *testing.T) {
	svc := registry.Service{Name: "files", Type: registry.TypeFile, Path: "/tmp/files"}
	for _, tc := range []struct {
		name string
		err  error
		code string
	}{
		{name: "missing", err: registry.PathNotFoundError(svc.Path), code: registry.CodePathNotFound},
		{name: "not-directory", err: registry.PathNotDirectoryError(svc.Path), code: registry.CodePathNotDirectory},
		{name: "not-accessible", err: registry.PathNotAccessibleError(svc.Path, errors.New("permission denied")), code: registry.CodePathNotAccessible},
	} {
		t.Run(tc.name, func(t *testing.T) {
			failure, ok := recoverableServiceFailure(svc, tc.err)
			if !ok || failure.RuntimeState != runtimesnapshot.ServiceRuntimeFailed || failure.Error == nil || failure.Error.Code != tc.code || len(failure.Error.Next) == 0 {
				t.Fatalf("recoverableServiceFailure() = %+v, %v; want actionable %s", failure, ok, tc.code)
			}
		})
	}
}

func TestSyncNodes_InvalidFunnelGuardrailsCloseExistingPublicListener(t *testing.T) {
	cases := []struct {
		name string
		svc  func(*testing.T) registry.Service
		code string
	}{
		{name: "allow", code: registry.CodeFunnelAllowConflict, svc: func(*testing.T) registry.Service {
			return registry.Service{Name: "public-app", Type: registry.TypeProxy, Target: "http://localhost:3000", Funnel: true, PublicAck: true, AllowedUsers: []string{"alice@example.com"}}
		}},
		{name: "control-url", code: registry.CodeFunnelControlURLConflict, svc: func(*testing.T) registry.Service {
			return registry.Service{Name: "public-app", Type: registry.TypeProxy, Target: "http://localhost:3000", Funnel: true, PublicAck: true, ControlURL: "https://control.example.com"}
		}},
		{name: "type", code: registry.CodeFunnelTypeConflict, svc: func(t *testing.T) registry.Service {
			return registry.Service{Name: "public-app", Type: registry.TypeFile, Path: t.TempDir(), Funnel: true, PublicAck: true}
		}},
		{name: "public-ack", code: registry.CodeFunnelPublicAckRequired, svc: func(*testing.T) registry.Service {
			return registry.Service{Name: "public-app", Type: registry.TypeProxy, Target: "http://localhost:3000", Funnel: true}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			testenv.SetHome(t, t.TempDir())
			if err := config.EnsureDir(); err != nil {
				t.Fatalf("EnsureDir() error = %v", err)
			}
			writeRegistry(t, []registry.Service{tc.svc(t)})
			s, err := New("key", "")
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			listener := &fakeListener{}
			s.nodes["public-app"] = &ServiceNode{
				service:              registry.Service{Name: "public-app", Type: registry.TypeProxy, Target: "http://localhost:3000", Funnel: true, PublicAck: true},
				funnelListenerActive: true,
				listener:             listener,
				cancel:               func() {},
			}
			oldNew := newTSNetServerFn
			newTSNetServerFn = func(registry.Service, string, string, string) tsnetServer {
				t.Fatal("invalid service reached tsnet construction")
				return nil
			}
			t.Cleanup(func() { newTSNetServerFn = oldNew })

			if err := s.syncNodes(context.Background()); err != nil {
				t.Fatalf("syncNodes() error = %v", err)
			}
			if !listener.closed.Load() || s.nodeRunning("public-app") {
				t.Fatalf("invalid %s reload kept old public listener/node", tc.name)
			}
			failure := s.serviceFailures["public-app"]
			if failure.Error == nil || failure.Error.Code != tc.code {
				t.Fatalf("failure = %+v, want %s", failure, tc.code)
			}
		})
	}
}

func TestSyncNodes_GlobalInvalidReloadClosesOnlyFunnelAndPreservesState(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}
	regPath := writeRegistry(t, nil)
	nodesDir, err := config.NodesDir()
	if err != nil {
		t.Fatalf("NodesDir() error = %v", err)
	}
	stateDir := filepath.Join(nodesDir, "public-app")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatalf("MkdirAll(state) error = %v", err)
	}
	marker := filepath.Join(stateDir, "state-marker")
	if err := os.WriteFile(marker, []byte("keep"), 0o600); err != nil {
		t.Fatalf("WriteFile(marker) error = %v", err)
	}

	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	publicListener := &fakeListener{}
	privateListener := &fakeListener{}
	s.nodes["public-app"] = &ServiceNode{service: registry.Service{Name: "public-app", Type: registry.TypeProxy, Funnel: true}, funnelListenerActive: true, listener: publicListener, cancel: func() {}}
	s.nodes["private-app"] = &ServiceNode{service: registry.Service{Name: "private-app", Type: registry.TypeProxy}, listener: privateListener, cancel: func() {}}
	if err := os.WriteFile(regPath, []byte("{invalid"), 0o600); err != nil {
		t.Fatalf("WriteFile(invalid) error = %v", err)
	}
	oldSettle := registrySettleDelay
	registrySettleDelay = time.Millisecond
	t.Cleanup(func() { registrySettleDelay = oldSettle })

	if err := s.syncNodes(context.Background()); err == nil {
		t.Fatal("syncNodes() error = nil, want global invalid reload error")
	}
	if !publicListener.closed.Load() || s.nodeRunning("public-app") {
		t.Fatal("global invalid reload kept active Funnel node")
	}
	if privateListener.closed.Load() || !s.nodeRunning("private-app") {
		t.Fatal("global invalid reload stopped private-only node")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("global invalid reload removed tsnet state marker: %v", err)
	}
	snapshotPath, err := config.RuntimeSnapshotPath()
	if err != nil {
		t.Fatalf("RuntimeSnapshotPath() error = %v", err)
	}
	snapshot, err := runtimesnapshot.Load(snapshotPath)
	if err != nil {
		t.Fatalf("runtime Load() error = %v", err)
	}
	if snapshot.GlobalError == nil || snapshot.GlobalError.Code != registry.CodeRegistryReloadInvalid || !snapshot.Partial {
		t.Fatalf("snapshot global evidence = %+v, partial=%v", snapshot.GlobalError, snapshot.Partial)
	}
	s.closeAllNodes()
}

func TestSyncNodes_RegistryAvailabilityDecisionMatrix(t *testing.T) {
	tests := []struct {
		name                 string
		registryData         *string
		wantError            bool
		wantPublicStopped    bool
		wantPrivateStopped   bool
		wantPublicStateGone  bool
		wantPrivateStateGone bool
	}{
		{name: "file-missing", wantPublicStopped: true, wantPrivateStopped: true, wantPublicStateGone: true, wantPrivateStateGone: true},
		{name: "zero-byte", registryData: ptrServerString(""), wantError: true, wantPublicStopped: true},
		{name: "partial-json", registryData: ptrServerString(`{"schema_version":1,"services":[`), wantError: true, wantPublicStopped: true},
		{name: "explicit-empty-services", registryData: ptrServerString(`{"schema_version":1,"services":[]}`), wantPublicStopped: true, wantPrivateStopped: true, wantPublicStateGone: true, wantPrivateStateGone: true},
		{name: "delete-one-service", registryData: ptrServerString(`{"schema_version":1,"services":[{"name":"private-app","type":"proxy","target":"http://localhost:3001"}]}`), wantPublicStopped: true, wantPublicStateGone: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			testenv.SetHome(t, t.TempDir())
			if err := config.EnsureDir(); err != nil {
				t.Fatal(err)
			}
			regPath, err := config.RegistryPath()
			if err != nil {
				t.Fatal(err)
			}
			if tc.registryData != nil {
				if err := os.WriteFile(regPath, []byte(*tc.registryData), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			nodesDir, err := config.NodesDir()
			if err != nil {
				t.Fatal(err)
			}
			markers := map[string]string{}
			for _, name := range []string{"public-app", "private-app"} {
				stateDir := filepath.Join(nodesDir, name)
				if err := os.MkdirAll(stateDir, 0o700); err != nil {
					t.Fatal(err)
				}
				markers[name] = filepath.Join(stateDir, "state-marker")
				if err := os.WriteFile(markers[name], []byte("keep"), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			s, err := New("key", "")
			if err != nil {
				t.Fatal(err)
			}
			publicListener := &fakeListener{}
			privateListener := &fakeListener{}
			s.nodes["public-app"] = &ServiceNode{
				service:              registry.Service{Name: "public-app", Type: registry.TypeProxy, Target: "http://localhost:3000", Funnel: true, PublicAck: true},
				funnelListenerActive: true,
				listener:             publicListener,
				cancel:               func() {},
			}
			s.nodes["private-app"] = &ServiceNode{
				service:  registry.Service{Name: "private-app", Type: registry.TypeProxy, Target: "http://localhost:3001"},
				listener: privateListener,
				cancel:   func() {},
			}
			oldSettle := registrySettleDelay
			registrySettleDelay = time.Millisecond
			t.Cleanup(func() {
				registrySettleDelay = oldSettle
				s.closeAllNodes()
			})

			syncErr := s.syncNodes(context.Background())
			if (syncErr != nil) != tc.wantError {
				t.Fatalf("syncNodes() error = %v, wantError=%v", syncErr, tc.wantError)
			}
			publicStopped := !s.nodeRunning("public-app") && publicListener.closed.Load()
			privateStopped := !s.nodeRunning("private-app") && privateListener.closed.Load()
			publicStateGone := fileMissing(markers["public-app"])
			privateStateGone := fileMissing(markers["private-app"])
			if publicStopped != tc.wantPublicStopped || privateStopped != tc.wantPrivateStopped || publicStateGone != tc.wantPublicStateGone || privateStateGone != tc.wantPrivateStateGone {
				t.Fatalf("node/state facts public_stopped=%v private_stopped=%v public_state_removed=%v private_state_removed=%v", publicStopped, privateStopped, publicStateGone, privateStateGone)
			}
			t.Logf("matrix input=%s node_stopped=public:%v,private:%v state_removed=public:%v,private:%v", tc.name, publicStopped, privateStopped, publicStateGone, privateStateGone)
		})
	}
}

func ptrServerString(value string) *string { return &value }

func fileMissing(path string) bool {
	_, err := os.Stat(path)
	return os.IsNotExist(err)
}

func TestSyncNodes_ClearsGlobalFailureAfterValidRegistryRecovery(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatal(err)
	}
	regPath := writeRegistry(t, nil)
	s, err := New("key", "")
	if err != nil {
		t.Fatal(err)
	}
	privateService := registry.Service{Name: "private-app", Type: registry.TypeProxy, Target: "http://localhost:3001"}
	s.nodes[privateService.Name] = &ServiceNode{service: privateService, listener: &fakeListener{}, cancel: func() {}}
	oldSettle := registrySettleDelay
	registrySettleDelay = time.Millisecond
	t.Cleanup(func() {
		registrySettleDelay = oldSettle
		s.closeAllNodes()
	})

	if err := os.WriteFile(regPath, []byte("{invalid"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.syncNodes(context.Background()); err == nil || s.globalFailure == nil {
		t.Fatalf("invalid sync error=%v globalFailure=%+v", err, s.globalFailure)
	}
	data, err := json.Marshal(registry.Registry{SchemaVersion: registry.CurrentRegistrySchemaVersion, Services: []registry.Service{privateService}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(regPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.syncNodes(context.Background()); err != nil {
		t.Fatalf("recovery syncNodes() error = %v", err)
	}
	if s.globalFailure != nil {
		t.Fatalf("globalFailure after recovery = %+v", s.globalFailure)
	}
	snapshotPath, err := config.RuntimeSnapshotPath()
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := runtimesnapshot.Load(snapshotPath)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.GlobalError != nil || snapshot.Partial {
		t.Fatalf("recovery snapshot global_error=%+v partial=%v", snapshot.GlobalError, snapshot.Partial)
	}
}

func TestSyncNodes_ClearsFailureAfterSameServiceRecovers(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}
	path := filepath.Join(t.TempDir(), "later")
	writeRegistry(t, []registry.Service{{Name: "files", Type: registry.TypeFile, Path: path}})
	fake := &fakeTSNetServer{certDomains: []string{"files.tailnet.ts.net"}}
	oldNew := newTSNetServerFn
	newTSNetServerFn = func(registry.Service, string, string, string) tsnetServer { return fake }
	t.Cleanup(func() { newTSNetServerFn = oldNew })
	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(s.closeAllNodes)
	if err := s.syncNodes(context.Background()); err != nil {
		t.Fatalf("first syncNodes() error = %v", err)
	}
	if failure := s.serviceFailures["files"]; failure.Error == nil || failure.Error.Code != registry.CodePathNotFound {
		t.Fatalf("first failure = %+v", failure)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatalf("Mkdir(recovery path) error = %v", err)
	}
	if err := s.syncNodes(context.Background()); err != nil {
		t.Fatalf("recovery syncNodes() error = %v", err)
	}
	if _, exists := s.serviceFailures["files"]; exists {
		t.Fatal("successful recovery retained stale service failure")
	}
	snapshotPath, _ := config.RuntimeSnapshotPath()
	snapshot, err := runtimesnapshot.Load(snapshotPath)
	if err != nil {
		t.Fatalf("runtime Load() error = %v", err)
	}
	if len(snapshot.Services) != 1 || snapshot.Services[0].RuntimeState != runtimesnapshot.ServiceRuntimeRunning || snapshot.Services[0].Error != nil {
		t.Fatalf("recovery snapshot = %+v", snapshot.Services)
	}
}

func TestSyncNodes_ValidationProgressSnapshotsRemainPartial(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}
	writeRegistry(t, []registry.Service{
		{Name: "invalid", Type: registry.TypeProxy},
		{Name: "healthy", Type: registry.TypeFile, Path: t.TempDir()},
	})
	oldNew := newTSNetServerFn
	newTSNetServerFn = func(registry.Service, string, string, string) tsnetServer { return &fakeTSNetServer{} }
	t.Cleanup(func() { newTSNetServerFn = oldNew })
	var snapshots []runtimesnapshot.Snapshot
	oldSave := runtimeSaveSnapshotFn
	runtimeSaveSnapshotFn = func(_ string, snapshot runtimesnapshot.Snapshot) error {
		snapshots = append(snapshots, snapshot)
		return nil
	}
	t.Cleanup(func() { runtimeSaveSnapshotFn = oldSave })
	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(s.closeAllNodes)
	if err := s.syncNodes(context.Background()); err != nil {
		t.Fatalf("syncNodes() error = %v", err)
	}
	if len(snapshots) < 3 {
		t.Fatalf("snapshots = %d, want validation partial, running partial, final complete", len(snapshots))
	}
	for i, snapshot := range snapshots[:len(snapshots)-1] {
		if !snapshot.Partial {
			t.Fatalf("snapshot[%d] Partial=false before authoritative final write", i)
		}
	}
	if snapshots[len(snapshots)-1].Partial {
		t.Fatal("final snapshot Partial=true, want authoritative complete")
	}
}

func TestSyncNodes_PartialStartFailureRemovesRuntimeSnapshot(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}

	oldNew := newTSNetServerFn
	newTSNetServerFn = func(svc registry.Service, stateDir, authKey, controlURL string) tsnetServer {
		if svc.Name == "bad" {
			return &fakeTSNetServer{upErr: errors.New("tsnet down")}
		}
		return &fakeTSNetServer{certDomains: []string{svc.Name + ".tailnet.ts.net"}}
	}
	t.Cleanup(func() { newTSNetServerFn = oldNew })

	writeRegistry(t, []registry.Service{
		{Name: "ok", Type: registry.TypeFile, Path: t.TempDir()},
	})
	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(s.closeAllNodes)
	if err := s.syncNodes(context.Background()); err != nil {
		t.Fatalf("initial syncNodes() error = %v", err)
	}
	snapshotPath, err := config.RuntimeSnapshotPath()
	if err != nil {
		t.Fatalf("RuntimeSnapshotPath() error = %v", err)
	}
	if _, err := runtimesnapshot.Load(snapshotPath); err != nil {
		t.Fatalf("initial runtime Load() error = %v", err)
	}

	regPath := writeRegistry(t, []registry.Service{
		{Name: "ok", Type: registry.TypeFile, Path: t.TempDir()},
		{Name: "bad", Type: registry.TypeFile, Path: t.TempDir()},
	})
	err = s.syncNodes(context.Background())
	if err == nil || !strings.Contains(err.Error(), `start service "bad"`) {
		t.Fatalf("syncNodes() error = %v, want bad service start error", err)
	}

	snapshot, loadErr := runtimesnapshot.Load(snapshotPath)
	reg, err := registry.Load(regPath)
	if err != nil {
		t.Fatalf("registry.Load() error = %v", err)
	}
	fingerprint, err := runtimesnapshot.RegistryFingerprint(reg)
	if err != nil {
		t.Fatalf("RegistryFingerprint() error = %v", err)
	}
	freshness := runtimesnapshot.Classify(snapshot, loadErr, runtimesnapshot.ExpectedRuntime{
		DaemonPID:                  s.daemonPID,
		DaemonStartedAtLowerBound:  s.daemonStartedAt,
		CurrentRegistryFingerprint: fingerprint,
	})
	if freshness.Exact {
		t.Fatalf("freshness = %+v, partial start failure must not leave an exact current snapshot", freshness)
	}
	if freshness.Status != runtimesnapshot.StatusMissing {
		t.Fatalf("freshness = %+v, want fail-closed missing snapshot after partial start failure", freshness)
	}
	if _, exists := s.nodes["ok"]; !exists {
		t.Fatal("already-running service should remain running after partial failure")
	}
	if _, exists := s.nodes["bad"]; exists {
		t.Fatal("failed service should not be in running node map")
	}
}

func TestSyncNodes_UpdatesRuntimeSnapshotFingerprintAfterSuccessfulReload(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}

	oldNew := newTSNetServerFn
	newTSNetServerFn = func(svc registry.Service, stateDir, authKey, controlURL string) tsnetServer {
		return &fakeTSNetServer{certDomains: []string{svc.Name + ".tailnet.ts.net"}}
	}
	t.Cleanup(func() { newTSNetServerFn = oldNew })

	writeRegistry(t, []registry.Service{
		{Name: "files", Type: registry.TypeFile, Path: t.TempDir()},
	})
	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(s.closeAllNodes)
	if err := s.syncNodes(context.Background()); err != nil {
		t.Fatalf("initial syncNodes() error = %v", err)
	}
	snapshotPath, err := config.RuntimeSnapshotPath()
	if err != nil {
		t.Fatalf("RuntimeSnapshotPath() error = %v", err)
	}
	initial, err := runtimesnapshot.Load(snapshotPath)
	if err != nil {
		t.Fatalf("initial runtime Load() error = %v", err)
	}

	regPath := writeRegistry(t, []registry.Service{
		{Name: "files", Type: registry.TypeFile, Path: t.TempDir()},
	})
	if err := s.syncNodes(context.Background()); err != nil {
		t.Fatalf("reload syncNodes() error = %v", err)
	}
	reloaded, err := runtimesnapshot.Load(snapshotPath)
	if err != nil {
		t.Fatalf("reloaded runtime Load() error = %v", err)
	}
	reg, err := registry.Load(regPath)
	if err != nil {
		t.Fatalf("registry.Load() error = %v", err)
	}
	wantFingerprint, err := runtimesnapshot.RegistryFingerprint(reg)
	if err != nil {
		t.Fatalf("RegistryFingerprint() error = %v", err)
	}
	if reloaded.RegistryFingerprint != wantFingerprint {
		t.Fatalf("registry_fingerprint = %q, want %q", reloaded.RegistryFingerprint, wantFingerprint)
	}
	if reloaded.RegistryFingerprint == initial.RegistryFingerprint {
		t.Fatalf("registry_fingerprint did not change after successful reload: %q", reloaded.RegistryFingerprint)
	}
}

func TestSyncNodes_UpdatesRuntimeSnapshotAfterServiceStops(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}
	writeRegistry(t, []registry.Service{})

	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	s.nodes["old"] = &ServiceNode{
		service:  registry.Service{Name: "old", Type: registry.TypeFile, Path: t.TempDir()},
		tsnetSrv: &fakeTSNetServer{},
		cancel:   func() {},
	}

	if err := s.syncNodes(context.Background()); err != nil {
		t.Fatalf("syncNodes() error = %v", err)
	}
	snapshotPath, err := config.RuntimeSnapshotPath()
	if err != nil {
		t.Fatalf("RuntimeSnapshotPath() error = %v", err)
	}
	snapshot, err := runtimesnapshot.Load(snapshotPath)
	if err != nil {
		t.Fatalf("runtime Load() error = %v", err)
	}
	if len(snapshot.Services) != 0 {
		t.Fatalf("snapshot services = %+v, want empty after service stop", snapshot.Services)
	}
}

func TestCloseAllNodes_RemovesRuntimeSnapshot(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}
	snapshotPath, err := config.RuntimeSnapshotPath()
	if err != nil {
		t.Fatalf("RuntimeSnapshotPath() error = %v", err)
	}
	if err := runtimesnapshot.Save(snapshotPath, runtimesnapshot.NewSnapshot(os.Getpid(), time.Now(), "sha256:test", time.Now(), nil)); err != nil {
		t.Fatalf("runtime Save() error = %v", err)
	}

	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	s.closeAllNodes()

	if _, err := os.Stat(snapshotPath); !os.IsNotExist(err) {
		t.Fatalf("runtime snapshot should be removed on close, stat err = %v", err)
	}
}

func TestSyncNodes_RuntimeSnapshotWriteFailureLoggedNonFatal(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}
	writeRegistry(t, []registry.Service{
		{Name: "files", Type: registry.TypeFile, Path: t.TempDir()},
	})

	oldNew := newTSNetServerFn
	newTSNetServerFn = func(svc registry.Service, stateDir, authKey, controlURL string) tsnetServer {
		return &fakeTSNetServer{certDomains: []string{"files.tailnet.ts.net"}}
	}
	t.Cleanup(func() { newTSNetServerFn = oldNew })

	oldSave := runtimeSaveSnapshotFn
	runtimeSaveSnapshotFn = func(path string, snapshot runtimesnapshot.Snapshot) error {
		return errors.New("disk full")
	}
	t.Cleanup(func() { runtimeSaveSnapshotFn = oldSave })

	var logBuf bytes.Buffer
	oldLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, nil)))
	t.Cleanup(func() { slog.SetDefault(oldLogger) })

	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(s.closeAllNodes)
	err = s.syncNodes(context.Background())
	if err != nil {
		t.Fatalf("syncNodes() error = %v, want snapshot write failure to be non-fatal", err)
	}
	if _, exists := s.nodes["files"]; !exists {
		t.Fatal("service should remain running after snapshot write failure")
	}
	if !strings.Contains(logBuf.String(), "runtime snapshot write failed") || !strings.Contains(logBuf.String(), "disk full") {
		t.Fatalf("logs = %s, want runtime snapshot warning", logBuf.String())
	}
}

func TestSyncNodes_HotReloadUsesFreshPerServiceAuthMaterial(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}

	oldNew := newTSNetServerFn
	var constructedKeys []string
	newTSNetServerFn = func(svc registry.Service, stateDir, authKey, controlURL string) tsnetServer {
		constructedKeys = append(constructedKeys, authKey)
		return &fakeTSNetServer{}
	}
	t.Cleanup(func() { newTSNetServerFn = oldNew })

	s, err := New("static-key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(s.closeAllNodes)

	var providerCalls []registry.Service
	s.SetAuthKeyProvider(func(ctx context.Context, svc registry.Service) (string, error) {
		providerCalls = append(providerCalls, svc)
		return fmt.Sprintf("key-%d", len(providerCalls)), nil
	})
	var cleanupNames []string
	var cleanupTags []string
	s.SetCleanupStaleNodesFn(func(ctx context.Context, targets []tailapi.CleanupTarget) (tailapi.CleanupResult, error) {
		for _, target := range targets {
			cleanupNames = append(cleanupNames, target.Hostname)
			cleanupTags = append(cleanupTags, target.Tags...)
		}
		return tailapi.CleanupResult{Matched: cleanupNames, Deleted: cleanupNames}, nil
	})

	writeRegistry(t, []registry.Service{
		{Name: "app", Type: registry.TypeFile, Path: t.TempDir(), Tags: []string{"tag:one"}},
	})
	if err := s.syncNodes(context.Background()); err != nil {
		t.Fatalf("first syncNodes() error = %v", err)
	}

	nodesDir, err := config.NodesDir()
	if err != nil {
		t.Fatalf("NodesDir() error = %v", err)
	}
	marker := filepath.Join(nodesDir, "app", "stale-state")
	if err := os.WriteFile(marker, []byte("old identity"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	writeRegistry(t, []registry.Service{
		{Name: "app", Type: registry.TypeFile, Path: t.TempDir(), Tags: []string{"tag:two"}, Ephemeral: true},
	})
	if err := s.syncNodes(context.Background()); err != nil {
		t.Fatalf("second syncNodes() error = %v", err)
	}

	if len(providerCalls) != 2 {
		t.Fatalf("provider calls = %d, want 2 fresh calls", len(providerCalls))
	}
	if providerCalls[0].Tags[0] != "tag:one" {
		t.Fatalf("first provider tags = %v, want [tag:one]", providerCalls[0].Tags)
	}
	if providerCalls[1].Tags[0] != "tag:two" || !providerCalls[1].Ephemeral {
		t.Fatalf("second provider service = %+v, want tag:two ephemeral=true", providerCalls[1])
	}
	if strings.Join(constructedKeys, ",") != "key-1,key-2" {
		t.Fatalf("constructed auth keys = %v, want fresh per sync", constructedKeys)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("expected stale state marker to be removed, stat err = %v", err)
	}
	if strings.Join(cleanupNames, ",") != "app" {
		t.Fatalf("cleanup names = %v, want [app]", cleanupNames)
	}
	if strings.Join(cleanupTags, ",") != "tag:one" {
		t.Fatalf("cleanup tags = %v, want old service tag [tag:one]", cleanupTags)
	}
}

func TestSyncNodes_CleanupFailureStillRestartsChangedService(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}

	oldNew := newTSNetServerFn
	var started []string
	newTSNetServerFn = func(svc registry.Service, stateDir, authKey, controlURL string) tsnetServer {
		started = append(started, svc.Name)
		return &fakeTSNetServer{}
	}
	t.Cleanup(func() { newTSNetServerFn = oldNew })

	oldSvc := registry.Service{Name: "app", Type: registry.TypeFile, Path: t.TempDir(), Tags: []string{"tag:old"}}
	newSvc := registry.Service{Name: "app", Type: registry.TypeFile, Path: t.TempDir(), Tags: []string{"tag:new"}}
	writeRegistry(t, []registry.Service{newSvc})

	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(s.closeAllNodes)
	oldNode := newNode(t, oldSvc)
	s.nodes["app"] = oldNode
	nodesDir, err := config.NodesDir()
	if err != nil {
		t.Fatal(err)
	}
	stateDir := filepath.Join(nodesDir, "app")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(stateDir, "old-state")
	if err := os.WriteFile(marker, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	s.SetCleanupStaleNodesFn(func(ctx context.Context, targets []tailapi.CleanupTarget) (tailapi.CleanupResult, error) {
		if _, err := os.Stat(marker); !os.IsNotExist(err) {
			t.Fatalf("remote cleanup ran before old local state was absent: %v", err)
		}
		return tailapi.CleanupResult{}, errors.New("tailnet cleanup down")
	})

	err = s.syncNodes(context.Background())
	if err == nil || !strings.Contains(err.Error(), "cleanup stale tailnet nodes before auth identity restart") {
		t.Fatalf("syncNodes() error = %v, want cleanup error", err)
	}
	if !oldNode.closed.Load() {
		t.Fatal("old node should be stopped before restart")
	}
	if strings.Join(started, ",") != "app" {
		t.Fatalf("started = %v, want [app]", started)
	}
	if got := s.nodes["app"].service.Tags; len(got) != 1 || got[0] != "tag:new" {
		t.Fatalf("running service tags = %v, want [tag:new]", got)
	}
}

func TestSyncNodes_StateRemovalFailureBlocksChangedIdentityRestart(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}

	oldNew := newTSNetServerFn
	var started []string
	newTSNetServerFn = func(svc registry.Service, stateDir, authKey, controlURL string) tsnetServer {
		started = append(started, svc.Name)
		return &fakeTSNetServer{}
	}
	t.Cleanup(func() { newTSNetServerFn = oldNew })

	oldRemove := removeServiceStateDirFn
	removeServiceStateDirFn = func(name string) error {
		return errors.New("permission denied")
	}
	t.Cleanup(func() { removeServiceStateDirFn = oldRemove })

	oldSvc := registry.Service{Name: "app", Type: registry.TypeFile, Path: t.TempDir(), Tags: []string{"tag:old"}}
	newSvc := registry.Service{Name: "app", Type: registry.TypeFile, Path: t.TempDir(), Tags: []string{"tag:new"}}
	writeRegistry(t, []registry.Service{newSvc})

	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	oldNode := newNode(t, oldSvc)
	s.nodes["app"] = oldNode

	err = s.syncNodes(context.Background())
	if err == nil || !strings.Contains(err.Error(), "remove state for auth identity change") {
		t.Fatalf("syncNodes() error = %v, want state removal error", err)
	}
	if !oldNode.closed.Load() {
		t.Fatal("old node should be stopped before restart")
	}
	if len(started) != 0 {
		t.Fatalf("started = %v, want no new node while old state remains", started)
	}
	if _, running := s.nodes["app"]; running {
		t.Fatal("changed identity reused old enrolled state after removal failed")
	}
}

func TestSyncNodes_UnchangedService(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}

	svc := registry.Service{
		Name:   "svc",
		Type:   registry.TypeProxy,
		Target: "http://localhost:3000",
	}
	writeRegistry(t, []registry.Service{svc})

	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	node := newNode(t, svc)
	s.nodes["svc"] = node

	if err := s.syncNodes(context.Background()); err != nil {
		t.Fatalf("syncNodes() error = %v", err)
	}

	if got := s.nodes["svc"]; got != node {
		t.Fatal("unchanged service should retain existing node")
	}
	if node.closed.Load() {
		t.Fatal("unchanged node should not be closed")
	}
}

func TestSyncNodes_ChangedService_RemovesOldNodeWhenRestartFails(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}

	oldSvc := registry.Service{
		Name:   "svc",
		Type:   registry.TypeProxy,
		Target: "http://localhost:3000",
	}
	newSvc := oldSvc
	newSvc.Target = "http://localhost:4000"
	writeRegistry(t, []registry.Service{newSvc})

	nodesDir, err := config.NodesDir()
	if err != nil {
		t.Fatalf("NodesDir() error = %v", err)
	}
	if err := os.RemoveAll(nodesDir); err != nil {
		t.Fatalf("RemoveAll() error = %v", err)
	}
	if err := os.WriteFile(nodesDir, []byte("not-a-directory"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	node := newNode(t, oldSvc)
	s.nodes["svc"] = node

	if err := s.syncNodes(context.Background()); err == nil {
		t.Fatal("syncNodes() error = nil, want restart failure")
	}

	if _, exists := s.nodes["svc"]; exists {
		t.Fatal("changed service should not keep old node when restart fails")
	}
	if !node.closed.Load() {
		t.Fatal("old node should be closed on restart")
	}
}

func TestSyncNodes_LoadError(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}

	regPath, err := config.RegistryPath()
	if err != nil {
		t.Fatalf("RegistryPath() error = %v", err)
	}
	if err := os.WriteFile(regPath, []byte("{invalid json"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if err := s.syncNodes(context.Background()); err == nil {
		t.Fatal("syncNodes() error = nil, want error")
	}
}

func TestWatchRegistry_MissingConfigDirReturns(t *testing.T) {
	testenv.SetHome(t, t.TempDir())

	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	done := make(chan struct{})
	go func() {
		s.watchRegistry(context.Background())
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("watchRegistry() did not return for missing config dir")
	}
}

func TestWatchRegistry_ReactsToCreate(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}

	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	s.nodes["stale"] = newNode(t, registry.Service{Name: "stale"})

	nodesDir, err := config.NodesDir()
	if err != nil {
		t.Fatalf("NodesDir() error = %v", err)
	}
	stateDir := filepath.Join(nodesDir, "stale")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	startRegistryWatcherTest(t, s)

	time.Sleep(150 * time.Millisecond)
	writeRegistry(t, []registry.Service{})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.RLock()
		_, exists := s.nodes["stale"]
		s.mu.RUnlock()
		if !exists {
			if _, err := os.Stat(stateDir); !os.IsNotExist(err) {
				t.Fatalf("expected %q to be removed, stat err = %v", stateDir, err)
			}
			return
		}
		time.Sleep(25 * time.Millisecond)
	}

	t.Fatal("watchRegistry() did not react to registry create event")
}

func TestRun(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}
	writeRegistry(t, nil)

	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	if err := s.Run(ctx); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestRun_InitialSyncFailureReturnsError(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}

	regPath, err := config.RegistryPath()
	if err != nil {
		t.Fatalf("RegistryPath() error = %v", err)
	}
	if err := os.WriteFile(regPath, []byte("{invalid json"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if err := s.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "initial sync failed") {
		t.Fatalf("Run() error = %v, want initial sync failure", err)
	}
}

func TestSyncNodesHandlesMalformedPersistedServicesBeforeTSNetSideEffects(t *testing.T) {
	fileAsPath := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(fileAsPath, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	missingPath := filepath.Join(t.TempDir(), "missing")

	cases := []struct {
		service         registry.Service
		recoverableCode string
	}{
		{service: registry.Service{Name: "unknown", Type: "websocket", Target: "http://localhost:3000"}},
		{service: registry.Service{Name: "file-empty", Type: registry.TypeFile}},
		{service: registry.Service{Name: "file-relative", Type: registry.TypeFile, Path: "relative"}, recoverableCode: registry.CodePathMustBeAbsolute},
		{service: registry.Service{Name: "file-missing", Type: registry.TypeFile, Path: missingPath}, recoverableCode: registry.CodePathNotFound},
		{service: registry.Service{Name: "file-notdir", Type: registry.TypeFile, Path: fileAsPath}, recoverableCode: registry.CodePathNotDirectory},
		{service: registry.Service{Name: "proxy-hostless", Type: registry.TypeProxy, Target: "https:///app"}},
		{service: registry.Service{Name: "proxy-relative", Type: registry.TypeProxy, Target: "localhost:3000"}},
		{service: registry.Service{Name: "proxy-unsupported", Type: registry.TypeProxy, Target: "ftp://example.com"}},
		{service: registry.Service{Name: "tcp-hostless", Type: registry.TypeTCP, Target: ":5432", Port: 5432}},
		{service: registry.Service{Name: "tcp-nonnum", Type: registry.TypeTCP, Target: "localhost:abc"}},
		{service: registry.Service{Name: "domain", Type: registry.TypeProxy, Target: "http://localhost:3000", Domain: "app.example.com"}, recoverableCode: registry.CodeFeatureUnavailable},
	}

	for _, tc := range cases {
		t.Run(tc.service.Name, func(t *testing.T) {
			testenv.SetHome(t, t.TempDir())
			if err := config.EnsureDir(); err != nil {
				t.Fatalf("EnsureDir() error = %v", err)
			}
			writeRegistry(t, []registry.Service{tc.service})

			var constructed atomic.Int32
			oldNew := newTSNetServerFn
			newTSNetServerFn = func(svc registry.Service, stateDir, authKey, controlURL string) tsnetServer {
				constructed.Add(1)
				return &fakeTSNetServer{}
			}
			t.Cleanup(func() { newTSNetServerFn = oldNew })

			s, err := New("key", "")
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			syncErr := s.syncNodes(context.Background())
			if syncErr != nil {
				t.Fatalf("syncNodes() error = %v, want isolated service failure", syncErr)
			}
			wantCode := tc.recoverableCode
			if wantCode == "" {
				wantCode = registry.CodeInvalidServiceConfig
			}
			failure, ok := s.serviceFailures[tc.service.Name]
			if !ok || failure.Error == nil || failure.Error.Code != wantCode {
				t.Fatalf("service failure = %+v, want code %q", failure, wantCode)
			}
			if got := constructed.Load(); got != 0 {
				t.Fatalf("tsnet constructions = %d, want 0", got)
			}
			if len(s.nodes) != 0 {
				t.Fatalf("running nodes = %d, want 0", len(s.nodes))
			}
		})
	}
}

func TestRunWatcherReadyBeforeInitialSyncAddConvergesWithoutLaterEvent(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}
	regPath, err := config.RegistryPath()
	if err != nil {
		t.Fatalf("RegistryPath() error = %v", err)
	}

	svc := registry.Service{Name: "added", Type: registry.TypeFile, Path: t.TempDir()}
	oldBefore := beforeInitialSyncFn
	beforeInitialSyncFn = func(context.Context) error {
		_, err := registry.Add(regPath, svc)
		return err
	}
	t.Cleanup(func() { beforeInitialSyncFn = oldBefore })

	var constructed atomic.Int32
	oldNew := newTSNetServerFn
	newTSNetServerFn = func(svc registry.Service, stateDir, authKey, controlURL string) tsnetServer {
		constructed.Add(1)
		return &fakeTSNetServer{certDomains: []string{svc.Name + ".tailnet.ts.net"}}
	}
	t.Cleanup(func() { newTSNetServerFn = oldNew })

	ctx, cancel := context.WithCancel(context.Background())
	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	var snapshotServices []runtimesnapshot.ServiceSnapshot
	s.SetReadyFunc(func() error {
		snapshotPath, err := runtimeSnapshotPathFn()
		if err != nil {
			return err
		}
		snapshot, err := runtimesnapshot.Load(snapshotPath)
		if err != nil {
			return err
		}
		snapshotServices = append([]runtimesnapshot.ServiceSnapshot(nil), snapshot.Services...)
		cancel()
		return nil
	})

	if err := s.Run(ctx); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := constructed.Load(); got != 1 {
		t.Fatalf("tsnet constructions = %d, want 1", got)
	}

	if len(snapshotServices) != 1 || snapshotServices[0].Name != "added" {
		t.Fatalf("snapshot services = %+v, want only added", snapshotServices)
	}
}

func TestRunMarksReadyWithZeroServicesAfterAuthoritativeSync(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}
	writeRegistry(t, nil)

	var constructed atomic.Int32
	oldNew := newTSNetServerFn
	newTSNetServerFn = func(svc registry.Service, stateDir, authKey, controlURL string) tsnetServer {
		constructed.Add(1)
		return &fakeTSNetServer{}
	}
	t.Cleanup(func() { newTSNetServerFn = oldNew })

	ctx, cancel := context.WithCancel(context.Background())
	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	readyCalled := false
	s.SetReadyFunc(func() error {
		readyCalled = true
		snapshotPath, err := runtimeSnapshotPathFn()
		if err != nil {
			return err
		}
		snapshot, err := runtimesnapshot.Load(snapshotPath)
		if err != nil {
			return err
		}
		if len(snapshot.Services) != 0 {
			return fmt.Errorf("snapshot services = %+v, want empty", snapshot.Services)
		}
		cancel()
		return nil
	})

	if err := s.Run(ctx); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !readyCalled {
		t.Fatal("ready callback was not called")
	}
	if got := constructed.Load(); got != 0 {
		t.Fatalf("tsnet constructions = %d, want 0", got)
	}
}

func TestRunFailsClosedOnPreSyncAndReadyErrors(t *testing.T) {
	for _, tc := range []struct {
		name       string
		beforeErr  error
		readyErr   error
		wantPrefix string
	}{
		{name: "before-initial-sync", beforeErr: errors.New("before failed"), wantPrefix: "before initial sync"},
		{name: "ready", readyErr: errors.New("ready failed"), wantPrefix: "mark ready"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testenv.SetHome(t, t.TempDir())
			if err := config.EnsureDir(); err != nil {
				t.Fatalf("EnsureDir() error = %v", err)
			}
			writeRegistry(t, nil)
			oldBefore := beforeInitialSyncFn
			beforeInitialSyncFn = func(context.Context) error { return tc.beforeErr }
			t.Cleanup(func() { beforeInitialSyncFn = oldBefore })
			s, err := New("key", "")
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			if tc.readyErr != nil {
				s.SetReadyFunc(func() error { return tc.readyErr })
			}
			err = s.Run(context.Background())
			if err == nil || !strings.Contains(err.Error(), tc.wantPrefix) {
				t.Fatalf("Run() error = %v, want %q", err, tc.wantPrefix)
			}
			if !s.shuttingDown.Load() {
				t.Fatal("Run() failure did not enter shutdown state")
			}
		})
	}
}

func TestRunDoesNotMarkReadyWhenSupersededInitialSyncFails(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}

	oldReg := &registry.Registry{SchemaVersion: registry.CurrentRegistrySchemaVersion, Services: []registry.Service{{
		Name: "old", Type: registry.TypeFile, Path: t.TempDir(),
	}}}
	newReg := &registry.Registry{SchemaVersion: registry.CurrentRegistrySchemaVersion, Services: []registry.Service{{
		Name: "new", Type: registry.TypeFile, Path: t.TempDir(),
	}}}

	var loadCount atomic.Int32
	oldLoad := registryLoadRuntimeFn
	registryLoadRuntimeFn = func(string) (*registry.Registry, []registry.ServiceIssue, error) {
		if loadCount.Add(1) == 1 {
			return oldReg, nil, nil
		}
		return newReg, nil, nil
	}
	t.Cleanup(func() { registryLoadRuntimeFn = oldLoad })

	firstDesiredLoaded := make(chan struct{})
	releaseFirst := make(chan struct{})
	newerFailure := errors.New("newer authoritative sync failed")
	oldAfterDesired := afterDesiredLoadedFn
	afterDesiredLoadedFn = func(ctx context.Context, generation uint64) error {
		if generation == 1 {
			close(firstDesiredLoaded)
			select {
			case <-releaseFirst:
			case <-ctx.Done():
				return ctx.Err()
			}
			return nil
		}
		return newerFailure
	}
	t.Cleanup(func() { afterDesiredLoadedFn = oldAfterDesired })

	oldNew := newTSNetServerFn
	newTSNetServerFn = func(svc registry.Service, stateDir, authKey, controlURL string) tsnetServer {
		return &fakeTSNetServer{certDomains: []string{svc.Name + ".tailnet.ts.net"}}
	}
	t.Cleanup(func() { newTSNetServerFn = oldNew })

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	readyCalled := false
	s.SetReadyFunc(func() error {
		readyCalled = true
		cancel()
		return nil
	})

	runDone := make(chan error, 1)
	go func() { runDone <- s.Run(ctx) }()
	select {
	case <-firstDesiredLoaded:
	case err := <-runDone:
		t.Fatalf("Run() returned before initial generation was superseded: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("initial generation did not reach desired-state barrier")
	}

	if err := s.syncNodes(context.Background()); !errors.Is(err, newerFailure) {
		t.Fatalf("superseding syncNodes() error = %v, want newer failure", err)
	}
	close(releaseFirst)

	select {
	case err := <-runDone:
		if err == nil || !strings.Contains(err.Error(), "initial sync failed") || !strings.Contains(err.Error(), newerFailure.Error()) {
			t.Fatalf("Run() error = %v, want failing authoritative sync", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run() did not fail closed after superseded initial sync")
	}
	if readyCalled {
		t.Fatal("ready callback was called while a newer authoritative sync failed")
	}
}

func TestRunWatcherReadyBeforeInitialSyncRemoveConvergesWithoutLaterEvent(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}
	regPath, err := config.RegistryPath()
	if err != nil {
		t.Fatalf("RegistryPath() error = %v", err)
	}
	if _, err := registry.Add(regPath, registry.Service{Name: "removed", Type: registry.TypeFile, Path: t.TempDir()}); err != nil {
		t.Fatalf("registry.Add() error = %v", err)
	}

	oldBefore := beforeInitialSyncFn
	beforeInitialSyncFn = func(context.Context) error {
		_, err := registry.Remove(regPath, "removed")
		return err
	}
	t.Cleanup(func() { beforeInitialSyncFn = oldBefore })

	var constructed atomic.Int32
	oldNew := newTSNetServerFn
	newTSNetServerFn = func(svc registry.Service, stateDir, authKey, controlURL string) tsnetServer {
		constructed.Add(1)
		return &fakeTSNetServer{}
	}
	t.Cleanup(func() { newTSNetServerFn = oldNew })

	ctx, cancel := context.WithCancel(context.Background())
	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	var snapshotServices []runtimesnapshot.ServiceSnapshot
	s.SetReadyFunc(func() error {
		snapshotPath, err := runtimeSnapshotPathFn()
		if err != nil {
			return err
		}
		snapshot, err := runtimesnapshot.Load(snapshotPath)
		if err != nil {
			return err
		}
		snapshotServices = append([]runtimesnapshot.ServiceSnapshot(nil), snapshot.Services...)
		cancel()
		return nil
	})

	if err := s.Run(ctx); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := constructed.Load(); got != 0 {
		t.Fatalf("tsnet constructions = %d, want 0", got)
	}

	if len(snapshotServices) != 0 {
		t.Fatalf("snapshot services = %+v, want empty", snapshotServices)
	}
}

func TestRunWatcherAddFailureReturnsStartupError(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}

	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	s.cfgDir = filepath.Join(t.TempDir(), "missing")

	err = s.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "registry watcher setup failed") {
		t.Fatalf("Run() error = %v, want watcher setup failure", err)
	}
}

func TestRunDoesNotMarkReadyOnListenerFailure(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}
	writeRegistry(t, []registry.Service{{
		Name:   "db",
		Type:   registry.TypeTCP,
		Target: "localhost:5432",
		Port:   5432,
	}})

	oldNew := newTSNetServerFn
	newTSNetServerFn = func(svc registry.Service, stateDir, authKey, controlURL string) tsnetServer {
		return &fakeTSNetServer{listenErr: errors.New("listen denied")}
	}
	t.Cleanup(func() { newTSNetServerFn = oldNew })

	readyCalled := false
	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	s.SetReadyFunc(func() error {
		readyCalled = true
		return nil
	})

	err = s.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), `start service "db"`) {
		t.Fatalf("Run() error = %v, want listener startup failure", err)
	}
	if readyCalled {
		t.Fatal("ready callback was called despite listener failure")
	}
}

func TestRun_InitialStartFailureClosesStartedNodes(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}

	writeRegistry(t, []registry.Service{
		{Name: "ok", Type: registry.TypeFile, Path: t.TempDir()},
		{Name: "bad", Type: registry.TypeFile, Path: t.TempDir()},
	})

	var okServer *fakeTSNetServer
	oldNew := newTSNetServerFn
	newTSNetServerFn = func(svc registry.Service, stateDir, authKey, controlURL string) tsnetServer {
		fake := &fakeTSNetServer{}
		if svc.Name == "ok" {
			okServer = fake
		}
		if svc.Name == "bad" {
			fake.upErr = errors.New("up failed")
		}
		return fake
	}
	t.Cleanup(func() { newTSNetServerFn = oldNew })

	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	err = s.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), `start service "bad"`) {
		t.Fatalf("Run() error = %v, want bad service start failure", err)
	}
	if okServer == nil {
		t.Fatal("expected ok service to start before bad service failed")
	}
	if !okServer.closed {
		t.Fatal("started node should be closed before Run returns")
	}
	if len(s.nodes) != 0 {
		t.Fatalf("nodes remaining after failed initial sync = %d, want 0", len(s.nodes))
	}
}

func TestStopNodeLocked_NonexistentNode(t *testing.T) {
	testenv.SetHome(t, t.TempDir())

	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	// Should be a no-op, not panic
	s.stopNodeLocked("nonexistent", false)
	s.stopNodeLocked("nonexistent", true)
}

func TestStopNodeLocked_NilListenerAndServer(t *testing.T) {
	testenv.SetHome(t, t.TempDir())

	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	s.nodes["test"] = &ServiceNode{
		service:  registry.Service{Name: "test"},
		listener: nil,
		tsnetSrv: nil,
		cancel:   func() {},
	}

	s.stopNodeLocked("test", false)

	if _, exists := s.nodes["test"]; exists {
		t.Error("node should be removed after stop")
	}
}

func TestSyncNodes_NewServiceStartFailure(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}

	// Register a proxy service but block NodesDir so startNodeLocked fails
	writeRegistry(t, []registry.Service{
		{Name: "newnode", Type: registry.TypeProxy, Target: "http://localhost:3000"},
	})

	nodesDir, err := config.NodesDir()
	if err != nil {
		t.Fatalf("NodesDir() error = %v", err)
	}
	if err := os.RemoveAll(nodesDir); err != nil {
		t.Fatalf("RemoveAll() error = %v", err)
	}
	if err := os.WriteFile(nodesDir, []byte("not-a-directory"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if err := s.syncNodes(context.Background()); err == nil {
		t.Fatal("syncNodes() error = nil, want start failure")
	}

	if _, exists := s.nodes["newnode"]; exists {
		t.Fatal("failed node should not be in map")
	}
}

func TestSyncNodes_AuthKeyProviderFailureReturnsError(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}

	writeRegistry(t, []registry.Service{
		{Name: "newnode", Type: registry.TypeFile, Path: t.TempDir(), Tags: []string{"tag:svc"}},
	})

	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	s.SetAuthKeyProvider(func(ctx context.Context, svc registry.Service) (string, error) {
		return "", errors.New("provider down")
	})

	err = s.syncNodes(context.Background())
	if err == nil || !strings.Contains(err.Error(), `auth key for service "newnode"`) {
		t.Fatalf("syncNodes() error = %v, want auth provider failure with service context", err)
	}
	if _, exists := s.nodes["newnode"]; exists {
		t.Fatal("failed node should not be in map")
	}
}

func TestCloseAllNodes_Empty(t *testing.T) {
	testenv.SetHome(t, t.TempDir())

	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	// Should not panic on empty map
	s.closeAllNodes()

	if len(s.nodes) != 0 {
		t.Fatalf("expected 0 nodes, got %d", len(s.nodes))
	}
}

func TestServiceChanged_Port(t *testing.T) {
	base := registry.Service{
		Name:   "a",
		Type:   registry.TypeProxy,
		Target: "http://localhost:3000",
		Port:   443,
	}

	changed := base
	changed.Port = 8443
	if !serviceChanged(base, changed) {
		t.Error("different port should be changed")
	}
}

func TestServiceChanged_Ephemeral(t *testing.T) {
	base := registry.Service{
		Name:   "a",
		Type:   registry.TypeProxy,
		Target: "http://localhost:3000",
	}

	changed := base
	changed.Ephemeral = true
	if !serviceChanged(base, changed) {
		t.Error("different ephemeral should be changed")
	}
}

func TestServiceChanged_ControlURL(t *testing.T) {
	base := registry.Service{
		Name:   "a",
		Type:   registry.TypeProxy,
		Target: "http://localhost:3000",
	}

	changed := base
	changed.ControlURL = "https://headscale.example.com"
	if !serviceChanged(base, changed) {
		t.Error("different controlURL should be changed")
	}
}

func TestServiceChangedWithFallback_ControlURL(t *testing.T) {
	base := registry.Service{
		Name:   "a",
		Type:   registry.TypeProxy,
		Target: "http://localhost:3000",
	}

	sameEffective := base
	sameEffective.ControlURL = "https://control.example.com"
	if serviceChangedWithFallback(base, sameEffective, "https://control.example.com") {
		t.Fatal("same effective control URL should not be changed")
	}

	differentEffective := base
	differentEffective.ControlURL = "https://headscale.example.com"
	if !serviceChangedWithFallback(base, differentEffective, "https://control.example.com") {
		t.Fatal("different effective control URL should be changed")
	}
}

func TestAuthIdentityChanged_EffectiveControlURL(t *testing.T) {
	testenv.SetHome(t, t.TempDir())

	s, err := New("key", "https://control.example.com")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	base := registry.Service{
		Name:       "a",
		Type:       registry.TypeProxy,
		Target:     "http://localhost:3000",
		Tags:       []string{"tag:web"},
		ControlURL: "",
	}

	sameEffective := base
	sameEffective.ControlURL = "https://control.example.com"
	if s.authIdentityChanged(base, sameEffective) {
		t.Fatal("same effective control URL should not be an auth identity change")
	}

	differentEffective := base
	differentEffective.ControlURL = "https://headscale.example.com"
	if !s.authIdentityChanged(base, differentEffective) {
		t.Fatal("different effective control URL should be an auth identity change")
	}
}

func TestServiceChanged_Tags_DifferentLength(t *testing.T) {
	base := registry.Service{
		Name:   "a",
		Type:   registry.TypeProxy,
		Target: "http://localhost:3000",
		Tags:   []string{"tag:web"},
	}

	changed := base
	changed.Tags = []string{"tag:web", "tag:prod"}
	if !serviceChanged(base, changed) {
		t.Error("different tag length should be changed")
	}
}

func TestServiceChanged_Tags_DifferentContent(t *testing.T) {
	base := registry.Service{
		Name:   "a",
		Type:   registry.TypeProxy,
		Target: "http://localhost:3000",
		Tags:   []string{"tag:web"},
	}

	changed := base
	changed.Tags = []string{"tag:api"}
	if !serviceChanged(base, changed) {
		t.Error("different tag content should be changed")
	}
}

func TestServiceChanged_TagsSameSetDifferentOrder(t *testing.T) {
	base := registry.Service{
		Name:   "a",
		Type:   registry.TypeProxy,
		Target: "http://localhost:3000",
		Tags:   []string{"tag:web", "tag:prod"},
	}

	changed := base
	changed.Tags = []string{"tag:prod", "tag:web"}
	if serviceChanged(base, changed) {
		t.Error("same tag set in different order should not be changed")
	}
}

func TestServiceChanged_Funnel(t *testing.T) {
	base := registry.Service{
		Name:   "a",
		Type:   registry.TypeProxy,
		Target: "http://localhost:3000",
	}

	changed := base
	changed.Funnel = true
	if !serviceChanged(base, changed) {
		t.Error("different funnel should be changed")
	}
}

func TestServiceChanged_Funnel_SameValue(t *testing.T) {
	base := registry.Service{
		Name:   "a",
		Type:   registry.TypeProxy,
		Target: "http://localhost:3000",
		Funnel: true,
	}

	if serviceChanged(base, base) {
		t.Error("identical services with funnel=true should not be changed")
	}
}

func TestServiceChanged_AllowedUsers_DifferentLength(t *testing.T) {
	base := registry.Service{
		Name:         "a",
		Type:         registry.TypeProxy,
		Target:       "http://localhost:3000",
		AllowedUsers: []string{"alice@example.com"},
	}

	changed := base
	changed.AllowedUsers = []string{"alice@example.com", "bob@example.com"}
	if !serviceChanged(base, changed) {
		t.Error("different AllowedUsers length should be changed")
	}
}

func TestServiceChanged_AllowedUsers_DifferentContent(t *testing.T) {
	base := registry.Service{
		Name:         "a",
		Type:         registry.TypeProxy,
		Target:       "http://localhost:3000",
		AllowedUsers: []string{"alice@example.com"},
	}

	changed := base
	changed.AllowedUsers = []string{"bob@example.com"}
	if !serviceChanged(base, changed) {
		t.Error("different AllowedUsers content should be changed")
	}
}

func TestServiceChanged_AllowedUsersSameSetDifferentOrder(t *testing.T) {
	base := registry.Service{
		Name:         "a",
		Type:         registry.TypeProxy,
		Target:       "http://localhost:3000",
		AllowedUsers: []string{"alice@example.com", "bob@example.com"},
	}

	changed := base
	changed.AllowedUsers = []string{"bob@example.com", "alice@example.com"}
	if serviceChanged(base, changed) {
		t.Error("same AllowedUsers set in different order should not be changed")
	}
}

func TestServiceChanged_Domain(t *testing.T) {
	base := registry.Service{
		Name:   "a",
		Type:   registry.TypeProxy,
		Target: "http://localhost:3000",
	}

	changed := base
	changed.Domain = "app.example.com"
	if !serviceChanged(base, changed) {
		t.Error("different domain should be changed")
	}
}

func TestServiceChanged_NoAutoProvision(t *testing.T) {
	base := registry.Service{Name: "a", Type: registry.TypeProxy, Target: "http://localhost:3000", Funnel: true, PublicAck: true}
	changed := base
	changed.NoAutoProvision = true
	if !serviceChanged(base, changed) {
		t.Error("different no_auto_provision should restart the service")
	}
}

func TestWatchRegistry_ContextCancelled(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}

	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		s.watchRegistry(ctx)
		close(done)
	}()

	// Give the watcher time to start
	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("watchRegistry() did not return after context cancellation")
	}
}

func TestWatchRegistry_BadCfgDir(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}

	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	// Point cfgDir to a non-existent directory so watcher.Add fails
	s.cfgDir = "/nonexistent/path/that/does/not/exist"

	done := make(chan struct{})
	go func() {
		s.watchRegistry(context.Background())
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("watchRegistry() did not return for bad cfgDir")
	}
}

func TestSyncNodes_FunnelChange_TriggersRestart(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}

	oldSvc := registry.Service{
		Name:   "svc",
		Type:   registry.TypeProxy,
		Target: "http://localhost:3000",
		Tags:   []string{"tag:tsmain"},
		Funnel: false,
	}
	newSvc := oldSvc
	newSvc.Funnel = true
	newSvc.PublicAck = true
	newSvc.Tags = []string{"tag:tsmain", registry.FunnelTag}
	writeRegistry(t, []registry.Service{newSvc})

	// Block NodesDir so startNodeLocked fails (we just want to verify the old node gets stopped)
	nodesDir, err := config.NodesDir()
	if err != nil {
		t.Fatalf("NodesDir() error = %v", err)
	}
	if err := os.RemoveAll(nodesDir); err != nil {
		t.Fatalf("RemoveAll() error = %v", err)
	}
	if err := os.WriteFile(nodesDir, []byte("not-a-directory"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	node := newNode(t, oldSvc)
	s.nodes["svc"] = node
	s.SetEnsureFunnelAttrFn(func(context.Context, tailapi.FunnelPolicyRequest) (tailapi.PolicyMutationResult, error) {
		return tailapi.PolicyMutationResult{WriteOutcome: tailapi.PolicyWriteUnchanged}, nil
	})

	if err := s.syncNodes(context.Background()); err == nil {
		t.Fatal("syncNodes() error = nil, want restart failure")
	}

	if !node.closed.Load() {
		t.Fatal("old node should be closed when funnel field changes")
	}
}

func TestNew_ConfigDirError(t *testing.T) {
	stubConfigDirError(t)

	_, err := New("dummy-authkey", "")
	if err == nil {
		t.Fatal("expected error from New when config.Dir fails")
	}
}

func TestSyncNodes_RegistryPathError(t *testing.T) {
	// Create a valid server first, then break its isolated config path.
	testenv.SetHome(t, t.TempDir())

	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	stubRegistryPathError(t)

	if err := s.syncNodes(context.Background()); err == nil {
		t.Fatal("expected error from syncNodes when RegistryPath fails")
	}
}

func TestWatchRegistry_RegistryPathError(t *testing.T) {
	// Create a server with a valid isolated config path, then break it.
	tmpHome := t.TempDir()
	testenv.SetHome(t, tmpHome)

	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	stubRegistryPathError(t)

	done := make(chan struct{})
	go func() {
		s.watchRegistry(context.Background())
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("watchRegistry() did not return when RegistryPath fails")
	}
}

func TestWatchRegistry_IgnoresNonRegistryFile(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}

	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	// Add a node that should NOT be removed by non-registry file changes
	svc := registry.Service{Name: "keep", Type: registry.TypeProxy, Target: "http://localhost:3000"}
	s.nodes["keep"] = newNode(t, svc)

	// Write a valid registry that includes the service
	writeRegistry(t, []registry.Service{svc})

	startRegistryWatcherTest(t, s)

	// Give the watcher time to start
	time.Sleep(150 * time.Millisecond)

	// Write a non-registry file in the config dir — should trigger the "continue" branch
	cfgDir, err := config.Dir()
	if err != nil {
		t.Fatalf("config.Dir() error = %v", err)
	}
	nonRegFile := filepath.Join(cfgDir, "unrelated.tmp")
	if err := os.WriteFile(nonRegFile, []byte("noise"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	// Give the watcher time to process the event
	time.Sleep(200 * time.Millisecond)

	// The "keep" node should still be present (no sync triggered)
	s.mu.RLock()
	_, exists := s.nodes["keep"]
	s.mu.RUnlock()
	if !exists {
		t.Fatal("node should still exist after non-registry file change")
	}
}

func TestWatchRegistry_SyncErrorOnReload(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}

	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	stopWatcher := startRegistryWatcherTest(t, s)

	// Give the watcher time to start
	time.Sleep(150 * time.Millisecond)

	// Write invalid JSON to registry — triggers syncNodes which returns Load error
	regPath, err := config.RegistryPath()
	if err != nil {
		t.Fatalf("RegistryPath() error = %v", err)
	}
	if err := os.WriteFile(regPath, []byte("{invalid json"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	// Give the watcher time to process the event and log the error
	time.Sleep(200 * time.Millisecond)

	// The watcher should still be running (not crashed) — cancel and verify it exits
	stopWatcher()

	// If we reach here without panic, the error path was handled gracefully
}

func TestSetEnsureTagsFn(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}

	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	called := false
	s.SetEnsureTagsFn(func(ctx context.Context, tags []string) error {
		called = true
		return nil
	})

	if s.ensureTagsFn == nil {
		t.Fatal("ensureTagsFn should be set")
	}

	// Verify it's callable
	s.ensureTagsFn(context.Background(), []string{"tag:test"})
	if !called {
		t.Fatal("ensureTagsFn was not called")
	}
}

func TestSyncNodes_EnsureTagsCalledOnNewService(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}

	writeRegistry(t, []registry.Service{
		{Name: "newapp", Type: "proxy", Target: "http://localhost:3000", Tags: []string{"tag:tsmain", "tag:shared"}},
	})

	fake := &fakeTSNetServer{localClient: localapitest.NewClient(nil), certDomains: []string{"newapp.tailnet.ts.net"}}
	nodesBuilt := 0
	oldNew := newTSNetServerFn
	newTSNetServerFn = func(registry.Service, string, string, string) tsnetServer {
		nodesBuilt++
		return fake
	}
	t.Cleanup(func() { newTSNetServerFn = oldNew })

	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(s.closeAllNodes)

	var ensuredTags []string
	nodesBuiltAtEnsure := -1
	s.SetEnsureTagsFn(func(ctx context.Context, tags []string) error {
		ensuredTags = tags
		nodesBuiltAtEnsure = nodesBuilt
		return nil
	})

	// The fake tsnet lets the new service's node come up, so the ensure has to
	// happen on the way to a started node, before that node is constructed.
	if err := s.syncNodes(context.Background()); err != nil {
		t.Fatalf("syncNodes() error = %v", err)
	}

	if len(ensuredTags) < 2 {
		t.Fatalf("expected at least 2 tags ensured, got: %v", ensuredTags)
	}
	if nodesBuiltAtEnsure != 0 || nodesBuilt != 1 || fake.upContext == nil {
		t.Fatalf("ensure ran after %d node construction(s); %d node(s) built, Up called %v; want tags ensured before the one new node is built and brought up",
			nodesBuiltAtEnsure, nodesBuilt, fake.upContext != nil)
	}
}

func TestSyncNodes_EnsureTagsNotCalledWhenNil(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}

	writeRegistry(t, []registry.Service{
		{Name: "app", Type: "proxy", Target: "http://localhost:3000", Tags: []string{"tag:tsmain"}},
	})

	fake := &fakeTSNetServer{localClient: localapitest.NewClient(nil), certDomains: []string{"app.tailnet.ts.net"}}
	oldNew := newTSNetServerFn
	newTSNetServerFn = func(registry.Service, string, string, string) tsnetServer { return fake }
	t.Cleanup(func() { newTSNetServerFn = oldNew })

	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(s.closeAllNodes)

	// ensureTagsFn is nil by default — should not panic, and must not keep
	// the service's node from starting.
	if err := s.syncNodes(context.Background()); err != nil {
		t.Fatalf("syncNodes() error = %v", err)
	}
	if fake.upContext == nil {
		t.Fatal("syncNodes() with a nil ensureTagsFn never brought the node up")
	}
}

func TestSyncNodes_EnsureTagsErrorLogged(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}

	writeRegistry(t, []registry.Service{
		{Name: "app", Type: "proxy", Target: "http://localhost:3000", Tags: []string{"tag:tsmain"}},
	})

	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	s.SetEnsureTagsFn(func(ctx context.Context, tags []string) error {
		return fmt.Errorf("ACL write denied")
	})

	if err := s.syncNodes(context.Background()); err == nil || !strings.Contains(err.Error(), "ACL write denied") {
		t.Fatalf("syncNodes() error = %v, want ACL write denied", err)
	}
}

func TestSyncNodes_PersistentEnsureTagsConflictDegradesWithoutBlockingServices(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}
	path := t.TempDir()
	writeRegistry(t, []registry.Service{{Name: "app", Type: registry.TypeFile, Path: path, Tags: []string{"tag:tsmain"}}})

	fake := &fakeTSNetServer{certDomains: []string{"app.tailnet.ts.net"}}
	oldNew := newTSNetServerFn
	newTSNetServerFn = func(registry.Service, string, string, string) tsnetServer { return fake }
	t.Cleanup(func() { newTSNetServerFn = oldNew })
	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(s.closeAllNodes)
	calls := 0
	s.SetEnsureTagsFn(func(context.Context, []string) error {
		calls++
		return tailapi.ErrPolicyConflict
	})

	if err := s.syncNodes(context.Background()); err != nil {
		t.Fatalf("syncNodes() error = %v, want ETag-conflict degradation", err)
	}
	if calls != 2 {
		t.Fatalf("EnsureTags calls = %d, want one fresh retry", calls)
	}
	if !s.nodeRunning("app") {
		t.Fatal("persistent ordinary-tag ETag conflict blocked unrelated service startup")
	}
}

func TestWatchRegistry_DebouncesRapidWrites(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}

	// Write initial registry
	writeRegistry(t, []registry.Service{
		{Name: "debounce-test", Type: registry.TypeFile, Path: t.TempDir()},
	})

	var syncCount atomic.Int32
	oldNew := newTSNetServerFn
	newTSNetServerFn = func(svc registry.Service, stateDir, authKey, controlURL string) tsnetServer {
		syncCount.Add(1)
		return &fakeTSNetServer{}
	}
	t.Cleanup(func() { newTSNetServerFn = oldNew })

	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(s.closeAllNodes)

	stopWatcher := startRegistryWatcherTest(t, s)

	// Give the watcher time to initialize
	time.Sleep(50 * time.Millisecond)

	// Write to registry rapidly 5 times within the debounce window (200ms)
	regPath, err := config.RegistryPath()
	if err != nil {
		t.Fatalf("RegistryPath() error = %v", err)
	}
	for i := 0; i < 5; i++ {
		data, _ := json.Marshal(registry.Registry{Services: []registry.Service{
			{Name: fmt.Sprintf("svc-%d", i), Type: registry.TypeFile, Path: t.TempDir()},
		}})
		if err := os.WriteFile(regPath, append(data, '\n'), 0o600); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}
		time.Sleep(20 * time.Millisecond) // 20ms apart, well within 200ms debounce
	}

	// Wait for debounce to fire (200ms) plus some margin
	time.Sleep(400 * time.Millisecond)

	stopWatcher()

	// With debounce, only the last write should trigger syncNodes (1 sync, not 5).
	// The sync creates one tsnet server per service in registry.
	count := syncCount.Load()
	if count > 2 {
		t.Fatalf("syncNodes called too many times: got %d tsnet constructions, want <= 2 (debounce should coalesce rapid writes)", count)
	}
	if count == 0 {
		t.Fatal("syncNodes was never called; debounce timer should have fired at least once")
	}
}

func TestRun_MissingRegistryStartsEmpty(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatal(err)
	}
	regPath, err := config.RegistryPath()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(regPath); !os.IsNotExist(err) {
		t.Fatalf("expected fresh config: %v", err)
	}
	s, err := New("", "")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	ready := false
	s.SetReadyFunc(func() error {
		ready = true
		snapshotPath, _ := config.RuntimeSnapshotPath()
		snapshot, err := runtimesnapshot.Load(snapshotPath)
		if err != nil || snapshot == nil || snapshot.GlobalError != nil || len(snapshot.Services) != 0 {
			t.Errorf("missing empty business snapshot: %+v %v", snapshot, err)
		}
		cancel()
		return nil
	})
	if err := s.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if !ready {
		t.Fatal("server never marked ready with absent registry")
	}
}

// TestStartLifecycleTicker_ReadsClockSeamBeforeSpawning is the ticker's half of
// the seam-capture contract that TestStartNode_ReadsServeTCPSeamBeforeSpawningAcceptLoop
// pins for the accept loop. The ticker outlives the call that starts it, so a
// read of serverNowFn from inside it is unordered with respect to every later
// write of that package-level variable -- and tests both install and restore
// stubs there. Capturing the function value before the goroutine exists is what
// keeps a later swap from reaching a ticker that is already running.
func TestStartLifecycleTicker_ReadsClockSeamBeforeSpawning(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatal(err)
	}
	writeRegistry(t, nil)
	s, err := New("key", "")
	if err != nil {
		t.Fatal(err)
	}

	captured := time.Date(2026, 9, 16, 1, 0, 0, 0, time.UTC)
	swapped := captured.Add(24 * time.Hour)

	oldNow, oldInterval := serverNowFn, lifecycleTickerInterval
	t.Cleanup(func() {
		serverNowFn = oldNow
		lifecycleTickerInterval = oldInterval
	})
	serverNowFn = func() time.Time { return captured }
	// Long enough that the swap below lands before the first tick, so the
	// assertion is about which function the goroutine holds rather than a race
	// between two goroutines.
	lifecycleTickerInterval = 150 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	observed := make(chan time.Time, 1)
	s.SetLifecycleReconcileFn(func(_ context.Context, now time.Time) (bool, error) {
		select {
		case observed <- now:
		default:
		}
		return false, nil
	})

	done := s.startLifecycleTicker(ctx)
	// The ticker is running; replace the seam it was started with.
	serverNowFn = func() time.Time { return swapped }

	select {
	case got := <-observed:
		if !got.Equal(captured) {
			t.Fatalf("ticker used the seam installed after it started: got %v, want %v", got, captured)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("lifecycle ticker never reconciled")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("lifecycle ticker did not stop")
	}
}
