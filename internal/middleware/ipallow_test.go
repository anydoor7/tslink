package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIPAllowList_EmptyAllowsAll(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	mw := IPAllowList(nil)
	wrapped := mw(handler)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "1.2.3.4:5678"
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestIPAllowList_AllowedIP(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	mw := IPAllowList([]string{"192.168.1.0/24"})
	wrapped := mw(handler)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "192.168.1.100:1234"
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestIPAllowList_BlockedIP(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	mw := IPAllowList([]string{"192.168.1.0/24"})
	wrapped := mw(handler)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rec.Code)
	}
}

func TestIPAllowList_CIDR(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	mw := IPAllowList([]string{"10.0.0.0/8", "172.16.0.0/12"})
	wrapped := mw(handler)

	tests := []struct {
		ip     string
		expect int
	}{
		{"10.1.2.3:9999", http.StatusOK},
		{"172.20.0.1:9999", http.StatusOK},
		{"192.168.1.1:9999", http.StatusForbidden},
		{"8.8.8.8:9999", http.StatusForbidden},
	}

	for _, tt := range tests {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = tt.ip
		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, req)

		if rec.Code != tt.expect {
			t.Errorf("IP %s: expected %d, got %d", tt.ip, tt.expect, rec.Code)
		}
	}
}

func TestIPAllowList_PlainIPEntry(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Pass a plain IP (not CIDR) as an allow list entry.
	mw := IPAllowList([]string{"192.168.1.50"})
	wrapped := mw(handler)

	// Allowed.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "192.168.1.50:1234"
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 for plain IP match, got %d", rec.Code)
	}

	// Blocked.
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "192.168.1.51:1234"
	rec = httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403 for non-matching plain IP, got %d", rec.Code)
	}
}

func TestIPAllowList_IPv6(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	mw := IPAllowList([]string{"::1"})
	wrapped := mw(handler)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "[::1]:1234"
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 for IPv6 match, got %d", rec.Code)
	}
}

func TestIPAllowList_PlainIPv6Entry(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Pass a plain IPv6 address (not CIDR) to exercise the /128 fallback branch.
	mw := IPAllowList([]string{"2001:db8::1"})
	wrapped := mw(handler)

	// Allowed: exact match.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "[2001:db8::1]:1234"
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 for plain IPv6 match, got %d", rec.Code)
	}

	// Blocked: different IPv6.
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "[2001:db8::2]:1234"
	rec = httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403 for non-matching IPv6, got %d", rec.Code)
	}
}

func TestIPAllowList_InvalidCIDRPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for invalid CIDR/IP entry")
		}
	}()

	IPAllowList([]string{"not-a-valid-ip-or-cidr"})
}

func TestIPAllowList_InvalidRemoteAddr(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	mw := IPAllowList([]string{"192.168.1.0/24"})
	wrapped := mw(handler)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "not-an-ip"
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403 for invalid IP, got %d", rec.Code)
	}
}
