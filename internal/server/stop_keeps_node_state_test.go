package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/registry"
	"github.com/monody0007/tslink/internal/testenv"
)

// Stopping a node never deletes its tsnet state; deleting a removed service's
// state is the reconciler's decision, made on ownership proof.
func TestStopNodeLockedKeepsNodeState(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatal(err)
	}
	s, err := New("key", "")
	if err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(config.NodesDirIn(mustConfigDir(t)), "test", "tailscaled.state")
	if err := os.MkdirAll(filepath.Dir(state), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(state, []byte("node key"), 0o600); err != nil {
		t.Fatal(err)
	}
	s.nodes["test"] = newNode(t, registry.Service{Name: "test"})
	s.mu.Lock()
	s.stopNodeLocked("test")
	s.mu.Unlock()
	if _, running := s.nodes["test"]; running {
		t.Fatal("control: the node was not stopped")
	}
	if _, err := os.Stat(state); err != nil {
		t.Fatalf("stopping the node deleted its state: %v", err)
	}
}
