package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/registry"
	"tailscale.com/ipn/ipnstate"
)

// DefaultMCPNodeName is the hostname of the dedicated tsnet node that carries
// the remote MCP control plane.
//
// The control plane deliberately does not travel as a registry service. A
// registry entry would appear in `tslink list`, would be a candidate for
// `tslink cleanup`, and could be given --funnel — which would publish the
// control plane to the public internet through an existing, already-audited
// code path. Keeping it off the registry means no code path in this program
// can make it publishable: the only listener it ever opens is ListenTLS on its
// own tsnet node.
const DefaultMCPNodeName = "tslink-mcp"

// MCPControlPlanePath is the single HTTP endpoint the control plane serves.
const MCPControlPlanePath = "/mcp"

// ErrMCPNoPrincipal is returned when the control plane is enabled without an
// authorization principal. It is a refusal to start, not a warning: an
// endpoint that answers every tailnet peer is the failure this feature exists
// to avoid.
var ErrMCPNoPrincipal = errors.New("mcp control plane is enabled but no principal is authorized; set mcp.allow in config.json to one or more login emails or tag:… entries, or disable the control plane")

// MCPControlPlane is the daemon-side configuration of the remote MCP
// transport. Handler carries the tool surface; this package never builds it,
// so the stdio and HTTP transports cannot drift into two tool registries.
type MCPControlPlane struct {
	// NodeName is the tsnet hostname. Empty means DefaultMCPNodeName.
	NodeName string
	// Tags are advertised when the daemon runs with a stored credential.
	Tags []string
	// AllowedUsers is the authorization principal list. Empty is a start-time
	// refusal, never "allow everyone".
	AllowedUsers []string
	// Handler is the MCP transport handler mounted at MCPControlPlanePath.
	Handler http.Handler
	// EventsSnapshot builds the body of one event frame. A nil function
	// disables the event stream entirely: MCPEventsPath is then not mounted,
	// and the node answers it with the mux's own 404. That is the default for
	// any caller that does not opt in, so the stream cannot appear by
	// accident.
	//
	// This package deliberately does not know the payload's shape. The views a
	// client needs are the CLI's own list and status results, which live in
	// package cmd; building them here would mean a second copy of them, and
	// the copy would be the one that rots.
	EventsSnapshot func(context.Context) (any, error)
	// EventsKeepalive is the heartbeat period. Zero means
	// DefaultMCPEventsKeepalive; out-of-range values are clamped to
	// [MinMCPEventsKeepalive, MaxMCPEventsKeepalive].
	EventsKeepalive time.Duration
}

// Validate reports whether the control plane may start.
func (c *MCPControlPlane) Validate() error {
	if c == nil {
		return errors.New("mcp control plane configuration is nil")
	}
	if c.Handler == nil {
		return errors.New("mcp control plane has no handler")
	}
	if len(nonEmptyPrincipals(c.AllowedUsers)) == 0 {
		return ErrMCPNoPrincipal
	}
	return nil
}

func (c *MCPControlPlane) nodeName() string {
	if name := strings.TrimSpace(c.NodeName); name != "" {
		return name
	}
	return DefaultMCPNodeName
}

// nonEmptyPrincipals drops blank entries so that a list of whitespace cannot
// masquerade as a configured principal.
func nonEmptyPrincipals(entries []string) []string {
	principals := make([]string, 0, len(entries))
	for _, entry := range entries {
		if trimmed := strings.TrimSpace(entry); trimmed != "" {
			principals = append(principals, trimmed)
		}
	}
	return principals
}

// mcpControlPlaneNode is the running control plane: one tsnet node, one TLS
// listener on that node, one HTTP server.
type mcpControlPlaneNode struct {
	name      string
	tsnetSrv  tsnetServer
	listener  net.Listener
	httpSrv   *http.Server
	cancel    context.CancelFunc
	closeOnce sync.Once
}

func (n *mcpControlPlaneNode) close() {
	n.closeOnce.Do(func() {
		if n.cancel != nil {
			n.cancel()
		}
		if n.httpSrv != nil {
			_ = closeHTTPServerFn(n.httpSrv)
		}
		if n.listener != nil {
			_ = n.listener.Close()
		}
		if n.tsnetSrv != nil {
			// Also reached when the node's Start or Up failed early.
			CloseTSNetServer(n.name, n.tsnetSrv)
		}
	})
}

var mcpNodeDirFn = config.MCPNodeDir

// SetMCPControlPlane installs the remote MCP control plane configuration. A nil
// argument leaves the daemon in its default state, where no control-plane
// listener is opened and no control-plane tsnet node is created.
func (s *Server) SetMCPControlPlane(cp *MCPControlPlane) {
	s.mcpControlPlane = cp
}

// MCPControlPlaneEnabled reports whether a control plane is configured.
func (s *Server) MCPControlPlaneEnabled() bool {
	return s.mcpControlPlane != nil
}

// startMCPControlPlane brings up the dedicated control-plane node. It is a
// no-op when no control plane is configured, which is the default: the
// function returns before any tsnet server is constructed.
func (s *Server) startMCPControlPlane(ctx context.Context) error {
	cp := s.mcpControlPlane
	if cp == nil {
		return nil
	}
	if err := cp.Validate(); err != nil {
		return err
	}
	name := cp.nodeName()

	stateDir, err := mcpNodeDirFn()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return err
	}

	// The node is described to tsnet with the same shape a service uses, but
	// this value never reaches registry.json and never becomes a ServiceNode.
	//
	// Ephemeral is deliberate on the credentialed path. The control-plane node
	// is not written to registry.json or to the node-ownership ledger, so
	// `tslink cleanup` never holds the exact NodeID proof it requires before
	// deleting a device. A persistent node would therefore outlive --mcp being
	// disabled or the daemon being uninstalled, and could only be removed by
	// hand in the Tailscale admin console. Marking it ephemeral lets the
	// Tailscale control plane reclaim the device after the daemon stops, with
	// no ledger entry.
	//
	// One field, two consumers, and they are deliberately not given the same
	// value on every path:
	//   1. s.authKeyProvider receives nodeService and forwards svc.Ephemeral
	//      into credentials.AuthKeyOptions, which sets the auth key's
	//      Devices.Create.Ephemeral capability (server-side registration).
	//      It is always asked for; a zero-credential provider answers "" and
	//      the request has no effect.
	//   2. newTSNetServerFn copies svc.Ephemeral into tsnet.Server.Ephemeral,
	//      which makes tsnet log in with LoginEphemeral (client-side pref).
	//      It is set only when an auth key was actually obtained.
	//
	// Why the asymmetry: reclaim of an ephemeral device is a server-side
	// property carried by the auth key. On the zero-credential path there is no
	// auth key, so the node is enrolled interactively as a user-owned device and
	// the client-side flag buys no server-side reclaim. It is not free either:
	// tsnet's Shutdown logs an ephemeral node out and persists LoggedOut, so a
	// LoginEphemeral node would force a fresh browser authorization on every
	// daemon restart, and because the control plane starts before the service
	// nodes are reconciled, every service would queue behind that login. The
	// zero-credential node is therefore persistent: one browser authorization
	// survives restarts, and disabling --mcp leaves a user-owned, untagged
	// device that the operator deletes in the Tailscale admin console.
	nodeService := registry.Service{Name: name, Type: registry.TypeProxy, Tags: append([]string(nil), cp.Tags...), Ephemeral: true}
	if err := s.mintedKeyControlURLError(name, s.controlURL); err != nil {
		return err
	}
	authKey, err := s.authKeyProvider(ctx, nodeService)
	if err != nil {
		return fmt.Errorf("auth key for mcp control plane: %w", err)
	}
	tsnetService := nodeService
	tsnetService.Ephemeral = authKey != ""
	tsnetSrv := newTSNetServerFn(tsnetService, stateDir, authKey, s.controlURL)

	nodeCtx, cancel := context.WithCancel(ctx)
	node := &mcpControlPlaneNode{name: name, tsnetSrv: tsnetSrv, cancel: cancel}
	committed := false
	defer func() {
		if !committed {
			node.close()
		}
	}()

	var status *ipnstate.Status
	if authKey == "" {
		status, err = s.waitForInteractiveNode(nodeCtx, tsnetSrv, name)
	} else {
		status, err = tsnetSrv.Up(nodeCtx)
	}
	if err != nil {
		return fmt.Errorf("tsnet up for mcp control plane: %w", err)
	}

	localClient, err := tsnetSrv.LocalClient()
	if err != nil {
		return fmt.Errorf("local client for mcp control plane: %w", err)
	}

	handler := newMCPControlPlaneHandler(cp, localClient, s.events)

	// ListenTLS binds the tsnet node only. There is no net.Listen here, no
	// host interface, no 0.0.0.0, and deliberately no ListenFunnel: the
	// control plane is unreachable from anywhere but the tailnet.
	ln, err := tsnetSrv.ListenTLS("tcp", ":443")
	if err != nil {
		return fmt.Errorf("listen TLS for mcp control plane: %w", err)
	}
	node.listener = newLimitedListener(ln, httpMaxActiveConns, "http", name)
	node.httpSrv = newHTTPServerFn(handler)

	go func() {
		if err := node.httpSrv.Serve(node.listener); err != nil && !errors.Is(err, http.ErrServerClosed) && !isClosedListenerError(err) {
			slog.Error("mcp control plane serve error", "name", name, "error", err)
		}
	}()

	s.mu.Lock()
	s.mcpNode = node
	s.mu.Unlock()
	committed = true

	slog.Warn("mcp.controlplane.enabled",
		"code", "mcp.controlplane.enabled",
		"message", "the remote MCP control plane is reachable by every authorized tailnet peer and can change services, publish Funnel and send invitations",
		"name", name,
		"path", MCPControlPlanePath,
		"principals", len(nonEmptyPrincipals(cp.AllowedUsers)),
		"host", runtimeHostFromStatus(status),
	)
	return nil
}

// NewMCPControlPlaneHandler assembles the control plane's HTTP surface. It is
// the single assembly used in production and asserted by tests, so a test
// cannot pass against a chain the daemon does not actually serve.
//
// Order is load-bearing. Origin validation is outermost after logging because
// the specification decides it before anything else and it must not be able to
// probe authorization. Authorization comes next, so an unauthorized request
// never reaches the routing mux and therefore never reaches a tool. Only the
// mounted MCP path exists on this node.
func NewMCPControlPlaneHandler(cp *MCPControlPlane, localClient *LocalClient) http.Handler {
	return newMCPControlPlaneHandler(cp, localClient, newEventHub())
}

// newMCPControlPlaneHandler is NewMCPControlPlaneHandler with the hub supplied.
// The daemon passes its own hub so runtime-state changes reach open streams;
// the exported constructor gives callers that only want the tool surface a
// private hub nothing ever publishes to.
func newMCPControlPlaneHandler(cp *MCPControlPlane, localClient *LocalClient, hub *eventHub) http.Handler {
	if hub == nil {
		hub = newEventHub()
	}
	mux := http.NewServeMux()
	mux.Handle(MCPControlPlanePath, ResourceBudgetMiddleware(cp.Handler))
	if cp.EventsSnapshot != nil {
		mux.Handle(MCPEventsPath, ResourceBudgetMiddleware(newMCPEventsHandler(cp, hub)))
	}
	// No identity resolver: the control plane's access line keeps the schema
	// with empty login and node fields. This is deliberately narrower than the
	// service nodes and it is a gap, stated rather than hidden: the
	// MCPAuthMiddleware below names the principal on its denial paths but not
	// on the accepted one, so an authorized control-plane call is currently
	// attributable to a source address and no account. Closing it is passing a
	// resolver here; it is left for a change that can weigh a cached principal
	// sitting beside this surface's authoritative uncached one.
	return AccessLogMiddleware(cp.nodeName(), nil, MCPOriginMiddleware(MCPAuthMiddleware(cp.AllowedUsers, localClient, mux)))
}

func (s *Server) closeMCPControlPlane() {
	s.mu.Lock()
	node := s.mcpNode
	s.mcpNode = nil
	s.mu.Unlock()
	if node != nil {
		node.close()
	}
}

// mcpDenied is the single rejection the control plane ever emits. Every denial
// path — unresolvable caller, missing profile, caller outside the principal
// list — writes exactly this, so a caller cannot tell whether a principal
// exists, whether it is the wrong one, or whether identity resolution failed.
func mcpDenied(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"jsonrpc": "2.0",
		"error":   map[string]any{"code": -32600, "message": "forbidden"},
	})
}

// MCPAuthMiddleware authorizes a caller against an explicit principal list,
// using the same Tailscale LocalClient WhoIs path the service proxy uses.
//
// It is fail-closed in every direction. An empty principal list denies every
// caller rather than allowing every caller, which is the opposite of
// ACLMiddleware's per-service contract and is deliberate: a service with no
// --allow is a public page inside the tailnet, while a control plane with no
// principal is a mistake.
func MCPAuthMiddleware(allowedUsers []string, localClient *LocalClient, next http.Handler) http.Handler {
	principals := nonEmptyPrincipals(allowedUsers)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(principals) == 0 {
			slog.Error("mcp control plane denied: no principal configured", "remote_addr", r.RemoteAddr)
			mcpDenied(w)
			return
		}
		if localClient == nil {
			slog.Warn("mcp control plane denied: no local client available", "remote_addr", r.RemoteAddr)
			mcpDenied(w)
			return
		}
		whois, err := localClient.WhoIs(r.Context(), r.RemoteAddr)
		if err != nil {
			slog.Warn("mcp control plane denied: failed to identify caller", "remote_addr", r.RemoteAddr, "error", err)
			mcpDenied(w)
			return
		}
		if whois == nil || whois.UserProfile == nil {
			slog.Warn("mcp control plane denied: caller identity missing user profile", "remote_addr", r.RemoteAddr)
			mcpDenied(w)
			return
		}
		var nodeTags []string
		if whois.Node != nil {
			nodeTags = whois.Node.Tags
		}
		if !isAllowed(whois.UserProfile.LoginName, nodeTags, principals) {
			slog.Info("mcp control plane denied: caller not authorized", "login", whois.UserProfile.LoginName, "remote_addr", r.RemoteAddr)
			mcpDenied(w)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// MCPOriginMiddleware implements the transport specification's Origin
// requirement. The 2026-07-28 Streamable HTTP transport states, under
// "Security Warning":
//
//	"Servers MUST validate the Origin header on all incoming connections to
//	prevent DNS rebinding attacks. If the Origin header is present and
//	invalid, servers MUST respond with HTTP 403 Forbidden. The HTTP response
//	body MAY comprise a JSON-RPC error response that has no id."
//
// A request with no Origin header is not a browser request and is allowed
// through; that is what every command-line MCP client sends. A request that
// does carry an Origin must have come from this endpoint's own https origin.
// Anything else — another host, a plain http origin, the opaque "null" origin
// a sandboxed document sends, or an unparseable value — is refused before the
// request reaches authorization or any tool.
func MCPOriginMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == "" {
			next.ServeHTTP(w, r)
			return
		}
		if !mcpOriginMatchesHost(origin, r.Host) {
			slog.Warn("mcp control plane denied: invalid Origin", "origin", origin, "host", r.Host, "remote_addr", r.RemoteAddr)
			mcpOriginForbidden(w)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// mcpOriginForbidden is the Origin rejection. It is separate from mcpDenied
// only so the message names the failing header; it leaks nothing about
// identity because it is decided before identity is ever resolved.
func mcpOriginForbidden(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"jsonrpc": "2.0",
		"error":   map[string]any{"code": -32600, "message": "forbidden: invalid Origin header"},
	})
}

func mcpOriginMatchesHost(origin, host string) bool {
	if host == "" {
		return false
	}
	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}
	if !strings.EqualFold(parsed.Scheme, "https") {
		return false
	}
	if parsed.Host == "" || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	return strings.EqualFold(parsed.Host, host)
}
