package cmd

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/monody0007/tslink/internal/registry"
	tsruntime "github.com/monody0007/tslink/internal/runtime"
)

func TestStatusSnapshotRespectsFunnelExpiry(t *testing.T) {
	dir := t.TempDir()
	regPath, pidPath := filepath.Join(dir, "registry.json"), filepath.Join(dir, "tslink.pid")
	snapshotPath := filepath.Join(dir, "runtime.json")
	deadline := time.Now().Add(-time.Second)
	started := deadline.Add(-time.Hour)
	svc := addStatusTestService(t, regPath, registry.Service{Name: "public", Type: registry.TypeProxy, Target: "http://localhost:3000", Funnel: true, PublicAck: true, FunnelExpiresAt: &deadline})
	snapshot := tsruntime.NewSnapshot(4242, started, statusRegistryFingerprint(t, regPath), deadline.Add(-time.Second), []tsruntime.ServiceState{{Service: svc, RuntimeHost: "public.tailnet.ts.net."}})
	if err := tsruntime.Save(snapshotPath, snapshot); err != nil {
		t.Fatal(err)
	}
	withStatusURLSeams(t, true, 4242, started)
	oldNow := statusNowFn
	statusNowFn = func() time.Time { return deadline.Add(time.Second) }
	t.Cleanup(func() { statusNowFn = oldNow })
	urls, err := getStatusURLs(pidPath, regPath, snapshotPath)
	if err != nil {
		t.Fatal(err)
	}
	url := findStatusService(t, urls, "public")
	if url.FunnelRequested || url.FunnelActive || url.FunnelState != tsruntime.FunnelStateNotRequested {
		t.Fatalf("URL expiry control: %+v", url)
	}
	ordinary, err := getPollableStatus(pidPath, regPath, snapshotPath, filepath.Join(dir, "auth-handoff.json"))
	if err != nil || len(ordinary.Services) != 1 {
		t.Fatalf("ordinary status: %+v err=%v", ordinary, err)
	}
	got := ordinary.Services[0]
	if got.FunnelRequested || got.FunnelActive || got.FunnelState != tsruntime.FunnelStateNotRequested {
		t.Fatalf("expired Funnel resurrected by snapshot: %+v", got)
	}
}

func TestStatusSnapshotBeforeExpiryAndPrivateControls(t *testing.T) {
	dir := t.TempDir()
	regPath, pidPath := filepath.Join(dir, "registry.json"), filepath.Join(dir, "tslink.pid")
	snapshotPath := filepath.Join(dir, "runtime.json")
	now := time.Now()
	deadline := now.Add(time.Hour)
	public := addStatusTestService(t, regPath, registry.Service{Name: "public", Type: registry.TypeProxy, Target: "http://localhost:3000", Funnel: true, PublicAck: true, FunnelExpiresAt: &deadline})
	private := addStatusTestService(t, regPath, registry.Service{Name: "private", Type: registry.TypeProxy, Target: "http://localhost:3001"})
	snapshot := tsruntime.NewSnapshot(4242, now.Add(-time.Minute), statusRegistryFingerprint(t, regPath), now, []tsruntime.ServiceState{
		{Service: public, RuntimeHost: "public.tailnet.ts.net."},
		{Service: private, RuntimeHost: "private.tailnet.ts.net."},
	})
	if err := tsruntime.Save(snapshotPath, snapshot); err != nil {
		t.Fatal(err)
	}
	withStatusURLSeams(t, true, 4242, now.Add(-time.Minute))
	oldNow := statusNowFn
	statusNowFn = func() time.Time { return now }
	t.Cleanup(func() { statusNowFn = oldNow })
	ordinary, err := getPollableStatus(pidPath, regPath, snapshotPath, filepath.Join(dir, "auth-handoff.json"))
	if err != nil {
		t.Fatal(err)
	}
	urls, err := getStatusURLs(pidPath, regPath, snapshotPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(ordinary.Services) != 2 {
		t.Fatalf("ordinary count=%d", len(ordinary.Services))
	}
	for _, service := range ordinary.Services {
		url := findStatusService(t, urls, service.Name)
		if service.FunnelRequested != url.FunnelRequested || service.FunnelActive != url.FunnelActive || service.FunnelState != url.FunnelState {
			t.Fatalf("status views disagree for %s: ordinary=%+v url=%+v", service.Name, service, url)
		}
		if service.Name == "public" && !service.FunnelRequested {
			t.Fatal("unexpired public Funnel lost")
		}
		if service.Name == "private" && (service.FunnelRequested || service.FunnelActive) {
			t.Fatal("private service became public")
		}
	}
}
