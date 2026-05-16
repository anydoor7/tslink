package metrics

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics holds all Prometheus metrics for TSLink and owns its own registry.
type Metrics struct {
	RequestsTotal     *prometheus.CounterVec
	RequestDuration   *prometheus.HistogramVec
	ActiveConnections *prometheus.GaugeVec
	ResponseBytes     *prometheus.CounterVec
	registry          *prometheus.Registry
}

// New creates a new Metrics instance with its own isolated Prometheus registry.
func New() *Metrics {
	reg := prometheus.NewRegistry()

	requestsTotal := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "tslink_requests_total",
			Help: "Total number of HTTP requests",
		},
		[]string{"service", "method", "status"},
	)
	requestDuration := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "tslink_request_duration_seconds",
			Help:    "Request duration in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"service", "method"},
	)
	activeConnections := prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "tslink_active_connections",
			Help: "Number of active connections",
		},
		[]string{"service"},
	)
	responseBytes := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "tslink_response_bytes_total",
			Help: "Total bytes sent in responses",
		},
		[]string{"service"},
	)

	reg.MustRegister(requestsTotal, requestDuration, activeConnections, responseBytes)

	return &Metrics{
		RequestsTotal:     requestsTotal,
		RequestDuration:   requestDuration,
		ActiveConnections: activeConnections,
		ResponseBytes:     responseBytes,
		registry:          reg,
	}
}

// responseWriter wraps http.ResponseWriter to capture status code and bytes written.
type responseWriter struct {
	http.ResponseWriter
	status      int
	bytes       int64
	wroteHeader bool
}

func (rw *responseWriter) WriteHeader(code int) {
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

// Flush implements http.Flusher for streaming responses.
func (rw *responseWriter) Flush() {
	if !rw.wroteHeader {
		rw.WriteHeader(http.StatusOK)
	}
	if flusher, ok := rw.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// Hijack implements http.Hijacker, required for WebSocket upgrade (101 Switching Protocols).
func (rw *responseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hj, ok := rw.ResponseWriter.(http.Hijacker); ok {
		return hj.Hijack()
	}
	return nil, nil, fmt.Errorf("underlying ResponseWriter does not implement http.Hijacker")
}

// Middleware wraps an HTTP handler with Prometheus instrumentation.
func (m *Metrics) Middleware(serviceName string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.ActiveConnections.WithLabelValues(serviceName).Inc()
		defer m.ActiveConnections.WithLabelValues(serviceName).Dec()

		start := time.Now()
		rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}

		next.ServeHTTP(rw, r)

		duration := time.Since(start).Seconds()
		statusLabel := fmt.Sprintf("%d", rw.status)

		m.RequestDuration.WithLabelValues(serviceName, r.Method).Observe(duration)
		m.RequestsTotal.WithLabelValues(serviceName, r.Method, statusLabel).Inc()
		m.ResponseBytes.WithLabelValues(serviceName).Add(float64(rw.bytes))
	})
}

// Handler returns the Prometheus metrics HTTP handler for this instance's registry.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}
