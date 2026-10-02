package server

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/health"
	"github.com/anydoor7/tslink/internal/registry"
	tsRuntime "github.com/anydoor7/tslink/internal/runtime"
)

type portalRun struct {
	config      registry.PortalConfig
	cancel      context.CancelFunc
	done        chan struct{}
	node        *mcpControlPlaneNode
	runtimeHost string
}

// syncPortal only reconciles its own node. Enrollment is asynchronous so it
// cannot hold up startup, requests or enrollment of existing app nodes.
func (s *Server) syncPortal(ctx context.Context, cfg *registry.PortalConfig) {
	s.portalLifecycleMu.Lock()
	defer s.portalLifecycleMu.Unlock()
	if ctx.Err() != nil {
		return
	}
	s.mu.Lock()
	old := s.portalRun
	if cfg != nil && cfg.Enabled && old != nil && reflect.DeepEqual(old.config, *cfg) && s.portalState.State != "failed" {
		s.mu.Unlock()
		return
	}
	s.portalRetryPending.Store(false)
	s.portalRun = nil
	s.portalState = tsRuntime.PortalState{State: "disabled"}
	s.mu.Unlock()
	if old != nil {
		old.cancel()
		<-old.done
		if old.node != nil {
			old.node.close()
		}
	}
	if cfg == nil || !cfg.Enabled || ctx.Err() != nil || s.shuttingDown.Load() {
		return
	}
	root := s.portalRoot
	if root == nil {
		root = context.WithoutCancel(ctx)
	}
	nodeCtx, cancel := context.WithCancel(root)
	run := &portalRun{config: *cfg, cancel: cancel, done: make(chan struct{})}
	run.config.Admins = append([]string(nil), cfg.Admins...)
	s.mu.Lock()
	s.portalRun = run
	s.portalState = tsRuntime.PortalState{Enabled: true, Hostname: cfg.Hostname, State: "starting"}
	s.mu.Unlock()
	// Capture seams before the worker exists, including the clock. The worker
	// is joined on disable, replacement and daemon shutdown.
	factory, now, timeout := newTSNetServerFn, serverNowFn, nodeStartupTimeout
	go func() {
		defer close(run.done)
		state, err := s.startPortalNode(nodeCtx, run, factory, now, timeout)
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.portalRun != run || nodeCtx.Err() != nil {
			return
		}
		if err != nil {
			state = tsRuntime.PortalState{Enabled: true, Hostname: cfg.Hostname, State: "failed", Error: "portal_start_failed"}
			s.portalRetryPending.Store(true)
		}
		s.portalState = state
		s.writeRuntimeSnapshotLocked(s.lastRegistryFingerprint, s.lastSnapshotComplete)
	}()
}

func (s *Server) startPortalNode(ctx context.Context, run *portalRun, factory func(registry.Service, string, string, string) tsnetServer, now func() time.Time, timeout time.Duration) (tsRuntime.PortalState, error) {
	state := tsRuntime.PortalState{Enabled: true, Hostname: run.config.Hostname}
	if err := registry.ValidatePortal(&run.config); err != nil {
		return state, err
	}
	if s.mcpControlPlane != nil && s.mcpControlPlane.nodeName() == run.config.Hostname {
		return state, fmt.Errorf("portal hostname collides with MCP node")
	}
	dir := filepath.Join(s.cfgDir, "portal-nodes", run.config.Hostname)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return state, err
	}
	svc := registry.Service{Name: run.config.Hostname, Type: registry.TypeProxy, Ephemeral: true}
	if s.credentialed {
		tag, err := config.DefaultTag()
		if err != nil {
			return state, err
		}
		svc.Tags = []string{tag}
	}
	if err := s.mintedKeyControlURLError(svc.Name, s.controlURL); err != nil {
		return state, err
	}
	technical, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	key, err := s.authKeyProvider(technical, svc)
	if err != nil {
		return state, err
	}
	svc.Ephemeral = key != ""
	node := &mcpControlPlaneNode{name: svc.Name, tsnetSrv: factory(svc, dir, key, s.controlURL)}
	run.node = node
	committed := false
	defer func() {
		if !committed {
			node.close()
		}
	}()
	var runtimeHost string
	if key == "" {
		status, err := s.waitForInteractiveNode(ctx, node.tsnetSrv, svc.Name)
		if err != nil {
			return state, err
		}
		runtimeHost = runtimeHostFromStatus(status)
		cancel()
		technical, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	} else {
		status, err := node.tsnetSrv.Up(technical)
		if err != nil {
			return state, err
		}
		runtimeHost = runtimeHostFromStatus(status)
	}
	lc, err := node.tsnetSrv.LocalClient()
	if err != nil || lc == nil {
		return state, fmt.Errorf("portal WhoIs client unavailable: %v", err)
	}
	ln, _, err := activateListener(technical, ctx, node.close, func() (net.Listener, error) {
		return node.tsnetSrv.ListenTLS("tcp", ":443")
	})
	if err != nil {
		return state, err
	}
	node.listener = newLimitedListener(ln, httpMaxActiveConns, "http", svc.Name)
	h := PortalHandler{RegistryPath: filepath.Join(s.cfgDir, "registry.json"), LocalClient: lc, CanonicalHost: func() string { return canonicalHostFor(node.tsnetSrv, runtimeHost) }, Apps: s.portalApps, Now: now}
	node.httpSrv = newHTTPServerFn(portalSecurityMiddleware(RequestLimitsMiddleware(svc, nil, h)))
	node.listener = configureServiceHTTP(node.httpSrv, svc, node.listener, nil)
	if err := ctx.Err(); err != nil {
		return state, err
	}
	go func() { _ = node.httpSrv.Serve(node.listener) }()
	run.runtimeHost = runtimeHost
	state.State = "running"
	if host := canonicalHostFor(node.tsnetSrv, runtimeHost); host != "" {
		state.URL = "https://" + host
	}
	committed = true
	return state, nil
}

func (s *Server) closePortal() {
	s.syncPortal(context.Background(), nil)
}

func (s *Server) portalApps(reg *registry.Registry, now time.Time) map[string]PortalApp {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make(map[string]PortalApp, len(reg.Services))
	for _, svc := range reg.Services {
		svc = registry.EffectiveServiceAt(svc, now)
		app := PortalApp{Name: svc.Name, Health: health.Unknown}
		if node := s.nodes[svc.Name]; node != nil && !serviceChangedWithFallback(node.service, svc, s.controlURL) {
			host := canonicalHostFor(node.tsnetSrv, node.runtimeHost)
			if host != "" {
				states := []tsRuntime.ServiceState{{Service: svc, RuntimeHost: node.runtimeHost, CertDomains: node.tsnetSrv.CertDomains()}}
				endpoint := tsRuntime.NewPartialSnapshot(0, time.Time{}, "", now, states).Services[0].Endpoint
				if endpoint.State == "exact" {
					app.URL = endpoint.Display
				}
			}
			if observed, ok := s.healthStates[svc.Name]; ok && observed.Identity == healthProbeIdentity(svc, host) && observed.Node == node {
				app.Health = health.CurrentAt(observed.Health, svc, now).State
			}
		}
		result[svc.Name] = app
	}
	return result
}

func (s *Server) refreshPortalURLLocked() {
	if s.portalState.State == "running" && s.portalRun != nil && s.portalRun.node != nil {
		host := canonicalHostFor(s.portalRun.node.tsnetSrv, s.portalRun.runtimeHost)
		s.portalState.URL = ""
		if host != "" {
			s.portalState.URL = "https://" + host
		}
	}
}
