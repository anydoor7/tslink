package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/health"
	"github.com/anydoor7/tslink/internal/inspect"
	"github.com/anydoor7/tslink/internal/output"
	"github.com/anydoor7/tslink/internal/registry"
)

func TestDoctorReportsUnexpectedResponseWithSafeSuggestion(t *testing.T) {
	backend := func(status int, body string) string {
		app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if status/100 == 3 {
				w.Header().Set("Location", "/login?next=private")
			}
			w.WriteHeader(status)
			io.WriteString(w, body)
		}))
		t.Cleanup(app.Close)
		return app.URL
	}
	for _, tc := range []struct {
		name   string
		svc    registry.Service
		code   string
		status string
		next   string
	}{
		{"401", registry.Service{Name: "opencode-server", Type: registry.TypeProxy, Target: backend(401, "")}, "health_status_mismatch", "401", "tslink health set opencode-server --status-max 401"},
		{"403", registry.Service{Name: "admin", Type: registry.TypeProxy, Target: backend(403, "")}, "health_status_mismatch", "403", "tslink health set admin --status-max 403"},
		{"404", registry.Service{Name: "micwave-phone", Type: registry.TypeProxy, Target: backend(404, "")}, "health_status_mismatch", "404", "tslink health set micwave-phone --path /health"},
		{"302 outside explicit 2xx", registry.Service{Name: "legacy", Type: registry.TypeProxy, Target: backend(302, ""), Health: &registry.HealthConfig{StatusMin: 200, StatusMax: 299}}, "health_status_mismatch", "302", "tslink health set legacy --status-max 399"},
		{"body", registry.Service{Name: "ready", Type: registry.TypeProxy, Target: backend(200, "starting private-body"), Health: &registry.HealthConfig{BodyContains: "ready"}}, "health_body_mismatch", "200", "tslink health set ready --path /health"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			newDoctorTestEnv(t, []registry.Service{tc.svc})
			doctorHTTPProbeFn = health.Probe
			result := buildDoctorResult(context.Background(), doctorOptions{})
			f := assertDoctorFinding(t, result, inspect.WarningCodeAppProbeUnexpected)
			if f.Severity != "warning" || f.Area != "app_probe" || f.Service != tc.svc.Name {
				t.Fatalf("%+v", f)
			}
			if f.Evidence["error_code"] != tc.code || f.Evidence["status_code"] != tc.status || f.Evidence["next"] != tc.next || !strings.Contains(f.Message, tc.next) {
				t.Fatalf("%+v", f)
			}
			for _, other := range result.Findings {
				if other.Code == inspect.WarningCodeAppProbeFailed {
					t.Fatalf("an answering app is also reported failed: %+v", other)
				}
			}
			if result.HealthExitCode != output.ExitWarning {
				t.Fatalf("exit=%d", result.HealthExitCode)
			}
			b, _ := json.Marshal(result)
			for _, leaked := range []string{"tslink add", "private", "/login"} {
				if strings.Contains(string(b), leaked) {
					t.Fatalf("doctor output contains %q: %s", leaked, b)
				}
			}
		})
	}
}

func TestMCPReducedDoctorReportsUnexpectedResponse(t *testing.T) {
	a := fakeMCPActions()
	a.status = func(context.Context) (any, error) {
		return mcpStatusSummary{Services: []mcpHealthService{
			{Name: "rootless", Status: "up", Health: health.State{State: health.Degraded, LastError: "health_status_mismatch", ConsecutiveFailures: 9, StatusCode: 404}},
			{Name: "offline", Status: "up", Health: health.State{State: health.Down, LastError: "health_request_failed", ConsecutiveFailures: 3}},
			{Name: "fine", Status: "up", Health: health.State{State: health.Healthy, StatusCode: 200}},
		}}, nil
	}
	v, err := mcpScopedDoctor(context.Background(), a, testRoleScope("owner"))
	if err != nil {
		t.Fatal(err)
	}
	result := v.(DoctorResult)
	if len(result.Findings) != 2 {
		t.Fatalf("%+v", result.Findings)
	}
	byService := map[string]DoctorFinding{}
	for _, f := range result.Findings {
		byService[f.Service] = f
	}
	f := byService["rootless"]
	if f.Code != inspect.WarningCodeAppProbeUnexpected || f.Severity != "warning" || f.Area != "health" || f.Evidence["status_code"] != "404" || f.Evidence["next"] != "tslink health set rootless --path /health" {
		t.Fatalf("%+v", f)
	}
	if f := byService["offline"]; f.Code != "app_health_unhealthy" {
		t.Fatalf("unreachable app keeps the unhealthy finding: %+v", f)
	}
	if strings.Contains(f.Message, "tslink add") {
		t.Fatal(f.Message)
	}
}

func TestHumanHealthShowsObservedStatus(t *testing.T) {
	checked := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	var out bytes.Buffer
	formatAppHealth(&out, "rootless", health.State{State: health.Degraded, LastChecked: &checked, LastError: "health_status_mismatch", ConsecutiveFailures: 4, StatusCode: 404}, health.Expiry{Source: "localclient"})
	if !strings.Contains(out.String(), "rootless app: degraded (checked 2030-01-01T00:00:00Z; consecutive failures 4) error=health_status_mismatch status=404") {
		t.Fatal(out.String())
	}
	out.Reset()
	formatAppHealth(&out, "offline", health.State{State: health.Down, LastChecked: &checked, LastError: "health_request_failed", ConsecutiveFailures: 3}, health.Expiry{Source: "localclient"})
	if strings.Contains(out.String(), "status=") || !strings.Contains(out.String(), "error=health_request_failed\n") {
		t.Fatal(out.String())
	}
}

// Each suggested command is executed against an isolated registry holding the
// app's stored settings: health set must accept it, keep the other health
// fields, and the backend's answer must then pass the check. Full doctor
// probes a real backend; reduced MCP doctor reads the stored settings itself.
func TestDoctorSuggestionIsAcceptedByHealthSet(t *testing.T) {
	for _, tc := range []struct {
		name   string
		stored *registry.HealthConfig
		answer int
		flags  string
	}{
		{"default range, 401 above maximum", nil, 401, "--status-max 401"},
		{"explicit 2xx, redirect above maximum", &registry.HealthConfig{StatusMin: 200, StatusMax: 299, Timeout: "7s"}, 302, "--status-max 399"},
		{"explicit 2xx, 403 above maximum", &registry.HealthConfig{StatusMin: 200, StatusMax: 299, Timeout: "7s"}, 403, "--status-max 403"},
		{"minimum only, redirect above maximum", &registry.HealthConfig{StatusMin: 250, Timeout: "7s"}, 307, "--status-max 399"},
		{"explicit 4xx, redirect below minimum", &registry.HealthConfig{StatusMin: 400, StatusMax: 499, Timeout: "7s"}, 302, "--status-min 300"},
		{"explicit 5xx, 401 below minimum", &registry.HealthConfig{StatusMin: 500, StatusMax: 599, Timeout: "7s"}, 401, "--status-min 401"},
	} {
		for _, surface := range []string{"doctor", "mcp"} {
			t.Run(surface+"/"+tc.name, func(t *testing.T) {
				app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if tc.answer/100 == 3 {
						w.Header().Set("Location", "/login")
					}
					w.WriteHeader(tc.answer)
				}))
				defer app.Close()
				svc := registry.Service{Name: "app", Type: registry.TypeProxy, Target: app.URL, Health: tc.stored}
				if code := health.Probe(context.Background(), svc); code != "health_status_mismatch" {
					t.Fatalf("precondition: probe %q", code)
				}
				env := newDoctorTestEnv(t, []registry.Service{svc})
				t.Setenv(config.ConfigDirEnv, env.dir)
				var f DoctorFinding
				if surface == "doctor" {
					doctorHTTPProbeFn = health.Probe
					f = assertDoctorFinding(t, buildDoctorResult(context.Background(), doctorOptions{}), inspect.WarningCodeAppProbeUnexpected)
				} else {
					a := fakeMCPActions()
					a.registryPath = env.regPath
					a.status = func(context.Context) (any, error) {
						return mcpStatusSummary{Services: []mcpHealthService{{Name: "app", Status: "up", Health: health.State{State: health.Degraded, LastError: "health_status_mismatch", ConsecutiveFailures: 4, StatusCode: tc.answer}}}}, nil
					}
					v, err := mcpScopedDoctor(context.Background(), a, testRoleScope("owner"))
					if err != nil {
						t.Fatal(err)
					}
					if len(v.(DoctorResult).Findings) != 1 {
						t.Fatalf("%+v", v)
					}
					f = v.(DoctorResult).Findings[0]
				}
				next := f.Evidence["next"]
				if next != "tslink health set app "+tc.flags || !strings.Contains(f.Message, next) {
					t.Fatalf("suggestion %q, want flags %q", next, tc.flags)
				}
				if _, err := runHealthSetRoot(t, strings.Fields(next)[3:]...); err != nil {
					t.Fatalf("health set refused the suggestion %q: %v", next, err)
				}
				reg, err := registry.Load(env.regPath)
				if err != nil {
					t.Fatal(err)
				}
				after := reg.Services[0]
				if tc.stored != nil && (after.Health == nil || after.Health.Timeout != tc.stored.Timeout) {
					t.Fatalf("other health settings changed: %+v", after.Health)
				}
				if code := health.Probe(context.Background(), after); code != "" {
					t.Fatalf("after %q the answer still fails: %q (%+v)", next, code, after.Health)
				}
			})
		}
	}
}
