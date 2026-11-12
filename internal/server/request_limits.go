package server

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/anydoor7/tslink/internal/inspect"
	"github.com/anydoor7/tslink/internal/registry"
)

type requestLimitError struct {
	code, message, limit string
	// visitor is what the caller reads: the limit and what happened, without
	// the service name or owner instructions. display is the limit for the
	// owner's warning; limit stays the machine value logged.
	visitor, display string
	status           int
}

// writeLimitRefusal answers a request a limit refused with TSLink's generated
// response: an HTML page for a browser navigation, otherwise the JSON error
// envelope with the stable code (request_body_limit, request_read_timeout),
// so an upload client's XHR or fetch can tell what happened.
func writeLimitRefusal(w http.ResponseWriter, r *http.Request, e *requestLimitError) {
	gatewayErrorPage(w, r, e.status, e.code, e.visitor, "")
}

func bodyLimitError(service string, limit int64) *requestLimitError {
	return &requestLimitError{code: registry.CodeRequestBodyLimit, message: fmt.Sprintf("service %s: request body exceeds %d bytes; adjust --max-request-body", service, limit), limit: fmt.Sprint(limit), display: registry.RequestBodyLimitText(limit), visitor: "Upload too large: this app accepts at most " + registry.RequestBodyLimitText(limit) + " per request. Ask the person who shared it to raise the limit.", status: http.StatusRequestEntityTooLarge}
}

func (e *requestLimitError) Error() string { return e.message }

type requestBudgetKey struct{}
type requestConnKey struct{}
type requestBudgetState struct {
	mu       sync.Mutex
	failure  *requestLimitError
	closing  bool
	readDone chan struct{} // joins timeout classification without locking across Read
}

// Retain at most one warning per limit for this node lifetime, with a count
// and the time of the most recent rejection, so status and doctor say that
// requests are being rejected now. Repeated violations still log; the
// snapshot is republished on the first one and then at most once per
// limitWarningRepublish, so a burst of rejected uploads cannot turn into a
// burst of snapshot writes. A refusal inside the window schedules one
// trailing publish at its end, so the last count of a burst is not left
// unpublished until some unrelated snapshot write.
type serviceLimitWarnings struct {
	mu       sync.Mutex
	now      func() time.Time // captured where the node starts (serverNowFn)
	started  time.Time        // when this node started; the count's scope
	window   time.Duration    // limitWarningRepublish unless a test sets it
	publish  func()           // writes the runtime snapshot; nil in tests that only read
	pending  bool             // a trailing publish is scheduled
	timer    *time.Timer      // the scheduled trailing publish
	stopped  bool             // the node stopped: no further publishes
	warnings []limitWarning
}

// stop cancels a scheduled trailing publish when the node stops, so no
// goroutine outlives the node it reports on.
func (s *serviceLimitWarnings) stop() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopped = true
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	s.pending = false
}

type limitWarning struct {
	view      inspect.WarningView
	count     uint64
	last      time.Time
	published time.Time
}

const limitWarningRepublish = 30 * time.Second

func (s *serviceLimitWarnings) add(w inspect.WarningView) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	if s.now != nil {
		now = s.now()
	}
	for i := range s.warnings {
		prior := &s.warnings[i]
		if prior.view.Code == w.Code {
			prior.count++
			prior.last = now
			window := s.window
			if window == 0 {
				window = limitWarningRepublish
			}
			if since := now.Sub(prior.published); since < window {
				if !s.pending && !s.stopped && s.publish != nil {
					s.pending = true
					s.timer = time.AfterFunc(window-since, s.trailingPublish)
				}
				return false
			}
			prior.published = now
			return true
		}
	}
	s.warnings = append(s.warnings, limitWarning{view: w, count: 1, last: now, published: now})
	return true
}
func (s *serviceLimitWarnings) trailingPublish() {
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return
	}
	s.pending, s.timer = false, nil
	now := time.Now()
	if s.now != nil {
		now = s.now()
	}
	for i := range s.warnings {
		s.warnings[i].published = now
	}
	publish := s.publish
	s.mu.Unlock()
	publish()
}

// limitWarningScope states what the count covers. It is in memory for this
// node only: changing limits restarts the node and starts a new count. Every
// refusal answered with a status (413, the upload 408) also stays in the
// access log; a header timeout happens before any request exists, so it is
// only in this count and the daemon log.
func limitWarningScope(code string, count uint64, started, last time.Time) string {
	since := "since this app's node last started"
	if !started.IsZero() {
		since += " at " + started.UTC().Format(time.RFC3339)
	}
	text := fmt.Sprintf(" Rejected %d request(s) %s; most recent at %s. Changing the limits restarts the node and resets this count.", count, since, last.UTC().Format(time.RFC3339))
	if code != registry.CodeRequestHeaderTimeout {
		text += " Each refusal stays in tslink access log (status 413 or 408)."
	}
	return text
}

func (s *serviceLimitWarnings) snapshot() []inspect.WarningView {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]inspect.WarningView, 0, len(s.warnings))
	for _, w := range s.warnings {
		view := w.view
		if !strings.HasSuffix(view.Message, ".") {
			view.Message += "."
		}
		view.Message += limitWarningScope(view.Code, w.count, s.started, w.last)
		out = append(out, view)
	}
	return out
}

// RequestLimitsMiddleware streams the original body. Deadlines are renewed
// before each read, so backend backpressure is not counted as client idleness.
// No response writer wrapper is introduced: Flush, Hijack and 1xx remain intact.
// A request that declares a body over the limit is refused with 413 at once.
func RequestLimitsMiddleware(service registry.Service, report func(inspect.WarningView), next http.Handler) http.Handler {
	return requestLimits(service, report, false, next)
}

// RequestLimitsAfterAccess is RequestLimitsMiddleware for a backend that sits
// behind an access check, so the caller is told about the limit only once the
// check lets them through. A caller who is refused gets the refusal, never
// the 413 and its app name and configured limit, whatever the size of the
// request: the gateway decides access before limits, and an app's own node
// must answer the same request the same way.
//
// Everything that protects the connection is unchanged and still comes first:
// the connection and header budgets, full duplex, the read-idle budget and the
// bounded disposal of a body nobody read (which a refusal, like a 413, leaves
// unread). Only the answer to a declared oversize waits for the access check.
func RequestLimitsAfterAccess(service registry.Service, report func(inspect.WarningView), access func(http.Handler) http.Handler, backend http.Handler) http.Handler {
	return requestLimits(service, report, true, access(requestRefusalGate(backend)))
}

// requestRefusal is a refusal of the request itself that a limit decided on
// sight of its headers and that is answered once the caller is let through.
type requestRefusal struct {
	status  int
	message string
	limit   *requestLimitError // a limit refusal answers with writeLimitRefusal
	report  func()             // the owner's warning and log, raised only for a caller let through
}

type requestRefusalKey struct{}

func (p *requestRefusal) answer(w http.ResponseWriter, r *http.Request) {
	if p.report != nil {
		p.report()
	}
	accessDeny(r, "limits")
	if p.limit != nil {
		writeLimitRefusal(w, r, p.limit)
		return
	}
	http.Error(w, p.message, p.status)
}

// requestRefusalGate answers a request that limits refused on its headers and
// whose caller the access check has since let through.
func requestRefusalGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if refusal, ok := r.Context().Value(requestRefusalKey{}).(*requestRefusal); ok {
			refusal.answer(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func requestLimits(service registry.Service, report func(inspect.WarningView), afterAccess bool, next http.Handler) http.Handler {
	limits, err := registry.ResolveRequestLimits(service.RequestLimits)
	if err != nil {
		refusal := &requestRefusal{status: http.StatusServiceUnavailable, message: err.Error()}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if afterAccess {
				next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestRefusalKey{}, refusal)))
				return
			}
			refusal.answer(w, r)
		})
	}
	readIdle, _ := time.ParseDuration(limits.ReadTimeout)
	reject := func(e *requestLimitError) {
		slog.Warn("request limit exceeded", "code", e.code, "name", service.Name, "limit", e.limit, "status", e.status)
		if report != nil {
			report(inspect.WarningView{Code: e.code, Severity: "warning", Source: "http.request_limits", Message: fmt.Sprintf("Service %q is rejecting requests over its %s (%s). Raise it with: %s", service.Name, e.code, e.display, registry.RequestLimitCommand(service.Name, e.code))})
		}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body == nil || r.Body == http.NoBody {
			next.ServeHTTP(w, r)
			return
		}
		ctl := http.NewResponseController(w)
		if r.ProtoMajor == 1 {
			// Otherwise Go can drain the original body during Write/Flush,
			// before our deferred disposal deadline has been installed.
			_ = ctl.EnableFullDuplex()
		}
		state := &requestBudgetState{}
		r = r.WithContext(context.WithValue(r.Context(), requestBudgetKey{}, state))
		body := r.Body
		if limits.MaxBodyBytes >= 0 {
			body = http.MaxBytesReader(w, body, limits.MaxBodyBytes)
		}
		wrapped := &progressBody{ReadCloser: body, ctl: ctl, idle: readIdle, state: state, service: service.Name, reject: reject}
		wrapped.http2 = r.ProtoMajor == 2
		if r.ProtoMajor == 1 {
			wrapped.conn, _ = r.Context().Value(requestConnKey{}).(*headerBudgetConn)
		}
		r.Body = wrapped
		// Go's post-handler drain uses its original body, bypassing Read.
		// Bound that disposal even for file handlers, ACL denial and early 413.
		defer wrapped.Close()
		if limits.MaxBodyBytes >= 0 && r.ContentLength > limits.MaxBodyBytes {
			e := bodyLimitError(service.Name, limits.MaxBodyBytes)
			refusal := &requestRefusal{status: e.status, message: e.message, limit: e, report: func() { reject(e) }}
			if !afterAccess {
				refusal.answer(w, r)
				return
			}
			r = r.WithContext(context.WithValue(r.Context(), requestRefusalKey{}, refusal))
		}
		next.ServeHTTP(w, r)
	})
}

type progressBody struct {
	io.ReadCloser
	ctl     *http.ResponseController
	idle    time.Duration
	state   *requestBudgetState
	service string
	reject  func(*requestLimitError)
	eof     bool // protected by state.mu
	conn    *headerBudgetConn
	http2   bool
}

// Disposal is no longer an upload: a steady drip must not prolong it. Give
// small, already-sent bodies a chance to drain/reuse the connection, but never
// wait longer than one second (or the owner's shorter inactivity window).
func (b *progressBody) disposalDeadline() {
	b.state.mu.Lock()
	defer b.state.mu.Unlock()
	if b.state.closing {
		return
	}
	// Serialize the transition with all deadline updates, but never with the
	// underlying Read/Close. Once closing, no read may renew or clear this bound.
	b.state.closing = true
	deadline := time.Time{}
	if !b.eof {
		deadline = time.Now().Add(min(b.idle, time.Second))
	}
	_ = b.ctl.SetReadDeadline(deadline)
}

func (b *progressBody) Close() error {
	// The proxy transport may close before the handler returns. Close itself
	// can drain the original body, so install its deadline before invoking it.
	b.disposalDeadline()
	err := b.ReadCloser.Close()
	if err != nil && b.conn != nil {
		// Go 1.26 does not mark an unsuccessful drain as an early close.
		// Its keep-alive transition must not extend the expired disposal bound.
		b.conn.stopReading()
	}
	return err
}

func (b *progressBody) Read(p []byte) (int, error) {
	b.state.mu.Lock()
	if b.state.closing {
		b.state.mu.Unlock()
		return 0, http.ErrBodyReadAfterClose
	}
	// A ResponseRecorder has no deadline API; real HTTP servers do. Error paths
	// are handled by the underlying read, preserving httptest handler tests.
	if !b.http2 {
		if err := b.ctl.SetReadDeadline(time.Now().Add(b.idle)); err != nil && !errors.Is(err, http.ErrNotSupported) {
			b.state.mu.Unlock()
			return 0, err
		}
	}
	done := make(chan struct{})
	b.state.readDone = done
	var timer *time.Timer
	if b.http2 {
		// HTTP/2 queues future deadline updates and cancellation on its
		// server loop. A delayed cancellation can expire a completed Read
		// during backend work. Own the timer here, and interrupt the stream
		// synchronously with a past deadline only while this Read is active.
		timer = time.AfterFunc(b.idle, func() {
			b.expireRead(done)
		})
	}
	b.state.mu.Unlock()
	n, err := b.ReadCloser.Read(p)
	b.state.mu.Lock()
	defer b.state.mu.Unlock()
	defer close(done)
	if timer != nil {
		timer.Stop()
	}
	b.state.readDone = nil
	if err == io.EOF {
		b.eof = true
	}
	if b.state.closing {
		// A disposal timeout is not a stalled upload. In particular, an in-flight
		// successful read must leave the absolute disposal deadline untouched.
		return n, err
	}
	var tooLarge *http.MaxBytesError
	var failure *requestLimitError
	if errors.As(err, &tooLarge) {
		failure = bodyLimitError(b.service, tooLarge.Limit)
	} else if isTimeout(err) {
		failure = &requestLimitError{code: registry.CodeRequestReadTimeout, message: fmt.Sprintf("service %s: request body made no progress for %s; adjust --request-read-timeout", b.service, b.idle), limit: b.idle.String(), display: b.idle.String(), visitor: "Upload stalled: no data arrived for " + b.idle.String() + ".", status: http.StatusRequestTimeout}
	}
	if failure != nil {
		if b.state.failure == nil {
			b.state.failure = failure
			b.reject(failure)
		}
		return n, failure
	}
	// Remove the read deadline while the proxy is writing to a slow backend or
	// streaming its response. In particular, upgrades must inherit no deadline.
	if !b.http2 {
		_ = b.ctl.SetReadDeadline(time.Time{})
	}
	return n, err
}

func (b *progressBody) expireRead(done chan struct{}) {
	b.state.mu.Lock()
	defer b.state.mu.Unlock()
	if b.state.readDone == done && !b.state.closing {
		_ = b.ctl.SetReadDeadline(time.Now().Add(-time.Nanosecond))
	}
}

func requestFailure(r *http.Request, err error) *requestLimitError {
	var failure *requestLimitError
	if errors.As(err, &failure) {
		return failure
	}
	// net/http cancels the request context as soon as a socket read times out.
	// The transport can report cancellation before the body wrapper returns;
	// join that in-flight read to preserve the specific 408 rather than a 502.
	if r.Context().Err() != nil {
		if state, ok := r.Context().Value(requestBudgetKey{}).(*requestBudgetState); ok {
			state.mu.Lock()
			done := state.readDone
			state.mu.Unlock()
			if done != nil {
				<-done
			}
			state.mu.Lock()
			defer state.mu.Unlock()
			return state.failure
		}
	}
	return nil
}

// Header timeouts happen before a handler exists. Observe them at the
// decrypted HTTP/1 connection, return 408, and retain the same owner warning.
type headerBudgetListener struct {
	net.Listener
	timeout  string
	report   func(inspect.WarningView)
	service  string
	once     sync.Once
	mu       sync.Mutex
	closed   bool
	pending  map[net.Conn]struct{}
	ready    chan preparedHTTPConn
	done     chan struct{}
	tlsSlots sync.Map // concrete *tls.Conn -> original capped connection
	workers  sync.WaitGroup
}
type preparedHTTPConn struct {
	conn     net.Conn
	original net.Conn
	err      error
}
type tlsStateConn interface {
	ConnectionState() tls.ConnectionState
	HandshakeContext(context.Context) error
}
type headerBudgetTLSConn struct {
	*headerBudgetConn
	tlsStateConn
}
type headerBudgetConn struct {
	net.Conn
	mu          sync.Mutex
	state       http.ConnState
	started     bool
	rejected    bool
	readStopped bool
	owner       *headerBudgetListener
}

func (l *headerBudgetListener) Accept() (net.Conn, error) {
	l.once.Do(func() { go l.acceptLoop() })
	select {
	case prepared := <-l.ready:
		l.mu.Lock()
		delete(l.pending, prepared.original)
		closed := l.closed
		l.mu.Unlock()
		if closed {
			if prepared.original != nil {
				l.closePrepared(prepared.original, prepared.conn)
			}
			return nil, net.ErrClosed
		}
		return prepared.conn, prepared.err
	case <-l.done:
		return nil, net.ErrClosed
	}
}

// Handshake outside the accept loop, under the connection cap and header
// timeout. Go 1.26's ALPN dispatch requires a concrete *tls.Conn. Return that
// connection for h2 and release its cap from ConnState; H1 retains the header
// observer. No plaintext HTTP/2 or protocol sniffing is enabled.
func (l *headerBudgetListener) acceptLoop() {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			// net/http owns the retry/backoff decision. Keep the accept loop
			// available for its next call; Close stops it after a fatal error.
			select {
			case l.ready <- preparedHTTPConn{err: err}:
				continue
			case <-l.done:
				return
			}
		}
		l.mu.Lock()
		if l.closed {
			l.mu.Unlock()
			_ = c.Close()
			return
		}
		l.pending[c] = struct{}{}
		l.workers.Add(1)
		l.mu.Unlock()
		go l.prepare(c)
	}
}

func (l *headerBudgetListener) prepare(c net.Conn) {
	defer l.workers.Done()
	var native *tls.Conn
	// Closing c also frees its connection-cap slot. Nothing after the
	// handoff to Accept can panic, so c is never closed under net/http.
	defer recoverConnPanic("http", l.service, func() { l.closePrepared(c, native) })
	handshake := func(ctx context.Context) error { return native.HandshakeContext(ctx) }
	switch c := c.(type) {
	case *tls.Conn:
		native = c
	case *limitedTLSConn:
		native = c.tlsConn
		// The capped connection times and reports the handshake; this is the
		// handshake every production TLS connection goes through.
		handshake = c.HandshakeContext
	}
	if native != nil {
		timeout, _ := time.ParseDuration(l.timeout)
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		err := handshake(ctx)
		cancel()
		if err != nil {
			l.mu.Lock()
			delete(l.pending, c)
			l.mu.Unlock()
			_ = c.Close()
			return
		}
	}
	wrapped := &headerBudgetConn{Conn: c, owner: l}
	var prepared net.Conn = wrapped
	if tlsConn, ok := c.(tlsStateConn); ok {
		prepared = &headerBudgetTLSConn{headerBudgetConn: wrapped, tlsStateConn: tlsConn}
	}
	if native != nil && native.ConnectionState().NegotiatedProtocol == "h2" {
		l.tlsSlots.Store(native, c)
		prepared = native
	}
	select {
	case l.ready <- preparedHTTPConn{conn: prepared, original: c}:
	case <-l.done:
		l.closePrepared(c, prepared)
	}
}

func (l *headerBudgetListener) closePrepared(original, prepared net.Conn) {
	l.tlsSlots.Delete(prepared)
	l.mu.Lock()
	delete(l.pending, original)
	l.mu.Unlock()
	closeUnservedHTTPConn(original)
}

// No HTTP response exists yet. Close the raw transport first so TLS close
// notification cannot make listener shutdown wait for a stalled peer.
func closeUnservedHTTPConn(c net.Conn) {
	switch c := c.(type) {
	case *tls.Conn:
		_ = c.NetConn().Close()
	case *limitedTLSConn:
		_ = c.tlsConn.NetConn().Close()
	}
	_ = c.Close()
}

func (l *headerBudgetListener) Close() error {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return net.ErrClosed
	}
	l.closed = true
	close(l.done)
	pending := make([]net.Conn, 0, len(l.pending))
	for c := range l.pending {
		pending = append(pending, c)
	}
	l.mu.Unlock()
	err := l.Listener.Close()
	for _, c := range pending {
		closeUnservedHTTPConn(c)
	}
	// A certificate provider may ignore HandshakeContext cancellation and
	// continue its own lookup after the raw transport is closed. Do not join
	// preparation here: net/http closes listeners synchronously, before it
	// observes Shutdown's context. Each worker retains ownership until it
	// either hands off through Accept or cleans up after seeing l.done.
	return err
}
func (c *headerBudgetConn) budgetConn() *headerBudgetConn { return c }

func (c *headerBudgetConn) stopReading() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.readStopped = true
	_ = c.Conn.SetReadDeadline(time.Now())
}

func (c *headerBudgetConn) SetReadDeadline(deadline time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.readStopped {
		deadline = time.Now()
	}
	return c.Conn.SetReadDeadline(deadline)
}
func (c *headerBudgetConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	c.mu.Lock()
	defer c.mu.Unlock()
	if n > 0 {
		c.started = true
	}
	if isTimeout(err) && !c.readStopped && !c.rejected && (c.state == http.StateNew || (c.state == http.StateIdle && c.started)) {
		c.rejected = true
		// A keep-alive with no next request is an ordinary idle close, not a warning.
		// The caller may be anyone in front of every gate, so the body names
		// neither the service nor an owner flag; the owner's warning does.
		msg := fmt.Sprintf("request_header_timeout: request headers did not arrive within %s", c.owner.timeout)
		if !c.started {
			// A new connection that sent no request byte at all is an idle
			// connection (Funnel and edge relays open them ahead of use), not a
			// slow client: it still gets the 408 and is closed, but it is not a
			// rejected request and does not tell the owner to raise the limit.
			slog.Info("idle connection closed before a request", "code", registry.CodeRequestHeaderTimeout, "name", c.owner.service, "limit", c.owner.timeout, "request_bytes", 0, "status", 408)
		} else {
			slog.Warn("request limit exceeded", "code", registry.CodeRequestHeaderTimeout, "name", c.owner.service, "limit", c.owner.timeout, "status", 408)
		}
		if c.owner.report != nil && c.started {
			c.owner.report(inspect.WarningView{Code: registry.CodeRequestHeaderTimeout, Severity: "warning", Source: "http.request_limits", Message: fmt.Sprintf("Service %q is rejecting requests over its %s (%s). Raise it with: %s", c.owner.service, registry.CodeRequestHeaderTimeout, c.owner.timeout, registry.RequestLimitCommand(c.owner.service, registry.CodeRequestHeaderTimeout))})
		}
		_ = c.Conn.SetWriteDeadline(time.Now().Add(time.Second))
		_, _ = fmt.Fprintf(c.Conn, "HTTP/1.1 408 Request Timeout\r\nConnection: close\r\nContent-Type: text/plain; charset=utf-8\r\nX-Content-Type-Options: nosniff\r\nContent-Length: %d\r\n\r\n%s", len(msg), msg)
	}
	return n, err
}

func configureServiceHTTP(srv *http.Server, service registry.Service, ln net.Listener, report func(inspect.WarningView)) net.Listener {
	limits, _ := registry.ResolveRequestLimits(service.RequestLimits)
	srv.ReadHeaderTimeout, _ = time.ParseDuration(limits.HeaderTimeout)
	srv.ReadTimeout = 0 // upload duration is unbounded; progressBody owns read idleness.
	srv.IdleTimeout, _ = time.ParseDuration(limits.IdleTimeout)
	listener := &headerBudgetListener{Listener: ln, service: service.Name, timeout: limits.HeaderTimeout, report: report, pending: make(map[net.Conn]struct{}), ready: make(chan preparedHTTPConn), done: make(chan struct{})}
	priorContext := srv.ConnContext
	srv.ConnContext = func(ctx context.Context, c net.Conn) context.Context {
		if priorContext != nil {
			ctx = priorContext(ctx, c)
		}
		if wrapped, ok := c.(interface{ budgetConn() *headerBudgetConn }); ok {
			ctx = context.WithValue(ctx, requestConnKey{}, wrapped.budgetConn())
		}
		return ctx
	}
	prior := srv.ConnState
	srv.ConnState = func(c net.Conn, state http.ConnState) {
		if state == http.StateClosed {
			if original, ok := listener.tlsSlots.LoadAndDelete(c); ok {
				_ = original.(net.Conn).Close()
			}
		}
		if wrapped, ok := c.(interface{ budgetConn() *headerBudgetConn }); ok {
			b := wrapped.budgetConn()
			b.mu.Lock()
			b.state = state
			if state == http.StateIdle {
				b.started = false
			}
			b.mu.Unlock()
		}
		if prior != nil {
			prior(c, state)
		}
	}
	return listener
}
