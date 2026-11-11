package cmd

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/mcpscope"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The exact withheld lists. An owner field added to DoctorResult, its daemon
// block or mcpStatusSummary changes these lists, so the addition is reviewed
// here instead of drifting silently.
func TestMCPReducedWithheldListsArePinned(t *testing.T) {
	for _, tc := range []struct {
		name string
		got  []string
		want []string
	}{
		{"doctor", mcpScopedDoctorWithheld(), []string{"access_log", "alerts", "credential_mode", "credential_tier", "credentials", "daemon.build_skew", "daemon.build_version", "daemon.executable", "daemon.identity_unverified", "daemon.pid", "guest_links", "health_exit_code", "health_status", "node_keys", "paths", "portal", "runtime_snapshot", "status", "supervision", "tailscale_ssh"}},
		{"status", mcpScopedStatusWithheld(), []string{"access_log", "alerts", "auth_url", "authenticated", "credential_stored", "credentials", "guest_links", "mcp_bindings", "next", "node_authorized", "portal", "services[].warnings", "status", "supervision"}},
		{"health", mcpScopedHealthWithheld(), []string{"services[].warnings"}},
		{"list", mcpScopedListWithheld(), []string{"services[].error.message", "services[].error.next", "services[].error.provision", "services[].warnings"}},
	} {
		if !slices.Equal(tc.got, tc.want) {
			t.Errorf("%s withheld = %q\nwant %q", tc.name, tc.got, tc.want)
		}
		if !slices.IsSorted(tc.got) {
			t.Errorf("%s withheld is not sorted: %q", tc.name, tc.got)
		}
	}
	// Each result gets its own copy: mutating one cannot change the next.
	first := mcpScopedDoctorWithheld()
	first[0] = "mutated"
	if mcpScopedDoctorWithheld()[0] == "mutated" {
		t.Fatal("withheld slices are shared between results")
	}
}

// Reduced results reuse these owner types whole. Every JSON path reachable
// from them is pinned: a new field anywhere inside, host evidence for
// example, turns this red so someone decides whether reduced roles may see
// it (and either strips it in the projection or adds it here).
func TestMCPReducedReusedTypesArePinned(t *testing.T) {
	for _, tc := range []struct {
		ty   reflect.Type
		want []string
	}{
		{reflect.TypeFor[mcpHealthService](), []string{"authorized", "health", "health.consecutive_failures", "health.kind", "health.last_checked", "health.last_error", "health.state", "health.status_code", "name", "node_key", "node_key.days_left", "node_key.expires_at", "node_key.next[]", "node_key.source", "node_key.state", "node_key.warning", "preserve_host", "request_limits", "request_limits.header_timeout", "request_limits.idle_timeout", "request_limits.max_body_bytes", "request_limits.read_timeout", "status", "warnings[]", "warnings[].code", "warnings[].message", "warnings[].severity", "warnings[].source"}},
		{reflect.TypeFor[ListServiceSummary](), []string{"error", "error.code", "error.message", "error.next[]", "error.provision", "error.provision.attempted", "error.provision.changed", "error.provision.reason", "error.provision.target", "error.provision.write_outcome", "funnel_active", "funnel_expires_at", "funnel_remaining", "funnel_requested", "funnel_state", "health", "health.consecutive_failures", "health.kind", "health.last_checked", "health.last_error", "health.state", "health.status_code", "name", "node_key", "node_key.days_left", "node_key.expires_at", "node_key.next[]", "node_key.source", "node_key.state", "node_key.warning", "preserve_host", "request_limits", "request_limits.header_timeout", "request_limits.idle_timeout", "request_limits.max_body_bytes", "request_limits.read_timeout", "state", "type", "url", "url_pending", "warnings[]", "warnings[].code", "warnings[].message", "warnings[].severity", "warnings[].source"}},
		{reflect.TypeFor[DoctorFinding](), []string{"area", "code", "evidence", "message", "service", "severity"}},
		{reflect.TypeFor[DoctorCounts](), []string{"critical", "errors", "findings", "info", "services", "warnings"}},
	} {
		if got := mcpJSONPaths("", tc.ty); !slices.Equal(got, tc.want) {
			t.Errorf("%s JSON paths changed; decide whether reduced MCP roles may see the difference, strip it in the reduced projection if not, then update this list.\ngot  %q\nwant %q", tc.ty, got, tc.want)
		}
	}
}

// An embedded struct without a JSON name would make withheld report its type
// name instead of its promoted fields; derivation refuses it.
func TestMCPWithheldDerivationRefusesUnnamedEmbedding(t *testing.T) {
	type Inner struct {
		Secret string `json:"secret"`
	}
	type owner struct {
		Inner
		Kept string `json:"kept"`
	}
	defer func() {
		if recover() == nil {
			t.Fatal("embedded struct without a JSON name was accepted")
		}
	}()
	mcpWithheldFields("", reflect.TypeFor[owner](), reflect.TypeFor[struct{}]())
}

// tools/list gives reduced roles the reduced output schemas and owners the
// strict owner schemas, unchanged.
func TestMCPToolsListAdvertisesOutputSchemaPerRole(t *testing.T) {
	for _, role := range []string{"owner", "viewer", "app-operator", "people-manager"} {
		scope := testRoleScope(role)
		a := scopeFixtureActions()
		s := mcpscope.Session{Who: "agent", Scope: scope}
		a.session = &s
		listed := mcpListToolsInProcess(t, a)
		for _, tool := range []string{"doctor", "status", "health", "list"} {
			got, ok := listed[tool]
			if !ok {
				t.Fatalf("%s: %s not advertised", role, tool)
			}
			want := mcpToolByName(t, tool).OutputSchema
			if role != "owner" {
				want = mcpScopedOutputSchema(tool)
			}
			if !jsonEqual(got, want) {
				t.Fatalf("%s %s advertised schema differs from the %s schema", role, tool, role)
			}
			props := mcpSchemaMap(t, got)["properties"].(map[string]any)
			if _, has := props["withheld"]; has != (role != "owner") {
				t.Fatalf("%s %s: withheld in schema = %t", role, tool, has)
			}
		}
		if role == "owner" {
			required := mcpSchemaMap(t, listed["doctor"])["required"].([]any)
			if len(required) != 13 {
				t.Fatalf("owner doctor schema lost required fields: %v", required)
			}
		}
	}
}

// mcpJSONPaths lists every JSON key path reachable from struct type t.
// Opaque leaf types (time, maps) end a path.
func mcpJSONPaths(prefix string, t reflect.Type) []string {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct || t == reflect.TypeFor[time.Time]() {
		return nil
	}
	paths := []string{}
	for i := range t.NumField() {
		field := t.Field(i)
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if !field.IsExported() || name == "-" {
			continue
		}
		if name == "" {
			name = field.Name
		}
		path := prefix + name
		ft := field.Type
		for ft.Kind() == reflect.Pointer {
			ft = ft.Elem()
		}
		if ft.Kind() == reflect.Slice {
			path += "[]"
			ft = ft.Elem()
		}
		paths = append(paths, path)
		paths = append(paths, mcpJSONPaths(path+".", ft)...)
	}
	slices.Sort(paths)
	return paths
}

// mcpListToolsInProcess runs tools/list against the real server built for
// actions and returns each tool's advertised output schema.
func mcpListToolsInProcess(t *testing.T, actions mcpActions) map[string]any {
	t.Helper()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	ctx := context.Background()
	serverSession, err := newMCPServer(actions).Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer serverSession.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "schema-probe", Version: "0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	listed, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]any{}
	for _, tool := range listed.Tools {
		out[tool.Name] = tool.OutputSchema
	}
	return out
}
