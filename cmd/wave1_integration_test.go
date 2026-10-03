package cmd

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/health"
	"github.com/anydoor7/tslink/internal/inspect"
	"github.com/anydoor7/tslink/internal/recipes"
	"github.com/anydoor7/tslink/internal/registry"
	tsruntime "github.com/anydoor7/tslink/internal/runtime"
)

func TestWave1RecipeHealthDefaults(t *testing.T) {
	recipeTestRegistry(t)
	for _, recipe := range recipes.List().Recipes {
		t.Run(recipe.ID, func(t *testing.T) {
			_, svc, err := recipeService(recipeRequest{RecipeID: recipe.ID})
			if err != nil || svc.Health == nil || svc.Health.Path != recipe.HealthPath {
				t.Fatalf("recipe health default lost: svc=%+v want=%q err=%v", svc, recipe.HealthPath, err)
			}
		})
	}
}

func TestWave1RecipeOverridesAndPeople(t *testing.T) {
	for _, entry := range []string{"apps", "add", "mcp"} {
		t.Run(entry, func(t *testing.T) {
			path := recipeTestRegistry(t)
			if entry == "mcp" {
				actions := defaultMCPActions(sharePaths{Registry: path}, io.Discard)
				raw := json.RawMessage(`{"recipe_id":"jupyter","name":"photos","no_daemon_install":true,"health":{"path":"/ready","timeout":"200ms"},"request_limits":{"max_body":"20GiB","read_timeout":"2m"}}`)
				result, err := callMCPTool(context.Background(), actions, "recipe_apply", raw)
				if err != nil || result == nil || result.IsError {
					t.Fatalf("MCP recipe apply: %+v %v", result, err)
				}
				validateAgainstToolOutputSchema(t, "recipe_apply", result.StructuredContent)
			} else {
				args := []string{"apps", "share", "jupyter", "--name", "photos"}
				if entry == "add" {
					args = []string{"add", "photos", "--recipe", "jupyter"}
				}
				args = append(args, "--yes", "--no-daemon-install", "--health-path", "/ready", "--health-timeout", "200ms", "--max-request-body", "20GiB", "--request-read-timeout", "2m", "--json")
				if out, err := runRecipeRoot(t, args...); err != nil {
					t.Fatalf("recipe CLI: %s %v", out, err)
				}
			}
			reg, err := registry.Load(path)
			if err != nil || len(reg.Services) != 1 {
				t.Fatalf("registry: %+v %v", reg, err)
			}
			svc := reg.Services[0]
			if !svc.PreserveHost || svc.Health == nil || svc.Health.Path != "/ready" || svc.Health.Timeout != "200ms" || svc.RequestLimits == nil || svc.RequestLimits.MaxBody != "20GiB" || svc.RequestLimits.ReadTimeout != "2m" {
				t.Fatalf("recipe overrides lost: %+v", svc)
			}
			now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
			expiry := now.Add(time.Hour)
			if _, err := registry.ChangePerson(path, "alice", []string{"photos"}, &expiry, true, false); err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadFile(path)
			if _, err := applyRecipe(context.Background(), recipeRequest{RecipeID: "jupyter", Name: "photos", NoDaemonInstall: true}, path, false, io.Discard); err != nil {
				t.Fatal(err)
			}
			after, _ := os.ReadFile(path)
			if string(before) != string(after) {
				t.Fatal("recipe reuse rewrote the people scope or explicit configuration")
			}
			reg, err = registry.Load(path)
			if err != nil || !reg.Services[0].PeopleScoped {
				t.Fatalf("people scope: %+v %v", reg, err)
			}
			for _, tc := range []struct {
				login string
				at    time.Time
				want  bool
			}{{"alice", now, true}, {"eve", now, false}, {"alice", expiry, false}} {
				allowed, authoritative := registry.PeopleAccessAt(reg, reg.Services[0], tc.login, nil, tc.at)
				if allowed != tc.want || !authoritative {
					t.Fatalf("grant %s at %s: allowed=%t authoritative=%t", tc.login, tc.at, allowed, authoritative)
				}
			}
		})
	}
}

func TestWave1CombinedShareConflict(t *testing.T) {
	path := recipeTestRegistry(t)
	original := registry.Service{Name: "photos", Type: registry.TypeProxy, Target: "http://localhost:2283"}
	if _, err := registry.Add(path, original); err != nil {
		t.Fatal(err)
	}
	candidate := original
	candidate.PreserveHost = true
	candidate.RequestLimits = registry.RecommendedUploadLimits()
	before, _ := os.ReadFile(path)
	_, err := registerShareWithOutcome(path, shareTargetSpec{NameBase: "photos", Service: candidate}, "photos")
	if err == nil {
		t.Fatal("incompatible share was reused")
	}
	for _, text := range []string{"request limits", "--max-request-body", "--request-read-timeout", "preserve_host=false", "preserve_host=true", "reconfigure"} {
		if !strings.Contains(err.Error(), text) {
			t.Errorf("combined diagnostic missing %q: %v", text, err)
		}
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("conflict changed the registry")
	}
}

func TestWave1CombinedProjections(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "registry.json")
	pid, snapshotPath, handoff := filepath.Join(dir, "tslink.pid"), filepath.Join(dir, "runtime.json"), filepath.Join(dir, "auth-handoff.json")
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	svc := addStatusTestService(t, path, registry.Service{Name: "photos", Type: registry.TypeProxy, Target: "http://localhost:2283", PreserveHost: true, RequestLimits: registry.RecommendedUploadLimits()})
	expiry := now.Add(3 * 24 * time.Hour)
	h := health.Result(health.State{ConsecutiveFailures: 2}, registry.TypeProxy, "health_status_mismatch", now)
	snapshot := tsruntime.NewSnapshot(4242, now.Add(-time.Hour), statusRegistryFingerprint(t, path), now, []tsruntime.ServiceState{{Service: svc, RuntimeHost: "photos.tailnet.ts.net", Health: h, NodeKey: health.ExpiryAt(&expiry, "localclient", now, nil), Warnings: []inspect.WarningView{{Code: registry.CodeRequestBodyLimit, Severity: "warning"}}}})
	if err := tsruntime.Save(snapshotPath, snapshot); err != nil {
		t.Fatal(err)
	}
	withStatusURLSeams(t, true, 4242, now.Add(-time.Hour))
	old := statusNowFn
	statusNowFn = func() time.Time { return now }
	t.Cleanup(func() { statusNowFn = old })
	actions := defaultMCPActions(sharePaths{Registry: path, PID: pid, Snapshot: snapshotPath, AuthHandoff: handoff}, io.Discard)
	for _, entry := range []struct {
		name string
		read func(context.Context) (any, error)
	}{
		{"status", func(ctx context.Context) (any, error) {
			return getPollableStatus(ctx, pid, path, snapshotPath, handoff)
		}},
		{"urls", func(ctx context.Context) (any, error) { return getStatusURLs(ctx, pid, path, snapshotPath) }},
		{"list", func(ctx context.Context) (any, error) {
			return loadListResultForPaths(ctx, path, pid, snapshotPath, listOptions{Verbose: true})
		}},
		{"MCP status", actions.status}, {"MCP list", actions.list},
	} {
		t.Run(entry.name, func(t *testing.T) {
			value, err := entry.read(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(entry.name, "MCP ") {
				validateAgainstToolOutputSchema(t, strings.TrimPrefix(entry.name, "MCP "), value)
			}
			b, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			for _, text := range []string{`"preserve_host":true`, `"max_body_bytes":21474836480`, `"state":"down"`, `"warning":"critical_3d"`, `"code":"request_body_limit"`} {
				if !strings.Contains(string(b), text) {
					t.Errorf("combined projection missing %s: %s", text, b)
				}
			}
		})
	}
}

func TestWave1DoctorCanonicalHost(t *testing.T) {
	for _, tc := range []struct {
		name           string
		certs          []string
		stale, invalid bool
	}{
		{"DNS", nil, false, false}, {"certificate", []string{"PUBLIC.TAILNET.TS.NET."}, false, false},
		{"invalid certificate", []string{"invalid:443"}, false, true}, {"stale", nil, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			want := "photos.tailnet.ts.net"
			if tc.name == "certificate" {
				want = "public.tailnet.ts.net"
			}
			app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Host != want || r.Header.Get("X-Forwarded-Host") != want {
					http.Error(w, "wrong Host", 421)
					return
				}
				io.WriteString(w, "ready")
			}))
			defer app.Close()
			env := newDoctorTestEnv(t, []registry.Service{{Name: "photos", Type: registry.TypeProxy, Target: app.URL, PreserveHost: true}})
			doctorHTTPProbeFn = health.Probe
			env.startedAt = time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
			doctorNowFn = func() time.Time { return env.startedAt }
			pidFileModTimeFn = func(string) (time.Time, error) { return env.startedAt, nil }
			env.writeExactSnapshot(t)
			snapshot, err := tsruntime.Load(env.snapshotPath)
			if err != nil {
				t.Fatal(err)
			}
			snapshot.Services[0].CertDomains = tc.certs
			if tc.stale {
				snapshot.UpdatedAt = env.startedAt.Add(-time.Hour)
			}
			if err := tsruntime.Save(env.snapshotPath, *snapshot); err != nil {
				t.Fatal(err)
			}
			result := buildDoctorResult(context.Background(), doctorOptions{})
			if tc.invalid {
				finding := assertDoctorFinding(t, result, inspect.WarningCodeAppProbeFailed)
				if finding.Evidence["error_code"] != "health_canonical_host_unavailable" || calls.Load() != 0 {
					t.Fatalf("untrusted doctor name contacted backend: %+v calls=%d", finding, calls.Load())
				}
			} else {
				if calls.Load() != 1 {
					t.Fatalf("healthy canonical doctor probe was not executed: %d snapshot=%+v canonical=%v findings=%+v", calls.Load(), result.RuntimeSnapshot, result.canonicalHosts, result.Findings)
				}
				for _, f := range result.Findings {
					if f.Code == inspect.WarningCodeAppProbeFailed {
						t.Fatalf("canonical doctor probe failed: %+v", f)
					}
				}
			}
		})
	}
}
