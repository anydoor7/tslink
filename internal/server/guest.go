package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/anydoor7/tslink/internal/accesslog"
	"github.com/anydoor7/tslink/internal/registry"
	"github.com/fsnotify/fsnotify"
)

const guestCookie = "__Host-TSLinkGuest"
const guestPINCookie = "__Host-TSLinkGuestPIN"
const guestMemoryLimit = 4096

type guestProxyKey struct{}

type guestSession struct {
	id     string
	expiry time.Time
	csrf   [32]byte
}
type guestSource struct {
	attempts int
	until    time.Time
}
type guestGate struct {
	path                 string
	svc                  registry.Service
	now                  func() time.Time
	writer               accesslog.Writer
	private, app         http.Handler
	mu                   sync.Mutex
	sessions, challenges map[[32]byte]guestSession
	sources              map[string]guestSource
	flights              map[*guestFlight]struct{}
	stop, done           chan struct{}
	unsubscribe          func()
	closeOnce            sync.Once
	closed               bool
}

// Constructed synchronously by startNodeLocked; no goroutine reads clock seams.
func newGuestGate(path string, svc registry.Service, now func() time.Time, writer accesslog.Writer, private, app http.Handler) http.Handler {
	g := &guestGate{path: path, svc: svc, now: now, writer: writer, private: private, app: app, sessions: map[[32]byte]guestSession{}, challenges: map[[32]byte]guestSession{}, sources: map[string]guestSource{}, flights: map[*guestFlight]struct{}{}, stop: make(chan struct{}), done: make(chan struct{})}
	g.unsubscribe = registry.WatchGuestCommits(path, g.observe)
	watcher, err := fsnotify.NewWatcher()
	if err == nil {
		if err = watcher.Add(filepath.Dir(path)); err != nil {
			_ = watcher.Close()
			watcher = nil
		}
	}
	go g.monitor(watcher)
	return g
}
func guestNonce() (string, error) {
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		return "", e
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
func guestKey(s string) [32]byte { return sha256.Sum256([]byte(s)) }
func (g *guestGate) prune(at time.Time) {
	for k, s := range g.sessions {
		if !at.Before(s.expiry) {
			delete(g.sessions, k)
		}
	}
	for k, s := range g.challenges {
		if !at.Before(s.expiry) {
			delete(g.challenges, k)
		}
	}
	for k, s := range g.sources {
		if !at.Before(s.until) {
			delete(g.sources, k)
		}
	}
}
func (g *guestGate) record(id, decision, reason string, at time.Time) {
	if g.writer != nil {
		g.writer.Record(accesslog.Event{Time: at.UTC(), Kind: "guest", App: g.svc.Name, Guest: &accesslog.GuestDecision{LinkID: id, App: g.svc.Name, Decision: decision, Reason: reason}, Decision: decision, Reason: reason})
	}
}
func (g *guestGate) deny(w http.ResponseWriter, r *http.Request, id, reason string, at time.Time) {
	accessDeny(r, reason)
	g.record(id, "denied", reason, at)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; frame-ancestors 'none'; base-uri 'none'")
	status := http.StatusUnauthorized
	if reason == "unavailable" {
		status = http.StatusServiceUnavailable
		w.Header().Set("Retry-After", "1")
	}
	w.WriteHeader(status)
	_ = guestDeniedPage.Execute(w, guestPageText(r, false, ""))
}
func setGuestCookie(w http.ResponseWriter, name, value string, expiry time.Time) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode, Expires: expiry})
}
func cookieValue(r *http.Request, name string) string {
	c, e := r.Cookie(name)
	if e != nil || len(c.Value) != 43 {
		return ""
	}
	return c.Value
}
func stripGuestSecrets(r *http.Request) *http.Request {
	r = r.Clone(context.WithValue(r.Context(), guestProxyKey{}, true))
	values := r.Header.Values("Cookie")
	r.Header.Del("Cookie")
	for _, value := range values {
		kept := []string{}
		for _, part := range strings.Split(value, ";") {
			name, _, _ := strings.Cut(strings.TrimSpace(part), "=")
			name = strings.TrimSpace(name)
			if name != guestCookie && name != guestPINCookie {
				kept = append(kept, part)
			}
		}
		if len(kept) > 0 {
			r.Header.Add("Cookie", strings.Join(kept, ";"))
		}
	}
	for name := range r.Header {
		n := strings.ToLower(strings.ReplaceAll(name, "_", "-"))
		if strings.HasPrefix(n, "x-tslink-guest-") {
			r.Header.Del(name)
		}
	}
	r.Header.Del("Referer")
	q := r.URL.Query()
	for _, key := range []string{"token", "pin", "guest_token", "guest_pin"} {
		q.Del(key)
	}
	r.URL.RawQuery = q.Encode()
	return r
}
func (g *guestGate) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	public, _ := r.Context().Value(accessFunnelKey{}).(bool)
	ctl := http.NewResponseController(w)
	if r.ProtoMajor == 1 {
		_ = ctl.EnableFullDuplex()
	}
	// Guest entrypoints have a small form budget and bounded disposal, independent
	// of the app's upload settings. Unauthorized bodies cannot delay the landing.
	budget := &progressBody{ReadCloser: r.Body, ctl: ctl, idle: 10 * time.Second, state: &requestBudgetState{}, service: "guest", reject: func(*requestLimitError) {}}
	if r.ProtoMajor == 1 {
		budget.conn, _ = r.Context().Value(requestConnKey{}).(*headerBudgetConn)
	}
	forwarded := false
	defer func() {
		if !forwarded && r.Body != nil {
			budget.idle = 100 * time.Millisecond
			_ = budget.Close()
		}
	}()
	if !public {
		if strings.HasPrefix(r.URL.Path, "/guest/") {
			w.Header().Set("Referrer-Policy", "no-referrer")
			w.Header().Set("Cache-Control", "no-store")
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		forwarded = true
		g.private.ServeHTTP(w, stripGuestSecrets(r))
		return
	}
	at := g.now()
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.URL.Path == "/guest/pin" {
		r.Body = budget
		g.pin(w, r, at)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/guest/") {
		if r.Method != http.MethodGet {
			g.deny(w, r, "", "invalid_token", at)
			return
		}
		token := strings.TrimPrefix(r.URL.Path, "/guest/")
		view, reason := registry.FindGuestToken(g.path, g.svc.Name, token, at)
		if reason != "allowed" {
			g.deny(w, r, view.ID, reason, at)
			return
		}
		if view.PINRequired {
			nonce, e := guestNonce()
			if e != nil {
				g.deny(w, r, view.ID, "unavailable", at)
				return
			}
			csrf, e := guestNonce()
			if e != nil {
				g.deny(w, r, view.ID, "unavailable", at)
				return
			}
			expiry := at.Add(5 * time.Minute)
			if view.ExpiresAt.Before(expiry) {
				expiry = view.ExpiresAt
			}
			g.mu.Lock()
			g.prune(at)
			full := len(g.challenges) >= guestMemoryLimit
			if !full {
				g.challenges[guestKey(nonce)] = guestSession{id: view.ID, expiry: expiry, csrf: guestKey(csrf)}
			}
			g.mu.Unlock()
			if full {
				g.deny(w, r, view.ID, "rate_limited", at)
				return
			}
			setGuestCookie(w, guestPINCookie, nonce, expiry)
			// The form action contains neither the link token nor any secret query.
			g.form(w, r, csrf, false)
			return
		}
		g.establish(w, r, view, at)
		return
	}
	key := guestKey(cookieValue(r, guestCookie))
	g.mu.Lock()
	session, ok := g.sessions[key]
	g.mu.Unlock()
	if !ok {
		g.deny(w, r, "", "session_required", at)
		return
	}
	// Grant expiry takes priority, preserving the reason even at cookie expiry.
	view, reason := registry.CheckGuest(g.path, g.svc.Name, session.id, at, true, false)
	if reason == "allowed" && !at.Before(session.expiry) {
		reason = "expired"
	}
	if reason != "allowed" {
		if reason != "unavailable" {
			g.mu.Lock()
			delete(g.sessions, key)
			g.mu.Unlock()
		}
		g.deny(w, r, view.ID, reason, at)
		return
	}
	g.record(view.ID, "allowed", "allowed", at)
	forwarded = true
	g.serveApp(w, r, view)
}

func (g *guestGate) form(w http.ResponseWriter, r *http.Request, csrf string, retry bool) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
	_ = guestForm.Execute(w, guestPageText(r, retry, csrf))
}
func (g *guestGate) pin(w http.ResponseWriter, r *http.Request, at time.Time) {
	key := guestKey(cookieValue(r, guestPINCookie))
	g.mu.Lock()
	challenge, ok := g.challenges[key]
	g.mu.Unlock()
	if !ok || !at.Before(challenge.expiry) || r.Method != http.MethodPost {
		g.deny(w, r, "", "csrf", at)
		return
	}
	// A synchronizer token bound to an HttpOnly host-only challenge cookie is
	// required. An Origin, when provided by the browser, must match as well.
	origin := r.Header.Get("Origin")
	if origin != "" {
		u, e := url.Parse(origin)
		if e != nil || u.Scheme != "https" || u.Host != r.Host || u.Path != "" || u.RawQuery != "" || u.User != nil {
			g.deny(w, r, challenge.id, "csrf", at)
			return
		}
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	if e := r.ParseForm(); e != nil {
		g.deny(w, r, challenge.id, "csrf", at)
		return
	}
	csrf := guestKey(r.PostForm.Get("csrf"))
	if len(r.PostForm["csrf"]) != 1 || subtle.ConstantTimeCompare(csrf[:], challenge.csrf[:]) != 1 {
		g.deny(w, r, challenge.id, "csrf", at)
		return
	}
	source, _ := r.Context().Value(guestSourceKey{}).(string)
	if source == "" {
		source = "unknown"
	}
	g.mu.Lock()
	g.prune(at)
	limit := g.sources[source]
	full := len(g.sources) >= guestMemoryLimit && limit.until.IsZero()
	limited := full || limit.attempts >= 10
	if !limited {
		limit.attempts++
		if limit.until.IsZero() {
			limit.until = at.Add(15 * time.Minute)
		}
		g.sources[source] = limit
	}
	g.mu.Unlock()
	if limited {
		g.deny(w, r, challenge.id, "rate_limited", at)
		return
	}
	view, reason := registry.CheckGuestPIN(g.path, g.svc.Name, challenge.id, r.PostForm.Get("pin"), at)
	if reason != "allowed" {
		if reason == "bad_pin" {
			g.record(challenge.id, "denied", reason, at)
			g.form(w, r, r.PostForm.Get("csrf"), true)
			return
		}
		g.deny(w, r, challenge.id, reason, at)
		return
	}
	g.mu.Lock()
	delete(g.challenges, key)
	g.mu.Unlock()
	g.establish(w, r, view, at)
}
func (g *guestGate) establish(w http.ResponseWriter, r *http.Request, view registry.GuestView, at time.Time) {
	nonce, e := guestNonce()
	if e != nil {
		g.deny(w, r, view.ID, "unavailable", at)
		return
	}
	g.mu.Lock()
	g.prune(at)
	full := len(g.sessions) >= guestMemoryLimit
	if !full {
		g.sessions[guestKey(nonce)] = guestSession{id: view.ID, expiry: view.ExpiresAt}
	}
	g.mu.Unlock()
	if full {
		g.deny(w, r, view.ID, "rate_limited", at)
		return
	}
	// Reserve capacity before recording a successful session; no cookie exposes
	// the random nonce until the final grant check and durable write succeed.
	view, reason := registry.CheckGuest(g.path, g.svc.Name, view.ID, at, false, true)
	g.mu.Lock()
	if reason == "allowed" {
		g.sessions[guestKey(nonce)] = guestSession{id: view.ID, expiry: view.ExpiresAt}
	} else {
		delete(g.sessions, guestKey(nonce))
	}
	g.mu.Unlock()
	if reason != "allowed" {
		g.deny(w, r, view.ID, reason, at)
		return
	}
	setGuestCookie(w, guestCookie, nonce, view.ExpiresAt)
	http.SetCookie(w, &http.Cookie{Name: guestPINCookie, Path: "/", HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode, MaxAge: -1})
	g.record(view.ID, "allowed", "allowed", at)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
