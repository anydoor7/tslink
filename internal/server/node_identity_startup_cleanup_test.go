package server

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/monody0007/tslink/internal/registry"
	"github.com/monody0007/tslink/internal/tailapi"
)

// Tags changed while the daemon was down, after identity records exist, so
// the next start resets that node and asks for stale-device cleanup. That
// cleanup carries no NodeIDs and can never delete; a device-listing outage
// there must be logged as degraded, not take the whole daemon down.
func TestNodeIdentityStartupTransitionCleanupFailureIsDegraded(t *testing.T) {
	services := legacyLayoutServices(t)
	markers := writeLegacyLayout(t, services)
	p := instrumentIdentity(t)
	startLegacyLayoutOnce(t, p)
	changed := append([]registry.Service(nil), services...)
	changed[1].Tags = []string{"tag:other"}
	writeRegistry(t, changed)

	oldLogger := slog.Default()
	logs := installCaptureLogger()
	t.Cleanup(func() { slog.SetDefault(oldLogger) })
	s := newIdentityProbeServer(t, "", p)
	var cleanupTargets []tailapi.CleanupTarget
	s.SetCleanupStaleNodesFn(func(_ context.Context, targets []tailapi.CleanupTarget) (tailapi.CleanupResult, error) {
		cleanupTargets = append(cleanupTargets, targets...)
		return tailapi.CleanupResult{}, errors.New("tailscale API 503")
	})
	obs, err := runUntilReady(t, s)
	if err != nil || !obs.ready {
		t.Fatalf("stale-node listing outage took the daemon down: Run err=%v ready=%v", err, obs.ready)
	}
	if strings.Join(obs.running, ",") != "api,eph,files" || len(obs.failures) != 0 {
		t.Fatalf("running=%v failures=%v, want every service up", obs.running, obs.failures)
	}
	if len(cleanupTargets) != 1 || cleanupTargets[0].Hostname != "api" || len(cleanupTargets[0].NodeIDs) != 0 {
		t.Fatalf("cleanup targets = %+v, want one hostname-only target for api", cleanupTargets)
	}
	if _, err := os.Stat(markers["api"]); !os.IsNotExist(err) {
		t.Fatalf("api kept its old enrollment across a tag change: %v", err)
	}
	assertStateIntact(t, markers, "files", "eph")

	var degraded bool
	logs.mu.Lock()
	for _, record := range logs.records {
		if record.Level != slog.LevelWarn || !strings.Contains(record.Message, "stale tailnet node cleanup failed") {
			continue
		}
		record.Attrs(func(a slog.Attr) bool {
			if a.Key == "degraded_mode" && a.Value.Bool() {
				degraded = true
			}
			return true
		})
	}
	logs.mu.Unlock()
	if !degraded {
		t.Fatal("cleanup outage was not logged as degraded")
	}
}
