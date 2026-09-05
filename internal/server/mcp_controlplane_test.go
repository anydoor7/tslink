package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/registry"
	runtimesnapshot "github.com/monody0007/tslink/internal/runtime"
	"github.com/monody0007/tslink/internal/testenv"
	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/ipn"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/tailcfg"
)

// mcpProbeHandler records whether the protected handler ever ran. Every
// rejection test asserts this stayed false: a 403 that still executed the tool
// would be a leak, not a rejection.
type mcpProbeHandler struct {
	ran bool
}

func (h *mcpProbeHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.ran = true
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"tool":"executed"}`))
}

func mcpAuthRequest() *http.Request {
	req := httptest.NewRequest(http.MethodPost, "https://tslink-mcp.example.ts.net/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	req.RemoteAddr = "100.64.0.9:1234"
	req.Host = "tslink-mcp.example.ts.net"
	req.Header.Set("Content-Type", "application/json")
	return req
}

// TestMCPAuthMiddlewareRejectsUnresolvableCaller pins the first rejection
// path: a caller whose Tailscale identity cannot be resolved gets 403 and the
// protected handler never runs.
func TestMCPAuthMiddlewareRejectsUnresolvableCaller(t *testing.T) {
	probe := &mcpProbeHandler{}
	lc := fakeWhoIsClient(t, nil, errors.New("whois unavailable"))
	handler := MCPAuthMiddleware([]string{"alice@example.com"}, lc, probe)

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, mcpAuthRequest())

	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusForbidden)
	}
	if probe.ran {
		t.Fatal("protected handler ran for an unresolvable caller; no tool may execute on a rejected request")
	}
}

// TestMCPAuthMiddlewareRejectsCallerOutsideAllowList pins the second rejection
// path: identity resolves, but the principal is not authorized.
func TestMCPAuthMiddlewareRejectsCallerOutsideAllowList(t *testing.T) {
	probe := &mcpProbeHandler{}
	who := &apitype.WhoIsResponse{
		UserProfile: &tailcfg.UserProfile{LoginName: "eve@example.com"},
		Node:        &tailcfg.Node{Tags: []string{"tag:other"}},
	}
	lc := fakeWhoIsClient(t, who, nil)
	handler := MCPAuthMiddleware([]string{"alice@example.com", "tag:admin"}, lc, probe)

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, mcpAuthRequest())

	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusForbidden)
	}
	if probe.ran {
		t.Fatal("protected handler ran for an unauthorized caller; no tool may execute on a rejected request")
	}
}

// TestMCPAuthMiddlewareAdmitsListedCaller is the positive control. Without it
// the two rejection tests above would also pass against a middleware that
// refuses everyone.
func TestMCPAuthMiddlewareAdmitsListedCaller(t *testing.T) {
	for _, tc := range []struct {
		name    string
		who     *apitype.WhoIsResponse
		allowed []string
	}{
		{
			name:    "login match",
			who:     &apitype.WhoIsResponse{UserProfile: &tailcfg.UserProfile{LoginName: "alice@example.com"}, Node: &tailcfg.Node{}},
			allowed: []string{"alice@example.com"},
		},
		{
			name:    "tag match",
			who:     &apitype.WhoIsResponse{UserProfile: &tailcfg.UserProfile{LoginName: "bob@example.com"}, Node: &tailcfg.Node{Tags: []string{"tag:admin"}}},
			allowed: []string{"tag:admin"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			probe := &mcpProbeHandler{}
			handler := MCPAuthMiddleware(tc.allowed, fakeWhoIsClient(t, tc.who, nil), probe)
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, mcpAuthRequest())
			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
			}
			if !probe.ran {
				t.Fatal("authorized caller did not reach the protected handler")
			}
		})
	}
}

// TestMCPAuthMiddlewareFailsClosedWithoutPrincipal is the inverse of
// ACLMiddleware's per-service contract. An empty list on a service means "no
// restriction"; on the control plane it must mean "nobody", even when the
// caller's identity resolves cleanly.
func TestMCPAuthMiddlewareFailsClosedWithoutPrincipal(t *testing.T) {
	for _, allowed := range [][]string{nil, {}, {"", "   "}} {
		probe := &mcpProbeHandler{}
		who := &apitype.WhoIsResponse{
			UserProfile: &tailcfg.UserProfile{LoginName: "alice@example.com"},
			Node:        &tailcfg.Node{},
		}
		handler := MCPAuthMiddleware(allowed, fakeWhoIsClient(t, who, nil), probe)

		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, mcpAuthRequest())

		if rr.Code != http.StatusForbidden {
			t.Fatalf("allowed=%q status = %d, want %d", allowed, rr.Code, http.StatusForbidden)
		}
		if probe.ran {
			t.Fatalf("allowed=%q: an empty principal list admitted a caller", allowed)
		}
	}
}

// TestMCPAuthMiddlewareRejectionsAreIndistinguishable compares the three denial
// responses byte for byte. A caller must not be able to learn from the wire
// whether a principal is configured, whether it is a different one, or whether
// identity resolution failed.
func TestMCPAuthMiddlewareRejectionsAreIndistinguishable(t *testing.T) {
	known := &apitype.WhoIsResponse{
		UserProfile: &tailcfg.UserProfile{LoginName: "eve@example.com"},
		Node:        &tailcfg.Node{},
	}
	cases := map[string]http.Handler{
		"no principal configured": MCPAuthMiddleware(nil, fakeWhoIsClient(t, known, nil), &mcpProbeHandler{}),
		"identity unresolvable":   MCPAuthMiddleware([]string{"alice@example.com"}, fakeWhoIsClient(t, nil, errors.New("whois unavailable")), &mcpProbeHandler{}),
		"caller not authorized":   MCPAuthMiddleware([]string{"alice@example.com"}, fakeWhoIsClient(t, known, nil), &mcpProbeHandler{}),
		"no local client":         MCPAuthMiddleware([]string{"alice@example.com"}, nil, &mcpProbeHandler{}),
		"identity without a user profile": MCPAuthMiddleware([]string{"alice@example.com"},
			fakeWhoIsClient(t, &apitype.WhoIsResponse{Node: &tailcfg.Node{}}, nil), &mcpProbeHandler{}),
	}

	var reference string
	var referenceHeader string
	for name, handler := range cases {
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, mcpAuthRequest())
		if rr.Code != http.StatusForbidden {
			t.Fatalf("%s: status = %d, want %d", name, rr.Code, http.StatusForbidden)
		}
		body := rr.Body.String()
		header := rr.Header().Get("Content-Type")
		if reference == "" {
			reference, referenceHeader = body, header
			continue
		}
		if body != reference {
			t.Fatalf("%s: body = %q, want identical rejection %q", name, body, reference)
		}
		if header != referenceHeader {
			t.Fatalf("%s: Content-Type = %q, want %q", name, header, referenceHeader)
		}
	}
	if !strings.Contains(reference, `"forbidden"`) {
		t.Fatalf("rejection body = %q, want a constant forbidden message", reference)
	}
	for _, leak := range []string{"alice@example.com", "eve@example.com", "whois unavailable", "principal"} {
		if strings.Contains(reference, leak) {
			t.Fatalf("rejection body %q leaks %q", reference, leak)
		}
	}
}

// TestMCPOriginMiddlewareEnforcesTheSpecMUST covers the transport spec's
// Origin requirement: present and invalid must be 403, and the request must
// not reach anything behind the middleware.
func TestMCPOriginMiddlewareEnforcesTheSpecMUST(t *testing.T) {
	const host = "tslink-mcp.example.ts.net"
	cases := []struct {
		name    string
		origin  string
		wantRun bool
	}{
		{"absent origin is a non-browser client", "", true},
		{"same https origin", "https://" + host, true},
		{"same origin different case", "https://TSLINK-MCP.example.TS.NET", true},
		{"foreign https origin", "https://evil.example.com", false},
		{"http origin on the same host", "http://" + host, false},
		{"opaque null origin", "null", false},
		{"origin with a path", "https://" + host + "/mcp", false},
		{"origin with a port", "https://" + host + ":8443", false},
		{"unparseable origin", "https://%zz", false},
		{"empty scheme", "//" + host, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			probe := &mcpProbeHandler{}
			handler := MCPOriginMiddleware(probe)
			req := mcpAuthRequest()
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			if tc.wantRun {
				if rr.Code != http.StatusOK || !probe.ran {
					t.Fatalf("status = %d ran = %v, want 200 and the handler to run", rr.Code, probe.ran)
				}
				return
			}
			if rr.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want %d", rr.Code, http.StatusForbidden)
			}
			if probe.ran {
				t.Fatal("handler ran behind a rejected Origin; no tool may execute")
			}
		})
	}
}

// TestMCPOriginMiddlewareRejectsBeforeAuthorization proves the ordering the
// spec implies: Origin is decided before identity is resolved, so a bad Origin
// cannot be used to probe the authorization layer.
func TestMCPOriginMiddlewareRejectsBeforeAuthorization(t *testing.T) {
	probe := &mcpProbeHandler{}
	whoisCalls := 0
	lc := fakeWhoIsClient(t, &apitype.WhoIsResponse{
		UserProfile: &tailcfg.UserProfile{LoginName: "alice@example.com"},
		Node:        &tailcfg.Node{},
	}, nil)
	counting := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		whoisCalls++
		MCPAuthMiddleware([]string{"alice@example.com"}, lc, probe).ServeHTTP(w, r)
	})
	handler := MCPOriginMiddleware(counting)

	req := mcpAuthRequest()
	req.Header.Set("Origin", "https://evil.example.com")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusForbidden)
	}
	if whoisCalls != 0 {
		t.Fatalf("authorization ran %d times behind a rejected Origin, want 0", whoisCalls)
	}
	if probe.ran {
		t.Fatal("handler ran behind a rejected Origin")
	}
}

func TestMCPControlPlaneValidate(t *testing.T) {
	handler := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	if err := (&MCPControlPlane{Handler: handler, AllowedUsers: []string{"alice@example.com"}}).Validate(); err != nil {
		t.Fatalf("Validate() error = %v, want nil", err)
	}
	for _, tc := range []struct {
		name string
		cp   *MCPControlPlane
		want error
	}{
		{"no principal", &MCPControlPlane{Handler: handler}, ErrMCPNoPrincipal},
		{"blank principals", &MCPControlPlane{Handler: handler, AllowedUsers: []string{"", "  "}}, ErrMCPNoPrincipal},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.cp.Validate(); !errors.Is(err, tc.want) {
				t.Fatalf("Validate() error = %v, want %v", err, tc.want)
			}
		})
	}
	if err := (&MCPControlPlane{AllowedUsers: []string{"alice@example.com"}}).Validate(); err == nil {
		t.Fatal("Validate() error = nil for a control plane with no handler")
	}
	if err := (*MCPControlPlane)(nil).Validate(); err == nil {
		t.Fatal("Validate() error = nil for a nil control plane")
	}
}

// newMCPControlPlaneTestServer isolates config state and records every tsnet
// server the control plane would construct.
func newMCPControlPlaneTestServer(t *testing.T, fake tsnetServer) (*Server, *int) {
	t.Helper()
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}
	writeRegistry(t, nil)
	constructed := 0
	oldNew := newTSNetServerFn
	newTSNetServerFn = func(registry.Service, string, string, string) tsnetServer {
		constructed++
		return fake
	}
	t.Cleanup(func() { newTSNetServerFn = oldNew })

	s, err := New("synthetic-auth", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return s, &constructed
}

// TestMCPControlPlaneIsOffByDefault is the default-off proof at the daemon
// layer: with nothing configured, startMCPControlPlane constructs no tsnet
// server, opens no listener, and leaves no node behind.
func TestMCPControlPlaneIsOffByDefault(t *testing.T) {
	fake := &fakeTSNetServer{}
	s, constructed := newMCPControlPlaneTestServer(t, fake)

	if s.MCPControlPlaneEnabled() {
		t.Fatal("a freshly constructed server reports an MCP control plane")
	}
	if err := s.startMCPControlPlane(context.Background()); err != nil {
		t.Fatalf("startMCPControlPlane() error = %v, want nil", err)
	}
	if *constructed != 0 {
		t.Fatalf("tsnet servers constructed = %d, want 0 with no control plane configured", *constructed)
	}
	if fake.listenTLSCalled != 0 || fake.listenCalled != 0 || fake.listenFunnelCalled != 0 {
		t.Fatalf("listeners opened (tls=%d tcp=%d funnel=%d), want none", fake.listenTLSCalled, fake.listenCalled, fake.listenFunnelCalled)
	}
	if s.mcpNode != nil {
		t.Fatal("a control-plane node exists with no control plane configured")
	}
}

// TestMCPControlPlaneRefusesToStartWithoutPrincipal is the start-time refusal.
// It also asserts that the refusal happens before any tsnet node is created,
// so an unauthorized configuration never briefly exists on the tailnet.
func TestMCPControlPlaneRefusesToStartWithoutPrincipal(t *testing.T) {
	fake := &fakeTSNetServer{}
	s, constructed := newMCPControlPlaneTestServer(t, fake)
	probe := &mcpProbeHandler{}
	s.SetMCPControlPlane(&MCPControlPlane{Handler: probe})

	err := s.startMCPControlPlane(context.Background())
	if !errors.Is(err, ErrMCPNoPrincipal) {
		t.Fatalf("startMCPControlPlane() error = %v, want ErrMCPNoPrincipal", err)
	}
	if !strings.Contains(err.Error(), "mcp.allow") {
		t.Fatalf("refusal %q does not say how to fix it", err)
	}
	if *constructed != 0 {
		t.Fatalf("tsnet servers constructed = %d, want 0 when the control plane refuses to start", *constructed)
	}
	if fake.listenTLSCalled != 0 || fake.listenFunnelCalled != 0 {
		t.Fatalf("listeners opened (tls=%d funnel=%d), want none", fake.listenTLSCalled, fake.listenFunnelCalled)
	}
	if s.mcpNode != nil {
		t.Fatal("a control-plane node survived a refused start")
	}
}

// TestMCPControlPlaneBindsTsnetOnly asserts the binding contract: exactly one
// TLS listener on the tsnet node, no Funnel listener, no raw TCP listener.
func TestMCPControlPlaneBindsTsnetOnly(t *testing.T) {
	fake := &fakeTSNetServer{
		dnsName: "tslink-mcp.example.ts.net.",
		localClient: fakeWhoIsClient(t, &apitype.WhoIsResponse{
			UserProfile: &tailcfg.UserProfile{LoginName: "alice@example.com"},
			Node:        &tailcfg.Node{},
		}, nil),
	}
	s, constructed := newMCPControlPlaneTestServer(t, fake)
	probe := &mcpProbeHandler{}
	s.SetMCPControlPlane(&MCPControlPlane{
		AllowedUsers: []string{"alice@example.com"},
		Handler:      probe,
	})

	if err := s.startMCPControlPlane(context.Background()); err != nil {
		t.Fatalf("startMCPControlPlane() error = %v", err)
	}
	t.Cleanup(s.closeMCPControlPlane)

	if *constructed != 1 {
		t.Fatalf("tsnet servers constructed = %d, want 1", *constructed)
	}
	if fake.listenTLSCalled != 1 {
		t.Fatalf("ListenTLS calls = %d, want 1", fake.listenTLSCalled)
	}
	if fake.listenFunnelCalled != 0 {
		t.Fatalf("ListenFunnel calls = %d, want 0; the control plane must never be publishable", fake.listenFunnelCalled)
	}
	if fake.listenCalled != 0 {
		t.Fatalf("Listen calls = %d, want 0", fake.listenCalled)
	}
	if s.mcpNode == nil || s.mcpNode.httpSrv == nil || s.mcpNode.httpSrv.Handler == nil {
		t.Fatal("control plane did not retain its assembled HTTP handler")
	}

	// The assembled handler is the shipped one: Origin first, then
	// authorization, then the mounted path.
	handler := s.mcpNode.httpSrv.Handler

	badOrigin := mcpAuthRequest()
	badOrigin.Header.Set("Origin", "https://evil.example.com")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, badOrigin)
	if rr.Code != http.StatusForbidden || probe.ran {
		t.Fatalf("bad Origin: status = %d ran = %v, want 403 and no execution", rr.Code, probe.ran)
	}

	authorized := mcpAuthRequest()
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, authorized)
	if rr.Code != http.StatusOK || !probe.ran {
		t.Fatalf("authorized request: status = %d ran = %v, want 200 and execution", rr.Code, probe.ran)
	}

	// Only MCPControlPlanePath is mounted; nothing else on the node answers.
	probe.ran = false
	other := httptest.NewRequest(http.MethodPost, "https://tslink-mcp.example.ts.net/registry.json", nil)
	other.RemoteAddr = "100.64.0.9:1234"
	other.Host = "tslink-mcp.example.ts.net"
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, other)
	if probe.ran {
		t.Fatal("a path other than the MCP endpoint reached the handler")
	}
	if rr.Code != http.StatusNotFound {
		t.Fatalf("unmounted path status = %d, want %d", rr.Code, http.StatusNotFound)
	}

	s.closeMCPControlPlane()
	if fake.closeCount.Load() == 0 {
		t.Fatal("closing the control plane did not close its tsnet node")
	}
	if s.mcpNode != nil {
		t.Fatal("control-plane node survived close")
	}
}

// TestMCPControlPlaneUnauthorizedCallerNeverReachesHandler is the end-to-end
// authorization assertion against the assembled node handler.
func TestMCPControlPlaneUnauthorizedCallerNeverReachesHandler(t *testing.T) {
	fake := &fakeTSNetServer{
		localClient: fakeWhoIsClient(t, &apitype.WhoIsResponse{
			UserProfile: &tailcfg.UserProfile{LoginName: "eve@example.com"},
			Node:        &tailcfg.Node{},
		}, nil),
	}
	s, _ := newMCPControlPlaneTestServer(t, fake)
	probe := &mcpProbeHandler{}
	s.SetMCPControlPlane(&MCPControlPlane{
		AllowedUsers: []string{"alice@example.com"},
		Handler:      probe,
	})
	if err := s.startMCPControlPlane(context.Background()); err != nil {
		t.Fatalf("startMCPControlPlane() error = %v", err)
	}
	t.Cleanup(s.closeMCPControlPlane)

	rr := httptest.NewRecorder()
	s.mcpNode.httpSrv.Handler.ServeHTTP(rr, mcpAuthRequest())
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusForbidden)
	}
	if probe.ran {
		t.Fatal("an unauthorized caller reached the MCP handler")
	}
}

// TestMCPControlPlaneSurfacesLocalClientFailure keeps the control plane from
// starting with an identity source it cannot query: without a LocalClient the
// authorization layer would deny everything, which is safe but silently
// useless. Failing the start says so instead.
func TestMCPControlPlaneSurfacesLocalClientFailure(t *testing.T) {
	fake := &fakeTSNetServer{}
	s, _ := newMCPControlPlaneTestServer(t, fake)
	s.SetMCPControlPlane(&MCPControlPlane{
		AllowedUsers: []string{"alice@example.com"},
		Handler:      &mcpProbeHandler{},
	})

	err := s.startMCPControlPlane(context.Background())
	if err == nil {
		t.Fatal("startMCPControlPlane() error = nil, want a local client failure")
	}
	if !strings.Contains(err.Error(), "local client") {
		t.Fatalf("error = %v, want a local client failure", err)
	}
	if fake.listenTLSCalled != 0 {
		t.Fatalf("ListenTLS calls = %d, want 0 when identity resolution is unavailable", fake.listenTLSCalled)
	}
	if s.mcpNode != nil {
		t.Fatal("a control-plane node survived a failed start")
	}
	if fake.closeCount.Load() == 0 {
		t.Fatal("a failed start left the tsnet node open")
	}
}

// TestMCPControlPlaneNodeStateIsSeparateFromServiceNodes pins the directory
// choice. Control-plane state under NodesDir would collide with a registry
// service of the same name.
func TestMCPControlPlaneNodeStateIsSeparateFromServiceNodes(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	nodesDir, err := config.NodesDir()
	if err != nil {
		t.Fatalf("NodesDir() error = %v", err)
	}
	mcpDir, err := config.MCPNodeDir()
	if err != nil {
		t.Fatalf("MCPNodeDir() error = %v", err)
	}
	if strings.HasPrefix(mcpDir, nodesDir+string('/')) || mcpDir == nodesDir {
		t.Fatalf("MCPNodeDir() = %q is inside NodesDir() = %q", mcpDir, nodesDir)
	}
}

// TestServerRunRefusesToStartWithoutMCPPrincipal drives the shipped Run path.
// It is what catches a control plane that is configured but never started, or
// started without its refusal: Run itself must fail.
func TestServerRunRefusesToStartWithoutMCPPrincipal(t *testing.T) {
	fake := &fakeTSNetServer{}
	s, constructed := newMCPControlPlaneTestServer(t, fake)
	s.SetMCPControlPlane(&MCPControlPlane{Handler: &mcpProbeHandler{}})

	err := s.Run(context.Background())
	if !errors.Is(err, ErrMCPNoPrincipal) {
		t.Fatalf("Run() error = %v, want ErrMCPNoPrincipal", err)
	}
	if *constructed != 0 {
		t.Fatalf("tsnet servers constructed = %d, want 0 when Run refuses", *constructed)
	}
	if fake.listenTLSCalled != 0 || fake.listenFunnelCalled != 0 {
		t.Fatalf("listeners opened (tls=%d funnel=%d), want none", fake.listenTLSCalled, fake.listenFunnelCalled)
	}
}

// TestServerRunStartsAndClosesTheControlPlane proves Run actually reaches
// startMCPControlPlane when one is configured, and that shutdown tears the
// node down. Without this, removing the call from Run would leave every
// direct-call test above passing while the daemon served nothing.
func TestServerRunStartsAndClosesTheControlPlane(t *testing.T) {
	fake := &fakeTSNetServer{
		localClient: fakeWhoIsClient(t, &apitype.WhoIsResponse{
			UserProfile: &tailcfg.UserProfile{LoginName: "alice@example.com"},
			Node:        &tailcfg.Node{},
		}, nil),
	}
	s, constructed := newMCPControlPlaneTestServer(t, fake)
	s.SetMCPControlPlane(&MCPControlPlane{
		AllowedUsers: []string{"alice@example.com"},
		Handler:      &mcpProbeHandler{},
	})

	ctx, cancel := context.WithCancel(context.Background())
	s.SetReadyFunc(func() error { cancel(); return nil })
	if err := s.Run(ctx); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if *constructed != 1 {
		t.Fatalf("tsnet servers constructed = %d, want 1 (the control plane node)", *constructed)
	}
	if fake.listenTLSCalled != 1 {
		t.Fatalf("ListenTLS calls = %d, want 1", fake.listenTLSCalled)
	}
	if fake.listenFunnelCalled != 0 {
		t.Fatalf("ListenFunnel calls = %d, want 0", fake.listenFunnelCalled)
	}
	if s.mcpNode != nil {
		t.Fatal("control-plane node survived Run returning")
	}
	if fake.closeCount.Load() == 0 {
		t.Fatal("shutdown did not close the control-plane tsnet node")
	}
}

// TestServerRunWithoutControlPlaneCreatesNoNode is the default-off proof
// against the shipped Run path rather than the helper.
func TestServerRunWithoutControlPlaneCreatesNoNode(t *testing.T) {
	fake := &fakeTSNetServer{}
	s, constructed := newMCPControlPlaneTestServer(t, fake)

	ctx, cancel := context.WithCancel(context.Background())
	s.SetReadyFunc(func() error { cancel(); return nil })
	if err := s.Run(ctx); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if *constructed != 0 {
		t.Fatalf("tsnet servers constructed = %d, want 0 with an empty registry and no control plane", *constructed)
	}
	if fake.listenTLSCalled != 0 || fake.listenCalled != 0 || fake.listenFunnelCalled != 0 {
		t.Fatalf("listeners opened (tls=%d tcp=%d funnel=%d), want none", fake.listenTLSCalled, fake.listenCalled, fake.listenFunnelCalled)
	}
	if s.mcpNode != nil {
		t.Fatal("a control-plane node exists with no control plane configured")
	}
}

// TestMCPControlPlaneHonoursConfiguredNodeName covers the node_name config
// key. It is the only way to move the control plane off its default hostname,
// so a silently ignored value would leave two installs colliding on one name.
func TestMCPControlPlaneHonoursConfiguredNodeName(t *testing.T) {
	fake := &fakeTSNetServer{
		localClient: fakeWhoIsClient(t, &apitype.WhoIsResponse{
			UserProfile: &tailcfg.UserProfile{LoginName: "alice@example.com"},
			Node:        &tailcfg.Node{},
		}, nil),
	}
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}
	writeRegistry(t, nil)
	var gotName string
	oldNew := newTSNetServerFn
	newTSNetServerFn = func(svc registry.Service, _, _, _ string) tsnetServer {
		gotName = svc.Name
		return fake
	}
	t.Cleanup(func() { newTSNetServerFn = oldNew })

	s, err := New("synthetic-auth", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	s.SetMCPControlPlane(&MCPControlPlane{
		NodeName:     "ops-control",
		AllowedUsers: []string{"alice@example.com"},
		Handler:      &mcpProbeHandler{},
	})
	if err := s.startMCPControlPlane(context.Background()); err != nil {
		t.Fatalf("startMCPControlPlane() error = %v", err)
	}
	t.Cleanup(s.closeMCPControlPlane)
	if gotName != "ops-control" {
		t.Fatalf("tsnet hostname = %q, want the configured node_name", gotName)
	}

	blank := &MCPControlPlane{NodeName: "   ", AllowedUsers: []string{"alice@example.com"}, Handler: &mcpProbeHandler{}}
	if blank.nodeName() != DefaultMCPNodeName {
		t.Fatalf("blank node name = %q, want %q", blank.nodeName(), DefaultMCPNodeName)
	}
}

// TestMCPControlPlaneNodeIsEphemeralOnBothConsumers pins the ephemeral
// contract of the control-plane node on the credentialed path. The node never
// enters registry.json or the node-ownership ledger, so `tslink cleanup` can
// never prove ownership of it; the only thing standing between "disable --mcp"
// and a device stranded in the admin console is the Ephemeral flag reaching
// both consumers: the auth key derivation (server-side capability) and the
// tsnet.Server (client pref). The test captures the registry.Service at both
// seams. The zero-credential counterpart is
// TestMCPControlPlaneInteractivePathIsPersistent.
func TestMCPControlPlaneNodeIsEphemeralOnBothConsumers(t *testing.T) {
	fake := &fakeTSNetServer{
		localClient: fakeWhoIsClient(t, &apitype.WhoIsResponse{
			UserProfile: &tailcfg.UserProfile{LoginName: "alice@example.com"},
			Node:        &tailcfg.Node{},
		}, nil),
	}
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}
	registryPath := writeRegistry(t, nil)

	var authKeySvc, tsnetSvc registry.Service
	authKeyCalls, tsnetCalls, ledgerWrites := 0, 0, 0

	oldNew := newTSNetServerFn
	newTSNetServerFn = func(svc registry.Service, _, authKey, _ string) tsnetServer {
		tsnetCalls++
		tsnetSvc = svc
		if authKey != "synthetic-mcp-auth" {
			t.Errorf("tsnet auth key = %q, want the provider's value", authKey)
		}
		return fake
	}
	t.Cleanup(func() { newTSNetServerFn = oldNew })
	oldRecord := recordOwnedNodeFn
	recordOwnedNodeFn = func(string, string, string, time.Time) error {
		ledgerWrites++
		return nil
	}
	t.Cleanup(func() { recordOwnedNodeFn = oldRecord })

	s, err := New("unused-static-key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	s.SetAuthKeyProvider(func(_ context.Context, svc registry.Service) (string, error) {
		authKeyCalls++
		authKeySvc = svc
		return "synthetic-mcp-auth", nil
	})
	s.SetMCPControlPlane(&MCPControlPlane{
		Tags:         []string{"tag:ops"},
		AllowedUsers: []string{"alice@example.com"},
		Handler:      &mcpProbeHandler{},
	})
	if err := s.startMCPControlPlane(context.Background()); err != nil {
		t.Fatalf("startMCPControlPlane() error = %v", err)
	}
	t.Cleanup(s.closeMCPControlPlane)

	// Consumer 1: the auth-key provider. cmd/serve.go forwards svc.Ephemeral
	// into credentials.AuthKeyOptions, so a false here means a persistent
	// device registration regardless of what tsnet asks for.
	if authKeyCalls != 1 {
		t.Fatalf("auth key provider calls = %d, want 1", authKeyCalls)
	}
	if !authKeySvc.Ephemeral {
		t.Fatalf("auth key provider received Ephemeral=false for the control-plane node; the derived auth key would create a persistent device that tslink cleanup cannot delete")
	}
	if authKeySvc.Name != DefaultMCPNodeName || !containsString(authKeySvc.Tags, "tag:ops") {
		t.Fatalf("auth key provider received %+v, want name %q with tag:ops", authKeySvc, DefaultMCPNodeName)
	}

	// Consumer 2: the tsnet.Server constructor copies svc.Ephemeral into
	// tsnet.Server.Ephemeral, which selects LoginEphemeral on the client.
	if tsnetCalls != 1 {
		t.Fatalf("tsnet constructor calls = %d, want 1", tsnetCalls)
	}
	if !tsnetSvc.Ephemeral {
		t.Fatalf("tsnet constructor received Ephemeral=false for the control-plane node; tsnet would log in as a persistent node")
	}
	if tsnetSvc.Name != DefaultMCPNodeName || !containsString(tsnetSvc.Tags, "tag:ops") {
		t.Fatalf("tsnet constructor received %+v, want name %q with tag:ops", tsnetSvc, DefaultMCPNodeName)
	}

	// The node must stay out of both durable stores. Registering it "for
	// completeness" would either make cleanup delete a node without ownership
	// proof or, via lifecycle reconciliation, disable deletion globally for a
	// ledger entry with no registry service.
	reg, err := registry.Load(registryPath)
	if err != nil {
		t.Fatalf("registry.Load() error = %v", err)
	}
	if len(reg.Services) != 0 {
		t.Fatalf("registry.json services = %+v, want none: the control-plane node must not be registered", reg.Services)
	}
	if ledgerWrites != 0 {
		t.Fatalf("ownership ledger writes = %d, want 0 for the control-plane node", ledgerWrites)
	}
	ledgerPath, err := config.NodeOwnershipPath()
	if err != nil {
		t.Fatalf("NodeOwnershipPath() error = %v", err)
	}
	if _, err := os.Stat(ledgerPath); !os.IsNotExist(err) {
		t.Fatalf("os.Stat(%q) err = %v, want not-exist: no ownership ledger may be created for the control-plane node", ledgerPath, err)
	}
	ledger, err := runtimesnapshot.LoadOwnership(ledgerPath)
	if err != nil || len(ledger.Nodes) != 0 {
		t.Fatalf("ownership ledger = %+v, err = %v, want an empty ledger", ledger, err)
	}
	if s.mcpNode == nil {
		t.Fatal("control-plane node was not committed")
	}
	if _, ok := s.nodes[DefaultMCPNodeName]; ok {
		t.Fatal("control-plane node was registered as a service node")
	}
}

// TestMCPControlPlaneInteractivePathIsPersistent pins the zero-credential
// contract, where the two consumers of Ephemeral are deliberately split. The
// auth-key provider is still asked for an ephemeral key, so that a daemon
// which later gains a stored credential derives the right capability with no
// code change. The tsnet.Server, however, must be constructed persistent:
// there is no auth key to carry server-side reclaim, and a LoginEphemeral
// node is logged out by tsnet's Shutdown, which would force a fresh browser
// authorization on every daemon restart with every service node queued
// behind it. See the comment in startMCPControlPlane.
func TestMCPControlPlaneInteractivePathIsPersistent(t *testing.T) {
	fake := &fakeInteractiveTSNetServer{fakeTSNetServer: fakeTSNetServer{
		localClient: fakeWhoIsClient(t, &apitype.WhoIsResponse{
			UserProfile: &tailcfg.UserProfile{LoginName: "alice@example.com"},
			Node:        &tailcfg.Node{},
		}, nil),
	}}
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}
	writeRegistry(t, nil)

	var authKeySvc, tsnetSvc registry.Service
	var gotAuthKey string
	oldNew := newTSNetServerFn
	newTSNetServerFn = func(svc registry.Service, _, authKey, _ string) tsnetServer {
		tsnetSvc = svc
		gotAuthKey = authKey
		return fake
	}
	t.Cleanup(func() { newTSNetServerFn = oldNew })
	oldStatusClient := tsnetStatusClientFn
	tsnetStatusClientFn = func(tsnetServer) (tsnetStatusClient, error) {
		return &sequenceTSNetStatusClient{statuses: []*ipnstate.Status{{
			BackendState: ipn.Running.String(),
			TailscaleIPs: []netip.Addr{netip.MustParseAddr("100.64.0.7")},
			Self:         &ipnstate.PeerStatus{DNSName: "tslink-mcp.example.ts.net."},
		}}}, nil
	}
	t.Cleanup(func() { tsnetStatusClientFn = oldStatusClient })

	s, err := New("", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	s.SetAuthKeyProvider(func(_ context.Context, svc registry.Service) (string, error) {
		authKeySvc = svc
		return "", nil
	})
	s.SetMCPControlPlane(&MCPControlPlane{
		AllowedUsers: []string{"alice@example.com"},
		Handler:      &mcpProbeHandler{},
	})
	if err := s.startMCPControlPlane(context.Background()); err != nil {
		t.Fatalf("startMCPControlPlane() error = %v", err)
	}
	t.Cleanup(s.closeMCPControlPlane)

	if gotAuthKey != "" {
		t.Fatalf("tsnet auth key = %q, want empty on the zero-credential path", gotAuthKey)
	}
	if !fake.startCalled || fake.upCalled {
		t.Fatalf("interactive path start=%v up=%v, want start without Up", fake.startCalled, fake.upCalled)
	}
	if !authKeySvc.Ephemeral {
		t.Fatal("auth key provider received Ephemeral=false on the zero-credential path; a later stored credential would derive a persistent device")
	}
	if tsnetSvc.Ephemeral {
		t.Fatal("tsnet constructor received Ephemeral=true on the zero-credential path; tsnet would log the user-owned node out on every shutdown and force a browser authorization on every restart")
	}
}
