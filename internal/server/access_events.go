package server

import (
	"context"
	"crypto/tls"
	"io"
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

// ListenFunnel accepts both tailnet and public connections. Only the trusted
// transport marker denotes public ingress; request headers cannot attest it.
func isAccessFunnelConn(c net.Conn) bool {
	for c != nil {
		switch conn := c.(type) {
		case *ipn.FunnelConn:
			return true
		case *tls.Conn:
			c = conn.NetConn()
		case *limitedTLSConn:
			c = conn.tlsConn
		case *limitedConn:
			c = conn.Conn
		case interface{ budgetConn() *headerBudgetConn }:
			c = conn.budgetConn().Conn
		default:
			return false
		}
	}
	return false
}

func configureAccessHTTP(srv *http.Server) {
	prior := srv.ConnContext
	srv.ConnContext = func(ctx context.Context, c net.Conn) context.Context {
		if prior != nil {
			ctx = prior(ctx, c)
		}
		return context.WithValue(ctx, accessFunnelKey{}, isAccessFunnelConn(c))
	}
}

type accessObservation struct {
	mu       sync.Mutex
	identity accesslog.Identity
	known    bool
	decision string
	reason   string
	grant    *accesslog.Grant
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
		defer func() {
			observation.mu.Lock()
			e := accesslog.Event{Time: at, Kind: "http", App: svc.Name, Identity: observation.identity, Method: r.Method, Status: rw.status, BytesIn: body.bytes.Load(), BytesOut: rw.bytes, DurationMS: float64(time.Since(start)) / float64(time.Millisecond), Decision: observation.decision, Reason: observation.reason, Grant: observation.grant}
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
