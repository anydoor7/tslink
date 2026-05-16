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
	"github.com/monody0007/tslink/internal/tailapi"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/tsnet"
)

type tsnetServer interface {
	Up(context.Context) (*ipnstate.Status, error)
	Listen(network, addr string) (net.Listener, error)
	ListenTLS(network, addr string) (net.Listener, error)
	ListenFunnel(network, addr string, opts ...tsnet.FunnelOption) (net.Listener, error)
	LocalClient() (*LocalClient, error)
	CertDomains() []string
	Close() error
}

var newTSNetServerFn = func(svc registry.Service, stateDir, authKey, controlURL string) tsnetServer {
	return &tsnet.Server{
		Hostname:      svc.Name,
		Dir:           stateDir,
		AuthKey:       authKey,
		Ephemeral:     svc.Ephemeral,
		ControlURL:    controlURL,
		AdvertiseTags: svc.Tags,
	}
}

// ServiceNode represents a single tsnet node serving one service.
type ServiceNode struct {
	tsnetSrv tsnetServer
	service  registry.Service
	listener net.Listener
	cancel   context.CancelFunc
	closed   atomic.Bool
}

// EnsureTagsFunc is the signature for ensuring ACL tags exist.
type EnsureTagsFunc func(ctx context.Context, tags []string) error

// AuthKeyProvider resolves auth material for a service immediately before its tsnet node starts.
type AuthKeyProvider func(ctx context.Context, svc registry.Service) (string, error)

// CleanupStaleNodesFunc removes stale tailnet nodes for service targets before forced reauth.
type CleanupStaleNodesFunc func(ctx context.Context, targets []tailapi.CleanupTarget) (tailapi.CleanupResult, error)

// Server manages multiple tsnet nodes, one per registered service.
type Server struct {
	nodes           map[string]*ServiceNode
	authKey         string
	authKeyProvider AuthKeyProvider
	controlURL      string
	mu              sync.RWMutex
	cfgDir          string
	metrics         *metrics.Metrics
	ensureTagsFn    EnsureTagsFunc
	cleanupNodesFn  CleanupStaleNodesFunc
}

// New creates a new multi-node server.
func New(authKey, controlURL string) (*Server, error) {
	cfgDir, err := config.Dir()
	if err != nil {
		return nil, err
	}
	return &Server{
		nodes:           make(map[string]*ServiceNode),
		authKey:         authKey,
		authKeyProvider: staticAuthKeyProvider(authKey),
		controlURL:      controlURL,
		cfgDir:          cfgDir,
		metrics:         metrics.New(),
		cleanupNodesFn:  tailapi.CleanupStaleNodesResult,
	}, nil
}

// SetEnsureTagsFn sets the function called to ensure ACL tags before starting nodes.
func (s *Server) SetEnsureTagsFn(fn EnsureTagsFunc) {
	s.ensureTagsFn = fn
}

// SetAuthKeyProvider sets the function used to resolve auth material per service.
func (s *Server) SetAuthKeyProvider(fn AuthKeyProvider) {
	if fn == nil {
		s.authKeyProvider = staticAuthKeyProvider(s.authKey)
		return
	}
	s.authKeyProvider = fn
}

// SetCleanupStaleNodesFn sets the function used to remove stale tailnet nodes before forced reauth.
func (s *Server) SetCleanupStaleNodesFn(fn CleanupStaleNodesFunc) {
	s.cleanupNodesFn = fn
}

func staticAuthKeyProvider(authKey string) AuthKeyProvider {
	return func(context.Context, registry.Service) (string, error) {
		return authKey, nil
	}
}

// Run starts all registered service nodes and watches for registry changes.
func (s *Server) Run(ctx context.Context) error {
	if err := s.syncNodes(ctx); err != nil {
		s.closeAllNodes()
		return fmt.Errorf("initial sync failed: %w", err)
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
	desiredOrder := make([]string, 0, len(reg.Services))
	for _, svc := range reg.Services {
		if err := validateServiceForStartup(svc); err != nil {
			return err
		}
		if _, seen := desired[svc.Name]; !seen {
			desiredOrder = append(desiredOrder, svc.Name)
		}
		desired[svc.Name] = svc
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Stop nodes for removed or changed services
	var authIdentityRestartTargets []tailapi.CleanupTarget
	var reloadErrs []error
	for name, node := range s.nodes {
		svc, exists := desired[name]
		if !exists {
			slog.Info("removing node", "name", name)
			s.stopNodeLocked(name, true) // remove state for deleted services
		} else if serviceChanged(node.service, svc) {
			authIdentityChanged := s.authIdentityChanged(node.service, svc)
			slog.Info("restarting node", "name", name, "auth_identity_changed", authIdentityChanged)
			s.stopNodeLocked(name, false)
			if authIdentityChanged {
				authIdentityRestartTargets = append(authIdentityRestartTargets, tailapi.CleanupTargetForService(node.service))
				if err := removeServiceStateDirFn(name); err != nil {
					reloadErr := fmt.Errorf("remove state for auth identity change %q: %w", name, err)
					slog.Warn("failed to remove node state before auth identity restart; continuing restart", "name", name, "error", err)
					reloadErrs = append(reloadErrs, reloadErr)
				}
			}
		}
	}

	if err := s.cleanupAuthIdentityNodes(ctx, authIdentityRestartTargets); err != nil {
		slog.Warn("failed to cleanup stale tailnet nodes before auth identity restart; continuing restart", "error", err)
		reloadErrs = append(reloadErrs, err)
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
				if errors.Is(err, tailapi.ErrNoAPIClient) {
					slog.Warn("degraded mode: skipped ACL tag ensure", "reason", err.Error(), "tags", tagsToEnsure, "degraded_mode", true)
				} else {
					slog.Error("failed to ensure ACL tags", "error", err)
					return fmt.Errorf("ensure ACL tags before start: %w", err)
				}
			}
		}
	}

	// Start nodes for new or changed services
	var startErrs []error
	for _, name := range desiredOrder {
		svc := desired[name]
		if _, running := s.nodes[name]; running {
			continue
		}
		if err := s.startNodeLocked(ctx, svc); err != nil {
			slog.Error("failed to start node", "name", name, "error", err)
			startErrs = append(startErrs, fmt.Errorf("start service %q: %w", name, err))
		}
	}

	return errors.Join(append(reloadErrs, startErrs...)...)
}

func serviceChanged(old, new registry.Service) bool {
	if old.Type != new.Type || old.Target != new.Target || old.Path != new.Path {
		return true
	}
	if old.Port != new.Port || old.Ephemeral != new.Ephemeral || old.ControlURL != new.ControlURL || old.Funnel != new.Funnel || old.Domain != new.Domain {
		return true
	}
	if !sameStringSet(old.Tags, new.Tags) {
		return true
	}
	if !sameStringSet(old.AllowedUsers, new.AllowedUsers) {
		return true
	}
	return false
}

func sameStringSet(a, b []string) bool {
	aSet := make(map[string]struct{}, len(a))
	for _, v := range a {
		aSet[v] = struct{}{}
	}
	bSet := make(map[string]struct{}, len(b))
	for _, v := range b {
		bSet[v] = struct{}{}
	}
	if len(aSet) != len(bSet) {
		return false
	}
	for v := range aSet {
		if _, ok := bSet[v]; !ok {
			return false
		}
	}
	return true
}

func (s *Server) authIdentityChanged(old, new registry.Service) bool {
	if old.Ephemeral != new.Ephemeral {
		return true
	}
	if !sameStringSet(old.Tags, new.Tags) {
		return true
	}
	return effectiveControlURL(old, s.controlURL) != effectiveControlURL(new, s.controlURL)
}

func effectiveControlURL(svc registry.Service, fallback string) string {
	if svc.ControlURL != "" {
		return svc.ControlURL
	}
	return fallback
}

func removeServiceStateDir(name string) error {
	nodesDir, err := config.NodesDir()
	if err != nil {
		return err
	}
	return os.RemoveAll(filepath.Join(nodesDir, name))
}

var removeServiceStateDirFn = removeServiceStateDir

func cleanupTargetHostnames(targets []tailapi.CleanupTarget) []string {
	hostnames := make([]string, 0, len(targets))
	for _, target := range targets {
		hostnames = append(hostnames, target.Hostname)
	}
	return hostnames
}

func (s *Server) cleanupAuthIdentityNodes(ctx context.Context, targets []tailapi.CleanupTarget) error {
	if len(targets) == 0 || s.cleanupNodesFn == nil {
		return nil
	}
	hostnames := cleanupTargetHostnames(targets)
	cleanup, err := s.cleanupNodesFn(ctx, targets)
	if err != nil {
		if errors.Is(err, tailapi.ErrNoAPIClient) {
			slog.Warn("degraded mode: skipped stale tailnet node cleanup", "reason", err.Error(), "hostnames", hostnames, "degraded_mode", true)
			return nil
		}
		return fmt.Errorf("cleanup stale tailnet nodes before auth identity restart: %w", err)
	}
	if cleanup.Skipped {
		slog.Warn("degraded mode: skipped stale tailnet node cleanup", "reason", cleanup.SkipReason, "hostnames", hostnames, "degraded_mode", true)
		return nil
	}
	if len(cleanup.Deleted) > 0 {
		slog.Info("removed stale tailnet nodes before auth identity restart", "matched", cleanup.Matched, "deleted", cleanup.Deleted)
	}
	return nil
}

func validateServiceForStartup(svc registry.Service) error {
	if err := registry.ValidateName(svc.Name); err != nil {
		return fmt.Errorf("service %q: %w", svc.Name, err)
	}
	if svc.Type == registry.TypeTCP && len(svc.AllowedUsers) > 0 {
		return fmt.Errorf("service %q: tcp services do not support allowed_users; remove allowed_users from registry.json", svc.Name)
	}
	for _, tag := range svc.Tags {
		if err := registry.ValidateTag(tag); err != nil {
			return fmt.Errorf("service %q has invalid tag %q: %w; fix with `tslink tags set %s tag:<lowercase-hyphen-name>` or edit registry.json", svc.Name, tag, err, svc.Name)
		}
	}
	return nil
}

func (s *Server) startNodeLocked(ctx context.Context, svc registry.Service) error {
	if err := validateServiceForStartup(svc); err != nil {
		return err
	}

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

	authKey, err := s.authKeyProvider(ctx, svc)
	if err != nil {
		return fmt.Errorf("auth key for service %q: %w", svc.Name, err)
	}
	tsnetSrv := newTSNetServerFn(svc, stateDir, authKey, controlURL)

	nodeCtx, cancel := context.WithCancel(ctx)

	if _, err := tsnetSrv.Up(nodeCtx); err != nil {
		cancel()
		tsnetSrv.Close()
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
