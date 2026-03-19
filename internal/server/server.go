package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/fsnotify/fsnotify"
	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/metrics"
	"github.com/monody0007/tslink/internal/registry"
	"tailscale.com/tsnet"
)

// ServiceNode represents a single tsnet node serving one service.
type ServiceNode struct {
	tsnetSrv *tsnet.Server
	service  registry.Service
	listener net.Listener
	cancel   context.CancelFunc
	closed   atomic.Bool
}

// EnsureTagsFunc is the signature for ensuring ACL tags exist.
type EnsureTagsFunc func(ctx context.Context, tags []string) error

// Server manages multiple tsnet nodes, one per registered service.
type Server struct {
	nodes        map[string]*ServiceNode
	authKey      string
	controlURL   string
	mu           sync.RWMutex
	cfgDir       string
	metrics      *metrics.Metrics
	ensureTagsFn EnsureTagsFunc
}

// New creates a new multi-node server.
func New(authKey, controlURL string) (*Server, error) {
	cfgDir, err := config.Dir()
	if err != nil {
		return nil, err
	}
	return &Server{
		nodes:      make(map[string]*ServiceNode),
		authKey:    authKey,
		controlURL: controlURL,
		cfgDir:     cfgDir,
		metrics:    metrics.New(),
	}, nil
}

// SetEnsureTagsFn sets the function called to ensure ACL tags before starting nodes.
func (s *Server) SetEnsureTagsFn(fn EnsureTagsFunc) {
	s.ensureTagsFn = fn
}

// Run starts all registered service nodes and watches for registry changes.
func (s *Server) Run(ctx context.Context) error {
	if err := s.syncNodes(ctx); err != nil {
		slog.Warn("initial sync failed", "error", err)
	}

	go s.watchRegistry(ctx)

	<-ctx.Done()
	s.closeAllNodes()
	return nil
}

// syncNodes compares registry to running nodes and starts/stops as needed.
func (s *Server) syncNodes(ctx context.Context) error {
	regPath, err := config.RegistryPath()
	if err != nil {
		return err
	}

	reg, err := registry.Load(regPath)
	if err != nil {
		return err
	}

	// Build desired state
	desired := make(map[string]registry.Service, len(reg.Services))
	for _, svc := range reg.Services {
		desired[svc.Name] = svc
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Stop nodes for removed or changed services
	for name, node := range s.nodes {
		svc, exists := desired[name]
		if !exists {
			slog.Info("removing node", "name", name)
			s.stopNodeLocked(name, true) // remove state for deleted services
		} else if serviceChanged(node.service, svc) {
			slog.Info("restarting node", "name", name)
			s.stopNodeLocked(name, false) // keep state for changed services
		}
	}

	// Ensure ACL tags exist before starting new/changed nodes
	if s.ensureTagsFn != nil {
		var tagsToEnsure []string
		tagSet := make(map[string]struct{})
		for name, svc := range desired {
			if _, running := s.nodes[name]; running {
				continue
			}
			for _, tag := range svc.Tags {
				if _, seen := tagSet[tag]; !seen {
					tagSet[tag] = struct{}{}
					tagsToEnsure = append(tagsToEnsure, tag)
				}
			}
		}
		if len(tagsToEnsure) > 0 {
			if err := s.ensureTagsFn(ctx, tagsToEnsure); err != nil {
				slog.Error("failed to ensure ACL tags", "error", err)
			}
		}
	}

	// Start nodes for new or changed services
	for name, svc := range desired {
		if _, running := s.nodes[name]; running {
			continue
		}
		if err := s.startNodeLocked(ctx, svc); err != nil {
			slog.Error("failed to start node", "name", name, "error", err)
		}
	}

	return nil
}

func serviceChanged(old, new registry.Service) bool {
	if old.Type != new.Type || old.Target != new.Target || old.Path != new.Path {
		return true
	}
	if old.Port != new.Port || old.Ephemeral != new.Ephemeral || old.ControlURL != new.ControlURL || old.Funnel != new.Funnel || old.Domain != new.Domain {
		return true
	}
	if len(old.Tags) != len(new.Tags) {
		return true
	}
	for i := range old.Tags {
		if old.Tags[i] != new.Tags[i] {
			return true
		}
	}
	if len(old.AllowedUsers) != len(new.AllowedUsers) {
		return true
	}
	for i := range old.AllowedUsers {
		if old.AllowedUsers[i] != new.AllowedUsers[i] {
			return true
		}
	}
	return false
}

func (s *Server) startNodeLocked(ctx context.Context, svc registry.Service) error {
	nodesDir, err := config.NodesDir()
	if err != nil {
		return err
	}

	stateDir := filepath.Join(nodesDir, svc.Name)
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return err
	}

	// Resolve control URL: per-service > global > default
	controlURL := s.controlURL
	if svc.ControlURL != "" {
		controlURL = svc.ControlURL
	}

	tsnetSrv := &tsnet.Server{
		Hostname:      svc.Name,
		Dir:           stateDir,
		AuthKey:       s.authKey,
		Ephemeral:     svc.Ephemeral,
		ControlURL:    controlURL,
		AdvertiseTags: svc.Tags,
	}

	nodeCtx, cancel := context.WithCancel(ctx)

	if _, err := tsnetSrv.Up(nodeCtx); err != nil {
		cancel()
		return fmt.Errorf("tsnet up for %q: %w", svc.Name, err)
	}

	// TCP proxy: raw TCP forwarding, no HTTP/TLS
	if svc.Type == registry.TypeTCP {
		port := svc.Port
		if port == 0 {
			port = 443
		}
		ln, err := tsnetSrv.Listen("tcp", fmt.Sprintf(":%d", port))
		if err != nil {
			cancel()
			tsnetSrv.Close()
			return fmt.Errorf("listen TCP for %q: %w", svc.Name, err)
		}

		node := &ServiceNode{
			tsnetSrv: tsnetSrv,
			service:  svc,
			listener: ln,
			cancel:   cancel,
		}

		go func() {
			serveTCP(ln, svc.Target, svc.Name)
		}()

		slog.Info("tcp node ready", "name", svc.Name, "target", svc.Target, "port", port)
		s.nodes[svc.Name] = node
		return nil
	}

	// HTTP-based services (proxy, file)
	var handler http.Handler
	var lc *LocalClient
	switch svc.Type {
	case registry.TypeProxy:
		var err2 error
		lc, err2 = tsnetSrv.LocalClient()
		if err2 != nil {
			cancel()
			tsnetSrv.Close()
			return fmt.Errorf("local client for %q: %w", svc.Name, err2)
		}
		h, err2 := NewProxyHandler(svc.Target, lc)
		if err2 != nil {
			cancel()
			tsnetSrv.Close()
			return fmt.Errorf("proxy handler for %q: %w", svc.Name, err2)
		}
		handler = h
	case registry.TypeFile:
		handler = NewFileHandler(svc.Path)
	default:
		cancel()
		tsnetSrv.Close()
		return fmt.Errorf("unknown service type %q", svc.Type)
	}

	// ACL middleware: enforce per-service access control
	if len(svc.AllowedUsers) > 0 {
		if lc == nil {
			var err2 error
			lc, err2 = tsnetSrv.LocalClient()
			if err2 != nil {
				cancel()
				tsnetSrv.Close()
				return fmt.Errorf("local client for %q (acl): %w", svc.Name, err2)
			}
		}
		handler = ACLMiddleware(svc.AllowedUsers, lc)(handler)
	}

	handler = AccessLogMiddleware(svc.Name, handler)
	handler = s.metrics.Middleware(svc.Name, handler)

	var ln net.Listener
	if svc.Funnel && svc.Type == registry.TypeProxy {
		ln, err = tsnetSrv.ListenFunnel("tcp", ":443")
	} else {
		ln, err = tsnetSrv.ListenTLS("tcp", ":443")
	}
	if err != nil {
		cancel()
		tsnetSrv.Close()
		return fmt.Errorf("listen TLS for %q: %w", svc.Name, err)
	}

	node := &ServiceNode{
		tsnetSrv: tsnetSrv,
		service:  svc,
		listener: ln,
		cancel:   cancel,
	}

	// Serve in background
	go func() {
		if err := http.Serve(ln, handler); err != nil && !isClosedListenerError(err) {
			slog.Error("node serve error", "name", svc.Name, "error", err)
		}
	}()

	if domains := tsnetSrv.CertDomains(); len(domains) > 0 {
		slog.Info("node ready", "name", svc.Name, "url", "https://"+domains[0])
	}

	if svc.Domain != "" {
		slog.Info("custom domain configured", "name", svc.Name, "domain", svc.Domain)
	}

	s.nodes[svc.Name] = node
	return nil
}

// stopNodeLocked stops a node. If removeState is true, its tsnet state dir is deleted.
// Use removeState=true only when a service is removed from the registry.
func (s *Server) stopNodeLocked(name string, removeState bool) {
	node, ok := s.nodes[name]
	if !ok {
		return
	}

	if !node.closed.CompareAndSwap(false, true) {
		// Already closed by another goroutine
		delete(s.nodes, name)
		return
	}

	node.cancel()
	if node.listener != nil {
		node.listener.Close()
	}
	if node.tsnetSrv != nil {
		node.tsnetSrv.Close()
	}

	if removeState {
		nodesDir, err := config.NodesDir()
		if err == nil {
			os.RemoveAll(filepath.Join(nodesDir, name))
		}
	}

	delete(s.nodes, name)
}

func (s *Server) closeAllNodes() {
	s.mu.Lock()
	defer s.mu.Unlock()

	for name := range s.nodes {
		s.stopNodeLocked(name, false) // keep state on graceful shutdown
	}
}

func (s *Server) watchRegistry(ctx context.Context) {
	regPath, err := config.RegistryPath()
	if err != nil {
		slog.Warn("registry watch disabled", "error", err)
		return
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		slog.Error("fsnotify setup failed", "error", err)
		return
	}
	defer watcher.Close()

	if err := watcher.Add(s.cfgDir); err != nil {
		slog.Error("watch directory failed", "dir", s.cfgDir, "error", err)
		return
	}

	regPath = filepath.Clean(regPath)

	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			if filepath.Clean(event.Name) != regPath {
				continue
			}
			if event.Has(fsnotify.Write) || event.Has(fsnotify.Create) {
				if err := s.syncNodes(ctx); err != nil {
					slog.Warn("reload registry failed", "error", err)
				}
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			slog.Error("fsnotify error", "error", err)
		}
	}
}

// MetricsHandler returns the Prometheus metrics HTTP handler.
func (s *Server) MetricsHandler() http.Handler {
	return s.metrics.Handler()
}

func isClosedListenerError(err error) bool {
	return errors.Is(err, net.ErrClosed) || strings.Contains(err.Error(), "use of closed network connection")
}
