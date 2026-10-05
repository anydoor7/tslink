package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/mcpscope"
	"github.com/anydoor7/tslink/internal/registry"
	"github.com/anydoor7/tslink/internal/tailapi"
	"github.com/anydoor7/tslink/internal/testenv"
	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/tailcfg"
)

func TestScopedControlPlaneRejectsInvalidBindings(t *testing.T) {
	cp := &MCPControlPlane{Handler: http.NotFoundHandler(), Bindings: []mcpscope.Binding{{Principal: "agent", Scope: mcpscope.Scope{Role: "custom"}}}}
	if err := cp.Validate(); err == nil {
		t.Fatal("invalid role reached control-plane startup")
	}
	cp.Bindings[0].Scope = mcpscope.Scope{Role: "viewer", Apps: []string{"photos"}}
	if err := cp.Validate(); err != nil {
		t.Fatal("valid control", err)
	}
}

func TestMCPScopeTagBindingMatchesOnlyNodeTagsHTTP(t *testing.T) {
	for _, tagged := range []bool{false, true} {
		var calls atomic.Int32
		cp := &MCPControlPlane{Bindings: []mcpscope.Binding{{Principal: "tag:helper", Scope: mcpscope.Scope{Role: "viewer", Apps: []string{"photos"}}}}, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			s, ok := mcpscope.FromContext(r.Context())
			if !ok || s.Who != "tag:helper" || s.Scope.Role != "viewer" {
				t.Error(s, ok)
			}
			calls.Add(1)
			w.WriteHeader(http.StatusNoContent)
		})}
		who := &apitype.WhoIsResponse{UserProfile: &tailcfg.UserProfile{LoginName: "tag:helper"}, Node: &tailcfg.Node{}}
		if tagged {
			who.Node.Tags = []string{"tag:helper"}
		}
		h := httptest.NewServer(NewMCPControlPlaneHandler(cp, fakeWhoIsClient(t, who, nil)))
		response, err := h.Client().Get(h.URL + MCPControlPlanePath)
		if err != nil {
			h.Close()
			t.Fatal(err)
		}
		response.Body.Close()
		h.Close()
		if tagged {
			if response.StatusCode != 204 || calls.Load() != 1 {
				t.Fatal("tagged control", response.StatusCode, calls.Load())
			}
		} else if response.StatusCode != 403 || calls.Load() != 0 {
			t.Fatal("login acquired tag authority", response.StatusCode, calls.Load())
		}
	}
}

func TestMCPScopeHTTPWhoIsTagAndClockCapture(t *testing.T) {
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	old := serverNowFn
	serverNowFn = func() time.Time { return now }
	defer func() { serverNowFn = old }()
	exp := now.Add(40 * time.Millisecond)
	cp := &MCPControlPlane{Bindings: []mcpscope.Binding{{Principal: "tag:helper", Scope: mcpscope.Scope{Role: "viewer", Apps: []string{"photos"}}, ExpiresAt: &exp}}, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s, ok := mcpscope.FromContext(r.Context())
		if !ok || s.Who != "tag:helper" || s.Scope.Role != "viewer" {
			t.Error(s, ok)
		}
		<-r.Context().Done()
		w.WriteHeader(204)
	})}
	who := &apitype.WhoIsResponse{UserProfile: &tailcfg.UserProfile{LoginName: "tagged-devices"}, Node: &tailcfg.Node{Tags: []string{"tag:helper"}}}
	h := NewMCPControlPlaneHandler(cp, fakeWhoIsClient(t, who, nil))
	serverNowFn = func() time.Time { panic("handler reread clock seam") }
	// The binding expires 40ms after the captured clock; in virtual time the
	// request must end at exactly that instant, not merely "soon".
	synctest.Test(t, func(t *testing.T) {
		// A virtual-time request bound, so a handler that ignored expiry ends
		// with a wrong elapsed time instead of a bubble deadlock.
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		started := time.Now()
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequestWithContext(ctx, "POST", "https://mcp.test/mcp", nil))
		if elapsed := time.Since(started); rr.Code != 204 || elapsed != 40*time.Millisecond {
			t.Fatalf("status=%d elapsed=%v, want 204 at the 40ms binding expiry", rr.Code, elapsed)
		}
	})
}

func TestMCPRestartReconcilesOnlySelectedGatewayAndKeepsIdentity(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatal(err)
	}
	old := newTSNetServerFn
	var started []string
	newTSNetServerFn = func(svc registry.Service, _, _, _ string) tsnetServer {
		started = append(started, svc.Name)
		return &fakeTSNetServer{}
	}
	defer func() { newTSNetServerFn = old }()
	photos := registry.Service{Name: "photos", Type: registry.TypeFile, Path: t.TempDir()}
	finance := registry.Service{Name: "finance", Type: registry.TypeFile, Path: t.TempDir()}
	path := writeRegistry(t, []registry.Service{photos, finance})
	s, err := New("key", "")
	if err != nil {
		t.Fatal(err)
	}
	defer s.closeAllNodes()
	if err := s.syncNodes(context.Background()); err != nil {
		t.Fatal(err)
	}
	photoNode, financeNode := s.nodes["photos"], s.nodes["finance"]
	nodesDir, err := config.NodesDir()
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(nodesDir, "photos", "identity-marker")
	if err := os.WriteFile(marker, []byte("enrolled-identity"), 0600); err != nil {
		t.Fatal(err)
	}
	cleanupCalls := 0
	s.SetCleanupStaleNodesFn(func(context.Context, []tailapi.CleanupTarget) (tailapi.CleanupResult, error) {
		cleanupCalls++
		return tailapi.CleanupResult{}, nil
	})
	started = nil
	session := mcpscope.Session{Who: "agent", Scope: mcpscope.Scope{Role: "app-operator", Apps: []string{"photos"}, MaxDuration: "1h"}}
	if _, err := registry.RequestAppRestart(path, session, "photos", time.Now); err != nil {
		t.Fatal(err)
	}
	if err := s.syncNodes(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !photoNode.closed.Load() || financeNode.closed.Load() || s.nodes["finance"] != financeNode || len(started) != 1 || started[0] != "photos" || cleanupCalls != 0 {
		t.Fatal("restart widened or changed identity", started, cleanupCalls)
	}
	if data, err := os.ReadFile(marker); err != nil || string(data) != "enrolled-identity" {
		t.Fatal("enrolled state changed", string(data), err)
	}
}
