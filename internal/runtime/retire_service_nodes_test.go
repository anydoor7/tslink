package runtime

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// B6a-4: RetireServiceNodes retires every row the ledger holds for the service
// when it runs, leaves other services' rows alone, and puts the ledger back
// exactly as it was when the registry write it wraps fails.

func retireFixture(t *testing.T) (path string, before OwnershipLedger) {
	t.Helper()
	path = filepath.Join(t.TempDir(), "node-ownership.json")
	at := time.Date(2035, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, row := range []struct{ service, node string }{{"web", "node-web-1"}, {"web", "node-web-2"}, {"other", "node-other"}} {
		if err := RecordOwnedNode(path, row.service, row.node, at); err != nil {
			t.Fatal(err)
		}
	}
	if err := MarkOwnedNodeIDsRetired(path, []string{"node-web-2"}, at.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	before, err := LoadOwnership(path)
	if err != nil {
		t.Fatal(err)
	}
	return path, before
}

func TestRetireServiceNodesRetiresTheServicesRowsWithTheCommit(t *testing.T) {
	path, before := retireFixture(t)
	retiredAt := time.Date(2035, 1, 2, 0, 0, 0, 0, time.UTC)
	committed := false
	owned, err := RetireServiceNodes(path, "web", retiredAt, func() error {
		committed = true
		return nil
	})
	if err != nil || !committed {
		t.Fatalf("RetireServiceNodes() = %v, committed=%t", err, committed)
	}
	var wantOwned []OwnedNode
	for _, node := range before.Nodes {
		if node.ServiceName == "web" {
			wantOwned = append(wantOwned, node)
		}
	}
	if !reflect.DeepEqual(owned, wantOwned) {
		t.Fatalf("owned = %+v, want the rows as they were before retirement %+v", owned, wantOwned)
	}
	after, err := LoadOwnership(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range after.Nodes {
		switch {
		case node.ServiceName == "web" && (node.RetiredAt == nil || !node.RetiredAt.Equal(retiredAt)):
			t.Fatalf("row %s retired_at = %v, want %v", node.NodeID, node.RetiredAt, retiredAt)
		case node.ServiceName == "other" && node.RetiredAt != nil:
			t.Fatalf("another service's row was retired: %+v", node)
		}
	}
}

func TestRetireServiceNodesPutsTheLedgerBackWhenTheCommitFails(t *testing.T) {
	path, before := retireFixture(t)
	commitErr := errors.New("synthetic registry write failure")
	sawRetired := false
	_, err := RetireServiceNodes(path, "web", time.Date(2035, 1, 2, 0, 0, 0, 0, time.UTC), func() error {
		// The retirement is on disk before the registry write runs.
		during, loadErr := LoadOwnership(path)
		if loadErr != nil {
			return loadErr
		}
		for _, node := range during.Nodes {
			if node.NodeID == "node-web-1" && node.RetiredAt != nil {
				sawRetired = true
			}
		}
		return commitErr
	})
	if !errors.Is(err, commitErr) {
		t.Fatalf("RetireServiceNodes() error = %v, want the commit failure", err)
	}
	if !sawRetired {
		t.Fatal("control: the retirement was not recorded before the commit ran")
	}
	after, err := LoadOwnership(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("ledger after a failed commit = %+v, want it back as %+v", after, before)
	}
}

func TestRetireServiceNodesRunsNoCommitWithoutAReadableLedger(t *testing.T) {
	path := filepath.Join(t.TempDir(), "node-ownership.json")
	if err := os.WriteFile(path, []byte("{ not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := RetireServiceNodes(path, "web", time.Now(), func() error {
		t.Fatal("commit ran although the removal could not be recorded")
		return nil
	}); err == nil {
		t.Fatal("RetireServiceNodes() error = nil for an unreadable ledger")
	}
}
