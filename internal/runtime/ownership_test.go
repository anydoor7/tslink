package runtime

import (
	"errors"
	"os"
	"path/filepath"
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
	if err := RemoveOwnedNodeIDs(path, []string{"node-owned-a"}); err != nil {
		t.Fatal(err)
	}
	ledger, _ = LoadOwnership(path)
	if len(ledger.Nodes) != 1 || ledger.Nodes[0].NodeID != "node-owned-b" {
		t.Fatalf("ledger after exact removal = %+v", ledger)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
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
		{"future_schema", `{"schema_version":2,"nodes":[]}`, "schema_version"},
		{"duplicate_node", `{"schema_version":1,"nodes":[{"service_name":"one","node_id":"node-dup","recorded_at":"2030-01-01T00:00:00Z"},{"service_name":"two","node_id":"node-dup","recorded_at":"2030-01-01T00:00:00Z"}]}`, "duplicate"},
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

func TestAdoptOwnedNodeRequiresUniqueNameAndCrossServiceBinding(t *testing.T) {
	path := filepath.Join(t.TempDir(), "node-ownership.json")
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := AdoptOwnedNode(path, "legacy", "node-legacy", now); err != nil {
		t.Fatal(err)
	}
	if err := AdoptOwnedNode(path, "legacy", "node-different", now); err == nil || !strings.Contains(err.Error(), "name is already bound") {
		t.Fatalf("same-name conflict = %v", err)
	}
	if err := AdoptOwnedNode(path, "other", "node-legacy", now); err == nil || !strings.Contains(err.Error(), "across services") {
		t.Fatalf("cross-service conflict = %v", err)
	}
	ledger, err := LoadOwnership(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Nodes) != 1 || ledger.Nodes[0].ServiceName != "legacy" || ledger.Nodes[0].NodeID != "node-legacy" {
		t.Fatalf("ledger = %+v, want only original adopted proof", ledger)
	}
}
