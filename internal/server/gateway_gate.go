package server

import (
	"context"
	"encoding/json"
	"html/template"
	"net"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/anydoor7/tslink/internal/accesslog"
	"github.com/anydoor7/tslink/internal/output"
	"github.com/anydoor7/tslink/internal/registry"
	"tailscale.com/client/tailscale/apitype"
)

type gatewayWhoKey struct{}

type gatewayGate struct {
	upstreamCheck func(context.Context) string
	path          string
	svc           registry.Service // no route denotes the dashboard connection gate
	lc            *LocalClient
	now           func() time.Time
	authority     string
	dashboard     string
	writer        accesslog.Writer
	expire        func(string, time.Time) (bool, error)
	dropped       atomic.Uint64
	totalDropped  *atomic.Uint64
	mu            sync.Mutex
	lastDrop      time.Time
}

func (g *gatewayGate) who(ctx context.Context, remote string) *apitype.WhoIsResponse {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if g.lc == nil {
		return nil
	}
	who, err := g.lc.WhoIs(ctx, remote)
	if err != nil || who == nil || who.UserProfile == nil {
		return nil
	}
	return who
}

func gatewayTags(who *apitype.WhoIsResponse) []string {
	if who.Node != nil {
		return who.Node.Tags
	}
	return nil
}

func (g *gatewayGate) registry() (*registry.Registry, error) {
	reg, issues, err := registry.PortalPreflight(g.path)
	if err != nil {
		return nil, err
	}
	for _, issue := range issues {
		if g.svc.PortalRoute != nil && issue.Name == g.svc.Name {
			return nil, issue.Err
		}
	}
	if reg.Portal == nil || !reg.Portal.Enabled || reg.Portal.Gateway == nil || !reg.Portal.Gateway.Enabled {
		return nil, registry.CodedError{Code: "registry_unavailable", Message: "gateway registry unavailable"}
	}
	return reg, nil
}

// Admit performs no TLS reads or writes. The returned tls.Conn from ListenTLS
// still has an unstarted handshake when this runs.
func (g *gatewayGate) admit(ctx context.Context, remote string) bool {
	// Remote policy is checked per request and per dial (including pooled
	// connections). Keeping admission out of the snapshot/Server.mu path is
	// defence in depth: route teardown already releases routesMu before waiting.
	who := g.who(ctx, remote)
	if who == nil {
		return false
	}
	reg, err := g.registry()
	if err != nil {
		return false
	}
	return gatewayAdmits(reg, who, g.svc.Name, g.svc.PortalRoute == nil, g.now())
}

// gatewayAdmits is the connection gate's decision for an identified caller.
// The dashboard admits a caller who can see an app or request one; an app
// route admits one who may use some routed app. A non-revoked human
// administrator passes either. name only scopes the administrator probe.
func gatewayAdmits(reg *registry.Registry, who *apitype.WhoIsResponse, name string, dashboard bool, now time.Time) bool {
	tags := gatewayTags(who)
	// A scoped dummy service admits only non-revoked human administrators.
	role := registry.Service{Name: name, Type: registry.TypeProxy, PeopleScoped: true}
	if allowed, _, admin := privateAppAccessAt(reg, role, who.UserProfile.LoginName, tags, now); allowed && admin {
		return true
	}
	_, canRequest := requestVisitor(reg, who)
	for _, svc := range reg.Services {
		decision := AppAccessDecisionAt(reg, svc, who.UserProfile.LoginName, tags, now)
		if dashboard {
			if decision.DirectoryVisible || (canRequest && svc.Requestable) {
				return true
			}
		} else if svc.PortalRoute != nil && decision.Allowed {
			return true
		}
	}
	return false
}

func (g *gatewayGate) drop(remote string) {
	g.dropped.Add(1)
	if g.totalDropped != nil {
		g.totalDropped.Add(1)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()
	if !g.lastDrop.IsZero() && now.Sub(g.lastDrop) < time.Second {
		return
	}
	g.lastDrop = now
	if g.writer != nil {
		app := g.svc.Name
		if g.svc.PortalRoute == nil {
			app = "@portal"
		}
		g.writer.Record(accesslog.Event{Time: now.UTC(), Kind: "tcp_open", App: app, Via: "portal", Decision: "denied", Reason: "conn_gate_drop", Identity: accesslog.Identity{Remote: accesslog.CoarseRemote(remote)}})
	}
}

// Admission is bounded independently of HTTP serving. A stalled WhoIs cannot
// serialize other peers; overload is closed without starting TLS.
const gatewayAdmissionLimit = 64

type gatewayListener struct {
	net.Listener
	gate   *gatewayGate
	ctx    context.Context
	cancel context.CancelFunc
	ready  chan net.Conn
	done   chan struct{}
	err    error // published by closing done
}

func newGatewayListener(ctx context.Context, ln net.Listener, gate *gatewayGate) *gatewayListener {
	ctx, cancel := context.WithCancel(ctx)
	l := &gatewayListener{Listener: ln, gate: gate, ctx: ctx, cancel: cancel, ready: make(chan net.Conn), done: make(chan struct{})}
	go l.admissions()
	return l
}

func (l *gatewayListener) admissions() {
	var workers sync.WaitGroup
	defer close(l.done)
	defer workers.Wait()
	defer l.cancel()
	slots := make(chan struct{}, gatewayAdmissionLimit)
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			l.err = err
			return
		}
		select {
		case slots <- struct{}{}:
		default:
			l.gate.drop(c.RemoteAddr().String())
			_ = c.Close()
			continue
		}
		workers.Add(1)
		go func() {
			defer workers.Done()
			defer func() { <-slots }()
			if l.gate.admit(l.ctx, c.RemoteAddr().String()) {
				// Defence in depth: Accept also selects on cancellation. Avoid
				// offering an already-cancelled connection to the handoff select.
				if l.ctx.Err() == nil {
					select {
					case l.ready <- c:
						return
					case <-l.ctx.Done():
					}
				}
				_ = c.Close()
				return
			}
			l.gate.drop(c.RemoteAddr().String())
			_ = c.Close()
		}()
	}
}

func (l *gatewayListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.ready:
		return c, nil
	case <-l.done:
		return nil, l.err
	case <-l.ctx.Done():
		return nil, net.ErrClosed
	}
}
func (l *gatewayListener) Close() error {
	l.cancel()
	err := l.Listener.Close()
	<-l.done
	return err
}

func gatewaySameOrigin(r *http.Request, origin string) bool {
	if len(r.Header.Values("Origin")) > 1 || len(r.Header.Values("Sec-Fetch-Site")) > 1 || len(r.Header.Values("Sec-Fetch-Mode")) > 1 {
		return false
	}
	site, mode := r.Header.Get("Sec-Fetch-Site"), r.Header.Get("Sec-Fetch-Mode")
	_, hasOrigin := r.Header["Origin"]
	upgrade := r.Header.Get("Upgrade") != "" || strings.Contains(strings.ToLower(r.Header.Get("Connection")), "upgrade")
	if !upgrade && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
		if mode == "navigate" || (mode == "" && site == "" && !hasOrigin) {
			return true
		}
	}
	if site != "" {
		return site == "same-origin" || site == "none"
	}
	return !hasOrigin || r.Header.Get("Origin") == origin
}

func gatewayBrowser(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	if mode := r.Header.Get("Sec-Fetch-Mode"); mode != "" {
		return mode == "navigate"
	}
	if r.Header.Get("Sec-Fetch-Site") != "" {
		return false
	}
	htmlQ, jsonQ := 0.0, 0.0
	for _, part := range strings.Split(r.Header.Get("Accept"), ",") {
		parts := strings.Split(part, ";")
		q := 1.0
		for _, param := range parts[1:] {
			k, v, ok := strings.Cut(strings.TrimSpace(param), "=")
			if ok && k == "q" {
				q, _ = strconv.ParseFloat(v, 64)
			}
		}
		switch strings.TrimSpace(parts[0]) {
		case "text/html":
			htmlQ = q
		case "application/json":
			jsonQ = q
		}
	}
	return htmlQ > 0 && htmlQ >= jsonQ
}

var gatewayPage = template.Must(template.New("gateway-message").Parse(`<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>TSLink</title><h1>{{.Message}}</h1>{{if .Dashboard}}<p><a href="{{.Dashboard}}">Back to your apps</a></p>{{if .Requestable}}<p><a href="{{.Dashboard}}/#request-title">Request access</a></p>{{end}}{{end}}</html>`))

func gatewayErrorPage(w http.ResponseWriter, r *http.Request, status int, reason, message, dashboard string, requestable bool) {
	w.Header().Set("Cache-Control", "no-store")
	if gatewayBrowser(r) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'none'")
		w.WriteHeader(status)
		if r.Method != http.MethodHead {
			_ = gatewayPage.Execute(w, struct {
				Message, Dashboard string
				Requestable        bool
			}{message, dashboard, requestable})
		}
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		_ = json.NewEncoder(w).Encode(output.NewFailureForError(appResponseCommand, registry.CodedError{Code: reason, Message: message}))
	}
}

func gatewayDecisionReason(reg *registry.Registry, svc registry.Service, who *apitype.WhoIsResponse, now time.Time, allowed bool) string {
	tags := gatewayTags(who)
	login, _ := registry.ResolvePersonLoginIn(reg, who.UserProfile.LoginName)
	if len(tags) == 0 {
		for _, p := range reg.People {
			if p.Login == login && p.Revoked {
				return "revoked"
			}
		}
		if reg.Portal != nil {
			if login == reg.Portal.Owner {
				return "owner"
			}
			for _, admin := range reg.Portal.Admins {
				if login == admin {
					return "admin"
				}
			}
		}
		if !allowed {
			_, reason := personDecision(reg, svc, login, tags, now)
			if reason == "expired" {
				return reason
			}
		}
	}
	if allowed {
		return "granted"
	}
	return "no_grant"
}

func sameGatewayTarget(a, b registry.Service) bool {
	return a.Type == b.Type && a.Target == b.Target && a.Path == b.Path && a.File == b.File && a.Upstream == b.Upstream && a.PreserveHost == b.PreserveHost && reflect.DeepEqual(a.PortalRoute, b.PortalRoute) && reflect.DeepEqual(a.RequestLimits, b.RequestLimits)
}

func (g *gatewayGate) handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		visible := false
		var who *apitype.WhoIsResponse
		var reg *registry.Registry
		deny := func(reason string, requestable bool) {
			accessDeny(r, reason)
			// The dashboard link follows the same rule as on an app's own
			// node: given to a caller who could open it, so the page is the
			// same on both paths for the same person and app.
			dashboard := ""
			if who != nil && reg != nil {
				dashboard = denialDashboard(g.dashboard, reg, who, g.svc.Name, g.now())
			}
			writeAccessDenial(w, r, accessDenial{Visitor: reason, App: g.svc.Name, Visible: visible, Requestable: requestable, Dashboard: dashboard})
		}
		who = g.who(r.Context(), r.RemoteAddr)
		if who == nil && accessGoneBeforeDecision(r) {
			return
		}
		if who == nil {
			deny("whois_failed", false)
			return
		}
		accessAttest(r, who, nil)
		var err error
		reg, err = g.registry()
		if err != nil {
			deny("registry_unavailable", false)
			return
		}
		now := g.now()
		needsLatch := false
		for _, p := range reg.People {
			for _, grant := range p.Grants {
				if !grant.Expired && grant.ExpiresAt != nil && !now.Before(*grant.ExpiresAt) {
					needsLatch = true
				}
			}
		}
		if needsLatch {
			if _, err := g.expire(g.path, now); err != nil {
				deny("registry_unavailable", false)
				return
			}
			reg, err = g.registry()
			if err != nil {
				deny("registry_unavailable", false)
				return
			}
		}
		var current *registry.Service
		for i := range reg.Services {
			if reg.Services[i].Name == g.svc.Name {
				current = &reg.Services[i]
				break
			}
		}
		if current == nil || current.PortalRoute == nil || !current.PeopleScoped || !sameGatewayTarget(g.svc, *current) {
			deny("route_changed", false)
			return
		}
		decision := AppAccessDecisionAt(reg, *current, who.UserProfile.LoginName, gatewayTags(who), now)
		allowed := decision.Allowed
		visible = decision.DirectoryVisible
		grant, _ := personDecision(reg, *current, who.UserProfile.LoginName, gatewayTags(who), now)
		if _, authoritative := registry.PeopleAccessAt(reg, *current, who.UserProfile.LoginName, gatewayTags(who), now); !authoritative {
			grant = matchedLegacy(who.UserProfile.LoginName, gatewayTags(who), current.AllowedUsers)
		}
		accessAttest(r, who, grant)
		reason := gatewayDecisionReason(reg, *current, who, now, allowed)
		if !allowed {
			deny(reason, current.Requestable)
			return
		}
		if r.Method == http.MethodConnect || r.URL.IsAbs() || r.URL.Host != "" || g.authority == "" {
			deny("no_grant", false)
			return
		}
		if !current.PortalRoute.AllowCrossOrigin && !gatewaySameOrigin(r, "https://"+g.authority) {
			deny("cross_origin_blocked", false)
			return
		}
		if g.upstreamCheck != nil {
			if code := g.upstreamCheck(r.Context()); code != "" {
				accessDeny(r, code)
				gatewayErrorPage(w, r, 503, code, "Remote upstream is unavailable", g.dashboard, false)
				return
			}
		}
		if a, ok := r.Context().Value(accessObservationKey{}).(*accessObservation); ok {
			a.mu.Lock()
			a.reason = reason
			a.mu.Unlock()
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), gatewayWhoKey{}, who)))
	})
}
