package cmd

import (
	"context"
	"github.com/anydoor7/tslink/internal/health"
	"github.com/anydoor7/tslink/internal/registry"
	tsruntime "github.com/anydoor7/tslink/internal/runtime"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReviewDurableAlertsBeatStaleSnapshot(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	pidPath := filepath.Join(dir, "tslink.pid")
	snapPath := filepath.Join(dir, "runtime.json")
	svc := addStatusTestService(t, regPath, registry.Service{Name: "app", Type: registry.TypeProxy, Target: "http://localhost:1234"})
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	snapshot := tsruntime.NewSnapshot(4242, now.Add(-time.Hour), statusRegistryFingerprint(t, regPath), now, []tsruntime.ServiceState{{Service: svc}})
	snapshot.Alerts = health.AlertsView{Notifier: "none", Events: []health.Event{}}
	if err := tsruntime.Save(snapPath, snapshot); err != nil {
		t.Fatal(err)
	}
	recorder := health.NewRecorder(filepath.Join(dir, health.StateFile), health.NotifierConfig{})
	recorder.Commit(context.Background(), []health.Event{{At: now, Kind: "app_down", Service: "app"}}, now)
	if recorder.Error != "" {
		t.Fatal(recorder.Error)
	}
	durable := readAlertsForRegistry(regPath)
	if len(durable.Events) != 1 {
		t.Fatal("invalid fixture", durable)
	}
	withStatusURLSeams(t, false, 0, time.Time{})
	got, err := readOnlyStatus.getPollableStatus(context.Background(), pidPath, regPath, snapPath, filepath.Join(dir, "auth-handoff.json"))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("durable events=%d, status events=%d, daemon_running=%v", len(durable.Events), len(got.Alerts.Events), got.DaemonRunning)
	if len(got.Alerts.Events) != 1 {
		t.Errorf("existing stale runtime snapshot hides newer committed alerts")
	}
	if err := os.WriteFile(filepath.Join(dir, health.StateFile), []byte(`{"version":2}`), 0600); err != nil {
		t.Fatal(err)
	}
	got, err = readOnlyStatus.getPollableStatus(context.Background(), pidPath, regPath, snapPath, filepath.Join(dir, "auth-handoff.json"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Alerts.Error != "alert_state_invalid" {
		t.Fatal("snapshot hid current journal error", got.Alerts)
	}
}
