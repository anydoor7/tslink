package server

import (
	"bufio"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// responseWriter wraps http.ResponseWriter to capture status code and bytes written.
type responseWriter struct {
	http.ResponseWriter
	status      int
	bytes       int64
	wroteHeader bool
}

func (rw *responseWriter) WriteHeader(code int) {
	// Informational responses do not commit the final status. ReverseProxy
	// forwards backend 1xx responses before its final response, so consuming
	// the one final-header slot here would turn a later 503 into an implicit 200.
	if code >= 100 && code < 200 && code != http.StatusSwitchingProtocols {
		rw.ResponseWriter.WriteHeader(code)
		return
	}
	if !rw.wroteHeader {
		rw.status = code
		rw.wroteHeader = true
		rw.ResponseWriter.WriteHeader(code)
	}
}

func (rw *responseWriter) Write(b []byte) (int, error) {
	if !rw.wroteHeader {
		rw.WriteHeader(http.StatusOK)
	}
	n, err := rw.ResponseWriter.Write(b)
	rw.bytes += int64(n)
	return n, err
}

// Unwrap exposes the wrapped ResponseWriter to http.ResponseController.
//
// Without it the controller stops at this wrapper and answers
// SetWriteDeadline with ErrNotSupported, because this type does not implement
// it. The event stream depends on a per-frame write deadline to keep a stalled
// reader from holding its handler goroutine open indefinitely, and every
// control-plane response passes through this middleware, so the deadline would
// be silently unavailable in production while remaining available in any test
// that skipped the middleware. Flush and Hijack stay on this type, so the
// controller still finds them here first and status/byte accounting is
// unaffected.
func (rw *responseWriter) Unwrap() http.ResponseWriter {
	return rw.ResponseWriter
}

// Hijack implements http.Hijacker, required for WebSocket upgrade (101 Switching Protocols).
func (rw *responseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hj, ok := rw.ResponseWriter.(http.Hijacker); ok {
		return hj.Hijack()
	}
	return nil, nil, fmt.Errorf("underlying ResponseWriter does not implement http.Hijacker")
}

// Flush implements http.Flusher for streaming responses when supported.
func (rw *responseWriter) Flush() {
	if !rw.wroteHeader {
		rw.WriteHeader(http.StatusOK)
	}
	if flusher, ok := rw.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// AccessLogMiddleware logs every HTTP request with structured fields via slog.
//
// identity may be nil, and is nil for every node that cannot resolve a caller.
// The login and node fields are then empty, which is the same shape the record
// has for a caller whose identity could not be established -- one schema, so a
// reader parsing these lines never has to handle a missing key.
//
// The two identity fields are the only ones sourced from outside the request
// itself, and they carry exactly what the local Tailscale daemon attested for
// the source address. Nothing here reads an X-Tailscale-* request header: those
// are attacker-controlled on the way in (the proxy strips them for that
// reason), and a log that repeated them would record a claim as a fact.
func AccessLogMiddleware(serviceName string, identity *IdentityResolver, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}

		// Resolved before the handler rather than beside the log call after
		// it. The request context is cancelled the moment the client goes
		// away, and a caller that disconnects mid-response is exactly the one
		// worth attributing; resolving afterwards would lose the identity for
		// every aborted request. On the ordinary path this costs nothing: the
		// answer is cached for the node, and the proxy that runs inside this
		// handler reads the same entry.
		login, node := identity.Principal(r.Context(), r.RemoteAddr)

		next.ServeHTTP(rw, r)

		durationMs := float64(time.Since(start).Nanoseconds()) / 1e6

		slog.Info("access",
			"service", serviceName,
			"method", r.Method,
			"path", r.URL.Path,
			"status", rw.status,
			"duration_ms", durationMs,
			"bytes", rw.bytes,
			"remote_addr", r.RemoteAddr,
			"user_agent", r.UserAgent(),
			"login", login,
			"node", node,
		)
	})
}
