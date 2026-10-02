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
		r.Body = wrapped
		// Go's post-handler drain uses its original body, bypassing Read.
		// Bound that disposal even for file handlers, ACL denial and early 413.
		defer wrapped.Close()
		if limits.MaxBodyBytes >= 0 && r.ContentLength > limits.MaxBodyBytes {
			e := &requestLimitError{code: registry.CodeRequestBodyLimit, message: fmt.Sprintf("service %s: request body exceeds %d bytes; adjust --max-request-body", service.Name, limits.MaxBodyBytes), limit: fmt.Sprint(limits.MaxBodyBytes), status: http.StatusRequestEntityTooLarge}
			reject(e)
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
	return b.ReadCloser.Close()
}

func (b *progressBody) Read(p []byte) (int, error) {
	b.state.mu.Lock()
	if b.state.closing {
		b.state.mu.Unlock()
		return 0, http.ErrBodyReadAfterClose
	}
	// A ResponseRecorder has no deadline API; real HTTP servers do. Error paths
	// are handled by the underlying read, preserving httptest handler tests.
	if err := b.ctl.SetReadDeadline(time.Now().Add(b.idle)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		b.state.mu.Unlock()
		return 0, err
	}
	done := make(chan struct{})
	b.state.readDone = done
	b.state.mu.Unlock()
	n, err := b.ReadCloser.Read(p)
	b.state.mu.Lock()
	defer b.state.mu.Unlock()
	defer close(done)
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
	_ = b.ctl.SetReadDeadline(time.Time{})
	return n, err
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
	timeout string
	report  func(inspect.WarningView)
	service string
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
	mu       sync.Mutex
	state    http.ConnState
	started  bool
	rejected bool
	owner    *headerBudgetListener
}

func (l *headerBudgetListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	wrapped := &headerBudgetConn{Conn: c, owner: l}
	if tlsConn, ok := c.(tlsStateConn); ok {
		return &headerBudgetTLSConn{headerBudgetConn: wrapped, tlsStateConn: tlsConn}, nil
	}
	return wrapped, nil
}
func (c *headerBudgetConn) budgetConn() *headerBudgetConn { return c }
func (c *headerBudgetConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	c.mu.Lock()
	defer c.mu.Unlock()
	if n > 0 {
		c.started = true
	}
	if isTimeout(err) && !c.rejected && (c.state == http.StateNew || (c.state == http.StateIdle && c.started)) {
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
	prior := srv.ConnState
	srv.ConnState = func(c net.Conn, state http.ConnState) {
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
	return &headerBudgetListener{Listener: ln, service: service.Name, timeout: limits.HeaderTimeout, report: report}
}
