package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/registry"
	"github.com/monody0007/tslink/internal/testenv"
	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/tailcfg"
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

func TestIsAllowed_EmailMatchIsCaseInsensitive(t *testing.T) {
	allowed := []string{"User@Example.com"}
	if !isAllowed("user@example.com", nil, allowed) {
		t.Error("email login match should be case-insensitive")
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

	// WhoIs fails; the stub answers instead of the host's tailscaled.
	lc := fakeWhoIsClient(t, nil, errors.New("whois unavailable"))
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

// fakeWhoIsClient creates a LocalClient that returns the given WhoIs response or error.
func fakeWhoIsClient(t *testing.T, resp *apitype.WhoIsResponse, respErr error) *LocalClient {
	t.Helper()

	return &LocalClient{
		OmitAuth: true,
		Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			if respErr != nil {
				return nil, respErr
			}
			body, err := json.Marshal(resp)
			if err != nil {
				t.Fatalf("json.Marshal() error = %v", err)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(string(body))),
			}, nil
		}),
	}
}

func TestStartNodeLocked_AssemblesAllowedUsersACL(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}

	who := &apitype.WhoIsResponse{
		UserProfile: &tailcfg.UserProfile{LoginName: "unauthorized@example.com"},
		Node:        &tailcfg.Node{},
	}
	fake := &fakeTSNetServer{localClient: fakeWhoIsClient(t, who, nil)}
	oldNew := newTSNetServerFn
	newTSNetServerFn = func(registry.Service, string, string, string) tsnetServer {
		return fake
	}
	t.Cleanup(func() { newTSNetServerFn = oldNew })

	s, err := New("synthetic-auth", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := s.startNodeLocked(context.Background(), registry.Service{
		Name:         "x",
		Type:         registry.TypeFile,
		Path:         t.TempDir(),
		AllowedUsers: []string{"authorized@example.com"},
	}); err != nil {
		t.Fatalf("startNodeLocked() error = %v", err)
	}
	t.Cleanup(func() { s.stopNodeLocked("x") })

	node, ok := s.nodes["x"]
	if !ok || node.httpSrv == nil || node.httpSrv.Handler == nil {
		t.Fatal("started node x did not retain its assembled HTTP handler")
	}
	handler := node.httpSrv.Handler

	deniedReq := httptest.NewRequest(http.MethodGet, "/", nil)
	deniedReq.RemoteAddr = "100.64.0.10:1234"
	denied := httptest.NewRecorder()
	handler.ServeHTTP(denied, deniedReq)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("unauthorized status = %d, want %d", denied.Code, http.StatusForbidden)
	}

	who.UserProfile.LoginName = "authorized@example.com"
	allowedReq := httptest.NewRequest(http.MethodGet, "/", nil)
	allowedReq.RemoteAddr = "100.64.0.11:1234"
	allowed := httptest.NewRecorder()
	handler.ServeHTTP(allowed, allowedReq)
	if allowed.Code != http.StatusOK {
		t.Fatalf("authorized status = %d, want %d", allowed.Code, http.StatusOK)
	}
}

func TestACLMiddleware_WhoIsError_WithFakeTransport(t *testing.T) {
	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	})

	lc := fakeWhoIsClient(t, nil, errors.New("whois unavailable"))
	mw := ACLMiddleware([]string{"alice@example.com"}, lc)
	handler := mw(inner)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "100.64.0.1:1234"
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if called {
		t.Error("inner handler should NOT have been called when WhoIs fails")
	}
	if rr.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rr.Code)
	}
}

func TestACLMiddleware_NilLocalClient_ReturnsForbidden(t *testing.T) {
	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	})

	mw := ACLMiddleware([]string{"alice@example.com"}, nil)
	handler := mw(inner)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if called {
		t.Error("inner handler should NOT have been called when LocalClient is nil")
	}
	if rr.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rr.Code)
	}
}

func TestACLMiddleware_NilUserProfile_ReturnsForbidden(t *testing.T) {
	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	})

	lc := fakeWhoIsClient(t, &apitype.WhoIsResponse{
		UserProfile: nil,
		Node:        &tailcfg.Node{},
	}, nil)
	mw := ACLMiddleware([]string{"alice@example.com"}, lc)
	handler := mw(inner)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "100.64.0.1:1234"
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if called {
		t.Error("inner handler should NOT have been called when UserProfile is nil")
	}
	if rr.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rr.Code)
	}
}

func TestACLMiddleware_AccessDenied(t *testing.T) {
	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	})

	lc := fakeWhoIsClient(t, &apitype.WhoIsResponse{
		UserProfile: &tailcfg.UserProfile{
			LoginName: "eve@example.com",
		},
		Node: &tailcfg.Node{},
	}, nil)

	mw := ACLMiddleware([]string{"alice@example.com"}, lc)
	handler := mw(inner)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "100.64.0.1:1234"
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if called {
		t.Error("inner handler should NOT have been called for denied user")
	}
	if rr.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("expected Content-Type application/json, got %q", ct)
	}
}

func TestACLMiddleware_AccessAllowed(t *testing.T) {
	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	lc := fakeWhoIsClient(t, &apitype.WhoIsResponse{
		UserProfile: &tailcfg.UserProfile{
			LoginName: "alice@example.com",
		},
		Node: &tailcfg.Node{},
	}, nil)

	mw := ACLMiddleware([]string{"alice@example.com"}, lc)
	handler := mw(inner)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "100.64.0.1:1234"
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if !called {
		t.Error("inner handler should have been called for allowed user")
	}
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

func TestACLMiddleware_AccessAllowed_ByTag(t *testing.T) {
	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	lc := fakeWhoIsClient(t, &apitype.WhoIsResponse{
		UserProfile: &tailcfg.UserProfile{
			LoginName: "bob@example.com",
		},
		Node: &tailcfg.Node{
			Tags: []string{"tag:admin"},
		},
	}, nil)

	mw := ACLMiddleware([]string{"tag:admin"}, lc)
	handler := mw(inner)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "100.64.0.1:1234"
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if !called {
		t.Error("inner handler should have been called for node with matching tag")
	}
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}
