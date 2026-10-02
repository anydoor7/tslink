package server

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/atomicfile"
	"github.com/anydoor7/tslink/internal/health"
	"github.com/anydoor7/tslink/internal/inspect"
	"github.com/anydoor7/tslink/internal/registry"
	tsruntime "github.com/anydoor7/tslink/internal/runtime"
)

func TestGuestCounterStatusPublicationAndRecovery(t *testing.T) {
	s, dir := healthTestServer(t)
	path := filepath.Join(dir, "registry.json")
	now := time.Date(2030, 7, 10, 12, 0, 0, 0, time.UTC)
	svc := registry.Service{Name: "photos", Type: registry.TypeProxy, Target: "http://127.0.0.1:1234"}
	if _, err := registry.Add(path, svc); err != nil {
		t.Fatal(err)
	}
	v, _, err := registry.CreateGuest(path, registry.CreateGuestOptions{App: "photos", Value: "2h", PublicAck: true, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	s.nodes["photos"] = &ServiceNode{service: svc, handlerCloser: &guestGate{path: path}}
	s.nodes["private"] = &ServiceNode{service: registry.Service{Name: "private", Type: registry.TypeProxy}}
	r := health.NewRecorder(filepath.Join(dir, health.StateFile), health.NotifierConfig{})
	probe := func(context.Context, registry.Service) string { return "" }
	s.healthCycle(context.Background(), r, now, probe)
	if len(s.guestCounterWarningsLocked()) != 0 {
		t.Fatal("warning without a failure")
	}
	if _, reason := registry.CheckGuest(path, "photos", v.ID, now, true, true); reason != "allowed" {
		t.Fatal(reason)
	}
	var logs bytes.Buffer
	oldLog := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(oldLog) })
	restore := atomicfile.SetDirectorySyncForTest(func(string) error { return errors.New("injected durability failure") })
	err = registry.FlushGuestCounters(path)
	restore()
	if !atomicfile.IsPublished(err) {
		t.Fatal("publication control", err)
	}
	if !strings.Contains(logs.String(), "guest counters persistence failed") || !strings.Contains(logs.String(), "injected durability failure") {
		t.Fatal("durability error not logged", logs.String())
	}
	// No app probe is due at +1s. The changed warning alone must publish status.
	s.healthCycle(context.Background(), r, now.Add(time.Second), probe)
	snapshot, err := tsruntime.Load(filepath.Join(dir, "runtime.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Services[0].Warnings) != 1 || snapshot.Services[0].Warnings[0].Code != inspect.WarningCodeGuestCounters || !strings.Contains(snapshot.Services[0].Warnings[0].Message, "durability is unconfirmed") {
		t.Fatal("durability warning missing from status", snapshot.Services)
	}
	if err := registry.FlushGuestCounters(path); err != nil {
		t.Fatal(err)
	}
	if len(s.guestCounterWarningsLocked()) != 1 {
		t.Fatal("no-op cleared durability warning")
	}
	if _, err := registry.RevokeGuest(path, v.ID, now); err != nil {
		t.Fatal(err)
	}
	s.healthCycle(context.Background(), r, now.Add(2*time.Second), probe)
	snapshot, err = tsruntime.Load(filepath.Join(dir, "runtime.json"))
	if err != nil || len(snapshot.Services[0].Warnings) != 0 {
		t.Fatal("durable recovery warning remained", err)
	}
	// An unpublished read failure retains its delta and has distinct status.
	o := registry.CreateGuestOptions{App: "photos", Value: "2h", PublicAck: true, Now: now}
	second, _, err := registry.CreateGuest(path, o)
	if err != nil {
		t.Fatal(err)
	}
	if _, reason := registry.CheckGuest(path, "photos", second.ID, now, true, false); reason != "allowed" {
		t.Fatal(reason)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := registry.FlushGuestCounters(path); !os.IsNotExist(err) {
		t.Fatal("unpublished failure control", err)
	}
	warning := s.guestCounterWarningsLocked()["photos"]
	if !strings.Contains(warning.Message, "remain pending") {
		t.Fatal("unpublished status", warning)
	}
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := registry.FlushGuestCounters(path); err != nil {
		t.Fatal(err)
	}
	if len(s.guestCounterWarningsLocked()) != 0 {
		t.Fatal("retry status not cleared")
	}
}
