package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/anydoor7/tslink/internal/health"
	"github.com/anydoor7/tslink/internal/registry"
	tsruntime "github.com/anydoor7/tslink/internal/runtime"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReReviewDoctorDoesNotHideDurableJournal(t *testing.T) {
	env := newDoctorTestEnv(t, []registry.Service{{Name: "app", Type: registry.TypeProxy, Target: "http://localhost:1234"}})
	env.writeExactSnapshot(t)
	p := filepath.Join(env.dir, health.StateFile)
	recorder := health.NewRecorder(p, health.NotifierConfig{})
	recorder.Commit(context.Background(), []health.Event{{At: env.startedAt, Kind: "app_down", Service: "app"}}, env.startedAt)
	if recorder.Error != "" {
		t.Fatal(recorder.Error)
	}
	if len(readAlertsForRegistry(env.regPath).Events) != 1 {
		t.Fatal("durable journal control")
	}
	result := buildDoctorResult(doctorOptions{ReadOnly: true})
	t.Logf("durable journal events=1 doctor events=%d daemon=%+v snapshot=%+v", len(result.Alerts.Events), result.Daemon, result.RuntimeSnapshot)
	if len(result.Alerts.Events) != 1 {
		t.Error("doctor hid a durably committed event behind an older exact runtime snapshot")
	}
	snapshot, err := tsruntime.Load(env.snapshotPath)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Alerts.Error = "alert_state_invalid"
	if err := tsruntime.Save(env.snapshotPath, *snapshot); err != nil {
		t.Fatal(err)
	}
	result = buildDoctorResult(doctorOptions{ReadOnly: true})
	if result.Alerts.Error != "" {
		t.Error("old snapshot error replaced a valid current journal", result.Alerts.Error)
	}
	snapshot.Alerts.Error = "alert_state_write_failed"
	if err := tsruntime.Save(env.snapshotPath, *snapshot); err != nil {
		t.Fatal(err)
	}
	result = buildDoctorResult(doctorOptions{ReadOnly: true})
	if result.Alerts.Error != "alert_state_write_failed" {
		t.Error("unpersistable write error was not supplemented", result.Alerts.Error)
	}
	if err := os.WriteFile(p, []byte("invalid json"), 0600); err != nil {
		t.Fatal(err)
	}
	if readAlertsForRegistry(env.regPath).Error != "alert_state_invalid" {
		t.Fatal("invalid journal control")
	}
	result = buildDoctorResult(doctorOptions{ReadOnly: true})
	t.Logf("journal error=alert_state_invalid doctor error=%q", result.Alerts.Error)
	if result.Alerts.Error != "alert_state_invalid" {
		t.Error("doctor hid current journal corruption")
	}
}

func TestReReviewStatusGlobalFailureKeepsJournalAuthority(t *testing.T) {
	env := newDoctorTestEnv(t, []registry.Service{{Name: "app", Type: registry.TypeProxy, Target: "http://localhost:1234"}})
	env.writeExactSnapshot(t)
	snap, err := tsruntime.Load(env.snapshotPath)
	if err != nil {
		t.Fatal(err)
	}
	snap.GlobalError = &tsruntime.ServiceError{Code: registry.CodeRegistryReloadInvalid, Message: "invalid registry"}
	if err := tsruntime.Save(env.snapshotPath, *snap); err != nil {
		t.Fatal(err)
	}
	r := health.NewRecorder(filepath.Join(env.dir, health.StateFile), health.NotifierConfig{})
	r.Commit(context.Background(), []health.Event{{Kind: "monitor_saturated", At: env.startedAt}}, env.startedAt)
	if r.Error != "" {
		t.Fatal(r.Error)
	}
	if err := os.WriteFile(env.regPath, []byte(`{`), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := readOnlyStatus.getPollableStatus(env.pidPath, env.regPath, env.snapshotPath, filepath.Join(env.dir, "auth-handoff.json"))
	if err != nil {
		t.Fatal(err)
	}
	if got.GlobalError == nil || len(got.Alerts.Events) != 1 {
		t.Error("global failure hid journal events", got.Alerts)
	}
	if err := os.WriteFile(r.Path, []byte(`{`), 0600); err != nil {
		t.Fatal(err)
	}
	got, err = readOnlyStatus.getPollableStatus(env.pidPath, env.regPath, env.snapshotPath, filepath.Join(env.dir, "auth-handoff.json"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Alerts.Error != "alert_state_invalid" {
		t.Error("global failure hid journal error", got.Alerts)
	}
}

func TestReReviewDoctorProjectsDurableMonitorSaturation(t *testing.T) {
	env := newDoctorTestEnv(t, []registry.Service{{Name: "app", Type: registry.TypeProxy, Target: "http://localhost:1234"}})
	env.writeExactSnapshot(t)
	p := filepath.Join(env.dir, health.StateFile)
	r := health.NewRecorder(p, health.NotifierConfig{})
	r.Commit(context.Background(), []health.Event{{At: env.startedAt, Kind: "monitor_saturated"}}, env.startedAt)
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var state map[string]any
	if err := json.Unmarshal(b, &state); err != nil {
		t.Fatal(err)
	}
	state["monitor_error"] = "health_monitor_saturated"
	b, err = json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, b, 0600); err != nil {
		t.Fatal(err)
	}
	result := buildDoctorResult(doctorOptions{ReadOnly: true})
	b, err = json.Marshal(result.Alerts)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"monitor_error":"health_monitor_saturated"`) {
		t.Error("doctor hid durable monitor saturation", string(b))
	}
	found := false
	for _, finding := range result.Findings {
		if finding.Code == "health_monitor_saturated" && finding.Service == "" {
			found = true
		}
	}
	if !found {
		t.Error("doctor did not report monitor-level finding")
	}
	var out bytes.Buffer
	formatAlerts(&out, result.Alerts)
	if !strings.Contains(out.String(), "monitor_error=health_monitor_saturated") {
		t.Error("formatted diagnostic hid monitor warning", out.String())
	}
}
