package server

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/health"
	"github.com/anydoor7/tslink/internal/registry"
	tsRuntime "github.com/anydoor7/tslink/internal/runtime"
	"tailscale.com/ipn/ipnstate"
)

type portalRun struct {
	routeSlots  map[int]chan struct{}
	dropped     atomic.Uint64
	startMu     sync.Mutex
	pending     bool
	routesMu    sync.Mutex
	routes      map[string]*gatewayRouteRun
	routesWG    sync.WaitGroup
	lc          *LocalClient
	now         func() time.Time
	timeout     time.Duration
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
	if cfg != nil && cfg.Enabled && old != nil && samePortalNodeConfig(old.config, *cfg) && s.portalState.State != "failed" {
		s.mu.Unlock()
		old.startMu.Lock()
		defer old.startMu.Unlock()
		select {
		case <-old.done:
			old.config = *cfg
			old.config.Admins = append([]string(nil), cfg.Admins...)
			s.mu.Lock()
			if s.portalState.Gateway != nil && cfg.Gateway != nil {
				copy := *s.portalState.Gateway
				copy.Ports = cfg.Gateway.Ports
				s.portalState.Gateway = &copy
			}
			s.mu.Unlock()
			s.syncGatewayRoutes(old)
		default:
			old.pending = true
		}
		return
	}
	s.portalRetryPending.Store(false)
	s.gatewayRouteRetryPending.Store(false)
	s.portalRun = nil
	s.portalState = tsRuntime.PortalState{State: "disabled"}
	s.mu.Unlock()
	if old != nil {
		old.cancel()
		<-old.done
		old.closeRoutes()
		if old.node != nil {
			old.node.close()
		}
		old.routesWG.Wait()
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
		defer s.finishPortalStart(nodeCtx, run)
		state, err := s.startPortalNode(nodeCtx, run, factory, now, timeout)
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.portalRun != run || nodeCtx.Err() != nil {
			return
		}
		if err != nil {
			state.Enabled, state.Hostname, state.State, state.Error = true, cfg.Hostname, "failed", "portal_start_failed"
			if code, ok := registry.ErrorCode(err); ok {
				state.Error = code
			}
			s.portalRetryPending.Store(true)
		}
		s.portalState = state
		s.writeRuntimeSnapshotLocked(s.lastRegistryFingerprint, s.lastSnapshotComplete)
	}()
}

func samePortalNodeConfig(a, b registry.PortalConfig) bool {
	if a.Gateway != nil && b.Gateway != nil && a.Gateway.Enabled && b.Gateway.Enabled {
		return a.Hostname == b.Hostname && a.Gateway.Tag == b.Gateway.Tag
	}
	return reflect.DeepEqual(a, b)
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
	gateway := run.config.Gateway != nil && run.config.Gateway.Enabled
	if gateway {
		svc.Ephemeral = false
	}
	if s.credentialed {
		tag, err := config.DefaultTag()
		if err != nil {
			return state, err
		}
		svc.Tags = []string{tag}
		if gateway && run.config.Gateway.Tag != "" {
			svc.Tags = []string{run.config.Gateway.Tag}
		}
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
	var marker, identity gatewayEnrollment
	if gateway {
		if key == "" && run.config.Gateway.Tag != "" {
			return state, registry.CodedError{Code: "gateway_tag_requires_credentials", Message: "gateway tags require a credentialed enrollment"}
		}
		identity = gatewayEnrollmentIdentity(key != "", svc.Tags, s.controlURL)
		marker, err = prepareGatewayEnrollment(s.cfgDir, svc.Name, identity)
		if err != nil {
			return state, err
		}
	}
	svc.Ephemeral = key != "" && !gateway
	node := &mcpControlPlaneNode{name: svc.Name, tsnetSrv: factory(svc, dir, key, s.controlURL)}
	run.node = node
	committed := false
	defer func() {
		if !committed {
			node.close()
		}
	}()
	var runtimeHost string
	var nodeStatus *ipnstate.Status
	if key == "" {
		status, err := s.waitForInteractiveNode(ctx, node.tsnetSrv, svc.Name)
		if err != nil {
			return state, err
		}
		runtimeHost = runtimeHostFromStatus(status)
		nodeStatus = status
		cancel()
		technical, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	} else {
		status, err := node.tsnetSrv.Up(technical)
		if err != nil {
			return state, err
		}
		runtimeHost = runtimeHostFromStatus(status)
		nodeStatus = status
	}
	lc, err := node.tsnetSrv.LocalClient()
	if err != nil || lc == nil {
		return state, fmt.Errorf("portal WhoIs client unavailable: %v", err)
	}
	if gateway {
		if marker.NodeID == "" {
			identity.NodeID = runtimeNodeIDFromStatus(nodeStatus)
			if identity.NodeID == "" {
				return state, fmt.Errorf("persistent enrollment has no node ID")
			}
			if err := writeGatewayEnrollment(dir, identity); err != nil {
				return state, err
			}
		}
		state.Gateway, err = recordGatewayIdentity(dir, run.config.Gateway, nodeStatus, now())
		if err != nil {
			return state, err
		}
		proof, err := readGatewayEnrollment(dir)
		if err != nil {
			return state, err
		}
		state.Gateway.Persistent = proof.NodeID == state.Gateway.NodeID && sameGatewayEnrollment(proof, identity)
		if !state.Gateway.Persistent {
			return state, registry.CodedError{Code: "gateway_ephemeral_refused", Message: "gateway persistent enrollment is not proven"}
		}
	}
	ln, _, err := activateListener(technical, ctx, node.close, func() (net.Listener, error) {
		return node.tsnetSrv.ListenTLS("tcp", ":443")
	})
	if err != nil {
		return state, err
	}
	if gateway {
		gate := &gatewayGate{path: filepath.Join(s.cfgDir, "registry.json"), svc: svc, lc: lc, now: now, writer: s.accessWriter, totalDropped: &run.dropped}
		ln = newGatewayListener(ctx, ln, gate)
	}
	node.listener = newLimitedListener(ln, httpMaxActiveConns, "http", svc.Name)
	c, configErr := health.LoadNotifier(filepath.Join(s.cfgDir, health.ConfigFile))
	requests, err := newPortalRequests(c, configErr, s.events.publish)
	if err != nil {
		return state, err
	}
	h := PortalHandler{RegistryPath: filepath.Join(s.cfgDir, "registry.json"), LocalClient: lc, CanonicalHost: func() string { return canonicalHostFor(node.tsnetSrv, runtimeHost) }, Apps: s.portalApps, Now: now, Requests: requests}
	node.httpSrv = newHTTPServerFn(portalSecurityMiddleware(RequestLimitsMiddleware(svc, nil, h)))
	node.listener = configureServiceHTTP(node.httpSrv, svc, node.listener, nil)
	if err := ctx.Err(); err != nil {
		return state, err
	}
	go func() { _ = node.httpSrv.Serve(node.listener) }()
	run.runtimeHost = runtimeHost
	run.lc, run.now, run.timeout = lc, now, timeout
	state.State = "running"
	if host := canonicalHostFor(node.tsnetSrv, runtimeHost); host != "" {
		state.URL = "https://" + host
	}
	committed = true
	if gateway {
		s.syncGatewayRoutes(run)
	}
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
		if reg.Portal != nil && reg.Portal.Gateway != nil && reg.Portal.Gateway.Enabled {
			app.HostLabel = "this host"
		}
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
		if svc.PortalRoute != nil && s.portalRun != nil {
			app.URL = ""
			for _, route := range s.portalRun.routeStates() {
				if route.App == svc.Name && route.Port == svc.PortalRoute.Port {
					app.URL = route.URL
					app.HostLabel = route.HostLabel
					if route.Health != "" {
						app.Health = route.Health
					}
					app.OfflineSince = route.OfflineSince
				}
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
		if s.portalState.Gateway != nil {
			copy := *s.portalState.Gateway
			copy.Routes = s.portalRun.routeStates()
			copy.Dropped = s.portalRun.dropped.Load()
			s.portalState.Gateway = &copy
		}
	}
}

// startMu makes the final route refresh and done publication atomic with the
// default branch of syncPortal. No registry event can fall between them.
func (s *Server) finishPortalStart(ctx context.Context, run *portalRun) {
	run.startMu.Lock()
	defer run.startMu.Unlock()
	s.mu.Lock()
	running := s.portalRun == run && s.portalState.State == "running"
	s.mu.Unlock()
	if run.pending && ctx.Err() == nil && running {
		s.syncGatewayRoutes(run)
	}
	run.pending = false
	close(run.done)
}
