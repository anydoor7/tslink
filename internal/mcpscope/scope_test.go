package mcpscope

import (
	"context"
	"strings"
	"testing"
	"time"
)

var testNow = time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)

func TestScopeValidation(t *testing.T) {
	for _, tc := range []struct {
		scope Scope
		valid bool
	}{
		{Scope{Role: "owner"}, true}, {Scope{Role: "owner", Apps: []string{"photos"}}, false},
		{Scope{Role: "viewer", Apps: []string{"photos"}}, true}, {Scope{Role: "viewer", Inventory: true}, true},
		{Scope{Role: "viewer"}, false}, {Scope{Role: "viewer", Inventory: true, MaxDuration: "1h"}, false},
		{Scope{Role: "custom", Apps: []string{"photos"}}, false},
		{Scope{Role: "app-operator", Apps: []string{"photos"}, MaxDuration: "1d"}, true},
		{Scope{Role: "people-manager", Apps: []string{"photos"}, MaxDuration: "1h"}, true},
		{Scope{Role: "people-manager", Apps: []string{"photos"}, MaxDuration: "never"}, false},
		{Scope{Role: "app-operator", Apps: []string{"photos"}, MaxDuration: "0"}, false},
		{Scope{Role: "app-operator", Inventory: true, MaxDuration: "1h"}, false},
		{Scope{Role: "viewer", Apps: []string{"photos", "photos"}}, false},
		{Scope{Role: "viewer", Apps: []string{"all"}}, false},
		{Scope{Role: "viewer", Apps: []string{"../photos"}}, false},
	} {
		if (tc.scope.Validate() == nil) != tc.valid {
			t.Errorf("%+v valid=%t", tc.scope, tc.valid)
		}
	}
}

func TestScopePrincipals(t *testing.T) {
	for _, tc := range []struct{ in, want string }{{" Alice@EXAMPLE.com ", "alice@example.com"}, {"K@example.com", "K@example.com"}, {"tag:agent", "tag:agent"}, {"", ""}, {"bad user", ""}, {"tag:???", ""}, {string([]byte{0xff}), ""}, {"ab\u0085cd", ""}, {strings.Repeat("a", 513), ""}} {
		got, err := Principal(tc.in)
		if got != tc.want || (err == nil) != (tc.want != "") {
			t.Errorf("principal %q = %q %v", tc.in, got, err)
		}
	}
}

func TestScopeAnchoredDeadline(t *testing.T) {
	b := Binding{Principal: "agent", Scope: Scope{Role: "viewer", Apps: []string{"photos"}}, IssuedAt: &testNow, For: "1d"}
	d, err := b.Deadline()
	if err != nil || !d.Equal(testNow.Add(24*time.Hour)) {
		t.Fatalf("deadline %v %v", d, err)
	}
	before := testNow.Add(time.Hour)
	s, err := Resolve("agent", nil, nil, []Binding{b}, before)
	if err != nil || !s.Active(before) || s.Active(*d) {
		t.Fatalf("session %+v %v", s, err)
	}
	if _, err := Resolve("agent", nil, nil, []Binding{b}, *d); err == nil {
		t.Fatal("expired binding authorized")
	}
	for _, bad := range []Binding{{For: "1h"}, {For: "1h", IssuedAt: &testNow, ExpiresAt: d}, {IssuedAt: &testNow}, {ExpiresAt: &time.Time{}}, {For: "never", IssuedAt: &testNow}, {For: "1h", IssuedAt: &time.Time{}}} {
		if _, err := bad.Deadline(); err == nil {
			t.Fatalf("invalid deadline %+v", bad)
		}
	}
	if d, err := (Binding{}).Deadline(); err != nil || d != nil {
		t.Fatal(d, err)
	}
	if d2, err := (Binding{ExpiresAt: d}).Deadline(); err != nil || !d2.Equal(*d) {
		t.Fatal(d2, err)
	}
}

func TestScopeResolveIdentityAndCompatibility(t *testing.T) {
	viewer := Scope{Role: "viewer", Apps: []string{"photos"}}
	bindings := []Binding{{Principal: "agent", Scope: viewer}, {Principal: "tag:helper", Scope: viewer}}
	for _, tc := range []struct {
		login       string
		tags, allow []string
		bindings    []Binding
		role        string
	}{
		{"agent", nil, nil, bindings, "viewer"}, {"bob", []string{"tag:helper"}, nil, bindings, "viewer"},
		{"tag:helper", nil, nil, bindings, ""},
		{"Owner", nil, []string{" owner "}, nil, "owner"}, {"bob", []string{"tag:admin"}, []string{"tag:admin"}, nil, "owner"},
		{"nobody", nil, nil, bindings, ""}, {"bad user", []string{"tag:helper"}, nil, bindings, ""},
		{"bob", []string{"tag:helper", "tag:other"}, nil, append(append([]Binding{}, bindings...), Binding{Principal: "tag:other", Scope: viewer}), ""},
		{"agent", []string{"tag:admin"}, []string{"tag:admin"}, bindings, "viewer"},
		{"agent", nil, []string{"agent"}, bindings, ""}, {"agent", nil, nil, []Binding{{Principal: "agent", Scope: Scope{Role: "unknown"}}}, ""},
	} {
		s, err := Resolve(tc.login, tc.tags, tc.allow, tc.bindings, testNow)
		if (err == nil) != (tc.role != "") || s.Scope.Role != tc.role {
			t.Errorf("resolve %+v = %+v %v", tc, s, err)
		}
	}
	if err := ValidateBindings([]string{"", "   "}, bindings); err != nil {
		t.Fatal(err)
	}
	if err := ValidateBindings([]string{"bad user"}, nil); err == nil {
		t.Fatal("invalid legacy identity")
	}
	if err := ValidateBindings(nil, append(bindings, bindings[0])); err == nil {
		t.Fatal("duplicate binding")
	}
	if err := ValidateBindings(nil, []Binding{{Principal: "Agent", Scope: viewer}}); err == nil {
		t.Fatal("noncanonical binding")
	}
	if err := ValidateBindings(nil, []Binding{{Principal: "agent", Scope: viewer, For: "1h"}}); err == nil {
		t.Fatal("bad expiry")
	}
	d := testNow
	if _, err := Resolve("bob", []string{"tag:helper"}, nil, []Binding{{Principal: "tag:helper", Scope: viewer, ExpiresAt: &d}}, testNow); err == nil {
		t.Fatal("expired tag")
	}
}

func TestScopeContextAndDenial(t *testing.T) {
	s := Session{Who: "agent", Scope: Scope{Role: "viewer", Apps: []string{"photos"}}}
	if _, ok := FromContext(context.Background()); ok {
		t.Fatal("context invented identity")
	}
	got, ok := FromContext(WithSession(context.Background(), s))
	if !ok || got.Who != "agent" {
		t.Fatal(got, ok)
	}
	for _, tc := range []struct {
		tool, app string
		allowed   bool
	}{{"list", "photos", true}, {"url", "photos", true}, {"url", "finance", false}, {"people_grant", "photos", false}, {"future_tool", "photos", false}} {
		err := s.Authorize(tc.tool, []string{tc.app}, testNow)
		if (err == nil) != tc.allowed {
			t.Errorf("%+v %v", tc, err)
		}
		if err != nil {
			d := err.(Denied)
			if d.StableCode() != "mcp_scope_denied" || d.Error() != "MCP scope does not permit this action" {
				t.Fatal(d)
			}
		}
	}
	if !(Scope{Role: "viewer", Inventory: true}).AllowsApp("finance") || !(Scope{Role: "owner"}).AllowsApp("finance") {
		t.Fatal("inventory/owner restriction")
	}
	if _, err := ParseDuration("-1h"); err == nil {
		t.Fatal("negative duration")
	}
}

func TestScopeTagPrincipalCaseDoesNotBroadenLegacyAuthority(t *testing.T) {
	for _, raw := range []string{"TAG:helper", "Tag:helper"} {
		if _, err := Principal(raw); err == nil {
			t.Fatal("noncanonical tag accepted", raw)
		}
		if err := ValidateBindings([]string{raw}, nil); err == nil {
			t.Fatal("legacy tag typo could acquire authority", raw)
		}
	}
	if p, err := Principal(" tag:helper "); err != nil || p != "tag:helper" {
		t.Fatal(p, err)
	}
	if p, err := Principal(" tag:HELPER "); err != nil || p != "tag:HELPER" {
		t.Fatal("valid legacy tag spelling changed", p, err)
	}
	if _, err := Resolve("bob", []string{"tag:helper"}, []string{"tag:HELPER"}, nil, testNow); err == nil {
		t.Fatal("tag case folding broadened owner authority")
	}
	s, err := Resolve("bob", []string{"tag:HELPER"}, []string{"tag:HELPER"}, nil, testNow)
	if err != nil || s.Scope.Role != "owner" || s.Who != "tag:HELPER" {
		t.Fatal("legacy uppercase tag compatibility", s, err)
	}
	if _, err := Resolve("tag:helper", nil, []string{"tag:helper"}, nil, testNow); err == nil {
		t.Fatal("login impersonated legacy tag")
	}
}

func TestScopeToolAllowlistAndGrantDeadline(t *testing.T) {
	for _, role := range []string{"viewer", "app-operator", "people-manager", "owner", "invalid"} {
		s := Scope{Role: role}
		for _, tool := range []string{"status", "people_grant", "people_revoke", "app_restart", "unshare"} {
			want := role == "owner" || tool == "status" && role != "invalid" || (tool == "people_grant" || tool == "people_revoke") && (role == "app-operator" || role == "people-manager") || tool == "app_restart" && role == "app-operator"
			if s.ToolAllowed(tool) != want {
				t.Fatalf("role=%s tool=%s allowed=%t want=%t", role, tool, s.ToolAllowed(tool), want)
			}
		}
	}
	expiry := testNow.Add(2 * time.Hour)
	for _, tc := range []struct {
		name, role, max, lifetime string
		expires                   *time.Time
		allowed                   bool
	}{
		{"owner", "owner", "", "3h", nil, true},
		{"maximum-inclusive", "people-manager", "2h", "2h", nil, true},
		{"over-maximum", "people-manager", "2h", "3h", nil, false},
		{"bad-maximum", "people-manager", "never", "1h", nil, false},
		{"forever", "owner", "", "never", nil, false},
		{"deadline-inclusive", "app-operator", "3h", "2h", &expiry, true},
		{"deadline-exceeded", "app-operator", "3h", "3h", &expiry, false},
		{"expired", "owner", "", "1h", &testNow, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := Session{Scope: Scope{Role: tc.role, MaxDuration: tc.max}, ExpiresAt: tc.expires}
			got, err := s.GrantDeadline(tc.lifetime, testNow)
			if (err == nil) != tc.allowed {
				t.Fatal(got, err)
			}
			if tc.allowed {
				d, _ := ParseDuration(tc.lifetime)
				if !got.Equal(testNow.Add(d)) {
					t.Fatal(got)
				}
			}
		})
	}
}
