package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIsAllowed_EmptyList(t *testing.T) {
	if !isAllowed("anyone@example.com", nil, nil) {
		t.Error("empty allowedUsers should allow all")
	}
	if !isAllowed("anyone@example.com", nil, []string{}) {
		t.Error("empty allowedUsers slice should allow all")
	}
}

func TestIsAllowed_MatchEmail(t *testing.T) {
	allowed := []string{"alice@example.com", "bob@example.com"}
	if !isAllowed("alice@example.com", nil, allowed) {
		t.Error("alice should be allowed")
	}
	if !isAllowed("bob@example.com", nil, allowed) {
		t.Error("bob should be allowed")
	}
}

func TestIsAllowed_NoMatchEmail(t *testing.T) {
	allowed := []string{"alice@example.com"}
	if isAllowed("eve@example.com", nil, allowed) {
		t.Error("eve should not be allowed")
	}
}

func TestIsAllowed_MatchTag(t *testing.T) {
	allowed := []string{"tag:admin"}
	if !isAllowed("anyone@example.com", []string{"tag:admin", "tag:web"}, allowed) {
		t.Error("node with tag:admin should be allowed")
	}
}

func TestIsAllowed_NoMatchTag(t *testing.T) {
	allowed := []string{"tag:admin"}
	if isAllowed("anyone@example.com", []string{"tag:web"}, allowed) {
		t.Error("node without tag:admin should not be allowed")
	}
}

func TestIsAllowed_MixedEmailAndTag(t *testing.T) {
	allowed := []string{"alice@example.com", "tag:admin"}

	// Match by email
	if !isAllowed("alice@example.com", nil, allowed) {
		t.Error("alice should be allowed by email")
	}

	// Match by tag
	if !isAllowed("bob@example.com", []string{"tag:admin"}, allowed) {
		t.Error("bob should be allowed by tag:admin")
	}

	// No match
	if isAllowed("eve@example.com", []string{"tag:web"}, allowed) {
		t.Error("eve with tag:web should not be allowed")
	}
}

func TestACLMiddleware_NoRestriction(t *testing.T) {
	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	// Empty allow list: should pass through without calling WhoIs (localClient can be nil)
	mw := ACLMiddleware(nil, nil)
	handler := mw(inner)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if !called {
		t.Error("inner handler should have been called")
	}
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

func TestACLMiddleware_NoRestriction_EmptySlice(t *testing.T) {
	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	mw := ACLMiddleware([]string{}, nil)
	handler := mw(inner)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if !called {
		t.Error("inner handler should have been called")
	}
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

func TestACLMiddleware_WhoIsError_ReturnsForbidden(t *testing.T) {
	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	})

	// A LocalClient with no running tailscale will fail WhoIs
	lc := &LocalClient{}
	mw := ACLMiddleware([]string{"alice@example.com"}, lc)
	handler := mw(inner)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if called {
		t.Error("inner handler should NOT have been called when WhoIs fails")
	}
	if rr.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("expected Content-Type application/json, got %q", ct)
	}
}
