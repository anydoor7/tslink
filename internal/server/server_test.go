package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/monody0007/tslink/internal/authmode"
	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/registry"
	runtimesnapshot "github.com/monody0007/tslink/internal/runtime"
	"github.com/monody0007/tslink/internal/tailapi"
	"github.com/monody0007/tslink/internal/testenv"
	"tailscale.com/ipn/ipnstate"
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

type fakeTSNetServer struct {
	upErr             error
	listenErr         error
	listenTLSErr      error
	localClient       *LocalClient
	closed            bool
	certDomains       []string
	dnsName           string
	listenCalled      int
	listenTLSCalled   int
	localClientCalled int
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

func (s *fakeTSNetServer) Up(context.Context) (*ipnstate.Status, error) {
	if s.upErr != nil {
		return nil, s.upErr
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
	return &fakeListener{}, nil
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
	s.closed = true
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
	return &ipnstate.Status{}, nil
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

func TestStartNodeLocked_UsesPerServiceAuthKeyProvider(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}

	s, err := New("static-key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

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

func TestSecuritySemantics_FileNoAllowStartsWithoutWhoIsDependency(t *testing.T) {
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
	if err == nil {
		t.Fatal("syncNodes() error = nil, want tcp allowed_users error")
	}
	if !strings.Contains(err.Error(), `service "db": tcp services do not support allowed_users`) {
		t.Fatalf("syncNodes() error = %v, want tcp allowed_users service context", err)
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
	if err == nil {
		t.Fatal("syncNodes() error = nil, want funnel allowed_users error")
	}
	if !strings.Contains(err.Error(), registry.ErrFunnelAllowedUsers) {
		t.Fatalf("syncNodes() error = %v, want funnel allowed_users error", err)
	}
	if !strings.Contains(err.Error(), registry.CodeFunnelAllowConflict) {
		t.Fatalf("syncNodes() error = %v, want stable code %s", err, registry.CodeFunnelAllowConflict)
	}
	if code, ok := registry.ErrorCode(err); !ok || code != registry.CodeFunnelAllowConflict {
		t.Fatalf("ErrorCode() = %q, %v; want %s, true", code, ok, registry.CodeFunnelAllowConflict)
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
	if !strings.Contains(logs, "skipping service with invalid startup config") ||
		!strings.Contains(logs, "public-app") ||
		!strings.Contains(logs, "tslink add public-app --funnel --public") {
		t.Fatalf("logs = %s, want skip warning with service name and remediation", logs)
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
	if err == nil {
		t.Fatal("syncNodes() error = nil, want funnel control_url error")
	}
	if !strings.Contains(err.Error(), registry.ErrFunnelControlURL) {
		t.Fatalf("syncNodes() error = %v, want funnel control_url error", err)
	}
	if !strings.Contains(err.Error(), registry.CodeFunnelControlURLConflict) {
		t.Fatalf("syncNodes() error = %v, want stable code %s", err, registry.CodeFunnelControlURLConflict)
	}
	if code, ok := registry.ErrorCode(err); !ok || code != registry.CodeFunnelControlURLConflict {
		t.Fatalf("ErrorCode() = %q, %v; want %s, true", code, ok, registry.CodeFunnelControlURLConflict)
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
			if err == nil {
				t.Fatal("syncNodes() error = nil, want funnel type conflict error")
			}
			if !strings.Contains(err.Error(), registry.ErrFunnelTypeConflict) {
				t.Fatalf("syncNodes() error = %v, want funnel type conflict error", err)
			}
			if !strings.Contains(err.Error(), registry.CodeFunnelTypeConflict) {
				t.Fatalf("syncNodes() error = %v, want stable code %s", err, registry.CodeFunnelTypeConflict)
			}
			if code, ok := registry.ErrorCode(err); !ok || code != registry.CodeFunnelTypeConflict {
				t.Fatalf("ErrorCode() = %q, %v; want %s, true", code, ok, registry.CodeFunnelTypeConflict)
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
	oldLoad := registryLoadFn
	registryLoadFn = func(string) (*registry.Registry, error) {
		if loadCount.Add(1) == 1 {
			return oldReg, nil
		}
		return newReg, nil
	}
	t.Cleanup(func() { registryLoadFn = oldLoad })

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
		return &fakeTSNetServer{dnsName: "db.tailnet.ts.net."}
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
	if endpoint.Display != "db.tailnet.ts.net:5432" || endpoint.Host != "db.tailnet.ts.net" || endpoint.State != "exact" {
		t.Fatalf("tcp endpoint = %+v, want concrete exact runtime DNS host", endpoint)
	}
	if strings.Contains(endpoint.Display, "<tailnet>") {
		t.Fatalf("tcp endpoint = %+v, must not mark placeholder exact", endpoint)
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
	oldNode := newNode(t, oldSvc)
	s.nodes["app"] = oldNode
	s.SetCleanupStaleNodesFn(func(ctx context.Context, targets []tailapi.CleanupTarget) (tailapi.CleanupResult, error) {
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

func TestSyncNodes_StateRemovalFailureStillRestartsChangedService(t *testing.T) {
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
	if strings.Join(started, ",") != "app" {
		t.Fatalf("started = %v, want [app]", started)
	}
	if got := s.nodes["app"].service.Tags; len(got) != 1 || got[0] != "tag:new" {
		t.Fatalf("running service tags = %v, want [tag:new]", got)
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

func TestSyncNodesRejectsMalformedPersistedServicesBeforeTSNetSideEffects(t *testing.T) {
	fileAsPath := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(fileAsPath, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	missingPath := filepath.Join(t.TempDir(), "missing")

	cases := []registry.Service{
		{Name: "unknown", Type: "websocket", Target: "http://localhost:3000"},
		{Name: "file-empty", Type: registry.TypeFile},
		{Name: "file-relative", Type: registry.TypeFile, Path: "relative"},
		{Name: "file-missing", Type: registry.TypeFile, Path: missingPath},
		{Name: "file-notdir", Type: registry.TypeFile, Path: fileAsPath},
		{Name: "proxy-hostless", Type: registry.TypeProxy, Target: "https:///app"},
		{Name: "proxy-relative", Type: registry.TypeProxy, Target: "localhost:3000"},
		{Name: "proxy-unsupported", Type: registry.TypeProxy, Target: "ftp://example.com"},
		{Name: "tcp-hostless", Type: registry.TypeTCP, Target: ":5432", Port: 5432},
		{Name: "tcp-nonnum", Type: registry.TypeTCP, Target: "localhost:abc"},
		{Name: "domain", Type: registry.TypeProxy, Target: "http://localhost:3000", Domain: "app.example.com"},
	}

	for _, svc := range cases {
		t.Run(svc.Name, func(t *testing.T) {
			testenv.SetHome(t, t.TempDir())
			if err := config.EnsureDir(); err != nil {
				t.Fatalf("EnsureDir() error = %v", err)
			}
			writeRegistry(t, []registry.Service{svc})

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
			if err := s.syncNodes(context.Background()); err == nil {
				t.Fatal("syncNodes() error = nil, want validation error")
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
	oldLoad := registryLoadFn
	registryLoadFn = func(string) (*registry.Registry, error) {
		if loadCount.Add(1) == 1 {
			return oldReg, nil
		}
		return newReg, nil
	}
	t.Cleanup(func() { registryLoadFn = oldLoad })

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

func TestMetricsHandler(t *testing.T) {
	testenv.SetHome(t, t.TempDir())

	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	handler := s.MetricsHandler()
	if handler == nil {
		t.Fatal("MetricsHandler() returned nil")
	}

	// The handler should respond to HTTP requests (Prometheus metrics endpoint)
	req, _ := http.NewRequest(http.MethodGet, "/metrics", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
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
		Funnel: false,
	}
	newSvc := oldSvc
	newSvc.Funnel = true
	newSvc.PublicAck = true
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

	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	var ensuredTags []string
	s.SetEnsureTagsFn(func(ctx context.Context, tags []string) error {
		ensuredTags = tags
		return nil
	})

	// syncNodes will fail on startNodeLocked (no real tsnet), but ensureTagsFn should be called first
	_ = s.syncNodes(context.Background())

	if len(ensuredTags) < 2 {
		t.Fatalf("expected at least 2 tags ensured, got: %v", ensuredTags)
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

	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	// ensureTagsFn is nil by default — should not panic
	_ = s.syncNodes(context.Background())
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
