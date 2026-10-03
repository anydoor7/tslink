package cmd

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/accesslog"
	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/duration"
	"github.com/anydoor7/tslink/internal/mcpaudit"
	"github.com/anydoor7/tslink/internal/mcpscope"
	"github.com/anydoor7/tslink/internal/registry"
	tsRuntime "github.com/anydoor7/tslink/internal/runtime"
	"github.com/anydoor7/tslink/internal/server"
)

func wave2Actions(t *testing.T, paths sharePaths, role, app string) mcpActions {
	t.Helper()
	now := peopleNowFn()
	oldClock := durationNowFn
	durationNowFn = func() time.Time { return now }
	a := defaultMCPActions(paths, io.Discard)
	durationNowFn = oldClock
	a.nowFn = func() time.Time { return now }
	a.session = &mcpscope.Session{Who: "owner", Identity: mcpscope.Identity{Login: "owner", Node: "owner-node"}, Scope: mcpscope.Scope{Role: role}}
	if role != "owner" {
		a.session.Scope.Apps = []string{app}
		if role != "viewer" {
			a.session.Scope.MaxDuration = "2h"
		}
	}
	return a
}

// The query traverses the real journal, dispatch, app filter, summary and MCP
// projection. An out-of-scope newer record must not consume the result limit.
func TestWave2MCPRolesAndScopedAccessLog(t *testing.T) {
	paths := peopleTestPaths(t)
	now := peopleNowFn()
	journal := mcpaudit.Journal{Path: mcpAuditPath(paths.Registry)}
	for i, app := range []string{"photos", "finance"} {
		err := journal.Record(t.Context(), mcpaudit.Entry{Kind: "mcp", ID: app, Time: now.Add(time.Duration(i) * time.Hour), Principal: "owner", Role: "owner", Phase: "completion", Tool: "extend", Apps: []string{app}, Result: "ok"})
		if err != nil {
			t.Fatal(err)
		}
	}
	a := wave2Actions(t, paths, "viewer", "photos")
	for _, tool := range []string{"access_log", "access_summary"} {
		r, err := callMCPTool(t.Context(), a, tool, json.RawMessage(`{"limit":1}`))
		if err != nil || r.IsError {
			t.Fatal(r, err)
		}
		b, _ := json.Marshal(r.StructuredContent)
		if strings.Contains(string(b), "finance") || !strings.Contains(string(b), "photos") || !strings.Contains(string(b), `"count":1`) {
			t.Fatalf("scope query: %s", b)
		}
	}
	for _, role := range []string{"viewer", "app-operator", "people-manager"} {
		a := wave2Actions(t, paths, role, "photos")
		for _, tool := range []string{"guest_create", "guest_list", "guest_show", "guest_revoke", "people_add", "people_update"} {
			if _, err := callMCPTool(t.Context(), a, tool, json.RawMessage(`{}`)); err == nil {
				t.Fatalf("%s exposed %s", role, tool)
			}
		}
		want := role == "people-manager"
		for _, tool := range []string{"requests_list", "requests_approve", "requests_deny"} {
			if a.session.Scope.ToolAllowed(tool) != want {
				t.Fatalf("request role %s %s", role, tool)
			}
		}
	}
}

func TestWave2ApprovalScopeOwnerAndLifetime(t *testing.T) {
	for _, tc := range []struct {
		name, app, lifetime, caller string
		expiry                      time.Duration
		revoked                     bool
	}{
		{"other_app", "finance", "1h", "owner", 0, false},
		{"too_long", "photos", "3h", "owner", 0, false},
		{"binding_ceiling", "photos", "2h", "owner", 90 * time.Minute, false},
		{"not_owner", "photos", "1h", "alice", 0, false},
		{"revoked_owner", "photos", "1h", "owner", 0, true},
		{"allowed", "photos", "until 2030-01-01T02:00:00Z", "owner", 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			paths, request := commandRequest(t)
			if _, err := registry.ChangePerson(paths.Registry, "alice", []string{"finance"}, nil, false, false); err != nil {
				t.Fatal(err)
			}
			a := wave2Actions(t, paths, "people-manager", tc.app)
			if tc.expiry > 0 {
				at := peopleNowFn().Add(tc.expiry)
				a.session.ExpiresAt = &at
			}
			if tc.revoked {
				if _, err := registry.ChangePerson(paths.Registry, "owner", []string{"finance"}, nil, false, false); err != nil {
					t.Fatal(err)
				}
				if _, err := registry.RemovePerson(paths.Registry, "owner"); err != nil {
					t.Fatal(err)
				}
			}
			before, err := os.ReadFile(paths.Registry)
			if err != nil {
				t.Fatal(err)
			}
			cp := &server.MCPControlPlane{Bindings: []mcpscope.Binding{{Principal: tc.caller, Scope: a.session.Scope, ExpiresAt: a.session.ExpiresAt}}, Handler: newMCPStreamableHandler(a)}
			srv := httptest.NewServer(server.NewMCPControlPlaneHandler(cp, mcpHTTPWhoIsClient(t, tc.caller)))
			defer srv.Close()
			response := portalRemoteCall(t, srv, "requests_approve", map[string]any{"id": request.ID, "for": tc.lifetime})
			failed := strings.Contains(string(response), `"isError":true`)
			if tc.name == "allowed" {
				if failed {
					t.Fatal(string(response))
				}
				reg, _, err := registry.Preflight(paths.Registry)
				if err != nil {
					t.Fatal(err)
				}
				if !registry.PersonGrantActiveAt(reg.People[0], request.App, peopleNowFn()) {
					t.Fatal("approval did not grant access")
				}
				return
			}
			if !failed {
				t.Fatalf("approval accepted %s: %s", tc.name, response)
			}
			after, err := os.ReadFile(paths.Registry)
			if err != nil {
				t.Fatal(err)
			}
			if string(before) != string(after) {
				t.Fatal("denied approval wrote registry")
			}
		})
	}
}

// Assert effects and receipts together; a successful MCP receipt alone cannot
// stand in for either a committed grant or the typed duration change.
func TestWave2LifecycleAudit(t *testing.T) {
	paths, request := commandRequest(t)
	now := peopleNowFn()
	if _, err := decideRequest(paths.Registry, requestDecisionArguments{ID: request.ID, For: "1h"}, true, now); err != nil {
		t.Fatal(err)
	}
	a := wave2Actions(t, paths, "people-manager", request.App)
	args, _ := json.Marshal(extendArguments{Service: request.App, Who: "alice", For: ptrWave2("2h")})
	result, err := callMCPTool(t.Context(), a, "extend", args)
	if err != nil || result.IsError {
		t.Fatal(result, err)
	}
	if changed, err := registry.ExpirePeople(paths.Registry, now.Add(2*time.Hour)); err != nil || !changed {
		t.Fatal(changed, err)
	}
	if changed, err := registry.ExpirePeople(paths.Registry, now.Add(3*time.Hour)); err != nil || changed {
		t.Fatal(changed, err)
	}
	log, err := accesslog.Query(filepath.Dir(paths.Registry), accesslog.Filter{App: request.App})
	if err != nil {
		t.Fatal(err)
	}
	changes := map[string]accesslog.Event{}
	for _, event := range log.Events {
		for _, change := range event.Changes {
			changes[change.Action] = event
		}
	}
	for action, surface := range map[string]string{"request_approved": "cli", "extended": "scoped_mcp", "grant_expired": "lifecycle"} {
		e, ok := changes[action]
		if !ok || e.Surface != surface || e.MCP == nil || e.MCP.Principal == "" || len(e.Changes) != 1 || e.Changes[0].Subject != "alice" {
			t.Fatalf("%s receipt: %+v", action, e)
		}
	}
	e := changes["extended"]
	if e.MCP.Identity.Login != "owner" || e.MCP.Identity.Node != "owner-node" || e.MCP.Role != "people-manager" || e.MCP.Phase != "completion" || e.Changes[0].ExpiresAt == nil || !e.Changes[0].ExpiresAt.Equal(now.Add(2*time.Hour)) {
		t.Fatalf("MCP mapping: %+v", e)
	}
	if log.Summary.Count != 4 {
		t.Fatalf("expected approval, intent, completion, expiry; got %+v", log)
	}
	b, _ := json.Marshal(log)
	if strings.Contains(string(b), "script") || strings.Contains(string(b), "2030-01-01T03:") {
		t.Fatal("unsafe note or duplicate expiry", string(b))
	}
	// A damaged journal is explicit and read-only; never silently empty.
	if err := os.WriteFile(mcpAuditPath(paths.Registry), []byte("["), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := accesslog.Query(filepath.Dir(paths.Registry), accesslog.Filter{}); err == nil {
		t.Fatal("partial journal accepted")
	}
}

func ptrWave2(s string) *string { return &s }

func TestWave2SharedLifetimePolicy(t *testing.T) {
	for _, value := range []string{"30m", "1h", "1d12h", "until 2030-01-02T12:00:00Z", "8d"} {
		t.Run(value, func(t *testing.T) {
			paths, request := commandRequest(t)
			now := peopleNowFn()
			_, submittedErr := registry.SubmitAccessRequest(paths.Registry, "charlie", request.App, value, "", now)
			if (submittedErr != nil) != (value == "30m") {
				t.Fatalf("request preference %q: %v", value, submittedErr)
			}
			_, personErr := registry.ChangePersonWithLifetime(paths.Registry, "bob", []string{"finance"}, false, registry.PersonLifetimeOptions{Value: &value, Audience: duration.Guest, Policy: duration.Policy{}, Now: now})
			_, _, guestErr := registry.CreateGuest(paths.Registry, registry.CreateGuestOptions{App: "finance", Value: value, PublicAck: true, Policy: duration.Policy{}, Now: now})
			// A member request uses the same parser/minimum; its maximum differs
			// intentionally from the guest/public policy.
			_, requestErr := decideRequest(paths.Registry, requestDecisionArguments{ID: request.ID, For: value}, true, now)
			invalid := value == "30m"
			if (personErr != nil) != (invalid || value == "8d") || (guestErr != nil) != (invalid || value == "8d") || (requestErr != nil) != invalid {
				t.Fatalf("%q person=%v guest=%v request=%v", value, personErr, guestErr, requestErr)
			}
			if invalid || value == "8d" {
				return
			}
			reg, _, err := registry.Preflight(paths.Registry)
			if err != nil {
				t.Fatal(err)
			}
			want, err := duration.ParseLifetime(value, now, time.UTC)
			if err != nil {
				t.Fatal(err)
			}
			for _, p := range reg.People {
				for _, g := range p.Grants {
					if g.ExpiresAt == nil || !g.ExpiresAt.Equal(*want.Deadline) {
						t.Fatalf("grant deadline %+v", g)
					}
				}
			}
			if len(reg.Guests) != 1 || !reg.Guests[0].ExpiresAt.Equal(*want.Deadline) {
				t.Fatal(reg.Guests)
			}
		})
	}
}

// Direct domain calls still use the locked callback; transport-only checks
// cannot authorize a now-out-of-scope request.
func TestWave2LockedApprovalCallback(t *testing.T) {
	paths, request := commandRequest(t)
	if _, err := registry.ChangePerson(paths.Registry, "alice", []string{"finance"}, nil, false, false); err != nil {
		t.Fatal(err)
	}
	now := peopleNowFn()
	s := mcpscope.Session{Who: "owner", Scope: mcpscope.Scope{Role: "people-manager", Apps: []string{"finance"}, MaxDuration: "2h"}}
	ctx := mcpscope.WithClock(mcpscope.WithSession(context.Background(), s), func() time.Time { return now })
	before, _ := os.ReadFile(paths.Registry)
	if _, err := decideRequestContext(ctx, paths.Registry, requestDecisionArguments{ID: request.ID, For: "1h"}, true, now); err == nil {
		t.Fatal("locked app scope bypassed")
	}
	after, _ := os.ReadFile(paths.Registry)
	if string(before) != string(after) {
		t.Fatal("locked refusal wrote state")
	}
	s.Scope.Apps = []string{request.App}
	ctx = mcpscope.WithClock(mcpscope.WithSession(context.Background(), s), func() time.Time { return now })
	if _, err := decideRequestContext(ctx, paths.Registry, requestDecisionArguments{ID: request.ID, For: "1h"}, true, now); err != nil {
		t.Fatal("control", err)
	}
}

func TestWave2GuestBindingLifetime(t *testing.T) {
	paths := peopleTestPaths(t)
	a := wave2Actions(t, paths, "owner", "")
	expiry := peopleNowFn().Add(90 * time.Minute)
	a.session.ExpiresAt = &expiry
	before, err := os.ReadFile(paths.Registry)
	if err != nil {
		t.Fatal(err)
	}
	result, err := callMCPTool(t.Context(), a, "guest_create", json.RawMessage(`{"app":"photos","for":"2h","public":true}`))
	if err != nil || !result.IsError {
		t.Fatal("guest outlived binding", result, err)
	}
	after, err := os.ReadFile(paths.Registry)
	if err != nil || string(before) != string(after) {
		t.Fatal("denied guest wrote registry", err)
	}
	result, err = callMCPTool(t.Context(), a, "guest_create", json.RawMessage(`{"app":"photos","for":"1h","public":true}`))
	if err != nil || result.IsError {
		t.Fatal("guest control", result, err)
	}
}

func TestWave2PostCommitAuditFailure(t *testing.T) {
	paths, request := commandRequest(t)
	if err := os.WriteFile(mcpAuditPath(paths.Registry), []byte("["), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := decideRequest(paths.Registry, requestDecisionArguments{ID: request.ID, For: "1h"}, true, peopleNowFn())
	if err == nil || !strings.Contains(err.Error(), "change committed") || !result.Changed {
		t.Fatal("missing committed failure", result, err)
	}
	reg, _, err := registry.Preflight(paths.Registry)
	if err != nil || reg.Requests[0].Status != registry.RequestApproved || !registry.PersonGrantActiveAt(reg.People[0], request.App, peopleNowFn()) {
		t.Fatal("audit failure rolled back authority", reg, err)
	}
	b, err := os.ReadFile(mcpAuditPath(paths.Registry))
	if err != nil || string(b) != "[" {
		t.Fatal("audit failure repaired evidence", string(b), err)
	}
}

func TestWave2LockedExtendEffectChecks(t *testing.T) {
	for _, cause := range []string{"wrong_app", "cancelled_inside_lock", "allowed"} {
		t.Run(cause, func(t *testing.T) {
			paths := peopleTestPaths(t)
			now := peopleNowFn()
			if _, err := registry.ChangePerson(paths.Registry, "alice", []string{"finance"}, nil, false, false); err != nil {
				t.Fatal(err)
			}
			app := "finance"
			if cause == "wrong_app" {
				app = "photos"
			}
			session := mcpscope.Session{Who: "agent", Scope: mcpscope.Scope{Role: "people-manager", Apps: []string{app}, MaxDuration: "2h"}}
			ctx, cancel := context.WithCancel(mcpscope.WithClock(mcpscope.WithSession(t.Context(), session), func() time.Time { return now }))
			defer cancel()
			options := registry.ExtendOptions{Context: ctx, Service: "finance", Who: "alice", Value: "1h", Now: now}
			if cause == "cancelled_inside_lock" {
				options.Authorize = func(*registry.Registry, string) error { cancel(); return nil }
			}
			before, err := os.ReadFile(paths.Registry)
			if err != nil {
				t.Fatal(err)
			}
			result, err := registry.ExtendDuration(paths.Registry, options)
			if cause == "allowed" {
				if err != nil || result.ExpiresAt == nil || !result.ExpiresAt.Equal(now.Add(time.Hour)) {
					t.Fatal(result, err)
				}
				return
			}
			if err == nil {
				t.Fatal("locked lifetime effect accepted", cause)
			}
			after, err := os.ReadFile(paths.Registry)
			if err != nil || string(before) != string(after) {
				t.Fatal("denied lifetime wrote state", err)
			}
		})
	}
}

func TestWave2PortalStatusConsumer(t *testing.T) {
	paths := peopleTestPaths(t)
	old := mcpStatusFn
	t.Cleanup(func() { mcpStatusFn = old })
	mcpStatusFn = func(context.Context, string, string, string, string) (StatusResult, error) {
		return StatusResult{GuestLinks: []registry.GuestView{}, Portal: tsRuntime.PortalState{Enabled: true, Hostname: "home", State: "running", URL: "https://home.private.example"}}, nil
	}
	for _, role := range []string{"owner", "viewer"} {
		a := wave2Actions(t, paths, role, "photos")
		result, err := callMCPTool(t.Context(), a, "status", json.RawMessage(`{}`))
		if err != nil || result.IsError {
			t.Fatal(result, err)
		}
		data, _ := json.Marshal(result.StructuredContent)
		if strings.Contains(string(data), "home.private.example") != (role == "owner") {
			t.Fatalf("portal consumer for %s: %s", role, data)
		}
	}
}

func TestWave2RequestDecisionFailurePaths(t *testing.T) {
	paths, request := commandRequest(t)
	now := peopleNowFn()
	a := wave2Actions(t, paths, "people-manager", request.App)
	before, _ := os.ReadFile(paths.Registry)
	r, err := callMCPTool(t.Context(), a, "requests_approve", json.RawMessage(`{"id":"`+request.ID+`","for":"invalid-duration"}`))
	if code := mcpResultCode(r, err); code != "usage_error" {
		t.Fatalf("invalid approval duration: %s %v", code, err)
	}
	ctx, cancel := context.WithCancel(mcpscope.WithClock(mcpscope.WithSession(t.Context(), *a.session), func() time.Time { return now }))
	defer cancel()
	args := requestDecisionArguments{ID: request.ID, For: "1h"}
	locked := requestDecisionAuthorization(ctx, args, true, now)
	_, _, err = registry.DecideAccessRequestAuthorized(paths.Registry, request.ID, registry.RequestApproved, "1h", "", false, duration.Policy{}, now, func(reg *registry.Registry) error {
		cancel() // Cancellation arrives after the registry lock has been acquired.
		return locked(reg)
	}, ctx)
	if err == nil {
		t.Fatal("cancelled locked approval committed")
	}
	after, readErr := os.ReadFile(paths.Registry)
	if readErr != nil || string(before) != string(after) {
		t.Fatal("refused approval wrote registry", readErr)
	}
	// An unmatched row must not stop lookup of the requested row.
	second, err := registry.SubmitAccessRequest(paths.Registry, "bob", request.App, "1h", "", now)
	if err != nil {
		t.Fatal(err)
	}
	r, err = callMCPTool(t.Context(), a, "requests_deny", json.RawMessage(`{"id":"`+second.ID+`"}`))
	if err != nil || r.IsError {
		t.Fatal("second-row decision control", r, err)
	}
	r, err = callMCPTool(t.Context(), a, "requests_deny", json.RawMessage(`{"id":"missing-request"}`))
	if err != nil || !r.IsError {
		t.Fatal("missing request accepted", r, err)
	}
}

func TestWave2ScopedPeopleGrantPolicyFailures(t *testing.T) {
	for _, failure := range []string{"protected_owner", "guest_maximum", "corrupt_policy", "allowed_guest"} {
		t.Run(failure, func(t *testing.T) {
			paths := peopleTestPaths(t)
			now, value := peopleNowFn(), "1h"
			if _, err := registry.ChangePersonWithLifetime(paths.Registry, "alice", []string{"photos"}, false, registry.PersonLifetimeOptions{Value: &value, Audience: duration.Guest, Now: now}); err != nil {
				t.Fatal(err)
			}
			if _, err := registry.ChangePerson(paths.Registry, "owner", []string{"photos"}, nil, false, false); err != nil {
				t.Fatal(err)
			}
			if err := registry.SetPortal(paths.Registry, &registry.PortalConfig{Enabled: true, Hostname: "home", Owner: "owner"}); err != nil {
				t.Fatal(err)
			}
			a := wave2Actions(t, paths, "people-manager", "photos")
			a.session.Scope.MaxDuration = "10d"
			who := "alice"
			if failure == "protected_owner" {
				who = "owner"
			}
			if failure == "guest_maximum" {
				value = "8d"
			}
			if failure == "corrupt_policy" {
				configPath, err := config.ConfigPath()
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(configPath, []byte(`{"durations":{"public_max":"broken"}}`), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := os.ReadFile(paths.Registry)
			r, err := callMCPTool(t.Context(), a, "people_grant", json.RawMessage(`{"who":"`+who+`","app":"photos","for":"`+value+`"}`))
			if failure == "allowed_guest" {
				if err != nil || r.IsError {
					t.Fatal("guest grant control", r, err)
				}
				return
			}
			if err != nil || !r.IsError {
				t.Fatal("scoped grant accepted", failure, r, err)
			}
			after, readErr := os.ReadFile(paths.Registry)
			if readErr != nil || string(before) != string(after) {
				t.Fatal("refused scoped grant wrote registry", readErr)
			}
		})
	}
}

func TestWave2LockedGuestRevokeRole(t *testing.T) {
	paths := peopleTestPaths(t)
	now := peopleNowFn()
	grant, _, err := registry.CreateGuest(paths.Registry, registry.CreateGuestOptions{App: "photos", Value: "1h", PublicAck: true, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	session := mcpscope.Session{Who: "reader", Scope: mcpscope.Scope{Role: "viewer", Apps: []string{"photos"}}}
	ctx := mcpscope.WithClock(mcpscope.WithSession(t.Context(), session), func() time.Time { return now })
	before, _ := os.ReadFile(paths.Registry)
	if _, err := registry.RevokeGuestContext(ctx, paths.Registry, grant.ID, now); err == nil {
		t.Fatal("viewer revoked guest through locked API")
	}
	after, err := os.ReadFile(paths.Registry)
	if err != nil || string(before) != string(after) {
		t.Fatal("rejected revoke wrote registry", err)
	}
	if _, err := registry.RevokeGuest(paths.Registry, grant.ID, now); err != nil {
		t.Fatal("owner revoke control", err)
	}
}
