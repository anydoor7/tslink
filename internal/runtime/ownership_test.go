package runtime

import (
	"bytes"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"
	"time"

	"github.com/monody0007/tslink/internal/registry"
)

func TestOwnershipLedgerRecordsDeduplicatesAndRemovesExactIDs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "node-ownership.json")
	first := time.Date(2026, 8, 31, 1, 0, 0, 0, time.UTC)
	if err := RecordOwnedNode(path, "web", "node-owned-a", first); err != nil {
		t.Fatal(err)
	}
	if err := RecordOwnedNode(path, "web", "node-owned-a", first.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := RecordOwnedNode(path, "web", "node-owned-b", first); err != nil {
		t.Fatal(err)
	}
	ledger, err := LoadOwnership(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Nodes) != 2 || ledger.Nodes[0].NodeID != "node-owned-a" || !ledger.Nodes[0].RecordedAt.Equal(first.Add(time.Hour)) {
		t.Fatalf("ledger = %+v", ledger)
	}
	retired := first.Add(2 * time.Hour)
	if err := MarkOwnedNodeIDsRetired(path, []string{"node-owned-a"}, retired); err != nil {
		t.Fatal(err)
	}
	ledger, err = LoadOwnership(path)
	if err != nil || ledger.Nodes[0].RetiredAt == nil || !ledger.Nodes[0].RetiredAt.Equal(retired) {
		t.Fatalf("retired ledger = %+v, err=%v", ledger, err)
	}
	if err := RecordOwnedNode(path, "web", "node-owned-a", retired.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	ledger, err = LoadOwnership(path)
	if err != nil || ledger.Nodes[0].RetiredAt != nil {
		t.Fatalf("reactivated ledger = %+v, err=%v, want retired_at cleared", ledger, err)
	}
	if err := RemoveOwnedNodeIDs(path, []string{"node-owned-a"}); err != nil {
		t.Fatal(err)
	}
	ledger, _ = LoadOwnership(path)
	if len(ledger.Nodes) != 1 || ledger.Nodes[0].NodeID != "node-owned-b" {
		t.Fatalf("ledger after exact removal = %+v", ledger)
	}
	// Windows reports only the read-only attribute through these bits, so the
	// 0600 the ledger is written with reads back as 0666 there.
	info, err := os.Stat(path)
	if err != nil || (goruntime.GOOS != "windows" && info.Mode().Perm() != 0o600) {
		t.Fatalf("ownership mode = %v, err=%v", info.Mode().Perm(), err)
	}
}

func TestOwnershipLedgerFailsClosedOnConflictAndMalformedData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "node-ownership.json")
	if err := RecordOwnedNode(path, "web", "node-owned", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := RecordOwnedNode(path, "other", "node-owned", time.Now()); err == nil || !strings.Contains(err.Error(), "conflict") {
		t.Fatalf("cross-service conflict error = %v", err)
	}
	if err := os.WriteFile(path, []byte(`{"schema_version":1,"nodes":[{"service_name":"web","node_id":"","recorded_at":"2026-08-31T00:00:00Z"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOwnership(path); err == nil || strings.Contains(err.Error(), "node-owned") {
		t.Fatalf("malformed ownership error = %v, want generic without NodeID", err)
	}
}

func TestOwnershipLedgerRejectsFutureSchemaAndDuplicateNodeID(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want string
	}{
		{"future_schema", `{"schema_version":3,"nodes":[]}`, "schema_version"},
		{"duplicate_node", `{"schema_version":2,"nodes":[{"service_name":"one","node_id":"node-dup","recorded_at":"2030-01-01T00:00:00Z"},{"service_name":"two","node_id":"node-dup","recorded_at":"2030-01-01T00:00:00Z"}]}`, "duplicate"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "node-ownership.json")
			if err := os.WriteFile(path, []byte(tc.body), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadOwnership(path); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestLoadOwnershipMissingIsEmpty(t *testing.T) {
	ledger, err := LoadOwnership(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil || ledger.SchemaVersion != OwnershipSchemaVersion || len(ledger.Nodes) != 0 {
		t.Fatalf("missing ledger = %+v, err=%v", ledger, err)
	}
}

func TestLoadOwnershipAcceptsLegacySchemaWithoutRetiredAtAsUnknownProvenance(t *testing.T) {
	path := filepath.Join(t.TempDir(), "node-ownership.json")
	if err := os.WriteFile(path, []byte(`{"schema_version":1,"nodes":[{"service_name":"legacy","node_id":"node-legacy","recorded_at":"2030-01-01T00:00:00Z"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	ledger, err := LoadOwnership(path)
	if err != nil {
		t.Fatal(err)
	}
	if ledger.SchemaVersion != legacyOwnershipSchemaVersion || len(ledger.Nodes) != 1 || ledger.Nodes[0].RetiredAt != nil {
		t.Fatalf("legacy ledger = %+v, want readable row with unknown retirement provenance", ledger)
	}
}

func TestLoadOwnershipMalformedErrorNamesPathAndRecovery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "node-ownership.json")
	if err := os.WriteFile(path, []byte(`{"schema_version":1,"nodes":[`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadOwnership(path)
	if err == nil || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "device deletion is disabled") {
		t.Fatalf("error = %v, want path and safe recovery boundary", err)
	}
	var coded registry.CodedError
	if !errors.As(err, &coded) || coded.Code != "internal_error" || len(coded.Next) < 2 {
		t.Fatalf("coded error = %+v, err=%v", coded, err)
	}
}

func TestLoadOwnershipRejectsInvalidRetiredAtOrdering(t *testing.T) {
	for _, tc := range []struct {
		name      string
		retiredAt string
	}{
		{name: "zero", retiredAt: "0001-01-01T00:00:00Z"},
		{name: "before_recorded_at", retiredAt: "2029-12-31T23:59:59Z"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "node-ownership.json")
			body := `{"schema_version":2,"nodes":[{"service_name":"legacy","node_id":"node-legacy","recorded_at":"2030-01-01T00:00:00Z","retired_at":"` + tc.retiredAt + `"}]}`
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadOwnership(path); err == nil || !strings.Contains(err.Error(), "invalid retired_at") || strings.Contains(err.Error(), "node-legacy") {
				t.Fatalf("error = %v, want generic retired_at integrity refusal", err)
			}
		})
	}
}

func TestOwnershipWriteClampsClockRollbackAndWarns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "node-ownership.json")
	recordedAt := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := RecordOwnedNode(path, "clocked", "node-clocked", recordedAt); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	oldLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(oldLogger) })
	if err := MarkOwnedNodeIDsRetired(path, []string{"node-clocked"}, recordedAt.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	ledger, err := LoadOwnership(path)
	if err != nil {
		t.Fatalf("LoadOwnership() after clamp = %v", err)
	}
	if len(ledger.Nodes) != 1 || ledger.Nodes[0].RetiredAt == nil || !ledger.Nodes[0].RetiredAt.Equal(recordedAt) {
		t.Fatalf("ledger = %+v, want retired_at clamped to recorded_at", ledger)
	}
	if warning := logs.String(); !strings.Contains(warning, "level=WARN") || !strings.Contains(warning, "clock rollback") || !strings.Contains(warning, "clamped retired_at to recorded_at") || strings.Contains(warning, "node-clocked") {
		t.Fatalf("warning = %q, want non-secret clock rollback diagnosis", warning)
	}
}

func TestOwnershipWritePreservesNormalRetirementTimeWithoutClampWarning(t *testing.T) {
	path := filepath.Join(t.TempDir(), "node-ownership.json")
	recordedAt := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	retiredAt := recordedAt.Add(time.Hour)
	if err := RecordOwnedNode(path, "normal", "node-normal", recordedAt); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	oldLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(oldLogger) })
	if err := MarkOwnedNodeIDsRetired(path, []string{"node-normal"}, retiredAt); err != nil {
		t.Fatal(err)
	}
	ledger, err := LoadOwnership(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Nodes) != 1 || ledger.Nodes[0].RetiredAt == nil || !ledger.Nodes[0].RetiredAt.Equal(retiredAt) {
		t.Fatalf("ledger = %+v, want exact normal retired_at", ledger)
	}
	if strings.Contains(logs.String(), "clock rollback") {
		t.Fatalf("normal write emitted clamp warning: %q", logs.String())
	}
}

func TestAdoptOwnedNodeRequiresUniqueNameAndCrossServiceBinding(t *testing.T) {
	path := filepath.Join(t.TempDir(), "node-ownership.json")
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := AdoptOwnedNode(path, "legacy", "node-legacy", now, true); err != nil {
		t.Fatal(err)
	}
	if err := AdoptOwnedNode(path, "legacy", "node-different", now, true); err == nil || !strings.Contains(err.Error(), "name is already bound") {
		t.Fatalf("same-name conflict = %v", err)
	}
	if err := AdoptOwnedNode(path, "other", "node-legacy", now, true); err == nil || !strings.Contains(err.Error(), "across services") {
		t.Fatalf("cross-service conflict = %v", err)
	}
	ledger, err := LoadOwnership(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Nodes) != 1 || ledger.Nodes[0].ServiceName != "legacy" || ledger.Nodes[0].NodeID != "node-legacy" || ledger.Nodes[0].RetiredAt == nil || !ledger.Nodes[0].RetiredAt.Equal(now) {
		t.Fatalf("ledger = %+v, want only original adopted proof with reviewed retired_at", ledger)
	}
}

func TestAdoptOwnedNodeRetiresExistingExactRow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "node-ownership.json")
	recordedAt := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	retiredAt := recordedAt.Add(time.Hour)
	if err := RecordOwnedNode(path, "stranded", "node-stranded", recordedAt); err != nil {
		t.Fatal(err)
	}
	if err := AdoptOwnedNode(path, "stranded", "node-stranded", retiredAt, true); err != nil {
		t.Fatal(err)
	}
	ledger, err := LoadOwnership(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Nodes) != 1 || !ledger.Nodes[0].RecordedAt.Equal(retiredAt) || ledger.Nodes[0].RetiredAt == nil || !ledger.Nodes[0].RetiredAt.Equal(retiredAt) {
		t.Fatalf("ledger = %+v, want existing exact row retired at review time", ledger)
	}
}

// Every earlier clamp test used a single-row ledger, which made "clamp every
// row" and "clamp row 0" indistinguishable; a mutation that clamped only the
// first row survived the suite while producing exactly the self-locking ledger
// the clamp exists to prevent. Multi-row is production-reachable: RecordOwnedNode
// appends one row per NodeID and NodeIDs rotate on tag or ephemeral changes.
func TestOwnershipWriteClampsEveryRolledBackRowAndLeavesOthersAlone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "node-ownership.json")
	base := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	recorded := map[string]time.Time{
		"node-a": base,
		"node-b": base.Add(24 * time.Hour),
		"node-c": base.Add(48 * time.Hour),
		"node-d": base.Add(72 * time.Hour),
	}
	for id, at := range recorded {
		if err := RecordOwnedNode(path, "multi", id, at); err != nil {
			t.Fatal(err)
		}
	}
	// A retirement stamp earlier than every recorded_at: three rows roll back,
	// node-d is not retired at all and must be untouched.
	if err := MarkOwnedNodeIDsRetired(path, []string{"node-a", "node-b", "node-c"}, base.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	ledger, err := LoadOwnership(path)
	if err != nil {
		t.Fatalf("LoadOwnership() after multi-row clamp = %v (a partial clamp writes a ledger this package refuses to read)", err)
	}
	if len(ledger.Nodes) != 4 {
		t.Fatalf("ledger rows = %d, want 4", len(ledger.Nodes))
	}
	for _, node := range ledger.Nodes {
		want, ok := recorded[node.NodeID]
		if !ok {
			t.Fatalf("unexpected node %q", node.NodeID)
		}
		if !node.RecordedAt.Equal(want) {
			t.Fatalf("node %q recorded_at = %v, want %v (clamp must not touch recorded_at)", node.NodeID, node.RecordedAt, want)
		}
		if node.NodeID == "node-d" {
			if node.RetiredAt != nil {
				t.Fatalf("node-d retired_at = %v, want nil (row was not retired)", node.RetiredAt)
			}
			continue
		}
		if node.RetiredAt == nil || !node.RetiredAt.Equal(want) {
			t.Fatalf("node %q retired_at = %v, want clamped to its own recorded_at %v", node.NodeID, node.RetiredAt, want)
		}
	}
}

// The reader rejects a zero retired_at outright. A zero retired_at is already
// Before any real recorded_at and therefore already clamped; the one case the
// clamp cannot repair is a zero recorded_at, and that must fail the write rather
// than persist a ledger LoadOwnership refuses.
func TestOwnershipWriteRefusesZeroRecordedAtRetirementInsteadOfWritingUnreadableLedger(t *testing.T) {
	path := filepath.Join(t.TempDir(), "node-ownership.json")
	recordedAt := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := RecordOwnedNode(path, "live", "node-live", recordedAt); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	err = AdoptOwnedNode(path, "zero", "node-adopted", time.Time{}, true)
	if err == nil || !strings.Contains(err.Error(), "zero recorded_at") {
		t.Fatalf("AdoptOwnedNode(zero now, retire) error = %v, want refusal naming zero recorded_at", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("ledger changed on refused write:\nbefore=%s\nafter=%s", before, after)
	}
	if _, err := LoadOwnership(path); err != nil {
		t.Fatalf("LoadOwnership() after refused write = %v, want still readable", err)
	}
}
