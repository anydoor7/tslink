package cmd

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/accesslog"
	"github.com/anydoor7/tslink/internal/health"
	"github.com/anydoor7/tslink/internal/inspect"
	"github.com/anydoor7/tslink/internal/mcpscope"
	"github.com/anydoor7/tslink/internal/registry"
)

// reducedReadFixture is a host whose daemon runs under a verified supervisor,
// with stored credentials, an enabled access log, a guest link and an owner
// doctor that has a warning. Every global value is non-zero, so a reduced
// projection that replaces it with a zero value is observable. Timestamps sit
// in 2030 so they cannot be mistaken for wall-clock reads.
func reducedReadFixture() mcpActions {
	at := time.Date(2030, 3, 4, 5, 6, 7, 0, time.UTC)
	a := fakeMCPActions()
	a.status = func(context.Context) (any, error) {
		return mcpStatusSummary{
			DaemonRunning: true, DaemonState: daemonStateRunning,
			Supervision:      Supervision{Manager: "launchd", Installed: true, Autostart: true, RestartOnExit: true, Path: "/secret-supervision-path", Detail: "secret-detail"},
			Credentials:      StatusCredentials{APIKey: StatusCredentialSlot{Present: true, Fingerprint: "secret-fingerprint", ExpiresAt: &at, ExpiryState: "ok"}},
			CredentialStored: true,
			AccessLog:        accesslog.Health{Enabled: true, Current: true, LastWrite: &at, UpdatedAt: at, Size: 4096},
			Alerts:           health.AlertsView{Notifier: "secret-notifier"},
			GuestLinks:       []registry.GuestView{{ID: "secret-guest", App: "finance", CreatedAt: at, ExpiresAt: at}},
			Services: []mcpHealthService{
				{Name: "photos", Status: "up", Health: health.State{State: health.Healthy, StatusCode: 200}, Warnings: []inspect.WarningView{{Code: "secret-warning"}}, Authorized: true},
				{Name: "finance", Status: "up", Health: health.State{State: health.Down}, Authorized: true},
			},
			Authenticated: true, NodeAuthorized: true, AuthorizedServiceCount: 2, ServiceCount: 2,
		}, nil
	}
	a.doctor = func(context.Context, bool) (any, error) {
		r := DoctorResult{GuestLinks: []registry.GuestView{}, SchemaVersion: 1, ExecutionStatus: doctorExecutionCompleted, CredentialMode: doctorCredentialAPIToken, Findings: []DoctorFinding{}}
		r.Daemon = DoctorDaemon{Running: true, PID: 4242}
		r.Counts.Services = 2
		r.addFinding(inspect.WarningCodeDaemonBuildSkew, "", "daemon", "secret-build-skew", nil)
		r.finalize()
		return r, nil
	}
	return a
}

func reducedToolPayload(t *testing.T, a mcpActions, scope mcpscope.Scope, tool string) map[string]any {
	t.Helper()
	s := mcpscope.Session{Who: "agent", Scope: scope}
	a.session = &s
	r, err := callMCPTool(context.Background(), a, tool, json.RawMessage(`{}`))
	if err != nil || r == nil || r.IsError {
		t.Fatalf("%s as %s: %+v %v", tool, scope.Role, r, err)
	}
	wire, err := json.Marshal(r.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(wire, &payload); err != nil {
		t.Fatal(err)
	}
	return payload
}

func reducedReadScopes() []mcpscope.Scope {
	return []mcpscope.Scope{
		{Role: "viewer", Inventory: true},
		testRoleScope("viewer"),
		testRoleScope("app-operator"),
		testRoleScope("people-manager"),
	}
}

// A reduced doctor must never contradict the owner doctor for the same host:
// each verdict or liveness field either carries the owner's value or is
// absent and named in withheld. A zero value would read as "not running" or
// "healthy" while the daemon runs and the owner sees a warning.
func TestMCPReducedDoctorNeverReportsAFalseZero(t *testing.T) {
	owner := reducedToolPayload(t, reducedReadFixture(), testRoleScope("owner"), "doctor")
	if owner["health_status"] != doctorStatusWarning || owner["health_exit_code"] != float64(64) || owner["daemon"].(map[string]any)["running"] != true {
		t.Fatalf("fixture owner doctor is not running+warning: %v", owner)
	}
	for _, scope := range reducedReadScopes() {
		t.Run(scope.Role+"/"+strings.Join(scope.Apps, ","), func(t *testing.T) {
			got := reducedToolPayload(t, reducedReadFixture(), scope, "doctor")
			daemon, _ := got["daemon"].(map[string]any)
			if daemon["running"] != true {
				t.Fatalf("reduced doctor daemon = %v while the daemon runs", got["daemon"])
			}
			withheld := reducedWithheld(t, got)
			for _, field := range []string{"status", "health_status", "health_exit_code", "credential_mode", "credential_tier", "supervision", "credentials", "guest_links", "access_log", "alerts", "portal", "paths", "node_keys", "runtime_snapshot", "tailscale_ssh"} {
				value, present := got[field]
				switch {
				case present && slices.Contains(withheld, field):
					t.Fatalf("%s is both present (%v) and withheld", field, value)
				case present && !jsonEqual(value, owner[field]):
					t.Fatalf("reduced %s = %v, owner reports %v, and it is not withheld", field, value, owner[field])
				case !present && !slices.Contains(withheld, field):
					t.Fatalf("%s is absent but not named in withheld %v", field, withheld)
				}
			}
			validateAgainstScopedOutputSchema(t, "doctor", scope, got)
		})
	}
}

// The deliberate redactions still hold: no PID, executable, paths, credential
// fingerprints, supervisor evidence, guest links, out-of-scope apps or global
// warning text reach a reduced doctor or status result.
func TestMCPReducedReadsKeepGlobalEvidenceWithheld(t *testing.T) {
	for _, scope := range reducedReadScopes() {
		for _, tool := range []string{"doctor", "status", "health", "list"} {
			t.Run(scope.Role+"/"+strings.Join(scope.Apps, ",")+"/"+tool, func(t *testing.T) {
				a := reducedReadFixture()
				a.list = func(context.Context) (any, error) {
					return map[string]any{"services": []ListServiceSummary{{Name: "photos", Type: registry.TypeProxy, State: "exact", FunnelState: "not_requested", Warnings: []inspect.WarningView{{Code: "secret-warning"}}}}}, nil
				}
				got := reducedToolPayload(t, a, scope, tool)
				wire, _ := json.Marshal(got)
				if strings.Contains(string(wire), "secret-") || strings.Contains(string(wire), "4242") {
					t.Fatalf("global evidence reached %s: %s", scope.Role, wire)
				}
				if !scope.Inventory && strings.Contains(string(wire), "finance") {
					t.Fatalf("out-of-scope app reached %s: %s", scope.Role, wire)
				}
				withheld := reducedWithheld(t, got)
				if tool != "doctor" && !slices.Contains(withheld, mcpWithheldServiceWarnings) {
					t.Fatalf("%s strips per-service warnings without naming them: %v", tool, withheld)
				}
				if tool == "status" {
					for _, field := range []string{"credential_stored", "supervision", "credentials", "access_log", "alerts", "guest_links", "authenticated", "node_authorized"} {
						if _, present := got[field]; present || !slices.Contains(withheld, field) {
							t.Fatalf("status %s: present=%t withheld=%v", field, present, withheld)
						}
					}
					if got["daemon_running"] != true || got["daemon_state"] != daemonStateRunning {
						t.Fatalf("status liveness: %v", got)
					}
					// Both fixture apps are authorized; a role sees its share.
					want := float64(1)
					if scope.Inventory {
						want = 2
					}
					if got["authorized_service_count"] != want {
						t.Fatalf("status authorized_service_count = %v, want %v", got["authorized_service_count"], want)
					}
				}
				validateAgainstScopedOutputSchema(t, tool, scope, got)
			})
		}
	}
}

func TestMCPOwnerReadsCarryNoWithheldMarker(t *testing.T) {
	for _, tool := range []string{"doctor", "status", "health", "list"} {
		got := reducedToolPayload(t, reducedReadFixture(), testRoleScope("owner"), tool)
		if _, present := got["withheld"]; present {
			t.Fatalf("owner %s carries withheld: %v", tool, got["withheld"])
		}
	}
}

func reducedWithheld(t *testing.T, payload map[string]any) []string {
	t.Helper()
	raw, ok := payload["withheld"].([]any)
	if !ok || len(raw) == 0 {
		t.Fatalf("reduced result has no withheld marker: %v", payload["withheld"])
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		out = append(out, v.(string))
	}
	return out
}

func jsonEqual(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

func mcpJSONValue(t *testing.T, value any) any {
	t.Helper()
	wire, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(wire, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func mcpSchemaMap(t *testing.T, schema any) map[string]any {
	t.Helper()
	out, ok := mcpJSONValue(t, schema).(map[string]any)
	if !ok {
		t.Fatalf("schema is not an object: %v", schema)
	}
	return out
}

// validateAgainstScopedOutputSchema checks a payload against the schema
// tools/list advertises to scope.
func validateAgainstScopedOutputSchema(t *testing.T, tool string, scope mcpscope.Scope, value any) {
	t.Helper()
	schema := mcpAdvertisedOutputSchema(mcpToolByName(t, tool), scope)
	if err := validateMCPJSONSchema(mcpSchemaMap(t, schema), mcpJSONValue(t, value), "$"); err != nil {
		t.Fatalf("%s as %s: %v", tool, scope.Role, err)
	}
}

// A status summary whose per-service flags do not add up to its count was
// built without the owner's decision; reduced status and doctor refuse it
// rather than report the role's apps as unauthorized. (health carries no
// count: its flags are the owner's payload unchanged.)
func TestMCPReducedReadsRefuseUnflaggedAuthorization(t *testing.T) {
	a := reducedReadFixture()
	a.status = func(context.Context) (any, error) {
		return mcpStatusSummary{DaemonRunning: true, DaemonState: daemonStateRunning, AuthorizedServiceCount: 1, ServiceCount: 1, Services: []mcpHealthService{{Name: "photos", Status: "up"}}}, nil
	}
	for _, tool := range []string{"status", "doctor"} {
		s := mcpscope.Session{Who: "agent", Scope: testRoleScope("viewer")}
		a.session = &s
		r, err := callMCPTool(context.Background(), a, tool, json.RawMessage(`{}`))
		if err != nil || r == nil || !r.IsError {
			t.Fatalf("%s accepted an unflagged authorized count: %+v %v", tool, r, err)
		}
	}
}
