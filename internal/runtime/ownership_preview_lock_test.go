package runtime

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// The ownership ledger is the deletion authorization record. PreviewAdoptOwnedNode
// backs `cleanup --adopt --dry-run`, so it must show exactly what AdoptOwnedNode
// would persist while leaving the on-disk ledger untouched. withOwnershipLock is
// the mutual exclusion around every write to that record.

func normalizedLedgerJSON(t *testing.T, ledger OwnershipLedger) string {
	t.Helper()
	nodes := append([]OwnedNode(nil), ledger.Nodes...)
	// saveOwnership sorts on write while adoptOwnedNode appends; normalize order
	// so the comparison is about content, not write-time ordering.
	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i].ServiceName == nodes[j].ServiceName {
			return nodes[i].NodeID < nodes[j].NodeID
		}
		return nodes[i].ServiceName < nodes[j].ServiceName
	})
	data, err := json.Marshal(OwnershipLedger{SchemaVersion: ledger.SchemaVersion, Nodes: nodes})
	if err != nil {
		t.Fatalf("marshal ledger: %v", err)
	}
	return string(data)
}

func readFileOrAbsent(t *testing.T, path string) (string, bool) {
	t.Helper()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", false
	}
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", path, err)
	}
	return string(data), true
}

func TestPreviewAdoptOwnedNodeMatchesWhatAdoptPersists(t *testing.T) {
	for _, retire := range []bool{false, true} {
		t.Run(fmt.Sprintf("retire=%t", retire), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "node-ownership.json")
			recordedAt := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
			if err := RecordOwnedNode(path, "docs", "nodeid-fake-docs", recordedAt.Add(-time.Hour)); err != nil {
				t.Fatalf("seed RecordOwnedNode() error = %v", err)
			}
			before, existed := readFileOrAbsent(t, path)
			if !existed {
				t.Fatal("seed ledger was not written")
			}

			preview, err := PreviewAdoptOwnedNode(path, "web", "nodeid-fake-web", recordedAt, retire)
			if err != nil {
				t.Fatalf("PreviewAdoptOwnedNode() error = %v", err)
			}

			after, _ := readFileOrAbsent(t, path)
			if after != before {
				t.Fatalf("PreviewAdoptOwnedNode() rewrote the ledger.\nbefore:\n%s\nafter:\n%s", before, after)
			}

			if err := AdoptOwnedNode(path, "web", "nodeid-fake-web", recordedAt, retire); err != nil {
				t.Fatalf("AdoptOwnedNode() error = %v", err)
			}
			persisted, err := LoadOwnership(path)
			if err != nil {
				t.Fatalf("LoadOwnership() error = %v", err)
			}
			if got, want := normalizedLedgerJSON(t, preview), normalizedLedgerJSON(t, persisted); got != want {
				t.Fatalf("preview diverged from what adopt persisted.\npreview:   %s\npersisted: %s", got, want)
			}

			// retire is what turns a preview row into cleanup authorization; it must
			// not be silently dropped on either side.
			var adopted *OwnedNode
			for i := range preview.Nodes {
				if preview.Nodes[i].NodeID == "nodeid-fake-web" {
					adopted = &preview.Nodes[i]
				}
			}
			if adopted == nil {
				t.Fatalf("preview ledger = %+v, want the adopted node present", preview.Nodes)
			}
			if retire && adopted.RetiredAt == nil {
				t.Fatal("preview dropped retired_at, so a dry-run would understate the deletion it authorizes")
			}
			if !retire && adopted.RetiredAt != nil {
				t.Fatal("preview invented retired_at, so a dry-run would claim a live service is deletable")
			}
		})
	}
}

func TestPreviewAdoptOwnedNodeDoesNotCreateAnAbsentLedger(t *testing.T) {
	path := filepath.Join(t.TempDir(), "node-ownership.json")

	preview, err := PreviewAdoptOwnedNode(path, "web", "nodeid-fake-web", time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC), true)
	if err != nil {
		t.Fatalf("PreviewAdoptOwnedNode() error = %v", err)
	}
	if len(preview.Nodes) != 1 || preview.Nodes[0].NodeID != "nodeid-fake-web" {
		t.Fatalf("preview nodes = %+v, want the single adopted node", preview.Nodes)
	}
	if _, existed := readFileOrAbsent(t, path); existed {
		t.Fatal("PreviewAdoptOwnedNode() created the ledger; a dry-run must not grant durable authorization")
	}
}

func TestPreviewAdoptOwnedNodeFailsClosedOnCorruptLedger(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "node-ownership.json")
	if err := os.WriteFile(path, []byte(`{"schema_version":2,"nodes":[`), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	preview, err := PreviewAdoptOwnedNode(path, "web", "nodeid-fake-web", time.Now(), true)
	if err == nil {
		t.Fatalf("PreviewAdoptOwnedNode() error = nil, want a refusal; preview = %+v", preview)
	}
	if len(preview.Nodes) != 0 {
		t.Fatalf("preview nodes = %+v, want an empty ledger on refusal", preview.Nodes)
	}
	if !strings.Contains(err.Error(), "device deletion is disabled") {
		t.Fatalf("PreviewAdoptOwnedNode() error = %v, want the actionable ledger-repair guidance", err)
	}
}

func TestPreviewAdoptOwnedNodeRejectsOwnershipConflictsWithoutTouchingLedger(t *testing.T) {
	recordedAt := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name        string
		serviceName string
		nodeID      string
		wantSubstr  string
	}{
		{
			name:        "same service bound to a different node",
			serviceName: "web",
			nodeID:      "nodeid-fake-other",
			wantSubstr:  "service ownership conflict",
		},
		{
			name:        "same node claimed by a different service",
			serviceName: "docs",
			nodeID:      "nodeid-fake-web",
			wantSubstr:  "node ownership conflict across services",
		},
		{
			name:        "blank node id",
			serviceName: "api",
			nodeID:      "   ",
			wantSubstr:  "cannot adopt an empty or invalid node ID",
		},
		{
			name:        "invalid service name",
			serviceName: "Not A Name",
			nodeID:      "nodeid-fake-api",
			wantSubstr:  "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "node-ownership.json")
			if err := RecordOwnedNode(path, "web", "nodeid-fake-web", recordedAt); err != nil {
				t.Fatalf("seed RecordOwnedNode() error = %v", err)
			}
			before, _ := readFileOrAbsent(t, path)

			_, err := PreviewAdoptOwnedNode(path, tc.serviceName, tc.nodeID, recordedAt, true)
			if err == nil {
				t.Fatal("PreviewAdoptOwnedNode() error = nil, want a conflict refusal")
			}
			if tc.wantSubstr != "" && !strings.Contains(err.Error(), tc.wantSubstr) {
				t.Fatalf("PreviewAdoptOwnedNode() error = %v, want it to mention %q", err, tc.wantSubstr)
			}
			if after, _ := readFileOrAbsent(t, path); after != before {
				t.Fatalf("refused preview still rewrote the ledger.\nbefore:\n%s\nafter:\n%s", before, after)
			}
		})
	}
}

func TestPreviewAdoptOwnedNodeLeavesLoadedLedgerRowsAlone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "node-ownership.json")
	recordedAt := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	if err := RecordOwnedNode(path, "web", "nodeid-fake-web", recordedAt); err != nil {
		t.Fatalf("seed RecordOwnedNode() error = %v", err)
	}

	// Re-adopting an existing row refreshes it; the untouched rows of other
	// services must survive the preview unchanged.
	if err := RecordOwnedNode(path, "docs", "nodeid-fake-docs", recordedAt); err != nil {
		t.Fatalf("seed RecordOwnedNode() error = %v", err)
	}
	preview, err := PreviewAdoptOwnedNode(path, "web", "nodeid-fake-web", recordedAt.Add(time.Hour), true)
	if err != nil {
		t.Fatalf("PreviewAdoptOwnedNode() error = %v", err)
	}
	if len(preview.Nodes) != 2 {
		t.Fatalf("preview nodes = %+v, want both existing rows preserved", preview.Nodes)
	}
	for _, node := range preview.Nodes {
		switch node.NodeID {
		case "nodeid-fake-docs":
			if node.RetiredAt != nil {
				t.Fatalf("preview retired an unrelated service row: %+v", node)
			}
			if !node.RecordedAt.Equal(recordedAt) {
				t.Fatalf("preview rewrote an unrelated recorded_at: %+v", node)
			}
		case "nodeid-fake-web":
			if node.RetiredAt == nil || !node.RecordedAt.Equal(recordedAt.Add(time.Hour)) {
				t.Fatalf("preview did not refresh and retire the adopted row: %+v", node)
			}
		default:
			t.Fatalf("unexpected preview row %+v", node)
		}
	}
}

func TestWithOwnershipLockRefusesSymlinkedLedgerDirectory(t *testing.T) {
	if os.Getenv("TSLINK_SKIP_SYMLINK_TESTS") == "1" {
		t.Skip("symlink creation disabled in this environment")
	}
	root := t.TempDir()
	real := filepath.Join(root, "real")
	if err := os.MkdirAll(real, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlink unsupported here: %v", err)
	}
	path := filepath.Join(link, "node-ownership.json")

	called := false
	err := withOwnershipLock(path, func() error {
		called = true
		return nil
	})
	if err == nil {
		t.Fatal("withOwnershipLock() error = nil, want refusal for a symlinked ledger directory")
	}
	if called {
		t.Fatal("withOwnershipLock() ran the write body through a symlinked directory")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("withOwnershipLock() error = %v, want it to name the symlink", err)
	}
	if err := RecordOwnedNode(path, "web", "nodeid-fake-web", time.Now()); err == nil {
		t.Fatal("RecordOwnedNode() wrote ownership proof through a symlinked directory")
	}
	if _, statErr := os.Stat(filepath.Join(real, "node-ownership.json")); !os.IsNotExist(statErr) {
		t.Fatalf("ledger materialized behind the symlink: %v", statErr)
	}
}

func TestWithOwnershipLockRefusesNonRegularLockFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "node-ownership.json")
	if err := os.MkdirAll(path+".lock", 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	called := false
	err := withOwnershipLock(path, func() error {
		called = true
		return nil
	})
	if err == nil {
		t.Fatal("withOwnershipLock() error = nil, want refusal when the lock path is not a regular file")
	}
	if called {
		t.Fatal("withOwnershipLock() ran the write body without holding a real lock")
	}
	if !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("withOwnershipLock() error = %v, want it to name the unsafe lock file", err)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatalf("ledger was written despite an unusable lock: %v", statErr)
	}
}

func TestOwnershipLockSerializesConcurrentWritersWithoutLosingRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "node-ownership.json")
	recordedAt := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)

	const writers = 16
	var start sync.WaitGroup
	var done sync.WaitGroup
	start.Add(1)
	errs := make([]error, writers)
	for i := 0; i < writers; i++ {
		done.Add(1)
		go func(i int) {
			defer done.Done()
			start.Wait()
			errs[i] = RecordOwnedNode(path, "web", fmt.Sprintf("nodeid-fake-%02d", i), recordedAt)
		}(i)
	}
	start.Done()
	done.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent RecordOwnedNode(%d) error = %v", i, err)
		}
	}

	ledger, err := LoadOwnership(path)
	if err != nil {
		t.Fatalf("LoadOwnership() error = %v", err)
	}
	seen := make(map[string]struct{}, len(ledger.Nodes))
	for _, node := range ledger.Nodes {
		seen[node.NodeID] = struct{}{}
	}
	if len(seen) != writers {
		var missing []string
		for i := 0; i < writers; i++ {
			id := fmt.Sprintf("nodeid-fake-%02d", i)
			if _, ok := seen[id]; !ok {
				missing = append(missing, id)
			}
		}
		t.Fatalf("ledger kept %d/%d concurrently recorded node IDs; lost %v", len(seen), writers, missing)
	}
}

func TestOwnershipLockSerializesInterleavedRecordAndRetire(t *testing.T) {
	path := filepath.Join(t.TempDir(), "node-ownership.json")
	recordedAt := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)

	const pairs = 8
	for i := 0; i < pairs; i++ {
		if err := RecordOwnedNode(path, "web", fmt.Sprintf("nodeid-fake-seed-%02d", i), recordedAt); err != nil {
			t.Fatalf("seed RecordOwnedNode(%d) error = %v", i, err)
		}
	}

	var start sync.WaitGroup
	var done sync.WaitGroup
	start.Add(1)
	errs := make([]error, 2*pairs)
	for i := 0; i < pairs; i++ {
		done.Add(2)
		go func(i int) {
			defer done.Done()
			start.Wait()
			errs[i] = RecordOwnedNode(path, "web", fmt.Sprintf("nodeid-fake-new-%02d", i), recordedAt)
		}(i)
		go func(i int) {
			defer done.Done()
			start.Wait()
			errs[pairs+i] = MarkOwnedNodeIDsRetired(path, []string{fmt.Sprintf("nodeid-fake-seed-%02d", i)}, recordedAt.Add(time.Hour))
		}(i)
	}
	start.Done()
	done.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent ownership write %d error = %v", i, err)
		}
	}

	ledger, err := LoadOwnership(path)
	if err != nil {
		t.Fatalf("LoadOwnership() error = %v", err)
	}
	if len(ledger.Nodes) != 2*pairs {
		t.Fatalf("ledger holds %d rows, want %d after interleaved record/retire", len(ledger.Nodes), 2*pairs)
	}
	retired := 0
	for _, node := range ledger.Nodes {
		if strings.HasPrefix(node.NodeID, "nodeid-fake-seed-") && node.RetiredAt == nil {
			t.Fatalf("seeded row %q lost its retirement to a concurrent write", node.NodeID)
		}
		if node.RetiredAt != nil {
			retired++
		}
	}
	if retired != pairs {
		t.Fatalf("%d rows retired, want %d", retired, pairs)
	}
}
