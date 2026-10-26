package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/anydoor7/tslink/internal/filelock"
	"github.com/anydoor7/tslink/internal/registry"
	"github.com/anydoor7/tslink/internal/tailapi"
)

type gatewayMutationCase struct {
	name, tool string
	args       map[string]any
}

func gatewayMutationCases() []gatewayMutationCase {
	return []gatewayMutationCase{
		{"enable", "portal_enable", map[string]any{"owner": "owner", "gateway": true}},
		{"route-add", "portal_route_add", map[string]any{"app": "app", "port": 8444}},
		{"route-remove", "portal_route_remove", map[string]any{"app": "app"}},
		{"portal-only", "add", map[string]any{"name": "new-app", "type": "proxy", "target": "localhost:9", "portal_only": true, "no_daemon_install": true}},
		{"remote", "add", map[string]any{"name": "new-app", "type": "proxy", "remote": "https://remote.tailnet.ts.net", "no_daemon_install": true}},
		{"re-add-routed", "add", map[string]any{"name": "app", "type": "proxy", "target": "localhost:10", "no_daemon_install": true}},
		{"remove-routed", "unshare", map[string]any{"name": "app"}},
		{"health-set-routed", "health_set", map[string]any{"app": "app", "path": "/health"}},
		{"disable", "portal_disable", map[string]any{}},
	}
}
func gatewayAuthorityPaths(t *testing.T) sharePaths {
	t.Helper()
	paths := gatewayCommandPaths(t)
	paths.Ownership = filepath.Join(filepath.Dir(paths.Registry), "ownership.json")
	if _, err := changePortal(paths, portalArguments{Owner: "owner", Gateway: true}, true); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Add(paths.Registry, registry.Service{Name: "app", Type: registry.TypeProxy, Target: "http://localhost:9", PortalRoute: &registry.PortalRoute{Port: 8443}}); err != nil {
		t.Fatal(err)
	}
	old := deleteDevicesFn
	t.Cleanup(func() { deleteDevicesFn = old })
	deleteDevicesFn = func(context.Context, tailapi.CleanupTarget) (tailapi.CleanupResult, error) {
		return tailapi.CleanupResult{}, tailapi.ErrNoAPIClient
	}
	return paths
}

func TestGatewayMCPServiceMutationsRequireOwner(t *testing.T) {
	for _, tc := range gatewayMutationCases()[3:] {
		t.Run(tc.name, func(t *testing.T) {
			paths := gatewayAuthorityPaths(t)
			actions := defaultMCPActions(paths, io.Discard)
			outsider := httptest.NewServer(mcpHTTPControlPlaneHandler(t, []string{"operator"}, "operator", actions))
			defer outsider.Close()
			before, err := os.ReadFile(paths.Registry)
			if err != nil {
				t.Fatal(err)
			}
			result := portalRemoteCall(t, outsider, tc.tool, tc.args)
			if !bytes.Contains(result, []byte("access_request_owner_required")) {
				t.Fatalf("non-owner mutated gateway: %s", result)
			}
			after, err := os.ReadFile(paths.Registry)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("refusal changed registry", err)
			}
			owner := httptest.NewServer(mcpHTTPControlPlaneHandler(t, []string{"owner"}, "owner", actions))
			defer owner.Close()
			result = portalRemoteCall(t, owner, tc.tool, tc.args)
			if bytes.Contains(result, []byte(`"isError":true`)) {
				t.Fatalf("owner control failed: %s", result)
			}
			after, err = os.ReadFile(paths.Registry)
			if err != nil || bytes.Equal(before, after) {
				t.Fatal("owner control did not mutate registry", err)
			}
		})
	}
}

// Reuse the context-check/registry-lock seam used by the MCP transaction tests.
// The changed owner is published while this action owns the lock, immediately
// before its registry read. This deterministically models a completed owner
// change after every pre-lock check, without an unsynchronized second writer.
// Moving authorization outside the transaction reads the previous owner.
type gatewayOwnerChangeContext struct {
	context.Context
	lock    *os.File
	path    string
	data    []byte
	mu      sync.Mutex
	changed bool
}

func (c *gatewayOwnerChangeContext) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.changed {
		return c.Context.Err()
	}
	acquired, err := filelock.TryLock(c.lock)
	if err != nil {
		return err
	}
	if acquired {
		if err := filelock.Unlock(c.lock); err != nil {
			return err
		}
		return c.Context.Err()
	}
	if err := os.WriteFile(c.path, c.data, 0600); err != nil {
		return err
	}
	c.changed = true
	return c.Context.Err()
}

func TestGatewayMCPOwnerChangesBeforeWrite(t *testing.T) {
	for _, tc := range gatewayMutationCases() {
		t.Run(tc.name, func(t *testing.T) {
			paths := gatewayAuthorityPaths(t)
			reg, err := registry.Load(paths.Registry)
			if err != nil {
				t.Fatal(err)
			}
			reg.Portal.Owner = "replacement"
			data, err := json.Marshal(reg)
			if err != nil {
				t.Fatal(err)
			}
			lock, err := os.OpenFile(paths.Registry+".lock", os.O_RDWR, 0600)
			if err != nil {
				t.Fatal(err)
			}
			defer lock.Close()
			var injected atomic.Pointer[gatewayOwnerChangeContext]
			wrap := func(ctx context.Context) context.Context {
				if err := requireRequestOwner(ctx, paths.Registry); err != nil {
					t.Error("pre-check control refused original owner", err)
				}
				next := &gatewayOwnerChangeContext{Context: ctx, lock: lock, path: paths.Registry, data: data}
				injected.Store(next)
				return next
			}
			actions := defaultMCPActions(paths, io.Discard)
			switch tc.tool {
			case "portal_enable", "portal_disable":
				original := actions.portalChange
				actions.portalChange = func(ctx context.Context, a portalArguments, b bool) (any, error) { return original(wrap(ctx), a, b) }
			case "portal_route_add", "portal_route_remove":
				original := actions.portalRouteChange
				actions.portalRouteChange = func(ctx context.Context, a portalRouteArguments, b bool) (any, error) {
					return original(wrap(ctx), a, b)
				}
			case "add":
				original := actions.add
				actions.add = func(ctx context.Context, a AddParams, b bool) (any, error) { return original(wrap(ctx), a, b) }
			case "unshare":
				original := actions.unshare
				actions.unshare = func(ctx context.Context, a string) (any, error) { return original(wrap(ctx), a) }
			case "health_set":
				original := actions.healthSet
				actions.healthSet = func(ctx context.Context, a healthSetArguments) (any, error) { return original(wrap(ctx), a) }
			}
			srv := httptest.NewServer(mcpHTTPControlPlaneHandler(t, []string{"owner"}, "owner", actions))
			defer srv.Close()
			result := portalRemoteCall(t, srv, tc.tool, tc.args)
			observed := injected.Load()
			if observed == nil {
				t.Fatal("transaction action did not execute")
			}
			observed.mu.Lock()
			changed := observed.changed
			observed.mu.Unlock()
			if !changed {
				t.Fatal("owner-change seam did not execute under registry lock")
			}
			if !bytes.Contains(result, []byte("access_request_owner_required")) {
				t.Fatalf("stale owner authorized write: %s", result)
			}
			after, err := os.ReadFile(paths.Registry)
			if err != nil || !bytes.Equal(after, data) {
				t.Fatal("denied operation modified the new owner's registry", err)
			}
		})
	}
}

func TestGatewayOffMCPDisablePreservesAuthorityBehavior(t *testing.T) {
	paths := gatewayCommandPaths(t)
	if _, err := changePortal(paths, portalArguments{Owner: "owner", Gateway: false, GatewaySet: true}, true); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(mcpHTTPControlPlaneHandler(t, []string{"operator"}, "operator", defaultMCPActions(paths, io.Discard)))
	defer srv.Close()
	result := portalRemoteCall(t, srv, "portal_disable", map[string]any{})
	reg, err := registry.Load(paths.Registry)
	if err != nil || reg.Portal == nil || reg.Portal.Enabled || reg.Portal.Gateway != nil || bytes.Contains(result, []byte(`"isError":true`)) {
		t.Fatalf("gateway-off disable changed: %s registry=%+v err=%v", result, reg, err)
	}
}
