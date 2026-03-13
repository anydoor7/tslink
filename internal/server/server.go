package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/fsnotify/fsnotify"
	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/registry"
	"tailscale.com/tsnet"
)

// ServiceNode represents a single tsnet node serving one service.
type ServiceNode struct {
	tsnetSrv *tsnet.Server
	service  registry.Service
	listener net.Listener
	cancel   context.CancelFunc
}

// Server manages multiple tsnet nodes, one per registered service.
type Server struct {
	nodes   map[string]*ServiceNode
	authKey string
	mu      sync.RWMutex
	cfgDir  string
}

// New creates a new multi-node server.
func New(authKey string) (*Server, error) {
	cfgDir, err := config.Dir()
	if err != nil {
		return nil, err
	}
	return &Server{
		nodes:   make(map[string]*ServiceNode),
		authKey: authKey,
		cfgDir:  cfgDir,
	}, nil
}

// Run starts all registered service nodes and watches for registry changes.
func (s *Server) Run(ctx context.Context) error {
	if err := s.syncNodes(ctx); err != nil {
		log.Printf("warning: initial sync: %v", err)
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
			log.Printf("removing node %q", name)
			s.stopNodeLocked(name, true) // remove state for deleted services
		} else if serviceChanged(node.service, svc) {
			log.Printf("restarting node %q", name)
			s.stopNodeLocked(name, false) // keep state for changed services
		}
	}

	// Start nodes for new or changed services
	for name, svc := range desired {
		if _, running := s.nodes[name]; running {
			continue
		}
		if err := s.startNodeLocked(ctx, svc); err != nil {
			log.Printf("failed to start node %q: %v", name, err)
		}
	}

	return nil
}

func serviceChanged(old, new registry.Service) bool {
	return old.Type != new.Type || old.Target != new.Target || old.Path != new.Path
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

	tsnetSrv := &tsnet.Server{
		Hostname: svc.Name,
		Dir:      stateDir,
		AuthKey:  s.authKey,
	}

	nodeCtx, cancel := context.WithCancel(ctx)

	if _, err := tsnetSrv.Up(nodeCtx); err != nil {
		cancel()
		return fmt.Errorf("tsnet up for %q: %w", svc.Name, err)
	}

	// Create handler based on service type
	var handler http.Handler
	switch svc.Type {
	case registry.TypeProxy:
		lc, err := tsnetSrv.LocalClient()
		if err != nil {
			cancel()
			tsnetSrv.Close()
			return fmt.Errorf("local client for %q: %w", svc.Name, err)
		}
		h, err := NewProxyHandler(svc.Target, lc)
		if err != nil {
			cancel()
			tsnetSrv.Close()
			return fmt.Errorf("proxy handler for %q: %w", svc.Name, err)
		}
		handler = h
	case registry.TypeFile:
		handler = NewFileHandler(svc.Path)
	default:
		cancel()
		tsnetSrv.Close()
		return fmt.Errorf("unknown service type %q", svc.Type)
	}

	ln, err := tsnetSrv.ListenTLS("tcp", ":443")
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
			log.Printf("node %q serve error: %v", svc.Name, err)
		}
	}()

	if domains := tsnetSrv.CertDomains(); len(domains) > 0 {
		log.Printf("node %q ready: https://%s", svc.Name, domains[0])
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
		log.Printf("registry watch disabled: %v", err)
		return
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Printf("fsnotify: %v", err)
		return
	}
	defer watcher.Close()

	if err := watcher.Add(s.cfgDir); err != nil {
		log.Printf("watch %s: %v", s.cfgDir, err)
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
					log.Printf("reload registry: %v", err)
				}
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			log.Printf("fsnotify error: %v", err)
		}
	}
}

func isClosedListenerError(err error) bool {
	return errors.Is(err, net.ErrClosed) || strings.Contains(err.Error(), "use of closed network connection")
}
