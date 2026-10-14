package cmd

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
