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
	"sync"
	"time"

	"github.com/anydoor7/tslink/internal/inspect"
	"github.com/anydoor7/tslink/internal/registry"
)

type requestLimitError struct {
	code, message, limit string
	status               int
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

// Retain at most one warning per limit for this node lifetime. Repeated
// violations still log, but cannot grow snapshots or trigger repeated writes.
type serviceLimitWarnings struct {
	mu       sync.Mutex
	warnings []inspect.WarningView
}

func (s *serviceLimitWarnings) add(w inspect.WarningView) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, prior := range s.warnings {
		if prior.Code == w.Code {
			return false
		}
	}
	s.warnings = append(s.warnings, w)
	return true
}
func (s *serviceLimitWarnings) snapshot() []inspect.WarningView {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]inspect.WarningView(nil), s.warnings...)
}

// RequestLimitsMiddleware streams the original body. Deadlines are renewed
// before each read, so backend backpressure is not counted as client idleness.
// No response writer wrapper is introduced: Flush, Hijack and 1xx remain intact.
func RequestLimitsMiddleware(service registry.Service, report func(inspect.WarningView), next http.Handler) http.Handler {
	limits, err := registry.ResolveRequestLimits(service.RequestLimits)
	if err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			accessDeny(r, "limits")
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
		})
	}
	readIdle, _ := time.ParseDuration(limits.ReadTimeout)
	reject := func(e *requestLimitError) {
		slog.Warn("request limit exceeded", "code", e.code, "name", service.Name, "limit", e.limit, "status", e.status)
		if report != nil {
			report(inspect.WarningView{Code: e.code, Severity: "warning", Source: "http.request_limits", Message: fmt.Sprintf("Service %q hit %s (%s); adjust %s on add or share.", service.Name, e.code, e.limit, registry.RequestLimitFlag(e.code))})
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
			e := &requestLimitError{code: registry.CodeRequestBodyLimit, message: fmt.Sprintf("service %s: request body exceeds %d bytes; adjust --max-request-body", service.Name, limits.MaxBodyBytes), limit: fmt.Sprint(limits.MaxBodyBytes), status: http.StatusRequestEntityTooLarge}
			reject(e)
			accessDeny(r, "limits")
			http.Error(w, e.message, e.status)
			return
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
		failure = &requestLimitError{code: registry.CodeRequestBodyLimit, message: fmt.Sprintf("service %s: request body exceeds %d bytes; adjust --max-request-body", b.service, tooLarge.Limit), limit: fmt.Sprint(tooLarge.Limit), status: http.StatusRequestEntityTooLarge}
	} else if isTimeout(err) {
		failure = &requestLimitError{code: registry.CodeRequestReadTimeout, message: fmt.Sprintf("service %s: request body made no progress for %s; adjust --request-read-timeout", b.service, b.idle), limit: b.idle.String(), status: http.StatusRequestTimeout}
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
	switch c := c.(type) {
	case *tls.Conn:
		native = c
	case *limitedTLSConn:
		native = c.tlsConn
	}
	if native != nil {
		timeout, _ := time.ParseDuration(l.timeout)
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		err := native.HandshakeContext(ctx)
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
		msg := fmt.Sprintf("service %s: request header timeout (%s); adjust --request-header-timeout", c.owner.service, c.owner.timeout)
		slog.Warn("request limit exceeded", "code", registry.CodeRequestHeaderTimeout, "name", c.owner.service, "limit", c.owner.timeout, "status", 408)
		if c.owner.report != nil {
			c.owner.report(inspect.WarningView{Code: registry.CodeRequestHeaderTimeout, Severity: "warning", Source: "http.request_limits", Message: msg})
		}
		_ = c.Conn.SetWriteDeadline(time.Now().Add(time.Second))
		_, _ = fmt.Fprintf(c.Conn, "HTTP/1.1 408 Request Timeout\r\nConnection: close\r\nContent-Type: text/plain\r\nContent-Length: %d\r\n\r\n%s", len(msg), msg)
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
