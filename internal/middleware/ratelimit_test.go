package middleware

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestRateLimit_AllowsUnderLimit(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	mw := RateLimit(10) // 10 req/s
	wrapped := mw(handler)

	for i := 0; i < 10; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "192.168.1.1:1234"
		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("request %d: expected 200, got %d", i, rec.Code)
		}
	}
}

func TestRateLimit_BlocksOverLimit(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	mw := RateLimit(2) // 2 req/s
	wrapped := mw(handler)

	// Use up all tokens.
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "10.0.0.1:5678"
		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, req)
	}

	// Next request should be blocked.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.1:5678"
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429, got %d", rec.Code)
	}
}

func TestRateLimit_RefillsOverTime(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	mw := RateLimit(2) // 2 req/s
	wrapped := mw(handler)

	// Exhaust tokens.
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "10.0.0.2:9999"
		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, req)
	}

	// Wait for refill.
	time.Sleep(600 * time.Millisecond)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.2:9999"
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 after refill, got %d", rec.Code)
	}
}

func TestRateLimit_PerIPIsolation(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	mw := RateLimit(1) // 1 req/s
	wrapped := mw(handler)

	// Exhaust IP A.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.3:1111"
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	// IP B should still work.
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.4:2222"
	rec = httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 for different IP, got %d", rec.Code)
	}
}

func TestRateLimit_RemoteAddrWithoutPort(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	mw := RateLimit(1)
	wrapped := mw(handler)

	// RemoteAddr without port (fallback path).
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.5"
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestEvictStaleLimiters(t *testing.T) {
	var limiters sync.Map

	// Create a limiter that is "old" (last seen 15 minutes ago).
	oldLim := newLimiter(10)
	oldLim.mu.Lock()
	oldLim.lastSeen = time.Now().Add(-15 * time.Minute)
	oldLim.mu.Unlock()
	limiters.Store("10.0.0.1", oldLim)

	// Create a limiter that is "fresh" (last seen just now).
	freshLim := newLimiter(10)
	limiters.Store("10.0.0.2", freshLim)

	// Evict entries older than 10 minutes.
	evictStaleLimiters(&limiters, 10*time.Minute)

	// Old entry should be gone.
	if _, ok := limiters.Load("10.0.0.1"); ok {
		t.Error("expected stale limiter to be evicted")
	}

	// Fresh entry should remain.
	if _, ok := limiters.Load("10.0.0.2"); !ok {
		t.Error("expected fresh limiter to be retained")
	}
}

func TestEvictStaleLimiters_AllStale(t *testing.T) {
	var limiters sync.Map

	for i := 0; i < 5; i++ {
		lim := newLimiter(10)
		lim.mu.Lock()
		lim.lastSeen = time.Now().Add(-20 * time.Minute)
		lim.mu.Unlock()
		limiters.Store(fmt.Sprintf("10.0.0.%d", i), lim)
	}

	evictStaleLimiters(&limiters, 10*time.Minute)

	count := 0
	limiters.Range(func(_, _ any) bool {
		count++
		return true
	})
	if count != 0 {
		t.Errorf("expected all stale limiters evicted, got %d remaining", count)
	}
}
