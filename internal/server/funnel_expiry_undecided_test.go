package server

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/registry"
	runtimesnapshot "github.com/monody0007/tslink/internal/runtime"
	"github.com/monody0007/tslink/internal/testenv"
)

// TestSyncNodes_FunnelWithoutDecidedExpiryIsNeverStarted pins the daemon side
// of a hand-written Funnel entry that records neither a deadline nor "never":
// the runtime loader isolates it, so no node (and so no public listener) is
// built for it, the failure is reported with its code, and the other service
// keeps running.
func TestSyncNodes_FunnelWithoutDecidedExpiryIsNeverStarted(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}
	regPath, err := config.RegistryPath()
	if err != nil {
		t.Fatal(err)
	}
	healthyDir, err := json.Marshal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	raw := `{"schema_version":1,"services":[` +
		`{"name":"pub","type":"proxy","target":"http://localhost:3000","funnel":true,"public_ack":true,"tags":["tag:tsmain"]},` +
		`{"name":"healthy","type":"file","path":` + string(healthyDir) + `}]}`
	if err := os.WriteFile(regPath, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}

	var started []string
	oldNew := newTSNetServerFn
	newTSNetServerFn = func(svc registry.Service, stateDir, authKey, controlURL string) tsnetServer {
		started = append(started, svc.Name)
		return &fakeTSNetServer{certDomains: []string{svc.Name + ".tailnet.ts.net"}}
	}
	t.Cleanup(func() { newTSNetServerFn = oldNew })

	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(s.closeAllNodes)
	if err := s.syncNodes(context.Background()); err != nil {
		t.Fatalf("syncNodes() error = %v, want per-service isolation", err)
	}
	if len(started) != 1 || started[0] != "healthy" {
		t.Fatalf("started services = %v, want only healthy", started)
	}
	if _, ok := s.nodes["pub"]; ok {
		t.Fatal("the undecided Funnel service was started")
	}

	snapshotPath, err := config.RuntimeSnapshotPath()
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := runtimesnapshot.Load(snapshotPath)
	if err != nil {
		t.Fatalf("runtime Load() error = %v", err)
	}
	for _, service := range snapshot.Services {
		if service.Name != "pub" {
			continue
		}
		if service.RuntimeState != runtimesnapshot.ServiceRuntimeFailed || service.FunnelActive || service.Error == nil || service.Error.Code != registry.CodeFunnelExpiryRequired {
			t.Fatalf("pub snapshot = %+v, want a failed, non-public service with %s", service, registry.CodeFunnelExpiryRequired)
		}
		return
	}
	t.Fatalf("snapshot services = %+v, want pub reported", snapshot.Services)
}
