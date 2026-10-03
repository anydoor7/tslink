package cmd

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/accesslog"
	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/inspect"
	"github.com/anydoor7/tslink/internal/registry"
	tsruntime "github.com/anydoor7/tslink/internal/runtime"
)

func TestAccessCurrentInstanceHealth(t *testing.T) {
	env := newDoctorTestEnv(t, nil)
	os.MkdirAll(filepath.Join(env.dir, "access-log"), 0700)
	os.WriteFile(filepath.Join(env.dir, "access-log", "health.json"), []byte(`{"enabled":true,"drops":99,"size_bytes":42}`), 0600)
	h := accesslog.Health{Enabled: true, Current: true, Error: "access_log_init_failed", Drops: 3, MissingHistory: []accesslog.HistoryWindow{{Start: env.startedAt, Reason: "access_log_init_failed"}}}
	snapshot := tsruntime.NewSnapshot(env.pid, env.startedAt, statusRegistryFingerprint(t, env.regPath), env.startedAt.Add(time.Second), nil)
	snapshot.AccessLog = &h
	if err := tsruntime.Save(env.snapshotPath, snapshot); err != nil {
		t.Fatal(err)
	}
	got := accessHealthForRegistry(env.regPath, env.pidPath, env.snapshotPath)
	if !got.Current || got.Drops != 3 || got.Error != "access_log_init_failed" || len(got.MissingHistory) != 1 {
		t.Fatal(got)
	}
	// Actual doctor and status consume the runtime file, never the old success.
	doctor := buildDoctorResult(context.Background(), doctorOptions{})
	hasFailure, hasMissing := false, false
	for _, finding := range doctor.Findings {
		hasFailure = hasFailure || finding.Code == inspect.WarningCodeAccessLogUnavailable
		hasMissing = hasMissing || finding.Code == inspect.WarningCodeAccessLogDrops
	}
	if !doctor.AccessLog.Current || doctor.AccessLog.Drops != 3 || !hasFailure || !hasMissing {
		t.Fatal(doctor.AccessLog, doctor.Findings)
	}
	status, err := getStatusURLs(context.Background(), env.pidPath, env.regPath, env.snapshotPath)
	if err != nil || !status.AccessLog.Current || status.AccessLog.Drops != 3 {
		t.Fatal(status.AccessLog, err)
	}
	var out bytes.Buffer
	formatAccessHealth(got, &out)
	if !strings.Contains(out.String(), "Missing access history:") || !strings.Contains(out.String(), "ongoing") {
		t.Fatal(out.String())
	}
	end := env.startedAt.Add(time.Minute)
	h.MissingHistory[0].End = &end
	h.Error = ""
	h.Drops = 0
	snapshot.AccessLog = &h
	tsruntime.Save(env.snapshotPath, snapshot)
	recovered := buildDoctorResult(context.Background(), doctorOptions{})
	hasMissing = false
	for _, finding := range recovered.Findings {
		hasMissing = hasMissing || finding.Code == inspect.WarningCodeAccessLogDrops
	}
	if !hasMissing {
		t.Fatal("closed missing-history window no longer reported")
	}
	out.Reset()
	formatAccessHealth(h, &out)
	if !strings.Contains(out.String(), end.Format(time.RFC3339Nano)) {
		t.Fatal(out.String())
	}
	for _, change := range []string{"pid", "start", "updated", "missing-field", "malformed", "missing-file"} {
		t.Run(change, func(t *testing.T) {
			candidate := snapshot
			switch change {
			case "pid":
				candidate.DaemonPID++
			case "start":
				candidate.DaemonStartedAt = env.startedAt.Add(-time.Minute)
			case "updated":
				candidate.UpdatedAt = env.startedAt.Add(-time.Minute)
			case "missing-field":
				candidate.AccessLog = nil
			}
			if err := tsruntime.Save(env.snapshotPath, candidate); err != nil {
				t.Fatal(err)
			}
			if change == "malformed" {
				os.WriteFile(env.snapshotPath, []byte("{"), 0600)
			}
			if change == "missing-file" {
				os.Remove(env.snapshotPath)
			}
			got := accessHealthForRegistry(env.regPath, env.pidPath, env.snapshotPath)
			if got.Current || got.Drops != 0 || got.Error != "access_log_runtime_unavailable" {
				t.Fatalf("previous instance treated as current: %+v", got)
			}
		})
	}
	isRunningFn = func(string) bool { return false }
	historical := accessHealthForRegistry(env.regPath, env.pidPath, env.snapshotPath)
	if historical.Current || historical.Drops != 99 {
		t.Fatal(historical)
	}
}
func TestAccessPathModeCommands(t *testing.T) {
	env := newDoctorTestEnv(t, []registry.Service{{Name: "photos", Type: registry.TypeProxy, Target: "http://localhost:3000"}})
	t.Setenv(config.ConfigDirEnv, env.dir)
	var out bytes.Buffer
	for _, mode := range []string{"prefix", "full", "off", ""} {
		if err := configSet("access-log-path-mode", mode, &out, false); err != nil {
			t.Fatal(err)
		}
		cfg, err := config.LoadGlobalConfig()
		if err != nil {
			t.Fatal(err)
		}
		got, set := accessLogOptionValue(cfg, "access-log-path-mode")
		if got != mode || set != (mode != "") {
			t.Fatal(got, set)
		}
	}
	if err := configSet("access-log-path-mode", "unsafe", &out, false); err == nil {
		t.Fatal("invalid global mode")
	}
	cmd, _, err := rootCmd.Find([]string{"access", "path"})
	if err != nil {
		t.Fatal(err)
	}
	cmd.SetOut(&out)
	defer cmd.SetOut(nil)
	for _, mode := range []string{"full", "prefix", "off", "inherit", "true", "false"} {
		if err := cmd.RunE(cmd, []string{"photos", mode}); err != nil {
			t.Fatal(err)
		}
		reg, _, err := registry.Preflight(env.regPath)
		if err != nil {
			t.Fatal(err)
		}
		svc := reg.Services[0]
		want := mode
		if mode == "inherit" || mode == "true" || mode == "false" {
			want = ""
		}
		if svc.AccessLogPathMode != want {
			t.Fatal(svc)
		}
	}
	svc := registry.Service{Name: "photos", Type: registry.TypeProxy, Target: "http://localhost:3000", AccessLogPathMode: "invalid"}
	if err := registry.ValidateService(svc); err == nil {
		t.Fatal("invalid service mode")
	}
}
