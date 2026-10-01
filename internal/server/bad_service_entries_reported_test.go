package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/registry"
	runtimesnapshot "github.com/anydoor7/tslink/internal/runtime"
	"github.com/anydoor7/tslink/internal/testenv"
	"github.com/anydoor7/tslink/internal/testenv/localapitest"
)

// serve passes per-service problems to the daemon (cmd
// TestServeStartsWhenOneServiceEntryIsBad), so the daemon must start the
// services that are fine and report each bad one as that service's issue,
// with the stable code serve used to refuse with, in memory and in
// runtime.json.
func TestDaemonReportsBadServiceEntriesPerService(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(t.TempDir(), "gone")
	services := []map[string]any{
		{"name": "web", "type": "proxy", "target": "http://127.0.0.1:3000", "tags": []string{"tag:tsmain"}},
		{"name": "docs", "type": "file", "path": missing, "tags": []string{"tag:tsmain"}},
		{"name": "cam", "type": "proxy", "target": "http://169.254.10.10:80", "tags": []string{"tag:tsmain"}},
		{"name": "allow-app", "type": "proxy", "target": "http://localhost:3000", "funnel": true, "public_ack": true, "allowed_users": []string{"alice@example.com"}, "tags": []string{"tag:tsmain"}},
		{"name": "control-app", "type": "proxy", "target": "http://localhost:3000", "funnel": true, "public_ack": true, "control_url": "https://headscale.example.com", "tags": []string{"tag:tsmain"}},
		{"name": "public-files", "type": "file", "path": t.TempDir(), "funnel": true, "public_ack": true, "tags": []string{"tag:tsmain"}},
		{"name": "public-db", "type": "tcp", "target": "localhost:5432", "port": 5432, "funnel": true, "public_ack": true, "tags": []string{"tag:tsmain"}},
		{"name": "legacy", "type": "proxy", "target": "http://localhost:3000", "tags": []string{"tag:Bad"}},
	}
	want := map[string]string{
		"docs":         registry.CodePathNotFound,
		"cam":          registry.CodeLinkLocalTargetRefused,
		"allow-app":    registry.CodeFunnelAllowConflict,
		"control-app":  registry.CodeFunnelControlURLConflict,
		"public-files": registry.CodeFunnelTypeConflict,
		"public-db":    registry.CodeFunnelTypeConflict,
		"legacy":       registry.CodeInvalidTag,
	}
	regPath, err := config.RegistryPath()
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(map[string]any{"schema_version": 1, "services": services})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(regPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	oldNew := newTSNetServerFn
	newTSNetServerFn = func(registry.Service, string, string, string) tsnetServer {
		return &fakeTSNetServer{localClient: localapitest.NewClient(nil)}
	}
	t.Cleanup(func() { newTSNetServerFn = oldNew })
	s, err := New("key", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.closeAllNodes)

	if err := s.syncNodes(context.Background()); err != nil {
		t.Fatalf("syncNodes() error = %v, want per-service issues only", err)
	}
	if got := runningNames(s); len(got) != 1 || got[0] != "web" {
		t.Fatalf("running = %v, want only web", got)
	}
	snapshotPath, err := config.RuntimeSnapshotPath()
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := runtimesnapshot.Load(snapshotPath)
	if err != nil {
		t.Fatal(err)
	}
	reported := map[string]string{}
	for _, svc := range snapshot.Services {
		if svc.Error != nil {
			reported[svc.Name] = svc.Error.Code
		}
	}
	for name, code := range want {
		s.mu.RLock()
		failure, failed := s.serviceFailures[name]
		s.mu.RUnlock()
		if !failed || failure.Error == nil || failure.Error.Code != code {
			t.Errorf("service %q failure = %+v, want code %s", name, failure.Error, code)
		}
		if reported[name] != code {
			t.Errorf("runtime.json reports %q for %q, want %s", reported[name], name, code)
		}
	}
}
