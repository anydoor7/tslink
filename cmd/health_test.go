package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/health"
	"github.com/anydoor7/tslink/internal/inspect"
	"github.com/anydoor7/tslink/internal/output"
	"github.com/anydoor7/tslink/internal/registry"
	tsruntime "github.com/anydoor7/tslink/internal/runtime"
	"github.com/spf13/cobra"
)

func TestStatusListMCPAndEventsExposeAppHealthAndExpiry(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	pidPath := filepath.Join(dir, "tslink.pid")
	snapshotPath := filepath.Join(dir, "runtime.json")
	handoff := filepath.Join(dir, "auth-handoff.json")
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	expiry := now.Add(3 * 24 * time.Hour)
	svc := addStatusTestService(t, regPath, registry.Service{Name: "app", Type: registry.TypeProxy, Target: "http://localhost:1234"})
	h := health.Result(health.State{ConsecutiveFailures: 2}, "proxy", "health_status_mismatch", now)
	snapshot := tsruntime.NewSnapshot(4242, now.Add(-time.Hour), statusRegistryFingerprint(t, regPath), now, []tsruntime.ServiceState{{Service: svc, RuntimeHost: "app.tailnet.ts.net", Health: h, NodeKey: health.ExpiryAt(&expiry, "localclient", now, nil)}})
	snapshot.Alerts = health.AlertsView{Notifier: "webhook", Destination: "[redacted]", Events: []health.Event{{ID: 1, Kind: "app_down", At: now, Service: "app"}}}
	// Status projects committed events from the journal, even if runtime
	// snapshot publication lags. Populate the actual durable source as well.
	recorder := health.NewRecorder(filepath.Join(dir, health.StateFile), health.NotifierConfig{})
	recorder.Commit(context.Background(), snapshot.Alerts.Events, now)
	if recorder.Error != "" {
		t.Fatal(recorder.Error)
	}
	if err := os.WriteFile(filepath.Join(dir, health.ConfigFile), []byte(`{"webhook":"https://notifier.invalid/private"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := tsruntime.Save(snapshotPath, snapshot); err != nil {
		t.Fatal(err)
	}
	withStatusURLSeams(t, true, 4242, now.Add(-time.Hour))
	oldNow := statusNowFn
	statusNowFn = func() time.Time { return now }
	t.Cleanup(func() { statusNowFn = oldNow })
	ordinary, err := getPollableStatus(pidPath, regPath, snapshotPath, handoff)
	if err != nil {
		t.Fatal(err)
	}
	if ordinary.Services[0].Health.State != health.Down || ordinary.Services[0].NodeKey.Warning != "critical_3d" || len(ordinary.Services[0].NodeKey.Next) == 0 {
		t.Fatalf("%+v", ordinary)
	}
	urls, err := getStatusURLs(pidPath, regPath, snapshotPath)
	if err != nil {
		t.Fatal(err)
	}
	if urls.Services[0].Health.LastChecked == nil || urls.Services[0].NodeKey.DaysLeft == nil || *urls.Services[0].NodeKey.DaysLeft != 3 {
		t.Fatalf("%+v", urls.Services[0])
	}
	for _, verbose := range []bool{false, true} {
		listed, err := loadListResultForPaths(regPath, pidPath, snapshotPath, listOptions{Verbose: verbose})
		if err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(listed)
		if !strings.Contains(string(b), `"state":"down"`) || !strings.Contains(string(b), `"node_key"`) {
			t.Fatalf("%s", b)
		}
	}
	actions := defaultMCPActions(sharePaths{Registry: regPath, PID: pidPath, Snapshot: snapshotPath, AuthHandoff: handoff}, io.Discard)
	status, err := actions.status()
	if err != nil {
		t.Fatal(err)
	}
	listed, err := actions.list()
	if err != nil {
		t.Fatal(err)
	}
	event, err := buildMCPEventState(listed, status)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(event)
	if !strings.Contains(string(b), `"kind":"app_down"`) || !strings.Contains(string(b), `"warning":"critical_3d"`) {
		t.Fatalf("%s", b)
	}
	var out bytes.Buffer
	formatStatus(ordinary, &out)
	formatStatusURLs(urls, &out)
	if !strings.Contains(out.String(), "app: down") || !strings.Contains(out.String(), "3d left") || !strings.Contains(out.String(), "notifier=webhook") {
		t.Fatal(out.String())
	}
}

func TestDoctorBusinessProbeDetectsListeningBrokenApp(t *testing.T) {
	defaultProbe := doctorHTTPProbeFn
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		fmt.Fprint(w, "private-response-secret")
	}))
	defer app.Close()
	newDoctorTestEnv(t, []registry.Service{{Name: "app", Type: registry.TypeProxy, Target: app.URL}})
	doctorHTTPProbeFn = defaultProbe
	result := buildDoctorResult(doctorOptions{})
	finding := assertDoctorFinding(t, result, inspect.WarningCodeAppProbeFailed)
	if finding.Evidence["error_code"] != "health_status_mismatch" {
		t.Fatal(finding)
	}
	b, _ := json.Marshal(result)
	if strings.Contains(string(b), "private-response-secret") {
		t.Fatalf("body leaked: %s", b)
	}
}

func TestDoctorNodeKeyThresholdWarningsAndNextSteps(t *testing.T) {
	for _, tc := range []struct {
		name string
		left time.Duration
		code string
		exit int
	}{{"ok", 30 * 24 * time.Hour, "", 0}, {"warning", 14 * 24 * time.Hour, inspect.WarningCodeNodeKeyExpiring, 64}, {"critical", 3 * 24 * time.Hour, inspect.WarningCodeNodeKeyCritical, 65}, {"expired", -time.Second, inspect.WarningCodeNodeKeyExpired, 65}, {"unknown", 0, inspect.WarningCodeNodeKeyUnknown, 0}} {
		t.Run(tc.name, func(t *testing.T) {
			env := newDoctorTestEnv(t, []registry.Service{{Name: "app", Type: registry.TypeProxy, Target: "http://localhost:1234"}})
			env.writeExactSnapshot(t)
			snapshot, err := tsruntime.Load(env.snapshotPath)
			if err != nil {
				t.Fatal(err)
			}
			var deadline *time.Time
			if tc.name != "unknown" {
				expires := env.startedAt.Add(tc.left)
				deadline = &expires
			}
			snapshot.Services[0].NodeKey = health.ExpiryAt(deadline, "localclient", env.startedAt, nil)
			if err := tsruntime.Save(env.snapshotPath, *snapshot); err != nil {
				t.Fatal(err)
			}
			result := buildDoctorResult(doctorOptions{})
			if tc.code != "" {
				assertDoctorFinding(t, result, tc.code)
			} else {
				for _, finding := range result.Findings {
					if strings.HasPrefix(finding.Code, "node_key_") {
						t.Fatal("healthy deadline warned", finding)
					}
				}
			}
			if got := output.ExitCode(doctorExit(result)); got != tc.exit {
				t.Fatalf("exit=%d findings=%+v", got, result.Findings)
			}
			key := result.NodeKeys["app"]
			if key.Warning != "" && (len(key.Next) == 0 || key.Next[0] != "tslink doctor --json") {
				t.Fatal("warning has no next command", key)
			}
			var out bytes.Buffer
			formatDoctor(result, &out)
			if !strings.Contains(out.String(), "Node app key expiry:") {
				t.Fatal(out.String())
			}
		})
	}
}

func TestStatusReadOnlySeesPersistedAlertsWithoutDaemon(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	addStatusTestService(t, regPath, registry.Service{Name: "app", Type: registry.TypeProxy, Target: "http://localhost:1234"})
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	r := health.NewRecorder(filepath.Join(dir, health.StateFile), health.NotifierConfig{})
	r.Commit(context.Background(), []health.Event{{Kind: "app_down", Service: "app", At: now}}, now)
	withStatusURLSeams(t, false, 0, time.Time{})
	got, err := readOnlyStatus.getPollableStatus(filepath.Join(dir, "tslink.pid"), regPath, filepath.Join(dir, "runtime.json"), filepath.Join(dir, "auth-handoff.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Alerts.Events) != 1 || got.Alerts.Events[0].Kind != "app_down" {
		t.Fatal(got.Alerts)
	}
	if got.Services[0].Health.State != health.Unknown {
		t.Fatal(got.Services)
	}
}

func TestAppHealthStaleObservationIsUnknown(t *testing.T) {
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	checked := now.Add(-3 * time.Minute)
	got := currentHealth(health.State{State: health.Healthy, LastChecked: &checked}, registry.Service{Type: registry.TypeProxy}, now)
	if got.State != health.Unknown || got.LastError != "health_observation_stale" {
		t.Fatal(got)
	}
	got = currentHealth(health.State{State: health.Healthy, LastChecked: &checked}, registry.Service{Type: registry.TypeProxy, Health: &registry.HealthConfig{Interval: "5m"}}, now)
	if got.State != health.Healthy {
		t.Fatal(got)
	}
}

func TestStatusMasksInvalidNotifierConfiguration(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, health.ConfigFile), []byte(`{"webhook":"file:///private-secret"}`), 0600); err != nil {
		t.Fatal(err)
	}
	v := readAlertsForRegistry(filepath.Join(dir, "registry.json"))
	if v.Error != "alert_webhook_invalid" || v.Notifier != "none" {
		t.Fatal(v)
	}
	var out bytes.Buffer
	formatAlerts(&out, v)
	if !strings.Contains(out.String(), "alert_webhook_invalid") || strings.Contains(out.String(), "private-secret") {
		t.Fatal(out.String())
	}
}

func TestAddHealthFlagsAndMCPArguments(t *testing.T) {
	var add *cobra.Command
	for _, cmd := range rootCmd.Commands() {
		if cmd.Name() == "add" {
			add = cmd
			break
		}
	}
	if add == nil {
		t.Fatal("add missing")
	}
	for _, flag := range []string{"health-path", "health-status-min", "health-status-max", "health-body", "health-timeout", "health-interval"} {
		f := add.Flags().Lookup(flag)
		oldValue, oldChanged := f.Value.String(), f.Changed
		t.Cleanup(func() { _ = f.Value.Set(oldValue); f.Changed = oldChanged })
	}
	for flag, value := range map[string]string{"health-path": "/ready", "health-status-min": "201", "health-status-max": "204", "health-body": "ready", "health-timeout": "2s", "health-interval": "2m"} {
		if err := add.Flags().Set(flag, value); err != nil {
			t.Fatal(err)
		}
	}
	h := healthConfigFromFlags(add)
	if h == nil || h.Path != "/ready" || h.StatusMin != 201 || h.BodyContains != "ready" || h.Interval != "2m" {
		t.Fatal(h)
	}
	p, _, err := addParamsFromMCPArguments(mcpAddArguments{Name: "app", Type: registry.TypeProxy, Target: "localhost:1234", Health: h})
	if err != nil {
		t.Fatal(err)
	}
	svc, err := buildService(p)
	if err != nil || svc.Health != h {
		t.Fatalf("%+v %v", svc, err)
	}
}
