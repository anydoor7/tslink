package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/registry"
	"github.com/monody0007/tslink/internal/tailapi"
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

func newNode(t *testing.T, svc registry.Service) *ServiceNode {
	t.Helper()

	return &ServiceNode{
		service: svc,
		cancel:  func() {},
	}
}

func markTSNetServerClosed(t *testing.T, srv *tsnet.Server) {
	t.Helper()

	field := reflect.ValueOf(srv).Elem().FieldByName("closed")
	ptr := unsafe.Pointer(field.UnsafeAddr())
	reflect.NewAt(field.Type(), ptr).Elem().SetBool(true)
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
	upErr  error
	closed bool
}

func (s *fakeTSNetServer) Up(context.Context) (*ipnstate.Status, error) {
	if s.upErr != nil {
		return nil, s.upErr
	}
	return &ipnstate.Status{}, nil
}

func (s *fakeTSNetServer) Listen(network, addr string) (net.Listener, error) {
	return &fakeListener{}, nil
}

func (s *fakeTSNetServer) ListenTLS(network, addr string) (net.Listener, error) {
	return &fakeListener{}, nil
}

func (s *fakeTSNetServer) ListenFunnel(network, addr string, opts ...tsnet.FunnelOption) (net.Listener, error) {
	return &fakeListener{}, nil
}

func (s *fakeTSNetServer) LocalClient() (*LocalClient, error) {
	return nil, errors.New("local client unavailable")
}

func (s *fakeTSNetServer) CertDomains() []string {
	return nil
}

func (s *fakeTSNetServer) Close() error {
	s.closed = true
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
	t.Setenv("HOME", t.TempDir())

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

func TestStopNodeLocked_CAS(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

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
	t.Setenv("HOME", t.TempDir())

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
	t.Setenv("HOME", t.TempDir())
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
	t.Setenv("HOME", t.TempDir())

	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ln := &fakeListener{}

	ts := &tsnet.Server{}
	markTSNetServerClosed(t, ts)

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
}

func TestCloseAllNodes(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

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
	t.Setenv("HOME", t.TempDir())
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
	t.Setenv("HOME", t.TempDir())
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

func TestStartNodeLocked_UsesPerServiceAuthKeyProvider(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
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

func TestStartNodeLocked_AuthKeyProviderErrorIncludesService(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
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
	t.Setenv("HOME", t.TempDir())
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
	if !strings.Contains(err.Error(), `service "legacy" has invalid tag "tag:Bad"`) {
		t.Fatalf("error = %v, want service/tag context", err)
	}
	if !strings.Contains(err.Error(), "tag:<lowercase-hyphen-name>") || !strings.Contains(err.Error(), "tslink tags set legacy") {
		t.Fatalf("error = %v, want grammar and migration action", err)
	}
}

func TestStartNodeLocked_RejectsTCPAllowedUsers(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
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

func TestSyncNodes_RemovesDeletedService(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
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

func TestSyncNodes_HotReloadUsesFreshPerServiceAuthMaterial(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
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
	t.Setenv("HOME", t.TempDir())
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
	t.Setenv("HOME", t.TempDir())
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
	t.Setenv("HOME", t.TempDir())
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
	t.Setenv("HOME", t.TempDir())
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
	t.Setenv("HOME", t.TempDir())
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
	t.Setenv("HOME", t.TempDir())

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
	t.Setenv("HOME", t.TempDir())
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

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.watchRegistry(ctx)

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
	t.Setenv("HOME", t.TempDir())

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
	t.Setenv("HOME", t.TempDir())
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

func TestRun_InitialStartFailureClosesStartedNodes(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
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
	t.Setenv("HOME", t.TempDir())

	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	// Should be a no-op, not panic
	s.stopNodeLocked("nonexistent", false)
	s.stopNodeLocked("nonexistent", true)
}

func TestStopNodeLocked_NilListenerAndServer(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

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
	t.Setenv("HOME", t.TempDir())
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
	t.Setenv("HOME", t.TempDir())
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
	t.Setenv("HOME", t.TempDir())

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

func TestAuthIdentityChanged_EffectiveControlURL(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

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
	t.Setenv("HOME", t.TempDir())

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
	t.Setenv("HOME", t.TempDir())
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
	t.Setenv("HOME", t.TempDir())
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
	t.Setenv("HOME", t.TempDir())
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
	t.Setenv("HOME", "")

	_, err := New("dummy-authkey", "")
	if err == nil {
		t.Fatal("expected error from New when config.Dir fails")
	}
}

func TestSyncNodes_RegistryPathError(t *testing.T) {
	// Create a valid server first, then break HOME
	t.Setenv("HOME", t.TempDir())

	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	// Now break HOME so config.RegistryPath() fails
	t.Setenv("HOME", "")

	if err := s.syncNodes(context.Background()); err == nil {
		t.Fatal("expected error from syncNodes when RegistryPath fails")
	}
}

func TestWatchRegistry_RegistryPathError(t *testing.T) {
	// Create server with valid HOME, then break it
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	// Break HOME so config.RegistryPath() fails inside watchRegistry
	t.Setenv("HOME", "")

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
	t.Setenv("HOME", t.TempDir())
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

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.watchRegistry(ctx)

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
	t.Setenv("HOME", t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}

	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.watchRegistry(ctx)

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
	cancel()

	// If we reach here without panic, the error path was handled gracefully
}

func TestSetEnsureTagsFn(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
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
	t.Setenv("HOME", t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}

	writeRegistry(t, []registry.Service{
		{Name: "newapp", Type: "proxy", Target: "localhost:3000", Tags: []string{"tag:tsmain", "tag:shared"}},
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
	t.Setenv("HOME", t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}

	writeRegistry(t, []registry.Service{
		{Name: "app", Type: "proxy", Target: "localhost:3000", Tags: []string{"tag:tsmain"}},
	})

	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	// ensureTagsFn is nil by default — should not panic
	_ = s.syncNodes(context.Background())
}

func TestSyncNodes_EnsureTagsErrorLogged(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}

	writeRegistry(t, []registry.Service{
		{Name: "app", Type: "proxy", Target: "localhost:3000", Tags: []string{"tag:tsmain"}},
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
