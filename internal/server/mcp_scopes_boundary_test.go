package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/mcpscope"
	"github.com/anydoor7/tslink/internal/registry"
	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/tailcfg"
)

// These probes use the assembled node handler, a real HTTP listener and
// backend/file, and the production per-request people-store reader.
func TestMCPScopedPeoplePreservesOtherAppAccess(t *testing.T) {
	for _, kind := range []string{registry.TypeProxy, registry.TypeFile} {
		for _, known := range []bool{true, false} {
			for _, revoke := range []bool{false, true} {
				label := kind + "/new-person/grant"
				if known {
					label = kind + "/existing-person/grant"
				}
				if revoke {
					label += "-revoke"
				}
				t.Run(label, func(t *testing.T) {
					backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "finance-content") }))
					defer backend.Close()
					svc := registry.Service{Name: "finance", Type: kind, Target: backend.URL, AllowedUsers: []string{"alice"}}
					route := "/"
					if kind == registry.TypeFile {
						svc.Target, svc.Path = "", t.TempDir()
						if err := os.WriteFile(filepath.Join(svc.Path, "balance.txt"), []byte("finance-content"), 0600); err != nil {
							t.Fatal(err)
						}
						route = "/balance.txt"
					}
					who := &apitype.WhoIsResponse{UserProfile: &tailcfg.UserProfile{LoginName: "alice"}}
					fake := &fakeTSNetServer{localClient: fakeWhoIsClient(t, who, nil)}
					handler := startHTTPNode(t, fake, svc)
					path, err := registryPathFn()
					if err != nil {
						t.Fatal(err)
					}
					if _, err := registry.Add(path, registry.Service{Name: "photos", Type: registry.TypeProxy, Target: backend.URL}); err != nil {
						t.Fatal(err)
					}
					if known {
						if _, err := registry.ChangePerson(path, "alice", []string{"finance"}, nil, false, false); err != nil {
							t.Fatal(err)
						}
					}
					h := httptest.NewServer(handler)
					defer h.Close()
					check := func() (int, string) {
						r, err := h.Client().Get(h.URL + route)
						if err != nil {
							t.Fatal(err)
						}
						defer r.Body.Close()
						b, err := io.ReadAll(r.Body)
						if err != nil {
							t.Fatal(err)
						}
						return r.StatusCode, string(b)
					}
					if status, body := check(); status != 200 || body != "finance-content" {
						t.Fatalf("before change: %d %q", status, body)
					}
					session := mcpscope.Session{Who: "manager", Scope: mcpscope.Scope{Role: "people-manager", Apps: []string{"photos"}, MaxDuration: "1h"}}
					now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
					_, changeErr := registry.ChangePersonApp(path, session, "alice", "photos", "30m", revoke, func() time.Time { return now })
					if !known && !revoke {
						if code, _ := registry.ErrorCode(changeErr); code != "mcp_person_owner_required" {
							t.Errorf("new person grant code=%q want=mcp_person_owner_required", code)
						}
					} else if changeErr != nil {
						t.Fatal(changeErr)
					}
					reg, err := registry.Load(path)
					if err != nil {
						t.Fatal(err)
					}
					if !known && len(reg.People) != 0 {
						t.Error("unknown login became a stored person")
					}
					status, body := check()
					t.Logf("known=%v revoke=%v finance HTTP status after photos change=%d", known, revoke, status)
					if status != 200 || body != "finance-content" {
						t.Fatalf("out-of-scope finance access changed: %d %q", status, body)
					}
				})
			}
		}
	}
}

func TestMCPOwnerEventsEndsAtBindingExpiry(t *testing.T) {
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	expiry := now.Add(120 * time.Millisecond)
	previous := serverNowFn
	serverNowFn = func() time.Time { return now }
	defer func() { serverNowFn = previous }()
	cp := &MCPControlPlane{Bindings: []mcpscope.Binding{{Principal: "owner", Scope: mcpscope.Scope{Role: "owner"}, ExpiresAt: &expiry}}, Handler: http.NotFoundHandler(), EventsSnapshot: func(context.Context) (any, error) { return map[string]any{"marker": "owner-snapshot"}, nil }}
	h := httptest.NewServer(NewMCPControlPlaneHandler(cp, fakeWhoIsClient(t, &apitype.WhoIsResponse{UserProfile: &tailcfg.UserProfile{LoginName: "owner"}}, nil)))
	defer h.Close()
	h.Client().Timeout = 2 * time.Second
	started := time.Now()
	response, err := h.Client().Get(h.URL + MCPEventsPath)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	b, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 || !strings.Contains(string(b), "owner-snapshot") {
		t.Fatalf("stream control failed: %d %s", response.StatusCode, b)
	}
	if time.Since(started) > time.Second {
		t.Fatalf("stream continued beyond expiry: %v", time.Since(started))
	}
	t.Logf("owner snapshot received, stream closed after %v", time.Since(started))
}

func TestMCPMultipleMatchingRemoteTagsAreRefused(t *testing.T) {
	viewer := mcpscope.Scope{Role: "viewer", Apps: []string{"photos"}}
	for _, tc := range []struct {
		name        string
		allow, tags []string
		bindings    []mcpscope.Binding
		want        int
	}{
		{"single-scoped-control", nil, []string{"tag:helper"}, []mcpscope.Binding{{Principal: "tag:helper", Scope: viewer}}, 200},
		{"single-legacy-control", []string{"tag:owner"}, []string{"tag:owner"}, nil, 200},
		{"explicit-login-wins", []string{"tag:owner"}, []string{"tag:owner", "tag:helper"}, []mcpscope.Binding{{Principal: "tagged-device", Scope: viewer}, {Principal: "tag:helper", Scope: viewer}}, 200},
		{"legacy-login-wins", []string{"tagged-device", "tag:owner", "tag:other"}, []string{"tag:owner", "tag:other"}, nil, 200},
		{"two-legacy-tags", []string{"tag:owner", "tag:other"}, []string{"tag:owner", "tag:other"}, nil, 403},
		{"duplicate-legacy-tag", []string{"tag:owner", "tag:owner"}, []string{"tag:owner"}, nil, 200},
		{"two-scoped-tags", nil, []string{"tag:helper", "tag:reader"}, []mcpscope.Binding{{Principal: "tag:helper", Scope: viewer}, {Principal: "tag:reader", Scope: viewer}}, 403},
		{"legacy-and-scoped-tags", []string{"tag:owner"}, []string{"tag:owner", "tag:helper"}, []mcpscope.Binding{{Principal: "tag:helper", Scope: viewer}}, 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cp := &MCPControlPlane{AllowedUsers: tc.allow, Bindings: tc.bindings, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				s, _ := mcpscope.FromContext(r.Context())
				io.WriteString(w, s.Scope.Role)
			})}
			lc := fakeWhoIsClient(t, &apitype.WhoIsResponse{UserProfile: &tailcfg.UserProfile{LoginName: "tagged-device"}, Node: &tailcfg.Node{Tags: tc.tags}}, nil)
			h := httptest.NewServer(NewMCPControlPlaneHandler(cp, lc))
			defer h.Close()
			r, err := h.Client().Get(h.URL + MCPControlPlanePath)
			if err != nil {
				t.Fatal(err)
			}
			defer r.Body.Close()
			b, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("matching tags=%v status=%d body=%s", tc.tags, r.StatusCode, b)
			if r.StatusCode != tc.want {
				t.Fatalf("multiple matching tags were resolved: status=%d want=%d body=%s", r.StatusCode, tc.want, b)
			}
		})
	}
}
