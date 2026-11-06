package server

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

const (
	// handshakeLogWindow bounds the log volume: per service, at most one
	// failure record and one slow-handshake record per window, however many
	// connections a scanner opens.
	handshakeLogWindow = 10 * time.Second
	// handshakeSlowThreshold is the duration above which a successful TLS
	// handshake is reported. A tailnet handshake normally takes tens of
	// milliseconds; seconds means the host or the certificate path is starved.
	handshakeSlowThreshold = 2 * time.Second
	// handshakeIdentityTimeout bounds the WhoIs lookup made while emitting a
	// record. It runs on the flush goroutine, never on a connection.
	handshakeIdentityTimeout = 500 * time.Millisecond
)

// Error classes reported for a failed TLS handshake.
const (
	// handshakeClassTimeout is a timeout on a connection whose ClientHello
	// arrival is not tracked: every connection to a Funnel-enabled app.
	handshakeClassTimeout = "timeout"
	// timeout_before_hello: the handshake budget expired and no ClientHello had
	// been parsed. Either the client or network did not deliver it, or TSLink
	// was too starved to read it; this is not proof the server is innocent.
	handshakeClassTimeoutBeforeHello = "timeout_before_hello"
	// timeout_after_hello: the ClientHello was parsed and the handshake still
	// did not finish in budget (certificate lookup, CPU, or the rest of the
	// exchange stalled).
	handshakeClassTimeoutAfterHello = "timeout_after_hello"
	handshakeClassEOF               = "eof"
	handshakeClassReset             = "reset"
	handshakeClassBadRecord         = "bad_record"
	handshakeClassClientAlert       = "client_alert"
	handshakeClassProtocol          = "protocol"
	handshakeClassOther             = "other"
)

// handshakeTeardown reports an error caused by this process closing or
// cancelling the connection (reload, shutdown). It is not evidence about the
// network or the client, so it is not counted.
func handshakeTeardown(err error) bool {
	return errors.Is(err, net.ErrClosed) || errors.Is(err, context.Canceled)
}

// classifyHandshakeError names the failure without echoing the error text,
// which embeds local and remote addresses. detail is non-empty only for a
// TLS alert sent by the client, whose text is a fixed protocol string.
func classifyHandshakeError(err error) (class, detail string) {
	var recordErr tls.RecordHeaderError
	if errors.As(err, &recordErr) {
		return handshakeClassBadRecord, ""
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) && opErr.Op == "remote error" && opErr.Err != nil {
		return handshakeClassClientAlert, opErr.Err.Error()
	}
	// TSLink serves on gVisor (tsnet): gonet reports a tcpip error as
	// &net.OpError{Err: errors.New(err.String())}, a plain string, never a
	// syscall errno. "connection reset by peer", "connection aborted" and
	// "endpoint is closed for send" are fixed strings from gvisor
	// tcpip/errors.go; "broken pipe" is syscall.EPIPE's text, kept for
	// completeness. Caveat: gonet also returns "endpoint is closed for send"
	// after a LOCAL close. Today a local close only follows listener close
	// (events dropped as teardown) or the context-deadline path (reported as a
	// timeout first), but a future local-close path would be logged as reset.
	if errors.As(err, &opErr) && opErr.Err != nil {
		switch opErr.Err.Error() {
		case "connection reset by peer", "connection aborted", "broken pipe", "endpoint is closed for send":
			return handshakeClassReset, ""
		}
	}
	var netErr net.Error
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &netErr) && netErr.Timeout():
		return handshakeClassTimeout, ""
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return handshakeClassEOF, ""
	case errors.Is(err, syscall.ECONNRESET), errors.Is(err, syscall.EPIPE), errors.Is(err, syscall.ECONNABORTED):
		return handshakeClassReset, ""
	case err != nil && strings.HasPrefix(err.Error(), "tls: "):
		return handshakeClassProtocol, ""
	}
	return handshakeClassOther, ""
}

type handshakeWindow struct {
	failed      int
	byClass     map[string]int
	firstClass  string
	firstDetail string
	firstAddr   string
	maxDuration time.Duration
	slow        int
	slowMax     time.Duration
	slowFirst   string
}

// handshakeMonitor measures TLS handshakes on one service's listener and logs
// aggregated evidence of failures and slow successes. Counters are cumulative
// for the life of the monitor; log records are rate limited per window.
type handshakeMonitor struct {
	service   string
	window    time.Duration
	threshold time.Duration
	now       func() time.Time
	// after schedules f once d has elapsed and returns a stop function.
	after    func(d time.Duration, f func()) func() bool
	identity *IdentityResolver

	mu      sync.Mutex
	pending *handshakeWindow
	stop    func() bool
	closed  bool

	Succeeded atomic.Int64
	Failed    atomic.Int64
	Slow      atomic.Int64
}

func newHandshakeMonitor(service string, identity *IdentityResolver) *handshakeMonitor {
	return &handshakeMonitor{
		service:   service,
		window:    handshakeLogWindow,
		threshold: handshakeSlowThreshold,
		now:       time.Now,
		after: func(d time.Duration, f func()) func() bool {
			return time.AfterFunc(d, f).Stop
		},
		identity: identity,
	}
}

// helloState says whether the TLS ClientHello arrived before the handshake
// ended. helloUntracked means this connection's TLS config has no hook.
type helloState int

const (
	helloUntracked helloState = iota
	helloNotSeen
	helloSeen
)

// observe records the outcome of one handshake that took d.
func (m *handshakeMonitor) observe(remoteAddr string, d time.Duration, err error, hello helloState) {
	if m == nil {
		return
	}
	if err != nil {
		if handshakeTeardown(err) {
			return
		}
		m.Failed.Add(1)
		class, detail := classifyHandshakeError(err)
		if class == handshakeClassTimeout {
			switch hello {
			case helloNotSeen:
				class = handshakeClassTimeoutBeforeHello
			case helloSeen:
				class = handshakeClassTimeoutAfterHello
			}
		}
		m.record(func(w *handshakeWindow) {
			if w.failed == 0 {
				w.firstClass, w.firstDetail, w.firstAddr = class, detail, remoteAddr
			}
			w.failed++
			if w.byClass == nil {
				w.byClass = map[string]int{}
			}
			w.byClass[class]++
			if d > w.maxDuration {
				w.maxDuration = d
			}
		})
		return
	}
	m.Succeeded.Add(1)
	if d < m.threshold {
		return
	}
	m.Slow.Add(1)
	m.record(func(w *handshakeWindow) {
		if w.slow == 0 {
			w.slowFirst = remoteAddr
		}
		w.slow++
		if d > w.slowMax {
			w.slowMax = d
		}
	})
}

func (m *handshakeMonitor) record(update func(*handshakeWindow)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return
	}
	if m.pending == nil {
		m.pending = &handshakeWindow{}
		m.stop = m.after(m.window, m.flush)
	}
	update(m.pending)
}

// flush emits the pending window, if any, and starts the next one on demand.
func (m *handshakeMonitor) flush() {
	m.mu.Lock()
	w := m.pending
	m.pending, m.stop = nil, nil
	m.mu.Unlock()
	if w == nil {
		return
	}
	m.emit(w, true)
}

// close emits what is pending and stops accepting events, so a reload does not
// lose the last window or leave a timer behind.
func (m *handshakeMonitor) close() {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.closed = true
	if m.stop != nil {
		m.stop()
	}
	w := m.pending
	m.pending, m.stop = nil, nil
	m.mu.Unlock()
	if w != nil {
		// Listener Close runs under http.Server's lock during reload and
		// shutdown, so it must not wait on LocalAPI: use the WhoIs cache only.
		m.emit(w, false)
	}
}

func (m *handshakeMonitor) client(addr string, lookup bool) string {
	if m.identity == nil || addr == "" {
		return "unknown"
	}
	if !lookup {
		if node := m.identity.cachedNode(addr); node != "" {
			return node
		}
		return "unknown"
	}
	ctx, cancel := context.WithTimeout(context.Background(), handshakeIdentityTimeout)
	defer cancel()
	if _, node := m.identity.Principal(ctx, addr); node != "" {
		return node
	}
	return "unknown"
}

func formatClassCounts(counts map[string]int) string {
	names := make([]string, 0, len(counts))
	for name := range counts {
		names = append(names, name)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, fmt.Sprintf("%s=%d", name, counts[name]))
	}
	return strings.Join(parts, ",")
}

// emit writes one record per kind. Fields carry no addresses, request data or
// login names: only the service, counts, error classes, durations and the
// client's node name when WhoIs already knows it.
func (m *handshakeMonitor) emit(w *handshakeWindow, lookup bool) {
	if w.failed > 0 {
		attrs := []any{
			"code", "tls.handshake.failed",
			"name", m.service,
			"count", w.failed,
			"window_seconds", int(m.window / time.Second),
			"classes", formatClassCounts(w.byClass),
			"first_class", w.firstClass,
			"first_client", m.client(w.firstAddr, lookup),
			"max_duration_ms", w.maxDuration.Milliseconds(),
			"total_failed", m.Failed.Load(),
		}
		if w.firstDetail != "" {
			attrs = append(attrs, "first_alert", w.firstDetail)
		}
		slog.Warn("tls handshake failures", attrs...)
	}
	if w.slow > 0 {
		slog.Warn("tls handshake slow",
			"code", "tls.handshake.slow",
			"name", m.service,
			"count", w.slow,
			"window_seconds", int(m.window/time.Second),
			"threshold_ms", m.threshold.Milliseconds(),
			"max_duration_ms", w.slowMax.Milliseconds(),
			"first_client", m.client(w.slowFirst, lookup),
			"total_slow", m.Slow.Load(),
		)
	}
}

// helloConn wraps the raw transport under a tracked TLS connection. The
// GetConfigForClient hook reaches it through ClientHelloInfo.Conn and sets seen
// when a ClientHello has been parsed; the connection reads it back through
// tls.Conn.NetConn. The flag lives and dies with the connection, so there is no
// registry to leak when a connection is dropped before its handshake.
type helloConn struct {
	net.Conn
	seen atomic.Bool
}

// helloTrackingListener is tls.NewListener with a transport wrapper and a
// GetConfigForClient hook (which returns nil, nil and so keeps the config
// unchanged). Only connections accepted here are split into before/after
// hello; others are untracked.
type helloTrackingListener struct {
	net.Listener
	cfg *tls.Config
}

func newHelloTrackingTLSListener(inner net.Listener, cfg *tls.Config) net.Listener {
	cfg = cfg.Clone()
	prior := cfg.GetConfigForClient
	cfg.GetConfigForClient = func(hi *tls.ClientHelloInfo) (*tls.Config, error) {
		if hc, ok := hi.Conn.(*helloConn); ok {
			hc.seen.Store(true)
		}
		if prior != nil {
			return prior(hi)
		}
		return nil, nil
	}
	return &helloTrackingListener{Listener: inner, cfg: cfg}
}

// Accept returns a *tls.Conn (limitedListener needs that type) over a helloConn.
func (l *helloTrackingListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return tls.Server(&helloConn{Conn: c}, l.cfg), nil
}

// takeHelloState reports whether a ClientHello arrived on the connection under
// tc. A conn not accepted from a tracking listener is helloUntracked.
func takeHelloState(tc *tls.Conn) helloState {
	hc, ok := tc.NetConn().(*helloConn)
	switch {
	case !ok:
		return helloUntracked
	case hc.seen.Load():
		return helloSeen
	}
	return helloNotSeen
}
