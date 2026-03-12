package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/fsnotify/fsnotify"
	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/registry"
	"tailscale.com/tsnet"
)

type Server struct {
	tsnetSrv    *tsnet.Server
	localClient *LocalClient
	mux         *http.ServeMux
	mu          sync.RWMutex
	routes      map[string]registry.Service
	handlers    map[string]http.Handler
}

func New() (*Server, error) {
	stateDir, err := config.TsnetStateDir()
	if err != nil {
		return nil, err
	}

	s := &Server{
		tsnetSrv: &tsnet.Server{
			Hostname: "tslink",
			Dir:      stateDir,
		},
		mux:      http.NewServeMux(),
		routes:   make(map[string]registry.Service),
		handlers: make(map[string]http.Handler),
	}
	s.mux.HandleFunc("/", s.serveHTTP)

	return s, nil
}

func (s *Server) Run(ctx context.Context) error {
	if _, err := s.tsnetSrv.Up(ctx); err != nil {
		return fmt.Errorf("tsnet up: %w", err)
	}

	localClient, err := s.tsnetSrv.LocalClient()
	if err != nil {
		_ = s.tsnetSrv.Close()
		return fmt.Errorf("local client: %w", err)
	}
	s.localClient = localClient

	if domains := s.tsnetSrv.CertDomains(); len(domains) > 0 {
		log.Printf("tslink ready: https://%s", domains[0])
	}

	if err := s.loadRegistry(); err != nil {
		log.Printf("warning: failed to load registry: %v", err)
	}

	go s.watchRegistry(ctx)

	ln, err := s.tsnetSrv.ListenTLS("tcp", ":443")
	if err != nil {
		_ = s.tsnetSrv.Close()
		return fmt.Errorf("listen TLS: %w", err)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- http.Serve(ln, s.mux)
	}()

	select {
	case <-ctx.Done():
		_ = ln.Close()
		serveErr := <-errCh
		closeErr := s.tsnetSrv.Close()
		if serveErr != nil && !isClosedListenerError(serveErr) {
			return serveErr
		}
		return closeErr
	case err := <-errCh:
		closeErr := s.tsnetSrv.Close()
		if err != nil && !isClosedListenerError(err) {
			return err
		}
		return closeErr
	}
}

func (s *Server) serveHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	if strings.HasPrefix(path, "/s/") {
		name := routeName(strings.TrimPrefix(path, "/s/"))

		s.mu.RLock()
		svc, ok := s.routes[name]
		handler := s.handlers[name]
		s.mu.RUnlock()

		if !ok || svc.Type != registry.TypeProxy || handler == nil {
			http.NotFound(w, r)
			return
		}

		http.StripPrefix("/s/"+name, handler).ServeHTTP(w, r)
		return
	}

	if strings.HasPrefix(path, "/f/") {
		name := routeName(strings.TrimPrefix(path, "/f/"))

		s.mu.RLock()
		svc, ok := s.routes[name]
		handler := s.handlers[name]
		s.mu.RUnlock()

		if !ok || svc.Type != registry.TypeFile || handler == nil {
			http.NotFound(w, r)
			return
		}

		if path == "/f/"+name {
			http.Redirect(w, r, path+"/", http.StatusMovedPermanently)
			return
		}

		http.StripPrefix("/f/"+name+"/", handler).ServeHTTP(w, r)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintln(w, "tslink is running")

	s.mu.RLock()
	services := make([]registry.Service, 0, len(s.routes))
	for _, svc := range s.routes {
		services = append(services, svc)
	}
	s.mu.RUnlock()

	if len(services) == 0 {
		fmt.Fprintln(w, "No services registered.")
		return
	}

	sort.Slice(services, func(i, j int) bool {
		return services[i].Name < services[j].Name
	})

	for _, svc := range services {
		if svc.Type == registry.TypeProxy {
			fmt.Fprintf(w, "/s/%s -> %s\n", svc.Name, svc.Target)
			continue
		}
		fmt.Fprintf(w, "/f/%s/ -> %s\n", svc.Name, svc.Path)
	}
}

func (s *Server) loadRegistry() error {
	regPath, err := config.RegistryPath()
	if err != nil {
		return err
	}

	reg, err := registry.Load(regPath)
	if err != nil {
		return err
	}

	routes := make(map[string]registry.Service, len(reg.Services))
	handlers := make(map[string]http.Handler, len(reg.Services))

	for _, svc := range reg.Services {
		switch svc.Type {
		case registry.TypeProxy:
			handler, err := NewProxyHandler(svc.Target, s.localClient)
			if err != nil {
				log.Printf("skip proxy %q: %v", svc.Name, err)
				continue
			}
			routes[svc.Name] = svc
			handlers[svc.Name] = handler
		case registry.TypeFile:
			routes[svc.Name] = svc
			handlers[svc.Name] = NewFileHandler(svc.Path)
		default:
			log.Printf("skip service %q: unknown type %q", svc.Name, svc.Type)
		}
	}

	s.mu.Lock()
	s.routes = routes
	s.handlers = handlers
	s.mu.Unlock()

	return nil
}

func (s *Server) watchRegistry(ctx context.Context) {
	regPath, err := config.RegistryPath()
	if err != nil {
		log.Printf("registry watch disabled: %v", err)
		return
	}

	cfgDir, err := config.Dir()
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

	if err := watcher.Add(cfgDir); err != nil {
		log.Printf("watch %s: %v", cfgDir, err)
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
				if err := s.loadRegistry(); err != nil {
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

func (s *Server) Close() error {
	if s.tsnetSrv == nil {
		return nil
	}
	return s.tsnetSrv.Close()
}

func routeName(path string) string {
	if path == "" {
		return ""
	}
	if idx := strings.Index(path, "/"); idx >= 0 {
		return path[:idx]
	}
	return path
}

func isClosedListenerError(err error) bool {
	return errors.Is(err, net.ErrClosed) || strings.Contains(err.Error(), "use of closed network connection")
}
