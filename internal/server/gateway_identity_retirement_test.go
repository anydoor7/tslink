package server

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/registry"
	tsruntime "github.com/anydoor7/tslink/internal/runtime"
	"tailscale.com/ipn/ipnstate"
)

func TestGatewayReplacedIdentityRetired(t *testing.T) {
	dir := t.TempDir()
	cfg := &registry.GatewayConfig{Enabled: true, Ports: registry.DefaultGatewayPorts()}
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	first, err := recordGatewayIdentity(dir, cfg, &ipnstate.Status{Self: &ipnstate.PeerStatus{ID: "old-node"}}, now)
	if err != nil || !first.OwnershipProven || first.IdentityChanged {
		t.Fatal(first, err)
	}
	next, err := recordGatewayIdentity(dir, cfg, &ipnstate.Status{Self: &ipnstate.PeerStatus{ID: "new-node"}}, now.Add(time.Hour))
	if err != nil || !next.OwnershipProven || !next.IdentityChanged {
		t.Fatal(next, err)
	}
	ledger, err := tsruntime.LoadOwnership(filepath.Join(dir, "ownership.json"))
	if err != nil || len(ledger.Nodes) != 2 {
		t.Fatal(ledger, err)
	}
	for _, node := range ledger.Nodes {
		if (node.NodeID == "old-node" && (node.RetiredAt == nil || !node.RetiredAt.Equal(now.Add(time.Hour)))) || (node.NodeID == "new-node" && node.RetiredAt != nil) {
			t.Fatalf("replaced gateway still active: %+v", ledger.Nodes)
		}
	}
	again, err := recordGatewayIdentity(dir, cfg, &ipnstate.Status{Self: &ipnstate.PeerStatus{ID: "new-node"}}, now.Add(2*time.Hour))
	if err != nil || !again.IdentityChanged {
		t.Fatal("identity warning lost on restart", again, err)
	}
	ledger, err = tsruntime.LoadOwnership(filepath.Join(dir, "ownership.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range ledger.Nodes {
		if node.NodeID == "old-node" && (node.RetiredAt == nil || !node.RetiredAt.Equal(now.Add(time.Hour))) {
			t.Fatal("retirement timestamp changed", ledger)
		}
	}
}
