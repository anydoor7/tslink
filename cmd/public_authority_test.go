package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/mcpscope"
	"github.com/anydoor7/tslink/internal/registry"
	"github.com/anydoor7/tslink/internal/server"
	"github.com/anydoor7/tslink/internal/tailapi"
	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/tailcfg"
)

// The public rule: over remote HTTP MCP, a change to a publicly exposed app,
// or one that makes an app public, needs the current portal owner, checked
// under the registry lock on the registry the write is based on.
//
// Fixture apps on a gateway install owned by "owner":
//
//	app     routed private app (the gateway rule)
//	priv    private app without a route
//	pub     guest-gated app without a route (the R2-5 shape: first link taken)
//	fun     open Funnel app
//	lapsed  Funnel whose deadline passed and was downgraded; public_ack kept
type publicFixture struct {
	paths   sharePaths
	guestID string
}

func publicAuthorityFixture(t *testing.T, owner string, gateway bool) publicFixture {
	t.Helper()
	paths := gatewayCommandPaths(t)
	paths.Ownership = paths.Registry[:len(paths.Registry)-len("registry.json")] + "ownership.json"
	if owner != "" || gateway {
		args := portalArguments{Owner: owner, Gateway: gateway, GatewaySet: true}
		if _, err := changePortal(paths, args, true); err != nil {
			t.Fatal(err)
		}
	}
	add := func(svc registry.Service) {
		t.Helper()
		if _, err := registry.Add(paths.Registry, svc); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC()
	if gateway {
		add(registry.Service{Name: "app", Type: registry.TypeProxy, Target: "http://localhost:9", PortalRoute: &registry.PortalRoute{Port: 8443}})
	}
	add(registry.Service{Name: "priv", Type: registry.TypeProxy, Target: "http://localhost:10"})
	add(registry.Service{Name: "pub", Type: registry.TypeProxy, Target: "http://localhost:11"})
	open := now.Add(48 * time.Hour)
	add(registry.Service{Name: "fun", Type: registry.TypeProxy, Target: "http://localhost:12", Funnel: true, PublicAck: true, FunnelExpiresAt: &open})
	past := now.Add(-time.Hour)
	add(registry.Service{Name: "lapsed", Type: registry.TypeProxy, Target: "http://localhost:13", PublicAck: true, FunnelExpiresAt: &past})
	// The link is created after every service, on the wall clock, so it
	// authorizes (a grant older than its app's created_at never does).
	created, err := createGuest(paths, guestArguments{App: "pub", For: "2h", Public: true}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if _, reason := registry.CheckGuest(paths.Registry, "pub", created.Grant.ID, time.Now(), false, false); reason != "allowed" {
		t.Fatalf("fixture link does not authorize: %q", reason)
	}
	reg, err := registry.Load(paths.Registry)
	if err != nil {
		t.Fatal(err)
	}
	for _, svc := range reg.Services {
		if svc.Name == "pub" && (!svc.GuestGate || svc.PortalRoute != nil) {
			t.Fatalf("fixture: pub = %+v", svc)
		}
		if svc.Name == "priv" && (svc.PubliclyExposed() || svc.PortalRoute != nil) {
			t.Fatalf("fixture: priv = %+v", svc)
		}
	}
	old := deleteDevicesFn
	t.Cleanup(func() { deleteDevicesFn = old })
	deleteDevicesFn = func(context.Context, tailapi.CleanupTarget) (tailapi.CleanupResult, error) {
		return tailapi.CleanupResult{}, tailapi.ErrNoAPIClient
	}
	guestAuthorityRuntime(t, paths)
	// share registers before it waits for the URL; a running daemon lets the
	// row reach the registration, and the wait is not what is under test.
	oldRunning := shareIsRunningFn
	t.Cleanup(func() { shareIsRunningFn = oldRunning })
	shareIsRunningFn = func(string) bool { return true }
	return publicFixture{paths: paths, guestID: created.Grant.ID}
}

// publicMutationCase is one row of the mutation table. ownerSaves reports
// whether the portal owner's control call is expected to commit a change (a
// share waits for a URL the fixture never publishes and rolls back).
type publicMutationCase struct {
	name, tool string
	args       func(publicFixture) map[string]any
	ownerSaves bool
}

func publicMutationCases() []publicMutationCase {
	static := func(m map[string]any) func(publicFixture) map[string]any {
		return func(publicFixture) map[string]any { return m }
	}
	return []publicMutationCase{
		{"add-retarget-funnel", "add", static(map[string]any{"name": "fun", "type": "proxy", "target": "localhost:22", "funnel": true, "public_ack": true, "funnel_ttl": "2h", "no_daemon_install": true}), true},
		{"add-makes-funnel-private", "add", static(map[string]any{"name": "fun", "type": "proxy", "target": "localhost:12", "funnel": false, "no_daemon_install": true}), true},
		{"add-private-to-funnel", "add", static(map[string]any{"name": "priv", "type": "proxy", "target": "localhost:10", "funnel": true, "public_ack": true, "funnel_ttl": "2h", "no_daemon_install": true}), true},
		{"add-new-funnel", "add", static(map[string]any{"name": "newpub", "type": "proxy", "target": "localhost:14", "funnel": true, "public_ack": true, "funnel_ttl": "2h", "no_daemon_install": true}), true},
		{"share-new-funnel", "share", static(map[string]any{"target": "localhost:15", "name": "sharedpub", "funnel": true, "public_ack": true, "funnel_ttl": "2h", "no_daemon_install": true}), false},
		{"unshare-gated", "unshare", static(map[string]any{"name": "pub"}), true},
		{"unshare-funnel", "unshare", static(map[string]any{"name": "fun"}), true},
		{"limits-set-unlimited-gated", "limits_set", static(map[string]any{"app": "pub", "max_body": "unlimited", "unlimited_ack": true}), true},
		{"limits-set-funnel", "limits_set", static(map[string]any{"app": "fun", "max_body": "64MiB"}), true},
		{"health-set-gated", "health_set", static(map[string]any{"app": "pub", "path": "/health"}), true},
		{"guest-create-gated-print-link", "guest_create", static(map[string]any{"app": "pub", "for": "2h", "public": true, "print_link": true}), true},
		{"guest-create-funnel", "guest_create", static(map[string]any{"app": "fun", "for": "2h", "public": true}), false}, // an open Funnel refuses guest links for everyone
		{"guest-create-makes-private-public", "guest_create", static(map[string]any{"app": "priv", "for": "2h", "public": true}), true},
		{"recipe-apply-new-funnel", "recipe_apply", static(map[string]any{"recipe_id": "jellyfin", "name": "rpub", "target": "localhost:8096", "funnel": true, "public_ack": true, "funnel_ttl": "2h", "no_daemon_install": true}), true},
		{"extend-funnel", "extend", static(map[string]any{"service": "fun", "for": "3h"}), true},
		{"extend-regrant-lapsed", "extend", static(map[string]any{"service": "lapsed", "for": "2h", "regrant": true}), true},
		{"tags-set-funnel", "tags_set", static(map[string]any{"service": "fun", "tag": "tag:other"}), true},
		{"app-restart-gated", "app_restart", static(map[string]any{"app": "pub"}), true},
	}
}

// Private, unrouted apps keep the MCP owner-role rule: these succeed for a
// remote operator who is not the portal owner.
func privateMutationCases() []publicMutationCase {
	static := func(m map[string]any) func(publicFixture) map[string]any {
		return func(publicFixture) map[string]any { return m }
	}
	return []publicMutationCase{
		{"add-retarget-private", "add", static(map[string]any{"name": "priv", "type": "proxy", "target": "localhost:16", "no_daemon_install": true}), true},
		{"limits-set-private", "limits_set", static(map[string]any{"app": "priv", "max_body": "64MiB"}), true},
		{"health-set-private", "health_set", static(map[string]any{"app": "priv", "path": "/health"}), true},
		{"tags-set-private", "tags_set", static(map[string]any{"service": "priv", "tag": "tag:other"}), true},
		{"app-restart-private", "app_restart", static(map[string]any{"app": "priv"}), true},
		{"unshare-private", "unshare", static(map[string]any{"name": "priv"}), true},
	}
}

// mutatingToolClass classifies every mutating MCP tool for the public rule.
// A new mutating tool fails TestEveryMutatingMCPToolIsClassifiedForThePublicRule
// until it is listed here with the reason it does or does not need the rule.
var mutatingToolClass = map[string]string{
	"add":                 "public: replaces a definition; guard on before and after",
	"share":               "public: creates or re-arms a Funnel; guard",
	"unshare":             "public: removes the app; authorizePortalServiceMutation before node retirement, and guard",
	"template_apply":      "not public: templates carry no Funnel or gate; new routed apps need the portal owner (gateway rule in the guard, TestRouteCreationOverRemoteMCPRequiresThePortalOwner)",
	"recipe_apply":        "public: funnel, public_ack and funnel_ttl inputs; adopting a tentative public share is a public settle; guard",
	"health_set":          "public: definition change; authorizePortalServiceMutation and guard",
	"limits_set":          "public: what an internet caller may send; authorizePortalServiceMutation and guard",
	"tags_set":            "public: definition change and node restart; guard",
	"app_restart":         "public: restarts the public listener; guard",
	"extend":              "public for the Funnel audience: public lifetime; guard. Person audience: tailnet grant, peopleMutationAuthorization",
	"guest_create":        "public: always leaves the app gated; pre-check on gated form and guard",
	"guest_revoke":        "exempt: an MCP owner-role caller may revoke existing grants when that is the whole public change (TestGuestRevokeOverRemoteMCPIsExemptForTheOwnerRole)",
	"portal_route_add":    "not public: gateway rule (portal owner) already; guest-gated and Funnel apps cannot be routed",
	"portal_route_remove": "not public: gateway rule already",
	"portal_enable":       "not public: portal is Tailnet-only; already requires the portal owner",
	"portal_disable":      "not public: portal is Tailnet-only; gateway disable already requires the portal owner",
	"people_add":          "not public: tailnet person grants; new grants on gated apps are refused",
	"people_update":       "not public: tailnet person grants",
	"people_remove":       "not public: tailnet person grants",
	"people_grant":        "not public: tailnet person grants",
	"people_revoke":       "not public: tailnet person grants",
	"invite_user":         "not public: tailnet invitations",
	"invite_device":       "not public: tailnet invitations",
	"invite_resend":       "not public: tailnet invitations",
	"invite_revoke":       "not public: tailnet invitations",
	"requests_list":       "not public: already requires the portal owner",
	"requests_approve":    "not public: already requires the portal owner",
	"requests_deny":       "not public: already requires the portal owner",
}

func TestEveryMutatingMCPToolIsClassifiedForThePublicRule(t *testing.T) {
	covered := map[string]bool{}
	for _, tc := range publicMutationCases() {
		covered[tc.tool] = true
	}
	for _, definition := range mcpToolDefinitions {
		if !mcpMutatingTool(definition.Name) {
			continue
		}
		class, ok := mutatingToolClass[definition.Name]
		if !ok {
			t.Errorf("mutating MCP tool %q is not classified for the public portal-owner rule", definition.Name)
			continue
		}
		if !strings.HasPrefix(class, "public") && !strings.HasPrefix(class, "exempt") && !strings.HasPrefix(class, "not public") {
			t.Errorf("tool %q has an unknown class %q", definition.Name, class)
		}
		if strings.HasPrefix(class, "public") && !covered[definition.Name] {
			t.Errorf("public-relevant tool %q has no row in publicMutationCases", definition.Name)
		}
	}
	for name := range mutatingToolClass {
		if !mcpMutatingTool(name) {
			t.Errorf("classified tool %q is not a mutating MCP tool", name)
		}
	}
}

func publicWhoIs(t *testing.T, login string, tags []string) *server.LocalClient {
	t.Helper()
	resp := &apitype.WhoIsResponse{UserProfile: &tailcfg.UserProfile{LoginName: login}, Node: &tailcfg.Node{Tags: tags}}
	return &server.LocalClient{OmitAuth: true, Transport: mcpHTTPRoundTripper(func(*http.Request) (*http.Response, error) {
		body, err := json.Marshal(resp)
		if err != nil {
			t.Fatal(err)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(body))}, nil
	})}
}

type publicCaller struct {
	name     string
	login    string
	tags     []string
	allow    []string
	bindings []mcpscope.Binding
}

// Remote principals that hold the MCP owner role but are not the portal owner.
func publicOutsiders() []publicCaller {
	return []publicCaller{
		{name: "login-allowed", login: "operator", allow: []string{"operator"}},
		{name: "tag-allowed", login: "tagged-devices", tags: []string{"tag:ops"}, allow: []string{"tag:ops"}},
		{name: "tagged-owner-login", login: "owner", tags: []string{"tag:ops"}, allow: []string{"tag:ops"}},
		{name: "owner-binding", login: "operator", bindings: []mcpscope.Binding{{Principal: "operator", Scope: mcpscope.Scope{Role: "owner"}}}},
	}
}

func publicServer(t *testing.T, c publicCaller, actions mcpActions) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(server.NewMCPControlPlaneHandler(&server.MCPControlPlane{AllowedUsers: c.allow, Bindings: c.bindings, Handler: newMCPStreamableHandler(actions)}, publicWhoIs(t, c.login, c.tags)))
	t.Cleanup(srv.Close)
	return srv
}

func registryBytes(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func refusedForOwner(result []byte) bool {
	return bytes.Contains(result, []byte("access_request_owner_required"))
}

func TestPublicMutationsOverRemoteMCPRequireThePortalOwner(t *testing.T) {
	for _, tc := range publicMutationCases() {
		t.Run(tc.name, func(t *testing.T) {
			for _, outsider := range publicOutsiders() {
				t.Run(outsider.name, func(t *testing.T) {
					f := publicAuthorityFixture(t, "owner", true)
					before := registryBytes(t, f.paths.Registry)
					ledger, _ := os.ReadFile(f.paths.Ownership)
					result := portalRemoteCall(t, publicServer(t, outsider, publicActions(f)), tc.tool, tc.args(f))
					if after, _ := os.ReadFile(f.paths.Ownership); !bytes.Equal(ledger, after) {
						t.Fatal("refusal wrote the node ownership ledger")
					}
					if !refusedForOwner(result) {
						t.Fatalf("non-owner changed public state: %s", result)
					}
					if strings.Contains(string(result), "access requests require") {
						t.Fatalf("refusal speaks of access requests: %s", result)
					}
					if !bytes.Equal(before, registryBytes(t, f.paths.Registry)) {
						t.Fatal("refusal changed the registry")
					}
				})
			}
			t.Run("portal-owner", func(t *testing.T) {
				f := publicAuthorityFixture(t, "owner", true)
				before := registryBytes(t, f.paths.Registry)
				result := portalRemoteCall(t, publicServer(t, publicCaller{login: "owner", allow: []string{"owner"}}, publicActions(f)), tc.tool, tc.args(f))
				if refusedForOwner(result) {
					t.Fatalf("portal owner refused: %s", result)
				}
				if tc.ownerSaves && (bytes.Contains(result, []byte(`"isError":true`)) || bytes.Equal(before, registryBytes(t, f.paths.Registry))) {
					t.Fatalf("portal owner control did not commit: %s", result)
				}
			})
			t.Run("revoked-owner", func(t *testing.T) {
				f := publicAuthorityFixture(t, "owner", true)
				if _, err := registry.RemovePerson(f.paths.Registry, "owner"); err != nil {
					t.Fatal(err)
				}
				before := registryBytes(t, f.paths.Registry)
				result := portalRemoteCall(t, publicServer(t, publicCaller{login: "owner", allow: []string{"owner"}}, publicActions(f)), tc.tool, tc.args(f))
				if !refusedForOwner(result) || !bytes.Equal(before, registryBytes(t, f.paths.Registry)) {
					t.Fatalf("revoked owner changed public state: %s", result)
				}
			})
			t.Run("local", func(t *testing.T) {
				f := publicAuthorityFixture(t, "owner", true)
				before := registryBytes(t, f.paths.Registry)
				result, err := callMCPTool(context.Background(), publicActions(f), tc.tool, publicArgsJSON(t, tc.args(f)))
				if err != nil {
					t.Fatal(err)
				}
				text := mcpResultText(t, result)
				if strings.Contains(text, "access_request_owner_required") {
					t.Fatalf("local caller refused: %s", text)
				}
				if tc.ownerSaves && (result.IsError || bytes.Equal(before, registryBytes(t, f.paths.Registry))) {
					t.Fatalf("local control did not commit: %s", text)
				}
			})
		})
	}
}

// A legacy install with no portal owner and no home gateway has no authority
// above the MCP owner role, which keeps governing public changes there. A
// home gateway whose owner is not bootstrapped yet is fail-closed, as its
// routed apps already are.
func TestPublicRuleWithoutAPortalOwner(t *testing.T) {
	for _, tc := range publicMutationCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Run("legacy-no-portal", func(t *testing.T) {
				f := publicAuthorityFixture(t, "", false)
				before := registryBytes(t, f.paths.Registry)
				result := portalRemoteCall(t, publicServer(t, publicOutsiders()[0], publicActions(f)), tc.tool, tc.args(f))
				if refusedForOwner(result) {
					t.Fatalf("legacy install refused the MCP owner role: %s", result)
				}
				if tc.ownerSaves && bytes.Equal(before, registryBytes(t, f.paths.Registry)) {
					t.Fatalf("legacy control did not commit: %s", result)
				}
			})
			t.Run("gateway-owner-unset", func(t *testing.T) {
				f := publicAuthorityFixture(t, "", true)
				before := registryBytes(t, f.paths.Registry)
				result := portalRemoteCall(t, publicServer(t, publicOutsiders()[0], publicActions(f)), tc.tool, tc.args(f))
				if !refusedForOwner(result) || !bytes.Equal(before, registryBytes(t, f.paths.Registry)) {
					t.Fatalf("unowned gateway allowed a public change: %s", result)
				}
			})
		})
	}
}

func TestPrivateUnroutedMutationsKeepTheMCPOwnerRole(t *testing.T) {
	for _, tc := range privateMutationCases() {
		t.Run(tc.name, func(t *testing.T) {
			for _, outsider := range publicOutsiders() {
				t.Run(outsider.name, func(t *testing.T) {
					f := publicAuthorityFixture(t, "owner", true)
					before := registryBytes(t, f.paths.Registry)
					result := portalRemoteCall(t, publicServer(t, outsider, publicActions(f)), tc.tool, tc.args(f))
					if bytes.Contains(result, []byte(`"isError":true`)) || bytes.Equal(before, registryBytes(t, f.paths.Registry)) {
						t.Fatalf("private app change by MCP owner role failed: %s", result)
					}
				})
			}
		})
	}
}

// R2-5: once the first guest link took the app off the gateway, a remote MCP
// owner-role principal who is not the portal owner could raise the body
// limit to unlimited, mint itself a link and delete the app.
func TestR25GatedAppWithoutRouteRefusesNonOwner(t *testing.T) {
	f := publicAuthorityFixture(t, "owner", true)
	srv := publicServer(t, publicOutsiders()[0], publicActions(f))
	for _, step := range []struct {
		tool string
		args map[string]any
	}{
		{"limits_set", map[string]any{"app": "pub", "max_body": "unlimited", "unlimited_ack": true}},
		{"guest_create", map[string]any{"app": "pub", "for": "2h", "public": true, "print_link": true}},
		{"unshare", map[string]any{"name": "pub"}},
	} {
		before := registryBytes(t, f.paths.Registry)
		result := portalRemoteCall(t, srv, step.tool, step.args)
		if !refusedForOwner(result) || bytes.Contains(result, []byte("guest/")) || !bytes.Equal(before, registryBytes(t, f.paths.Registry)) {
			t.Fatalf("%s on the gated app: %s", step.tool, result)
		}
	}
}

// The owner changes while the call holds the registry lock, after every
// pre-lock check passed for the original owner: the write must be refused
// against the new owner and leave the new owner's registry unchanged.
func TestPublicRuleOwnerChangesBeforeWrite(t *testing.T) {
	for _, tc := range publicMutationCases() {
		t.Run(tc.name, func(t *testing.T) {
			f := publicAuthorityFixture(t, "owner", true)
			reg, err := registry.Load(f.paths.Registry)
			if err != nil {
				t.Fatal(err)
			}
			reg.Portal.Owner = "replacement"
			data, err := json.Marshal(reg)
			if err != nil {
				t.Fatal(err)
			}
			lock, err := os.OpenFile(f.paths.Registry+".lock", os.O_RDWR, 0600)
			if err != nil {
				t.Fatal(err)
			}
			defer lock.Close()
			var injected atomic.Pointer[gatewayOwnerChangeContext]
			actions := publicActions(f)
			wrapPublicActions(&actions, func(ctx context.Context) context.Context {
				next := &gatewayOwnerChangeContext{Context: ctx, lock: lock, path: f.paths.Registry, data: data}
				injected.Store(next)
				return next
			})
			result := portalRemoteCall(t, publicServer(t, publicCaller{login: "owner", allow: []string{"owner"}}, actions), tc.tool, tc.args(f))
			observed := injected.Load()
			if observed == nil {
				t.Fatal("action did not run")
			}
			observed.mu.Lock()
			changed := observed.changed
			observed.mu.Unlock()
			if !changed {
				t.Fatal("owner-change seam did not run under the registry lock")
			}
			if !refusedForOwner(result) {
				t.Fatalf("stale owner authorized a public write: %s", result)
			}
			if !bytes.Equal(registryBytes(t, f.paths.Registry), data) {
				t.Fatal("refused write changed the new owner's registry")
			}
		})
	}
}

// wrapPublicActions routes the context of every public-relevant action
// through wrap.
func wrapPublicActions(a *mcpActions, wrap func(context.Context) context.Context) {
	add, share, unshare, limits, health, guest, extend, tags, restart := a.add, a.share, a.unshare, a.limitsSet, a.healthSet, a.guest, a.extend, a.tagsSet, a.appRestart
	a.add = func(ctx context.Context, p AddParams, b bool) (any, error) { return add(wrap(ctx), p, b) }
	a.share = func(ctx context.Context, r shareRequest) (ShareResult, error) { return share(wrap(ctx), r) }
	a.unshare = func(ctx context.Context, n string) (any, error) { return unshare(wrap(ctx), n) }
	a.limitsSet = func(ctx context.Context, x limitsSetArguments) (any, error) { return limits(wrap(ctx), x) }
	a.healthSet = func(ctx context.Context, x healthSetArguments) (any, error) { return health(wrap(ctx), x) }
	a.guest = func(ctx context.Context, n string, x guestArguments) (any, error) { return guest(wrap(ctx), n, x) }
	a.extend = func(ctx context.Context, x extendArguments) (any, error) { return extend(wrap(ctx), x) }
	a.tagsSet = func(ctx context.Context, s, tag string) (any, error) { return tags(wrap(ctx), s, tag) }
	a.appRestart = func(ctx context.Context, app string) (any, error) { return restart(wrap(ctx), app) }
	recipe := a.recipeApply
	a.recipeApply = func(ctx context.Context, r recipeRequest, dry bool) (any, error) { return recipe(wrap(ctx), r, dry) }
}

func publicArgsJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// publicActions are the shipped actions; share does not wait for a URL.
func publicActions(f publicFixture) mcpActions {
	a := defaultMCPActions(f.paths, io.Discard)
	a.share = func(ctx context.Context, req shareRequest) (ShareResult, error) {
		req.Now = time.Now()
		return executeShare(ctx, f.paths, req, 0, io.Discard)
	}
	return a
}

// E: pulling a leaked link only reduces exposure, so an MCP owner-role caller
// may revoke an existing grant without being the portal owner. The exemption
// is exactly that: the receipt names the caller, and a reduced role still
// cannot call the tool.
func TestGuestRevokeOverRemoteMCPIsExemptForTheOwnerRole(t *testing.T) {
	for _, outsider := range append(publicOutsiders(), publicCaller{name: "revoked-owner", login: "owner", allow: []string{"owner"}}) {
		t.Run(outsider.name, func(t *testing.T) {
			f := publicAuthorityFixture(t, "owner", true)
			if outsider.name == "revoked-owner" {
				if _, err := registry.RemovePerson(f.paths.Registry, "owner"); err != nil {
					t.Fatal(err)
				}
			}
			result := portalRemoteCall(t, publicServer(t, outsider, publicActions(f)), "guest_revoke", map[string]any{"id": f.guestID})
			if bytes.Contains(result, []byte(`"isError":true`)) {
				t.Fatalf("owner-role revoke refused: %s", result)
			}
			if _, reason := registry.CheckGuest(f.paths.Registry, "pub", f.guestID, time.Now(), false, false); reason != "revoked" {
				t.Fatalf("link still %q", reason)
			}
			journal, err := os.ReadFile(mcpAuditPath(f.paths.Registry))
			if err != nil || !bytes.Contains(journal, []byte(`"tool":"guest_revoke"`)) || !bytes.Contains(journal, []byte(`"principal":"`+outsider.login)) && !bytes.Contains(journal, []byte(`"principal":"tag:ops"`)) {
				t.Fatalf("receipt does not name the caller: %s %v", journal, err)
			}
		})
	}
	t.Run("reduced-role", func(t *testing.T) {
		f := publicAuthorityFixture(t, "owner", true)
		before := registryBytes(t, f.paths.Registry)
		expiry := time.Now().Add(time.Hour)
		caller := publicCaller{login: "helper", bindings: []mcpscope.Binding{{Principal: "helper", Scope: mcpscope.Scope{Role: "app-operator", Apps: []string{"pub"}, MaxDuration: "2h"}, ExpiresAt: &expiry}}}
		// guest_revoke is owner-only before and after this rule: a reduced
		// role is answered as for an unknown tool (HTTP 400) and nothing runs.
		status, result := publicRemoteCallStatus(t, publicServer(t, caller, publicActions(f)), "guest_revoke", map[string]any{"id": f.guestID})
		if status == http.StatusOK && !bytes.Contains(result, []byte(`"isError":true`)) || !bytes.Equal(before, registryBytes(t, f.paths.Registry)) {
			t.Fatalf("reduced role revoked a link: %d %s", status, result)
		}
	})
	t.Run("unowned-gateway", func(t *testing.T) {
		f := publicAuthorityFixture(t, "", true)
		result := portalRemoteCall(t, publicServer(t, publicOutsiders()[0], publicActions(f)), "guest_revoke", map[string]any{"id": f.guestID})
		if bytes.Contains(result, []byte(`"isError":true`)) {
			t.Fatalf("owner-role revoke refused on an unowned gateway: %s", result)
		}
	})
}

// M2: settling a tentative public registration writes no registry but keeps
// an exposure its creator would roll back. A non-owner's identical share or a
// recipe that adopts it by name is refused, and the owner's rollback works.
func TestSettlingATentativePublicShareRequiresThePortalOwner(t *testing.T) {
	for _, via := range []string{"share", "recipe_apply"} {
		t.Run(via, func(t *testing.T) {
			f := publicAuthorityFixture(t, "owner", true)
			req := shareRequest{Target: "localhost:15", Ephemeral: true, Funnel: true, PublicAck: true, FunnelTTL: "2h", FunnelTTLSet: true, Now: time.Now()}
			spec, err := inferShareTarget(req.Target, req.Ephemeral)
			if err != nil {
				t.Fatal(err)
			}
			if spec, err = applyShareExposure(spec, req); err != nil {
				t.Fatal(err)
			}
			reg, err := registerShareWithOutcomeContext(context.Background(), f.paths.Registry, spec, "")
			if err != nil || !reg.Created {
				t.Fatalf("owner tentative share: %+v %v", reg, err)
			}
			mark := f.paths.Registry + ".tentative-" + reg.Service.Name
			args := map[string]any{"target": "localhost:15", "funnel": true, "public_ack": true, "funnel_ttl": "2h", "no_daemon_install": true}
			if via == "recipe_apply" {
				args = map[string]any{"recipe_id": "jellyfin", "name": reg.Service.Name, "target": "localhost:8096", "no_daemon_install": true}
			}
			result := portalRemoteCall(t, publicServer(t, publicOutsiders()[0], publicActions(f)), via, args)
			if !refusedForOwner(result) {
				t.Fatalf("non-owner settled the owner's tentative public share: %s", result)
			}
			if _, err := os.Stat(mark); err != nil {
				t.Fatalf("tentative mark gone: %v", err)
			}
			if removed, err := registry.RemoveIfUnchanged(f.paths.Registry, reg.Service); err != nil || !removed {
				t.Fatalf("owner's rollback: removed=%v err=%v", removed, err)
			}
		})
	}
}

// M4: route creation on a home gateway needs the portal owner on every door,
// not only add: template_apply and share register through the same guard.
func TestRouteCreationOverRemoteMCPRequiresThePortalOwner(t *testing.T) {
	for _, tc := range []struct {
		name, tool string
		args       map[string]any
	}{
		{"add-new-private", "add", map[string]any{"name": "n2", "type": "proxy", "target": "localhost:32", "no_daemon_install": true}},
		{"share-new-private", "share", map[string]any{"target": "localhost:77", "no_daemon_install": true}},
		{"template-local-web", "template_apply", map[string]any{"name": "local-web", "no_daemon_install": true}},
		{"template-local-ai-suite", "template_apply", map[string]any{"name": "local-ai-suite", "no_daemon_install": true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, outsider := range publicOutsiders() {
				f := publicAuthorityFixture(t, "owner", true)
				before := registryBytes(t, f.paths.Registry)
				result := portalRemoteCall(t, publicServer(t, outsider, publicActions(f)), tc.tool, tc.args)
				if !refusedForOwner(result) || !bytes.Equal(before, registryBytes(t, f.paths.Registry)) {
					t.Fatalf("%s: non-owner created a routed app: %s", outsider.name, result)
				}
			}
			f := publicAuthorityFixture(t, "owner", true)
			result := portalRemoteCall(t, publicServer(t, publicCaller{login: "owner", allow: []string{"owner"}}, publicActions(f)), tc.tool, tc.args)
			if refusedForOwner(result) {
				t.Fatalf("portal owner refused: %s", result)
			}
		})
	}
}

func publicRemoteCallStatus(t *testing.T, srv *httptest.Server, tool string, args map[string]any) (int, []byte) {
	t.Helper()
	r := mcpHTTPToolCallRequest(tool)
	b, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(b, &wire); err != nil {
		t.Fatal(err)
	}
	wire["params"].(map[string]any)["arguments"] = args
	if b, err = json.Marshal(wire); err != nil {
		t.Fatal(err)
	}
	r.Body = io.NopCloser(bytes.NewReader(b))
	r.ContentLength, r.RequestURI = int64(len(b)), ""
	r.URL.Scheme, r.URL.Host = "http", strings.TrimPrefix(srv.URL, "http://")
	r.Host = r.URL.Host
	resp, err := srv.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, body
}

// M1: a revocation tombstone on the future owner's login must not lock the
// owner out. Remote person changes on an unowned gateway are fail-closed;
// the daemon does not bootstrap a revoked login; and designating the owner
// locally restores the login in the same write.
func TestOwnerTombstoneCannotLockTheOwnerOut(t *testing.T) {
	ownerCaller := publicCaller{login: "owner", allow: []string{"owner"}}
	ownerWorks := func(t *testing.T, f publicFixture) {
		t.Helper()
		result := portalRemoteCall(t, publicServer(t, ownerCaller, publicActions(f)), "limits_set", map[string]any{"app": "pub", "max_body": "64MiB"})
		if bytes.Contains(result, []byte(`"isError":true`)) {
			t.Fatalf("owner locked out: %s", result)
		}
	}
	t.Run("unowned-gateway-refuses-remote-tombstone", func(t *testing.T) {
		f := publicAuthorityFixture(t, "", true)
		before := registryBytes(t, f.paths.Registry)
		result := portalRemoteCall(t, publicServer(t, publicOutsiders()[0], publicActions(f)), "people_remove", map[string]any{"who": "owner"})
		if !refusedForOwner(result) || !bytes.Equal(before, registryBytes(t, f.paths.Registry)) {
			t.Fatalf("remote tombstone on an unowned gateway: %s", result)
		}
		if err := registry.BootstrapPortalOwner(context.Background(), f.paths.Registry, "home", "owner"); err != nil {
			t.Fatal(err)
		}
		ownerWorks(t, f)
	})
	t.Run("bootstrap-skips-a-revoked-login-and-local-enable-restores-it", func(t *testing.T) {
		f := publicAuthorityFixture(t, "", true)
		if _, err := registry.RemovePerson(f.paths.Registry, "owner"); err != nil {
			t.Fatal(err)
		}
		if err := registry.BootstrapPortalOwner(context.Background(), f.paths.Registry, "home", "owner"); err != nil {
			t.Fatal(err)
		}
		if reg, err := registry.Load(f.paths.Registry); err != nil || reg.Portal.Owner != "" {
			t.Fatalf("revoked login bootstrapped as owner: %+v %v", reg.Portal, err)
		}
		if _, err := changePortal(f.paths, portalArguments{Owner: "owner"}, true); err != nil {
			t.Fatal(err)
		}
		reg, err := registry.Load(f.paths.Registry)
		if err != nil || reg.Portal.Owner != "owner" {
			t.Fatalf("owner = %+v %v", reg.Portal, err)
		}
		for _, p := range reg.People {
			if p.Login == "owner" && p.Revoked {
				t.Fatal("local owner designation kept the tombstone")
			}
		}
		ownerWorks(t, f)
	})
	t.Run("legacy-tombstone-then-local-owner", func(t *testing.T) {
		f := publicAuthorityFixture(t, "", false)
		result := portalRemoteCall(t, publicServer(t, publicOutsiders()[0], publicActions(f)), "people_remove", map[string]any{"who": "owner"})
		if bytes.Contains(result, []byte(`"isError":true`)) {
			t.Fatalf("legacy install: MCP owner role may still remove people: %s", result)
		}
		if _, err := changePortal(f.paths, portalArguments{Owner: "owner", Gateway: false, GatewaySet: true}, true); err != nil {
			t.Fatal(err)
		}
		ownerWorks(t, f)
	})
	t.Run("existing-owner-local-reenable-restores", func(t *testing.T) {
		f := publicAuthorityFixture(t, "owner", true)
		if _, err := registry.RemovePerson(f.paths.Registry, "owner"); err != nil {
			t.Fatal(err)
		}
		if _, err := changePortal(f.paths, portalArguments{Owner: "owner"}, true); err != nil {
			t.Fatal(err)
		}
		ownerWorks(t, f)
	})
}

// R2-1: only an invocation that names the owner restores a revoked owner,
// and it says so (human output, JSON data, receipt). Any other enable keeps
// a deliberate revocation and tells the operator how to restore it.
func TestPortalEnableRestoresARevokedOwnerOnlyWhenNamed(t *testing.T) {
	revoked := func(t *testing.T, path string) bool {
		t.Helper()
		reg, err := registry.Load(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range reg.People {
			if p.Login == "owner" {
				return p.Revoked
			}
		}
		return false
	}
	t.Run("unnamed-keeps-the-tombstone", func(t *testing.T) {
		f := publicAuthorityFixture(t, "owner", true)
		if _, err := registry.RemovePerson(f.paths.Registry, "owner"); err != nil {
			t.Fatal(err)
		}
		view, err := changePortal(f.paths, portalArguments{Gateway: true, GatewaySet: true}, true)
		if err != nil {
			t.Fatal(err)
		}
		if !revoked(t, f.paths.Registry) {
			t.Fatal("portal enable without --owner restored the revoked owner")
		}
		if view.OwnerRevoked != "owner" || view.OwnerRestored != "" {
			t.Fatalf("view = %+v", view)
		}
		notice := portalOwnerNotice(view)
		if !strings.Contains(notice, "is revoked") || !strings.Contains(notice, "tslink portal enable --owner owner") {
			t.Fatalf("notice = %q", notice)
		}
		encoded, _ := json.Marshal(view)
		if !bytes.Contains(encoded, []byte(`"owner_revoked":"owner"`)) || bytes.Contains(encoded, []byte("owner_restored")) {
			t.Fatalf("json = %s", encoded)
		}
		result := portalRemoteCall(t, publicServer(t, publicCaller{login: "owner", allow: []string{"owner"}}, publicActions(f)), "limits_set", map[string]any{"app": "pub", "max_body": "64MiB"})
		if !refusedForOwner(result) {
			t.Fatalf("revoked owner regained remote authority: %s", result)
		}
	})
	t.Run("named-restores-and-reports", func(t *testing.T) {
		f := publicAuthorityFixture(t, "owner", true)
		if _, err := registry.RemovePerson(f.paths.Registry, "owner"); err != nil {
			t.Fatal(err)
		}
		view, err := changePortal(f.paths, portalArguments{Owner: "owner"}, true)
		if err != nil {
			t.Fatal(err)
		}
		if revoked(t, f.paths.Registry) || view.OwnerRestored != "owner" || view.OwnerRevoked != "" {
			t.Fatalf("named owner not restored: %+v", view)
		}
		if notice := portalOwnerNotice(view); !strings.Contains(notice, "Restored portal owner owner") {
			t.Fatalf("notice = %q", notice)
		}
		encoded, _ := json.Marshal(view)
		if !bytes.Contains(encoded, []byte(`"owner_restored":"owner"`)) {
			t.Fatalf("json = %s", encoded)
		}
		journal, err := os.ReadFile(mcpAuditPath(f.paths.Registry))
		if err != nil || !bytes.Contains(journal, []byte(`"action":"portal_owner_restored"`)) || !bytes.Contains(journal, []byte(`"subject":"owner"`)) {
			t.Fatalf("no restore receipt: %s %v", journal, err)
		}
		// A second named enable has nothing to restore and reports nothing.
		again, err := changePortal(f.paths, portalArguments{Owner: "owner"}, true)
		if err != nil || again.OwnerRestored != "" || again.OwnerRevoked != "" {
			t.Fatalf("repeat = %+v %v", again, err)
		}
	})
}
