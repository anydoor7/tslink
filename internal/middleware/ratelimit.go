package middleware

import (
	"net"
	"net/http"
	"sync"
	"time"
)

type limiter struct {
	tokens     float64
	maxTokens  float64
	refillRate float64 // tokens per second
	lastRefill time.Time
	mu         sync.Mutex
}

func newLimiter(rps float64) *limiter {
	return &limiter{
		tokens:     rps,
		maxTokens:  rps,
		refillRate: rps,
		lastRefill: time.Now(),
	}
}

func (l *limiter) allow() bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(l.lastRefill).Seconds()
	l.tokens += elapsed * l.refillRate
	if l.tokens > l.maxTokens {
		l.tokens = l.maxTokens
	}
	l.lastRefill = now

	if l.tokens >= 1 {
		l.tokens--
		return true
	}
	return false
}

// RateLimit returns a Middleware that limits requests per second per remote IP.
// It uses a token bucket algorithm. When the limit is exceeded, it responds
// with 429 Too Many Requests.
func RateLimit(requestsPerSecond float64) Middleware {
	var limiters sync.Map

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip, _, err := net.SplitHostPort(r.RemoteAddr)
			if err != nil {
				ip = r.RemoteAddr
			}

			val, _ := limiters.LoadOrStore(ip, newLimiter(requestsPerSecond))
			lim := val.(*limiter)

			if !lim.allow() {
				http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
