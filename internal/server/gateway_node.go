package server

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/anydoor7/tslink/internal/health"
	"github.com/anydoor7/tslink/internal/registry"
	tsRuntime "github.com/anydoor7/tslink/internal/runtime"
	"tailscale.com/ipn/ipnstate"
)

type gatewayRouteRun struct {
	health   health.State
	done     chan struct{}
	slot     chan struct{}
	failures uint
	retryAt  time.Time
	dialer   UpstreamDialer
	remote   *gatewayRemoteUpstream
	svc      registry.Service
	state    tsRuntime.GatewayRouteState // guarded by portalRun.routesMu
	ctx      context.Context
	cancel   context.CancelFunc
	mu       sync.Mutex
	http     *http.Server
	listener net.Listener
	closer   io.Closer
}

func (r *gatewayRouteRun) close() {
	r.cancel()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.http != nil {
		_ = r.http.Close()
	}
	if r.listener != nil {
		_ = r.listener.Close()
	}
	if r.closer != nil {
		_ = r.closer.Close()
	}
}

func (run *portalRun) closeRoutes() {
	run.routesMu.Lock()
	routes := run.routes
	run.routes = nil
	run.routesMu.Unlock()
	for _, r := range routes {
		r.close()
	}
}

func (run *portalRun) routeStates() []tsRuntime.GatewayRouteState {
	run.routesMu.Lock()
	defer run.routesMu.Unlock()
	states := make([]tsRuntime.GatewayRouteState, 0, len(run.routes))
	for _, r := range run.routes {
		state := r.state
		if r.health.LastChecked != nil {
			state.Health = health.CurrentAt(r.health, r.svc, run.now()).State
		}
		states = append(states, state)
	}
	sort.Slice(states, func(i, j int) bool { return states[i].App < states[j].App })
	return states
}

// A separate ledger keeps gateway ownership out of app-node cleanup. Historical
// IDs remain as evidence: once changed, the finding survives daemon restarts.
func recordGatewayIdentity(dir string, cfg *registry.GatewayConfig, status *ipnstate.Status, now time.Time) (*tsRuntime.GatewayState, error) {
	view := &tsRuntime.GatewayState{Enabled: true, Ports: cfg.Ports, Routes: []tsRuntime.GatewayRouteState{}}
	view.NodeID = runtimeNodeIDFromStatus(status)
	if status != nil && status.Self != nil {
		view.Tagged = status.Self.Tags != nil && status.Self.Tags.Len() > 0
	}
	if view.NodeID == "" || len(view.NodeID) > 256 {
		return view, fmt.Errorf("gateway node ownership unavailable")
	}
	path := filepath.Join(dir, "ownership.json")
	acquired, err := tsRuntime.TryUpdateOwnership(path, func(ledger *tsRuntime.OwnershipLedger) (bool, error) {
		found := false
		for i, node := range ledger.Nodes {
			if node.NodeID != view.NodeID {
				view.IdentityChanged = true
				continue
			}
			if node.ServiceName != "gateway" {
				return false, fmt.Errorf("gateway ownership conflicts with another service")
			}
			ledger.Nodes[i].RecordedAt = now.UTC()
			ledger.Nodes[i].RetiredAt = nil
			found = true
		}
		if !found {
			ledger.Nodes = append(ledger.Nodes, tsRuntime.OwnedNode{ServiceName: "gateway", NodeID: view.NodeID, RecordedAt: now.UTC()})
		}
		return true, nil
	})
	if err != nil {
		return view, err
	}
	if !acquired {
		return view, fmt.Errorf("gateway ownership writer busy")
	}
	view.OwnershipProven = true
	return view, nil
}

// Routes have a listener lifetime, never ownership of the shared tsnet server.
// ListenTLS has no context in tsnet. Each activation gets one tracked worker;
// timeouts report failure without closing the node or retrying a stuck call.
// A late listener is closed; daemon teardown closes tsnet and joins all workers.
func (s *Server) syncGatewayRoutes(run *portalRun) {
	if run.config.Gateway == nil || !run.config.Gateway.Enabled || run.node == nil {
		return
	}
	s.gatewayRouteRetryPending.Store(false)
	reg, issues, err := registry.PortalPreflight(filepath.Join(s.cfgDir, "registry.json"))
	if err != nil {
		run.closeRoutes()
		return
	}
	wanted := map[string]registry.Service{}
	invalid := map[string]bool{}
	for _, issue := range issues {
		invalid[issue.Name] = true
	}
	for _, svc := range reg.Services {
		if svc.PortalRoute != nil && !invalid[svc.Name] {
			wanted[svc.Name] = svc
		}
	}
	run.routesMu.Lock()
	var closing []*gatewayRouteRun
	defer func() {
		run.routesMu.Unlock()
		// Close joins admission workers. Never hold routesMu across that wait:
		// snapshot writers acquire it while holding Server.mu.
		for _, route := range closing {
			route.close()
		}
	}()
	if run.routes == nil {
		run.routes = map[string]*gatewayRouteRun{}
	}
	if run.routeSlots == nil {
		run.routeSlots = map[int]chan struct{}{}
	}
	failures := map[string]uint{}
	for name, route := range run.routes {
		svc, ok := wanted[name]
		if ok && reflect.DeepEqual(route.svc, svc) {
			retry := false
			if route.state.State == "failed" {
				s.gatewayRouteRetryPending.Store(true)
				select {
				case <-route.done:
					retry = !run.now().Before(route.retryAt)
				default:
				}
			}
			if !retry {
				delete(wanted, name)
				continue
			}
			failures[name] = route.failures
		}
		route.cancel()
		closing = append(closing, route)
		delete(run.routes, name)
	}
	for name, svc := range wanted {
		ctx, cancel := context.WithCancel(context.Background())
		port := svc.PortalRoute.Port
		slot := run.routeSlots[port]
		if slot == nil {
			slot = make(chan struct{}, 1)
			run.routeSlots[port] = slot
		}
		route := &gatewayRouteRun{done: make(chan struct{}), slot: slot, failures: failures[name], svc: svc, ctx: ctx, cancel: cancel, state: tsRuntime.GatewayRouteState{App: name, Port: svc.PortalRoute.Port, State: "starting", HostLabel: "this host"}}
		run.routes[name] = route
		run.routesWG.Add(1)
		go func() { defer run.routesWG.Done(); defer close(route.done); s.startGatewayRoute(run, route) }()
	}
}

func (s *Server) setGatewayRouteState(run *portalRun, r *gatewayRouteRun, state, reason, routeURL string) {
	run.routesMu.Lock()
	if run.routes[r.svc.Name] == r && r.ctx.Err() == nil {
		r.state.State, r.state.Reason, r.state.URL = state, reason, routeURL
		if state == "failed" {
			r.failures++
			backoff := time.Second << min(r.failures-1, 5)
			r.retryAt = run.now().Add(backoff)
			s.gatewayRouteRetryPending.Store(true)
		}
	}
	run.routesMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.portalRun == run {
		s.writeRuntimeSnapshotLocked(s.lastRegistryFingerprint, s.lastSnapshotComplete)
	}
}

func (s *Server) startGatewayRoute(run *portalRun, r *gatewayRouteRun) {
	svc := r.svc
	fail := func(reason string) { s.setGatewayRouteState(run, r, "failed", reason, "") }
	// A cancelled activation can still be inside tsnet.ListenTLS. Serialize
	// lifetimes per port so a replacement cannot collide with its late listener.
	wait := run.timeout
	if wait <= 0 {
		wait = 30 * time.Second
	}
	timerSlot := time.NewTimer(wait)
	defer timerSlot.Stop()
	select {
	case r.slot <- struct{}{}:
		defer func() { <-r.slot }()
	case <-r.ctx.Done():
		return
	case <-timerSlot.C:
		fail("route_listener_failed")
		return
	}
	var dialer UpstreamDialer = &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	var remote *gatewayRemoteUpstream
	if svc.Upstream == "tailnet" {
		remote = s.remoteGatewayUpstream(run, svc)
		if code := remote.check(r.ctx); code != "" {
			fail(code)
			return
		}
		dialer = remote
	}
	run.routesMu.Lock()
	r.dialer, r.remote = dialer, remote
	if remote != nil {
		r.state.HostLabel = remote.hostLabel()
	}
	run.routesMu.Unlock()
	host := canonicalHostFor(run.node.tsnetSrv, run.runtimeHost)
	if host == "" {
		fail("gateway_hostname_unavailable")
		return
	}
	authority := net.JoinHostPort(host, strconv.Itoa(svc.PortalRoute.Port))
	gate := &gatewayGate{path: filepath.Join(s.cfgDir, "registry.json"), svc: svc, lc: run.lc, now: run.now, authority: authority, dashboard: "https://" + host, writer: s.accessWriter, expire: registry.TryExpirePeople, totalDropped: &run.dropped}
	if remote != nil {
		gate.upstreamCheck = remote.check
	}
	var handler http.Handler
	var closer io.Closer
	var err error
	if svc.Type == registry.TypeProxy {
		handler, err = NewProxyHandlerWithOptions(svc.Target, nil, ProxyOptions{PreserveHost: svc.PreserveHost, Gateway: &GatewayProxyOptions{Authority: func() string { return authority }, App: svc.Name, HostLabel: r.state.HostLabel, Remote: remote != nil, Observe: func(up bool) { s.observeGatewayUpstream(run, r, up) }}, Dialer: dialer})
		if err == nil {
			closer = handler.(io.Closer)
		}
	} else if svc.File != "" {
		var h *FileHandler
		h, err = NewSingleFileHandler(svc.Path, svc.File)
		handler, closer = h, h
	} else {
		var h *FileHandler
		h, err = NewFileHandler(svc.Path)
		handler, closer = h, h
	}
	if err != nil {
		fail("upstream_down")
		return
	}
	defer closer.Close()
	handler = gate.handler(RequestLimitsMiddleware(svc, nil, handler))
	handler = AccessEventMiddleware(svc, s.accessOptions, s.accessWriter, nil, run.now, handler)
	logged := handler
	handler = http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		logged.ServeHTTP(w, req.WithContext(context.WithValue(req.Context(), gatewayAccessKey{}, true)))
	})
	srv := &http.Server{Handler: handler, MaxHeaderBytes: httpMaxHeaderBytes, BaseContext: func(net.Listener) context.Context { return r.ctx }}
	result := make(chan listenerActivationResult, 1)
	go func() {
		ln, err := run.node.tsnetSrv.ListenTLS("tcp", ":"+strconv.Itoa(svc.PortalRoute.Port))
		result <- listenerActivationResult{ln, err}
	}()
	timeout := run.timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	var ln net.Listener
	select {
	case got := <-result:
		ln, err = got.listener, got.err
	case <-timer.C:
		fail("route_listener_failed")
		got := <-result
		if got.listener != nil {
			_ = got.listener.Close()
		}
		return
	case <-r.ctx.Done():
		got := <-result
		if got.listener != nil {
			_ = got.listener.Close()
		}
		return
	}
	if err != nil {
		fail("route_listener_failed")
		return
	}
	ln = newGatewayListener(r.ctx, ln, gate)
	ln = newLimitedListener(ln, httpMaxActiveConns, "http", svc.Name)
	ln = configureServiceHTTP(srv, svc, ln, nil)
	r.mu.Lock()
	if r.ctx.Err() != nil {
		r.mu.Unlock()
		_ = ln.Close()
		return
	}
	r.http, r.listener, r.closer = srv, ln, closer
	r.mu.Unlock()
	s.setGatewayRouteState(run, r, "running", "", "https://"+authority+"/")
	_ = srv.Serve(ln)
	if r.ctx.Err() == nil {
		fail("route_listener_failed")
	}
	_ = ln.Close()
}

func (s *Server) observeGatewayUpstream(run *portalRun, r *gatewayRouteRun, up bool) {
	run.routesMu.Lock()
	changed := false
	if run.routes[r.svc.Name] == r && r.ctx.Err() == nil {
		state := health.Down
		if up {
			state = health.Healthy
		}
		changed = r.state.Health != state
		r.state.Health = state
		now := run.now().UTC()
		r.health = health.State{State: state, Kind: r.svc.Type, LastChecked: &now}
		r.state.HealthChecked = &now
		if up {
			r.state.OfflineSince = nil
		} else if r.state.OfflineSince == nil {
			now := run.now().UTC()
			r.state.OfflineSince = &now
		}
	}
	run.routesMu.Unlock()
	if changed {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.portalRun == run {
			s.writeRuntimeSnapshotLocked(s.lastRegistryFingerprint, s.lastSnapshotComplete)
		}
	}
}

// Retry route activations without reloading or restarting unrelated app nodes.
func (s *Server) retryGatewayRoutes(ctx context.Context) {
	s.portalLifecycleMu.Lock()
	defer s.portalLifecycleMu.Unlock()
	if ctx.Err() != nil || s.shuttingDown.Load() {
		return
	}
	s.mu.Lock()
	run := s.portalRun
	running := s.portalState.State == "running"
	s.mu.Unlock()
	if run == nil || !running {
		return
	}
	run.startMu.Lock()
	defer run.startMu.Unlock()
	select {
	case <-run.done:
		s.syncGatewayRoutes(run)
	default:
	}
}
