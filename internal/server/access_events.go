package server

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/anydoor7/tslink/internal/accesslog"
	"github.com/anydoor7/tslink/internal/registry"
	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/ipn"
)

type accessObservationKey struct{}
type accessFunnelKey struct{}
type guestSourceKey struct{}
type gatewayAccessKey struct{}

// ListenFunnel accepts both tailnet and public connections. Only the trusted
// transport marker denotes public ingress; request headers cannot attest it.
func accessFunnelConn(c net.Conn) *ipn.FunnelConn {
	for c != nil {
		switch conn := c.(type) {
		case *ipn.FunnelConn:
			return conn
		case *tls.Conn:
			c = conn.NetConn()
		case *limitedTLSConn:
			c = conn.tlsConn
		case *limitedConn:
			c = conn.Conn
		case interface{ budgetConn() *headerBudgetConn }:
			c = conn.budgetConn().Conn
		default:
			return nil
		}
	}
	return nil
}

func isAccessFunnelConn(c net.Conn) bool { return accessFunnelConn(c) != nil }

func configureAccessHTTP(srv *http.Server) {
	prior := srv.ConnContext
	srv.ConnContext = func(ctx context.Context, c net.Conn) context.Context {
		if prior != nil {
			ctx = prior(ctx, c)
		}
		funnel := accessFunnelConn(c)
		if funnel != nil {
			source := "unknown"
			if funnel.Src.IsValid() {
				source = funnel.Src.Addr().Unmap().String()
			}
			ctx = context.WithValue(ctx, guestSourceKey{}, source)
		}
		return context.WithValue(ctx, accessFunnelKey{}, funnel != nil)
	}
}

type accessObservation struct {
	mu         sync.Mutex
	identity   accesslog.Identity
	known      bool
	decision   string
	reason     string
	errorClass string
	grant      *accesslog.Grant
	// gone marks a client that left before any access decision was made.
	gone bool
}

// statusClientClosedRequest is the access-log status of a request whose
// client went away while TSLink was proxying it. It is never a 5xx.
const statusClientClosedRequest = 499

// accessGoneBeforeDecision reports whether the client canceled the request
// before TSLink could identify it, for example a phone dropping the connection
// during WhoIs. That is not a denial: no access event is recorded and nothing
// is logged as an identification failure.
func accessGoneBeforeDecision(r *http.Request) bool {
	if r.Context().Err() == nil {
		return false
	}
	if a, ok := r.Context().Value(accessObservationKey{}).(*accessObservation); ok {
		a.mu.Lock()
		a.gone = true
		a.mu.Unlock()
	}
	slog.Debug("access: client canceled before identification")
	return true
}

// serverCancel is the cause TSLink gives when it cancels in-flight requests
// itself; reason names it in the access log. The client did not go away and
// the backend did not fail.
type serverCancel struct{ reason string }

func (c serverCancel) Error() string { return "request canceled by tslink: " + c.reason }

var (
	// errRouteClosed cancels the requests of a gateway route being torn down.
	errRouteClosed error = serverCancel{"route_closed"}
	// errNodeClosed cancels the requests an app node still serves when its
	// graceful shutdown runs out of time, on stop or reload, and the guest
	// requests a guest-gated node ends as it stops.
	errNodeClosed error = serverCancel{"node_closed"}
)

// serverCause reports whether TSLink itself canceled ctx, and its reason.
func serverCause(ctx context.Context) (serverCancel, bool) {
	var cause serverCancel
	return cause, errors.As(context.Cause(ctx), &cause)
}

// serverCanceled reports whether a proxy error is TSLink's own cancellation
// of the request, and its reason. The transport reports a canceled request
// either as context.Canceled or as the context's cause itself.
func serverCanceled(r *http.Request, err error) (string, bool) {
	cause, ok := serverCause(r.Context())
	if !ok {
		return "", false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, cause) {
		return cause.reason, true
	}
	return "", false
}

// accessServerCanceled records an allowed request that TSLink canceled: the
// reason only, never a backend failure.
func accessServerCanceled(r *http.Request, reason string) {
	if a, ok := r.Context().Value(accessObservationKey{}).(*accessObservation); ok {
		a.mu.Lock()
		a.reason = reason
		a.mu.Unlock()
	}
}

// clientCanceled reports whether a proxy error is the cancellation of the
// request itself, as when the client goes away, rather than a backend
// failure. Other errors on an ended request, such as a guest grant ending
// while the backend is offline, keep their backend classification. Check
// serverCanceled first: TSLink's own cancellations also end the request.
func clientCanceled(r *http.Request, err error) bool {
	return errors.Is(err, context.Canceled) && r.Context().Err() != nil
}

// accessClientCanceled records an allowed request whose client left during
// proxying: status 499 and reason client_canceled, never a backend failure.
func accessClientCanceled(w http.ResponseWriter, r *http.Request) {
	if a, ok := r.Context().Value(accessObservationKey{}).(*accessObservation); ok {
		a.mu.Lock()
		a.reason = "client_canceled"
		a.mu.Unlock()
	}
	w.WriteHeader(statusClientClosedRequest)
}

// accessBackendFailure classifies a real backend failure for the access log.
func accessBackendFailure(r *http.Request, class string) {
	if a, ok := r.Context().Value(accessObservationKey{}).(*accessObservation); ok {
		a.mu.Lock()
		a.errorClass = class
		a.mu.Unlock()
	}
}

func accessDeny(r *http.Request, reason string) {
	if a, ok := r.Context().Value(accessObservationKey{}).(*accessObservation); ok {
		a.mu.Lock()
		a.decision = "denied"
		a.reason = reason
		a.mu.Unlock()
	}
}
func accessAttest(r *http.Request, who *apitype.WhoIsResponse, grant *accesslog.Grant) {
	if a, ok := r.Context().Value(accessObservationKey{}).(*accessObservation); ok {
		a.mu.Lock()
		a.identity = attestedIdentity(who)
		a.known = true
		a.grant = grant
		a.mu.Unlock()
	}
}
func attestedIdentity(who *apitype.WhoIsResponse) accesslog.Identity {
	id := accesslog.Identity{Tags: []string{}}
	if who == nil {
		return id
	}
	if who.Node != nil {
		id.Node = who.Node.ComputedName
		id.Tags = append(id.Tags, who.Node.Tags...)
	}
	if len(id.Tags) == 0 && who.UserProfile != nil {
		id.Login = who.UserProfile.LoginName
	}
	return id
}
func resolveAccess(identity *IdentityResolver, remote string) accesslog.Identity {
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	id := attestedIdentity(identity.whoIs(ctx, remote))
	id.Remote = remote
	return id
}

type resolvingAccessWriter interface {
	RecordResolved(accesslog.Event, func() accesslog.Identity) bool
}

func recordAccess(writer accesslog.Writer, e accesslog.Event, identity *IdentityResolver, remote string, known bool) {
	if writer == nil {
		return
	}
	if !known {
		if async, ok := writer.(resolvingAccessWriter); ok {
			async.RecordResolved(e, func() accesslog.Identity { return resolveAccess(identity, remote) })
			return
		}
		// Simple writers used by other integrations receive coarse, unattributed
		// metadata; they never trigger network I/O on the serving path.
		e.Identity.Remote = accesslog.CoarseRemote(remote)
	}
	writer.Record(e)
}

type accessBody struct {
	io.ReadCloser
	bytes atomic.Int64
}

func (b *accessBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.bytes.Add(int64(n))
	return n, err
}

// AccessEventMiddleware wraps authorization, limits and the app. Only actual
// request-body reads and response writes are counted, excluding HTTP framing.
func AccessEventMiddleware(svc registry.Service, opts accesslog.Options, writer accesslog.Writer, identity *IdentityResolver, now func() time.Time, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		at := now().UTC()
		observation := &accessObservation{decision: "allowed"}
		r = r.WithContext(context.WithValue(r.Context(), accessObservationKey{}, observation))
		rw := &responseWriter{ResponseWriter: w, status: http.StatusOK, countHijack: true}
		body := &accessBody{ReadCloser: r.Body}
		if r.Body != nil {
			r.Body = body
		}
		returned := false
		defer func() {
			observation.mu.Lock()
			if observation.gone {
				observation.mu.Unlock()
				return
			}
			// TSLink cut a response it had already started, as when a route
			// teardown or forced node close aborts the body copy: the handler
			// did not return. The forwarded status stays; the reason says
			// TSLink ended it. A response that completed keeps its reason.
			if !returned && observation.decision == "allowed" {
				if cause, ok := serverCause(r.Context()); ok {
					observation.reason = cause.reason
				}
			}
			e := accesslog.Event{Time: at, Kind: "http", App: svc.Name, Identity: observation.identity, Method: r.Method, Status: rw.status, BytesIn: body.bytes.Load(), BytesOut: rw.bytes, DurationMS: float64(time.Since(start)) / float64(time.Millisecond), Decision: observation.decision, Reason: observation.reason, ErrorClass: observation.errorClass, Grant: observation.grant}
			if via, _ := r.Context().Value(gatewayAccessKey{}).(bool); via {
				e.Via = "portal"
			}
			if rw.hijacked != nil {
				e.BytesIn += rw.hijacked.in.Load() + rw.hijackBuffered
				e.BytesOut += rw.hijacked.out.Load()
			}
			known := observation.known
			observation.mu.Unlock()
			e.PathMode = opts.ModeFor(svc.AccessLogPathMode, svc.AccessLogPath)
			e.Path = accesslog.PathForMode(r.URL.EscapedPath(), e.PathMode)
			public, _ := r.Context().Value(accessFunnelKey{}).(bool)
			if svc.Funnel && public {
				e.Identity = accesslog.Identity{Login: "public", Tags: []string{}}
				known = true
			}
			recordAccess(writer, e, identity, r.RemoteAddr, known)
		}()
		next.ServeHTTP(rw, r)
		returned = true
	})
}
func matchedLegacy(login string, tags, allow []string) *accesslog.Grant {
	for _, entry := range allow {
		if isAllowed(login, tags, []string{entry}) {
			return &accesslog.Grant{Kind: "legacy_allow", Entry: entry}
		}
	}
	return nil
}

// personDecision describes the F1 public access contract without changing its
// authority or expiry latch semantics.
func personDecision(reg *registry.Registry, svc registry.Service, login string, tags []string, now time.Time) (*accesslog.Grant, string) {
	if len(tags) > 0 {
		return nil, "people"
	}
	normalized, err := registry.NormalizePerson(login)
	if err != nil {
		// The authoritative F1 decision already resolved legacy keys. Preserve
		// its exact ASCII-normalized key for attribution without a second read.
		b := []byte(strings.Trim(login, " \t\r\n\v\f"))
		for i, c := range b {
			if c >= 'A' && c <= 'Z' {
				b[i] = c + 32
			}
		}
		normalized = string(b)
	}
	for _, p := range reg.People {
		if p.Login != normalized {
			continue
		}
		grant := &accesslog.Grant{Kind: "person", Entry: p.Login}
		for _, g := range p.Grants {
			if g.App == svc.Name && (g.Expired || g.ExpiresAt != nil && !now.Before(*g.ExpiresAt)) && !p.Revoked {
				return grant, "expired"
			}
		}
		return grant, "people"
	}
	return nil, "people"
}
