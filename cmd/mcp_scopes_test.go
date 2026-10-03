package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/health"
	"github.com/anydoor7/tslink/internal/inspect"
	"github.com/anydoor7/tslink/internal/mcpaudit"
	"github.com/anydoor7/tslink/internal/mcpscope"
	"github.com/anydoor7/tslink/internal/output"
	"github.com/anydoor7/tslink/internal/registry"
	tsRuntime "github.com/anydoor7/tslink/internal/runtime"
	"github.com/anydoor7/tslink/internal/server"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Independent expected capability table. Every shipped tool must be listed:
// adding a tool or widening a role without updating the policy test is red.
var scopeExpected = map[string]string{
	"share": "owner", "add": "owner", "list": "read", "unshare": "owner", "status": "read", "url": "read-app",
	"tags_list": "read", "tags_set": "owner", "access_explain": "read-app", "doctor": "read", "logs": "owner",
	"invite_user": "owner", "invite_device": "owner", "invite_list": "owner", "invite_revoke": "owner", "invite_resend": "owner",
	"template_list": "read", "template_plan": "owner", "template_apply": "owner", "apps_detect": "owner",
	"recipe_list": "read", "recipe_plan": "owner", "recipe_apply": "owner", "people_add": "owner", "people_update": "owner",
	"people_list": "read", "people_remove": "owner", "people_grant": "manage-app", "people_revoke": "manage-app",
	"portal_enable": "owner", "portal_disable": "owner", "extend": "manage-app",
	"guest_create": "owner", "guest_list": "owner", "guest_show": "owner", "guest_revoke": "owner",
	"requests_list": "requests", "requests_approve": "requests", "requests_deny": "requests",
	"access_log": "read", "access_summary": "read",
	"app_restart": "operate-app", "health": "read", "mcp_audit": "owner",
}

func TestMCPScopeProjectionAndMalformedInputs(t *testing.T) {
	scope := testRoleScope("viewer")
	for _, tool := range []string{"list", "status", "health", "people_list", "people_grant", "people_revoke", "tags_list", "access_explain"} {
		if _, err := mcpProject(tool, make(chan int), scope); err == nil {
			t.Fatal("unencodable projection accepted", tool)
		}
		if _, err := mcpProject(tool, "unexpected type", scope); err == nil {
			t.Fatal("malformed projection accepted", tool)
		}
	}
	v, err := mcpProject("list", map[string]any{"services": []ListServiceSummary{{Name: "photos", Warnings: []inspect.WarningView{{Code: "global"}}, Error: &tsRuntime.ServiceError{Code: "error", Message: "finance secret-token", Next: []string{"finance"}}}}}, scope)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(v)
	if strings.Contains(string(b), "finance") || strings.Contains(string(b), "global") || strings.Contains(string(b), "secret-token") {
		t.Fatal(string(b))
	}
	v, err = mcpProject("tags_list", map[string]any{}, scope)
	if err != nil {
		t.Fatal(err)
	}
	b, _ = json.Marshal(v)
	if string(b) != `{"services":[]}` {
		t.Fatal(string(b))
	}
	a := scopeFixtureActions()
	s := mcpscope.Session{Who: "agent", Scope: scope}
	a.session = &s
	a.audit = func(context.Context, mcpaudit.Entry) error { t.Fatal("hidden tool reached audit/effects"); return nil }
	if _, err := callMCPTool(context.Background(), a, "add", json.RawMessage(`{"name":"photos"}`)); err == nil {
		t.Fatal("hidden tool accepted at choke point")
	}
	for _, raw := range []json.RawMessage{json.RawMessage(`{"probe_external":"yes"}`), json.RawMessage(`{"unexpected":true}`)} {
		r, err := callMCPTool(context.Background(), a, "doctor", raw)
		if err != nil || !r.IsError {
			t.Fatal(r, err)
		}
	}
	for _, value := range []any{nil, "bad"} {
		a.status = func(ctx context.Context) (any, error) {
			if value == nil {
				return nil, errors.New("status unavailable")
			}
			return value, nil
		}
		if _, err := mcpScopedDoctor(context.Background(), a, scope); err == nil {
			t.Fatal("bad status accepted")
		}
	}
	for _, tool := range []string{"url", "app_restart", "people_grant"} {
		if _, err := mcpCallApps(a, tool, json.RawMessage(`{`)); err == nil {
			t.Fatal("partial input accepted", tool)
		}
	}
	if r, err := callMCPTool(context.Background(), a, "url", json.RawMessage(`{"name":"all"}`)); mcpResultCode(r, err) != "mcp_scope_denied" {
		t.Fatal("ungranted app named all accepted", r, err)
	}
	for _, tc := range []struct {
		result *mcp.CallToolResult
		err    error
		want   string
	}{
		{nil, errors.New("error"), "protocol_error"}, {nil, nil, "unknown"},
		{&mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "{"}}}, nil, "error"},
	} {
		if code := mcpResultCode(tc.result, tc.err); code != tc.want {
			t.Fatal(code, tc.want)
		}
	}
	for _, role := range []string{"app-operator", "people-manager"} {
		s, err := mcpStdioScope(role, "photos,finance", "", false)
		if err != nil || s.Scope.MaxDuration != "24h" || len(s.Scope.Apps) != 2 {
			t.Fatal(s, err)
		}
	}
}

func TestMCPScopeAuditCLIAndIndirectAttribution(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(config.ConfigDirEnv, dir)
	path := filepath.Join(dir, "registry.json")
	for _, app := range []string{"photos", "finance"} {
		if _, err := registry.Add(path, registry.Service{Name: app, Type: registry.TypeProxy, Target: "http://localhost:3000"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := registry.ChangePerson(path, "alice", []string{"finance"}, nil, true, false); err != nil {
		t.Fatal(err)
	}
	a := defaultMCPActions(sharePaths{Registry: path}, io.Discard)
	for _, tc := range []struct {
		tool, raw string
		apps      []string
	}{
		{"share", `{}`, []string{}},
		{"invite_revoke", `{"invite_id":"id"}`, []string{"finance", "photos"}},
		{"people_remove", `{"who":"alice"}`, []string{"finance"}},
		{"people_update", `{"who":"alice","apps":["all"],"replace_invites":{"photos":"id"},"reconcile_invites":{"finance":"id"}}`, []string{"finance", "photos"}},
		{"template_apply", `{"name":"local-web"}`, []string{"api", "web"}},
		{"recipe_apply", `{"recipe_id":"immich"}`, []string{"immich"}},
		{"add", `{`, []string{}},
	} {
		got, err := mcpCallApps(a, tc.tool, json.RawMessage(tc.raw))
		if err != nil || !reflect.DeepEqual(got, tc.apps) {
			t.Fatal(tc, got, err)
		}
	}
	if _, err := registry.Add(path, registry.Service{Name: "all", Type: registry.TypeProxy, Target: "http://localhost:3000"}); err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{"add", "unshare", "app_restart"} {
		apps, err := mcpCallApps(a, tool, json.RawMessage(`{"name":"all","app":"all"}`))
		if err != nil || !reflect.DeepEqual(apps, []string{"all"}) {
			t.Fatal("single-app name confused with selector", tool, apps, err)
		}
	}
	apps, err := mcpCallApps(a, "people_add", json.RawMessage(`{"apps":["all"]}`))
	if err != nil || !reflect.DeepEqual(apps, []string{"all", "finance", "photos"}) {
		t.Fatal("global selector omitted registered app named all", apps, err)
	}
	j := mcpaudit.Journal{Path: mcpAuditPath(path)}
	if err := j.Record(context.Background(), mcpaudit.Entry{ID: "test", Time: time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC), Principal: "family-agent", Role: "people-manager", Tool: "people_grant", Apps: []string{"photos"}, Result: "ok"}); err != nil {
		t.Fatal(err)
	}
	command, _, err := rootCmd.Find([]string{"mcp-audit"})
	if err != nil {
		t.Fatal(err)
	}
	oldOut, oldErr := command.OutOrStdout(), command.ErrOrStderr()
	oldJSON, _ := rootCmd.PersistentFlags().GetBool("json")
	t.Cleanup(func() {
		command.SetOut(oldOut)
		command.SetErr(oldErr)
		_ = rootCmd.PersistentFlags().Set("json", strconv.FormatBool(oldJSON))
	})
	for _, mode := range []string{"false", "true"} {
		var buf bytes.Buffer
		command.SetOut(&buf)
		if err := rootCmd.PersistentFlags().Set("json", mode); err != nil {
			t.Fatal(err)
		}
		if err := command.RunE(command, nil); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(buf.String(), "family-agent") || !strings.Contains(buf.String(), "photos") {
			t.Fatal(buf.String())
		}
		if mode == "true" {
			var envelope map[string]any
			if err := json.Unmarshal(buf.Bytes(), &envelope); err != nil || envelope["ok"] != true {
				t.Fatal(envelope, err)
			}
		}
	}
	if err := os.WriteFile(j.Path, []byte("["), 0600); err != nil {
		t.Fatal(err)
	}
	if err := command.RunE(command, nil); err == nil {
		t.Fatal("audit CLI accepted partial journal")
	}
	t.Setenv(config.ConfigDirEnv, "")
	t.Setenv("HOME", "")
	t.Setenv("APPDATA", "") // Windows uses os.UserConfigDir rather than HOME.
	if err := command.RunE(command, nil); err == nil {
		t.Fatal("audit CLI accepted unavailable home path")
	}
}

func testRoleScope(role string) mcpscope.Scope {
	s := mcpscope.Scope{Role: role}
	if role != "owner" {
		s.Apps = []string{"photos"}
	}
	if role == "app-operator" || role == "people-manager" {
		s.MaxDuration = "2h"
	}
	return s
}
func expectedScopeTool(role, tool string) bool {
	if role == "owner" {
		return true
	}
	switch scopeExpected[tool] {
	case "read", "read-app":
		return true
	case "manage-app":
		return role == "app-operator" || role == "people-manager"
	case "requests":
		return role == "people-manager"
	case "operate-app":
		return role == "app-operator"
	}
	return false
}

func TestMCPAuthorizationMatrix(t *testing.T) {
	if len(scopeExpected) != len(mcpToolDefinitions) {
		t.Fatal("tool matrix drift", len(scopeExpected), len(mcpToolDefinitions))
	}
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, d := range mcpToolDefinitions {
		if _, ok := scopeExpected[d.Name]; !ok {
			t.Fatal("uncovered tool", d.Name)
		}
		for _, role := range []string{"viewer", "app-operator", "people-manager", "owner"} {
			for _, app := range []string{"photos", "finance"} {
				t.Run(d.Name+"/"+role+"/"+app, func(t *testing.T) {
					s := mcpscope.Session{Who: "agent", Scope: testRoleScope(role)}
					allowed := expectedScopeTool(role, d.Name) && (role == "owner" || app == "photos")
					err := s.Authorize(d.Name, []string{app}, now)
					if (err == nil) != allowed {
						t.Fatalf("authorized=%t want=%t error=%v", err == nil, allowed, err)
					}
					if err != nil {
						var denied mcpscope.Denied
						if !errors.As(err, &denied) || denied.StableCode() != "mcp_scope_denied" {
							t.Fatal(err)
						}
					}
				})
			}
		}
	}
}

func scopeFixtureActions() mcpActions {
	a := fakeMCPActions()
	a.list = func(ctx context.Context) (any, error) {
		return map[string]any{"services": []ListServiceSummary{{Name: "photos", Type: registry.TypeProxy, State: "exact", FunnelState: "not_requested"}, {Name: "finance", Type: registry.TypeProxy, State: "failed", FunnelState: "not_requested"}}}, nil
	}
	a.status = func(ctx context.Context) (any, error) {
		return mcpStatusSummary{GuestLinks: []registry.GuestView{}, DaemonState: "absent", ServiceCount: 99, AuthorizedServiceCount: 99, AuthURL: "secret-enrollment", Next: []string{"finance"}, Credentials: StatusCredentials{MetadataError: "finance-secret"}, MCPBindings: []mcpBindingView{{Binding: mcpscope.Binding{Principal: "finance-owner", Scope: mcpscope.Scope{Role: "owner"}}}}, Services: []mcpHealthService{{Name: "photos", Status: "up", Health: health.State{State: health.Degraded}}, {Name: "finance", Status: "up", Health: health.State{State: health.Down}}}}, nil
	}
	a.tagsList = func() (any, error) {
		return TagsListResult{Services: []TagsServiceEntry{{Name: "photos", Tags: []string{"tag:photos"}}, {Name: "finance", Tags: []string{"tag:finance"}}}}, nil
	}
	a.peopleList = func() (any, error) {
		return PeopleListResult{People: []PeopleView{{Login: "alice", Grants: []PeopleGrantView{{PersonGrant: registry.PersonGrant{App: "photos"}}, {PersonGrant: registry.PersonGrant{App: "finance"}}}, Invites: []registry.PersonInvite{{App: "finance", ID: "secret-invite"}}}, {Login: "finance-person", Grants: []PeopleGrantView{{PersonGrant: registry.PersonGrant{App: "finance"}}}}}}, nil
	}
	return a
}

func TestMCPScopesHTTPRealHandlerMatrix(t *testing.T) {
	for _, role := range []string{"viewer", "app-operator", "people-manager", "owner"} {
		t.Run(role, func(t *testing.T) {
			a := scopeFixtureActions()
			cp := &server.MCPControlPlane{Bindings: []mcpscope.Binding{{Principal: "agent@example.com", Scope: testRoleScope(role)}}, Handler: newMCPStreamableHandler(a), EventsSnapshot: mcpEventsSnapshotFn(a)}
			if err := cp.Validate(); err != nil {
				t.Fatal(err)
			}
			h := httptest.NewServer(server.NewMCPControlPlaneHandler(cp, mcpHTTPWhoIsClient(t, "agent@example.com")))
			defer h.Close()
			client := mcp.NewClient(&mcp.Implementation{Name: "scope-client", Version: "1"}, nil)
			sess, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: h.URL + server.MCPControlPlanePath, HTTPClient: h.Client(), DisableStandaloneSSE: true}, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer sess.Close()
			listed, err := sess.ListTools(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			got := map[string]bool{}
			for _, tool := range listed.Tools {
				got[tool.Name] = true
			}
			for _, definition := range mcpToolDefinitions {
				tool := definition.Name
				allowed := expectedScopeTool(role, tool)
				if got[tool] != allowed {
					t.Fatalf("advertised %s=%t want %t", tool, got[tool], allowed)
				}
				for _, app := range []string{"photos", "finance"} {
					t.Run(tool+"/"+app, func(t *testing.T) {
						var args map[string]any
						raw := strings.ReplaceAll(mcpToolMinimalArguments[tool], "web", app)
						if err := json.Unmarshal([]byte(raw), &args); err != nil {
							t.Fatal(err)
						}
						if !allowed {
							status, body := scopeUnknownHTTP(t, h, tool)
							controlStatus, controlBody := scopeUnknownHTTP(t, h, "nonexistent_tool")
							if status != 400 || status != controlStatus || strings.ReplaceAll(body, tool, "NAME") != strings.ReplaceAll(controlBody, "nonexistent_tool", "NAME") {
								t.Fatalf("hidden vs nonexistent: %d %q; %d %q", status, body, controlStatus, controlBody)
							}
							return
						}
						if tool == "template_plan" || tool == "template_apply" {
							args["name"] = "local-web"
						}
						result, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: tool, Arguments: args})
						if err != nil {
							t.Fatal(err)
						}
						if role != "owner" && strings.HasSuffix(scopeExpected[tool], "-app") && app == "finance" {
							if code := mcpToolResultFailure(t, result).Error.Code; code != "mcp_scope_denied" {
								t.Fatal(code)
							}
							return
						}
						if result.IsError {
							t.Fatal(result.Content)
						}
						if role != "owner" {
							encoded, _ := json.Marshal(result)
							if strings.Contains(string(encoded), "finance") || strings.Contains(string(encoded), "secret-") {
								t.Fatalf("out-of-scope egress: %s", encoded)
							}
							validateAgainstToolOutputSchema(t, tool, result.StructuredContent)
						}
					})
				}
			}
			if role != "owner" {
				resp, err := h.Client().Get(h.URL + server.MCPEventsPath)
				if err != nil {
					t.Fatal(err)
				}
				body, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				if resp.StatusCode != 404 || strings.Contains(string(body), "finance") {
					t.Fatal(resp.StatusCode, string(body))
				}
			}
		})
	}
}

func TestMCPScopedStdioShippedBinary(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(config.ConfigDirEnv, dir)
	path := filepath.Join(dir, "registry.json")
	for _, app := range []string{"photos", "finance"} {
		if _, err := registry.Add(path, registry.Service{Name: app, Type: registry.TypeProxy, Target: "http://localhost:3000"}); err != nil {
			t.Fatal(err)
		}
	}
	input := initializedMCPInput(strings.Join([]string{
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"list","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"url","arguments":{"name":"finance"}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"add","arguments":{"name":"photos","type":"proxy","target":"http://localhost:3000"}}}`,
		`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"list","arguments":{"scope":"owner"}}}`,
	}, "\n"))
	stdout, stderr, code := runCompiledTSLinkWithConfigDir(t, dir, input, "mcp", "--scope", "viewer", "--apps", "photos")
	if code != 0 {
		t.Fatal(code, stderr)
	}
	frames := decodeMCPResponses(t, stdout)
	list := mcpFrameByID(t, frames, float64(3))
	encoded, _ := json.Marshal(list)
	if !strings.Contains(string(encoded), "photos") || strings.Contains(string(encoded), "finance") {
		t.Fatal(string(encoded))
	}
	if mcpFrameByID(t, frames, float64(5))["error"] == nil {
		t.Fatal("stdio exposed add")
	}
	for _, id := range []float64{4, 6} {
		result := mcpFrameByID(t, frames, id)["result"].(map[string]any)
		if result["isError"] != true {
			t.Fatal("stdio escalated", result)
		}
	}
	reg, _, err := registry.Preflight(path)
	if err != nil || len(reg.Services) != 2 {
		t.Fatal(reg, err)
	}
	_, _, code = runCompiledTSLinkWithConfigDir(t, dir, "", "mcp", "--scope", "viewer")
	if code != output.ExitUsage {
		t.Fatal("scope without apps/inventory", code)
	}
}

func scopeUnknownHTTP(t *testing.T, h *httptest.Server, tool string) (int, string) {
	t.Helper()
	fixture := mcpHTTPToolCallRequest(tool)
	r, err := http.NewRequest(http.MethodPost, h.URL+server.MCPControlPlanePath, fixture.Body)
	if err != nil {
		t.Fatal(err)
	}
	r.Header = fixture.Header
	response, err := h.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, string(data)
}

func TestMCPScopedBindingExpiryAndLegacyHTTP(t *testing.T) {
	exp := time.Now().Add(-time.Hour)
	for _, tc := range []struct {
		name     string
		allow    []string
		bindings []mcpscope.Binding
		status   int
	}{
		{"expired", nil, []mcpscope.Binding{{Principal: "agent", Scope: testRoleScope("viewer"), ExpiresAt: &exp}}, 403},
		{"legacy-owner", []string{"agent"}, nil, 200},
		{"login-binding", nil, []mcpscope.Binding{{Principal: "agent", Scope: testRoleScope("viewer")}}, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			h := server.NewMCPControlPlaneHandler(&server.MCPControlPlane{AllowedUsers: tc.allow, Bindings: tc.bindings, Handler: newMCPStreamableHandler(mcpHTTPCountingActions(&calls))}, mcpHTTPWhoIsClient(t, "agent"))
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, mcpHTTPToolCallRequest("list"))
			if rr.Code != tc.status {
				t.Fatal(rr.Code, rr.Body.String())
			}
			if (calls > 0) != (tc.status == 200) {
				t.Fatal(calls)
			}
			if tc.status == 403 && !strings.Contains(rr.Body.String(), "mcp_scope_denied") {
				t.Fatal(rr.Body.String())
			}
		})
	}
}

func TestMCPScopeMutationAuditAndDuration(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(config.ConfigDirEnv, dir)
	path := filepath.Join(dir, "registry.json")
	for _, app := range []string{"photos", "finance"} {
		if _, err := registry.Add(path, registry.Service{Name: app, Type: registry.TypeProxy, Target: "http://localhost:3000"}); err != nil {
			t.Fatal(err)
		}
	}
	a := defaultMCPActions(sharePaths{Registry: path}, io.Discard)
	if _, err := registry.ChangePerson(path, "alice", []string{"finance"}, nil, false, false); err != nil {
		t.Fatal(err)
	}
	s := mcpscope.Session{Who: "agent@example.com", Scope: testRoleScope("people-manager")}
	a.session = &s
	for _, tc := range []struct{ app, lifetime, code string }{{"photos", "1h", "ok"}, {"finance", "1h", "mcp_scope_denied"}, {"photos", "never", "mcp_scope_denied"}, {"photos", "3h", "mcp_scope_denied"}} {
		raw, _ := json.Marshal(map[string]any{"who": "alice", "app": tc.app, "for": tc.lifetime})
		result, err := callMCPTool(context.Background(), a, "people_grant", raw)
		if code := mcpResultCode(result, err); code != tc.code {
			t.Fatal(code, tc)
		}
		if tc.code == "ok" {
			validateAgainstToolOutputSchema(t, "people_grant", result.StructuredContent)
		}
	}
	result, err := callMCPTool(context.Background(), a, "people_revoke", json.RawMessage(`{"who":"alice","app":"photos"}`))
	if err != nil || result.IsError {
		t.Fatal(err, result)
	}
	entries, err := (mcpaudit.Journal{Path: mcpAuditPath(path)}).Read()
	if err != nil || len(entries) != 7 {
		t.Fatal(len(entries), err)
	}
	for _, e := range entries {
		if e.Principal != "agent@example.com" || e.Role != "people-manager" || e.Time.IsZero() || e.Tool == "" || len(e.Apps) != 1 {
			t.Fatal(e)
		}
	}
	if entries[0].Result != "started" || entries[1].Result != "ok" || entries[0].ID != entries[1].ID || entries[2].Result != "denied" {
		t.Fatal(entries)
	}
	reg, _, err := registry.Preflight(path)
	if err != nil || registry.PersonGrantActiveAt(reg.People[0], "photos", time.Now()) {
		t.Fatal(reg, err)
	}
	before, _ := os.ReadFile(path)
	a.audit = func(context.Context, mcpaudit.Entry) error { return errors.New("secret-token") }
	result, err = callMCPTool(context.Background(), a, "people_grant", json.RawMessage(`{"who":"bob","app":"photos","for":"1h"}`))
	if mcpResultCode(result, err) != "mcp_audit_unavailable" {
		t.Fatal(result, err)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("audit refusal did not prevent mutation")
	}
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), "secret-token") {
		t.Fatal("journal error leaked")
	}
	calls := 0
	a.audit = func(context.Context, mcpaudit.Entry) error {
		calls++
		if calls == 2 {
			return errors.New("failed")
		}
		return nil
	}
	result, err = callMCPTool(context.Background(), a, "people_grant", json.RawMessage(`{"who":"bob","app":"photos","for":"1h"}`))
	if mcpResultCode(result, err) != "mcp_audit_unavailable" || calls != 2 {
		t.Fatal(result, err, calls)
	}
}

func TestMCPScopeExpiryAfterDurableIntent(t *testing.T) {
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	expiry := now.Add(time.Minute)
	for _, completionFails := range []bool{false, true} {
		a := scopeFixtureActions()
		s := mcpscope.Session{Who: "agent", Scope: testRoleScope("app-operator"), ExpiresAt: &expiry}
		a.session, a.nowFn = &s, func() time.Time { return now }
		called := false
		a.appRestart = func(context.Context, string) (any, error) { called = true; return map[string]any{}, nil }
		var receipts []mcpaudit.Entry
		a.audit = func(ctx context.Context, e mcpaudit.Entry) error {
			receipts = append(receipts, e)
			if len(receipts) == 1 {
				now = expiry
			} else if completionFails {
				return errors.New("unavailable")
			}
			return nil
		}
		now = expiry.Add(-time.Minute)
		r, err := callMCPTool(context.Background(), a, "app_restart", json.RawMessage(`{"app":"photos"}`))
		want := "mcp_scope_denied"
		if completionFails {
			want = "mcp_audit_unavailable"
		}
		if called || mcpResultCode(r, err) != want || len(receipts) != 2 || receipts[0].Result != "started" || receipts[1].Result != "denied" || receipts[0].ID != receipts[1].ID {
			t.Fatal(called, r, err, receipts)
		}
	}
}

func TestMCPScopeDefaultRestartAndDomainRefusals(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(config.ConfigDirEnv, dir)
	path := filepath.Join(dir, "registry.json")
	if _, err := registry.Add(path, registry.Service{Name: "photos", Type: registry.TypeProxy, Target: "http://localhost:3000"}); err != nil {
		t.Fatal(err)
	}
	a := defaultMCPActions(sharePaths{Registry: path}, io.Discard)
	s := mcpscope.Session{Who: "operator", Scope: mcpscope.Scope{Role: "app-operator", Apps: []string{"photos", "missing"}, MaxDuration: "2h"}}
	a.session = &s
	r, err := callMCPTool(context.Background(), a, "app_restart", json.RawMessage(`{"app":"photos"}`))
	if err != nil || r.IsError {
		t.Fatal(r, err)
	}
	reg, _, err := registry.Preflight(path)
	if err != nil || reg.Services[0].RestartGeneration != 1 {
		t.Fatal(reg, err)
	}
	validateAgainstToolOutputSchema(t, "app_restart", r.StructuredContent)
	r, err = callMCPTool(context.Background(), a, "people_grant", json.RawMessage(`{"who":"alice","app":"missing","for":"1h"}`))
	if mcpResultCode(r, err) != "not_found" {
		t.Fatal(r, err)
	}
	r, err = callMCPTool(context.Background(), a, "people_revoke", json.RawMessage(`{"who":"alice","app":"photos","for":"1h"}`))
	if mcpResultCode(r, err) != "usage_error" {
		t.Fatal(r, err)
	}
	entries, err := a.auditRead()
	if err != nil || len(entries) != 6 || entries[1].Result != "ok" || entries[3].Result != "not_found" || entries[5].Result != "usage_error" {
		t.Fatal(entries, err)
	}
}

func TestMCPScopesStatusDoctorAndSafeErrors(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(config.ConfigDirEnv, dir)
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	past := now.Add(-time.Hour)
	cfg := config.GlobalConfig{MCP: &config.MCPConfig{Allow: []string{"tag:wide"}, Bindings: []mcpscope.Binding{{Principal: "agent", Scope: testRoleScope("viewer"), ExpiresAt: &past}}}}
	if err := config.SaveGlobalConfig(cfg); err != nil {
		t.Fatal(err)
	}
	views := mcpBindingViews(now)
	if len(views) != 2 || !views[0].Legacy || !views[1].Expired {
		t.Fatal(views)
	}
	var statusText bytes.Buffer
	formatStatus(StatusResult{MCPBindings: views}, &statusText)
	if !strings.Contains(statusText.String(), "role=viewer apps=[photos]") || !strings.Contains(statusText.String(), "expired=true") || !strings.Contains(statusText.String(), "tag:wide role=owner") {
		t.Fatal(statusText.String())
	}
	d := DoctorResult{Findings: []DoctorFinding{}}
	diagnoseMCPBindings(&d, cfg, now)
	if len(d.Findings) != 2 || d.Findings[0].Code != "mcp_binding_expired" || d.Findings[1].Code != "mcp_owner_tag" {
		t.Fatal(d.Findings)
	}
	a := scopeFixtureActions()
	s := mcpscope.Session{Who: "agent", Scope: testRoleScope("viewer")}
	a.session = &s
	a.url = func(context.Context, string, time.Duration) (any, error) {
		return nil, registry.CodedError{Code: "not_found", Message: "finance secret-token", Next: []string{"finance"}}
	}
	r, err := callMCPTool(context.Background(), a, "url", json.RawMessage(`{"name":"photos"}`))
	if mcpResultCode(r, err) != "not_found" {
		t.Fatal(r, err)
	}
	encoded, _ := json.Marshal(r)
	if strings.Contains(string(encoded), "finance") || strings.Contains(string(encoded), "secret-token") {
		t.Fatal("domain error leaked")
	}
	r, err = callMCPTool(context.Background(), a, "doctor", json.RawMessage(`{"probe_external":true}`))
	if mcpResultCode(r, err) != "mcp_scope_denied" {
		t.Fatal(r, err)
	}
	for _, role := range []string{"viewer", "app-operator", "people-manager", "owner"} {
		s, err := mcpStdioScope(role, "", "", role == "viewer")
		if role == "viewer" || role == "owner" {
			if err != nil {
				t.Fatal(err)
			}
		} else if err == nil || reflect.DeepEqual(s.Scope, mcpscope.Scope{}) {
			t.Fatal(role, s, err)
		}
	}
}
