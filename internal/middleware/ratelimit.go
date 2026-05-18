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
	lastSeen   time.Time
	mu         sync.Mutex
}

func newLimiter(rps float64) *limiter {
	now := time.Now()
	return &limiter{
		tokens:     rps,
		maxTokens:  rps,
		refillRate: rps,
		lastRefill: now,
		lastSeen:   now,
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
	l.lastSeen = now

	if l.tokens >= 1 {
		l.tokens--
		return true
	}
	return false
}

// RateLimit returns a Middleware that limits requests per second per remote IP.
// It uses a token bucket algorithm. When the limit is exceeded, it responds
// with 429 Too Many Requests.
//
// Each call to RateLimit creates an independent instance with its own per-IP
// limiter map and cleanup goroutine. The cleanup goroutine removes entries
// that have not been seen for 10 minutes and runs every 5 minutes.
func RateLimit(requestsPerSecond float64) Middleware {
	var limiters sync.Map

	// Start a per-instance cleanup goroutine that evicts stale entries.
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			evictStaleLimiters(&limiters, 10*time.Minute)
		}
	}()

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

// evictStaleLimiters removes entries from the sync.Map that have not been
// accessed within the given maxAge duration.
func evictStaleLimiters(limiters *sync.Map, maxAge time.Duration) {
	now := time.Now()
	limiters.Range(func(key, value any) bool {
		lim := value.(*limiter)
		lim.mu.Lock()
		stale := now.Sub(lim.lastSeen) > maxAge
		lim.mu.Unlock()
		if stale {
			limiters.Delete(key)
		}
		return true
	})
}
