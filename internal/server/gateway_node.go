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
	defer run.routesMu.Unlock()
	for _, r := range run.routes {
		r.close()
	}
	run.routes = nil
}

func (run *portalRun) routeStates() []tsRuntime.GatewayRouteState {
	run.routesMu.Lock()
	defer run.routesMu.Unlock()
	states := make([]tsRuntime.GatewayRouteState, 0, len(run.routes))
	for _, r := range run.routes {
		states = append(states, r.state)
	}
	sort.Slice(states, func(i, j int) bool { return states[i].App < states[j].App })
	return states
}

// A separate ledger keeps gateway ownership out of app-node cleanup. Historical
// IDs remain as evidence: once changed, the finding survives daemon restarts.
func recordGatewayIdentity(dir string, cfg *registry.GatewayConfig, status *ipnstate.Status, now time.Time) (*tsRuntime.GatewayState, error) {
	view := &tsRuntime.GatewayState{Enabled: true, Persistent: true, Ports: cfg.Ports, Routes: []tsRuntime.GatewayRouteState{}}
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
	reg, issues, err := registry.PortalPreflight(filepath.Join(s.cfgDir, "registry.json"))
	if err != nil || len(issues) > 0 {
		run.closeRoutes()
		return
	}
	wanted := map[string]registry.Service{}
	for _, svc := range reg.Services {
		if svc.PortalRoute != nil {
			wanted[svc.Name] = svc
		}
	}
	run.routesMu.Lock()
	defer run.routesMu.Unlock()
	if run.routes == nil {
		run.routes = map[string]*gatewayRouteRun{}
	}
	for name, route := range run.routes {
		svc, ok := wanted[name]
		if ok && reflect.DeepEqual(route.svc, svc) {
			delete(wanted, name)
			continue
		}
		route.close()
		delete(run.routes, name)
	}
	for name, svc := range wanted {
		ctx, cancel := context.WithCancel(context.Background())
		route := &gatewayRouteRun{svc: svc, ctx: ctx, cancel: cancel, state: tsRuntime.GatewayRouteState{App: name, Port: svc.PortalRoute.Port, State: "starting", HostLabel: "this host"}}
		run.routes[name] = route
		run.routesWG.Add(1)
		go func() { defer run.routesWG.Done(); s.startGatewayRoute(run, route) }()
	}
}

func (s *Server) setGatewayRouteState(run *portalRun, r *gatewayRouteRun, state, reason, routeURL string) {
	run.routesMu.Lock()
	if run.routes[r.svc.Name] == r && r.ctx.Err() == nil {
		r.state.State, r.state.Reason, r.state.URL = state, reason, routeURL
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
	// R2 installs the tagged tsnet dialer; never fall back to host networking.
	if svc.Upstream == "tailnet" {
		fail("remote_upstream_unavailable")
		return
	}
	host := canonicalHostFor(run.node.tsnetSrv, run.runtimeHost)
	if host == "" {
		fail("gateway_hostname_unavailable")
		return
	}
	authority := net.JoinHostPort(host, strconv.Itoa(svc.PortalRoute.Port))
	gate := &gatewayGate{path: filepath.Join(s.cfgDir, "registry.json"), svc: svc, lc: run.lc, now: run.now, authority: authority, dashboard: "https://" + host, writer: s.accessWriter, expire: registry.TryExpirePeople}
	var handler http.Handler
	var closer io.Closer
	var err error
	if svc.Type == registry.TypeProxy {
		handler, err = NewProxyHandlerWithOptions(svc.Target, nil, ProxyOptions{PreserveHost: svc.PreserveHost, Gateway: &GatewayProxyOptions{Authority: func() string { return authority }, App: svc.Name, HostLabel: "this host", Observe: func(up bool) { s.observeGatewayUpstream(run, r, up) }}, Dialer: &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}})
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
