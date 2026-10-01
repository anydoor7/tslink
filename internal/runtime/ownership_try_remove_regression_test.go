package runtime

import (
	"path/filepath"
	"testing"
	"time"
)

func TestTryRemoveOwnedNodesIfUnchangedProtectsReactivatedIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ownership.json")
	now := time.Now()
	if err := RecordOwnedNode(path, "orphan", "old-node", now); err != nil {
		t.Fatal(err)
	}
	if err := MarkOwnedNodeIDsRetired(path, []string{"old-node"}, now); err != nil {
		t.Fatal(err)
	}
	baseline, err := LoadOwnership(path)
	if err != nil || len(baseline.Nodes) != 1 {
		t.Fatalf("baseline ledger: nodes=%d err=%v", len(baseline.Nodes), err)
	}
	if err := RecordOwnedNode(path, "orphan", "old-node", now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	acquired, err := TryRemoveOwnedNodesIfUnchanged(path, baseline.Nodes)
	if err != nil || !acquired {
		t.Fatalf("conditional removal: acquired=%v err=%v", acquired, err)
	}
	current, err := LoadOwnership(path)
	if err != nil || len(current.Nodes) != 1 || current.Nodes[0].RetiredAt != nil {
		t.Fatalf("reactivated identity was removed or altered: %+v err=%v", current.Nodes, err)
	}
	acquired, err = TryRemoveOwnedNodesIfUnchanged(path, current.Nodes)
	if err != nil || !acquired {
		t.Fatalf("unchanged removal: acquired=%v err=%v", acquired, err)
	}
	current, err = LoadOwnership(path)
	if err != nil || len(current.Nodes) != 0 {
		t.Fatalf("unchanged row was not removed: %+v err=%v", current.Nodes, err)
	}
}
