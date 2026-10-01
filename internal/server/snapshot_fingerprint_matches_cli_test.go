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

// The daemon's half of the shared fingerprint: with an entry isolated as a
// per-service issue, the fingerprint the sync writes into runtime.json is the
// one the CLI derives from registry.json.
func TestDaemonSnapshotFingerprintIsTheCLIsWithAnIsolatedEntry(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatal(err)
	}
	regPath, err := config.RegistryPath()
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(map[string]any{"schema_version": registry.CurrentRegistrySchemaVersion, "services": []map[string]any{
		{"name": "docs", "type": "file", "path": filepath.Join(t.TempDir(), "gone"), "tags": []string{"tag:tsmain"}},
		{"name": "web", "type": "proxy", "target": "http://127.0.0.1:3000", "tags": []string{"tag:tsmain"}},
	}})
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
		t.Fatalf("syncNodes() error = %v", err)
	}
	snapshotPath, err := config.RuntimeSnapshotPath()
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := runtimesnapshot.Load(snapshotPath)
	if err != nil {
		t.Fatal(err)
	}
	cli, err := runtimesnapshot.CurrentRegistryFingerprint(regPath)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.RegistryFingerprint != cli {
		t.Fatalf("daemon wrote fingerprint %s, CLI derives %s", snapshot.RegistryFingerprint, cli)
	}
}
