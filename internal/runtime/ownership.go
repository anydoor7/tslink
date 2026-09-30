package runtime

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/monody0007/tslink/internal/atomicfile"
	"github.com/monody0007/tslink/internal/filelock"
	"github.com/monody0007/tslink/internal/registry"
)

const (
	legacyOwnershipSchemaVersion = 1
	OwnershipSchemaVersion       = 2
)

// OwnedNode is durable proof that a concrete Tailscale StableNodeID was
// observed from a TSLink-managed tsnet node for ServiceName.
type OwnedNode struct {
	ServiceName string     `json:"service_name"`
	NodeID      string     `json:"node_id"`
	RecordedAt  time.Time  `json:"recorded_at"`
	RetiredAt   *time.Time `json:"retired_at,omitempty"`
}

type OwnershipLedger struct {
	SchemaVersion int         `json:"schema_version"`
	Nodes         []OwnedNode `json:"nodes"`
}

func emptyOwnershipLedger() OwnershipLedger {
	return OwnershipLedger{SchemaVersion: OwnershipSchemaVersion, Nodes: []OwnedNode{}}
}

// ownershipLoadError quotes the path without escaping it. %q doubles every
// backslash, so a Windows user was told about a file named
// "C:\\Users\\...", which is not a path they can copy or recognise.
func ownershipLoadError(path string, err error) error {
	return registry.CodedError{
		Code:    "internal_error",
		Message: fmt.Sprintf("cannot safely read node ownership ledger \"%s\": %v; device deletion is disabled until the ledger is repaired or moved aside", path, err),
		Next: []string{
			fmt.Sprintf("Back up and inspect \"%s\"", path),
			fmt.Sprintf("Move \"%s\" aside only after preserving it for recovery", path),
			"tslink cleanup --dry-run",
		},
		MessageOnly: true,
	}
}

// LoadOwnership reads the durable ownership ledger. An absent or empty file is
// an empty ledger; malformed data returns an actionable coded error so callers
// can disable deletion without taking down service availability.
func LoadOwnership(path string) (OwnershipLedger, error) {
	if err := atomicfile.ConvergePrivateFile(path); err != nil {
		return OwnershipLedger{}, ownershipLoadError(path, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return emptyOwnershipLedger(), nil
		}
		return OwnershipLedger{}, ownershipLoadError(path, err)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return emptyOwnershipLedger(), nil
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var ledger OwnershipLedger
	if err := decoder.Decode(&ledger); err != nil {
		return OwnershipLedger{}, ownershipLoadError(path, fmt.Errorf("decode node ownership ledger: %w", err))
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return OwnershipLedger{}, ownershipLoadError(path, fmt.Errorf("decode node ownership ledger: unexpected trailing JSON value"))
	} else if !errors.Is(err, io.EOF) {
		return OwnershipLedger{}, ownershipLoadError(path, fmt.Errorf("decode node ownership ledger: %w", err))
	}
	if ledger.SchemaVersion != legacyOwnershipSchemaVersion && ledger.SchemaVersion != OwnershipSchemaVersion {
		return OwnershipLedger{}, ownershipLoadError(path, fmt.Errorf("unsupported node ownership schema_version: %d", ledger.SchemaVersion))
	}
	if ledger.Nodes == nil {
		ledger.Nodes = []OwnedNode{}
	}
	seen := make(map[string]struct{}, len(ledger.Nodes))
	for _, node := range ledger.Nodes {
		if err := registry.ValidateName(node.ServiceName); err != nil {
			return OwnershipLedger{}, ownershipLoadError(path, fmt.Errorf("invalid ownership service name: %w", err))
		}
		if strings.TrimSpace(node.NodeID) == "" || len(node.NodeID) > 256 {
			return OwnershipLedger{}, ownershipLoadError(path, fmt.Errorf("ownership ledger contains an invalid node ID"))
		}
		if _, duplicate := seen[node.NodeID]; duplicate {
			return OwnershipLedger{}, ownershipLoadError(path, fmt.Errorf("ownership ledger contains a duplicate node ID"))
		}
		if node.RetiredAt != nil && (node.RetiredAt.IsZero() || node.RetiredAt.Before(node.RecordedAt)) {
			return OwnershipLedger{}, ownershipLoadError(path, fmt.Errorf("ownership ledger contains an invalid retired_at timestamp"))
		}
		seen[node.NodeID] = struct{}{}
	}
	return ledger, nil
}

func withOwnershipLock(path string, fn func() error) error {
	if err := atomicfile.EnsurePrivateDir(filepath.Dir(path)); err != nil {
		return err
	}
	lockPath := path + ".lock"
	if err := atomicfile.ConvergePrivateFile(lockPath); err != nil {
		return err
	}
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lockFile.Close()
	if err := filelock.Lock(lockFile); err != nil {
		return err
	}
	defer filelock.Unlock(lockFile)
	return fn()
}

// tryWithOwnershipLock leaves cleanup's proof unresolved when a writer owns
// the ledger lock. It never starts a waiter that could delete state later.
func tryWithOwnershipLock(path string, fn func() error) (bool, error) {
	if err := atomicfile.EnsurePrivateDir(filepath.Dir(path)); err != nil {
		return false, err
	}
	lockPath := path + ".lock"
	if err := atomicfile.ConvergePrivateFile(lockPath); err != nil {
		return false, err
	}
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return false, err
	}
	defer lockFile.Close()
	acquired, err := filelock.TryLock(lockFile)
	if err != nil || !acquired {
		return acquired, err
	}
	defer filelock.Unlock(lockFile)
	return true, fn()
}

// WithLockedOwnership keeps a read and its local-state action atomic with
// RecordOwnedNode and RemoveOwnedNodeIDs. The callback must not write the
// ownership ledger or acquire this lock again.
func WithLockedOwnership(path string, fn func(OwnershipLedger) error) error {
	return withOwnershipLock(path, func() error {
		ledger, err := LoadOwnership(path)
		if err != nil {
			return err
		}
		return fn(ledger)
	})
}

// TryWithLockedOwnership reports false on contention without invoking fn.
func TryWithLockedOwnership(path string, fn func(OwnershipLedger) error) (bool, error) {
	return tryWithOwnershipLock(path, func() error {
		ledger, err := LoadOwnership(path)
		if err != nil {
			return err
		}
		return fn(ledger)
	})
}

func saveOwnership(path string, ledger OwnershipLedger) error {
	ledger.SchemaVersion = OwnershipSchemaVersion
	if ledger.Nodes == nil {
		ledger.Nodes = []OwnedNode{}
	}
	clockRollbackClamped := false
	for i := range ledger.Nodes {
		if ledger.Nodes[i].RetiredAt == nil {
			continue
		}
		// Mirror LoadOwnership's predicate exactly (IsZero || Before). A zero
		// retired_at is already Before any real recorded_at, so the only case the
		// clamp cannot repair is a node whose recorded_at is itself zero: clamping
		// would write a zero retired_at that the reader still refuses. Refuse the
		// write instead of producing a ledger this package cannot read back.
		if !ledger.Nodes[i].RetiredAt.IsZero() && !ledger.Nodes[i].RetiredAt.Before(ledger.Nodes[i].RecordedAt) {
			continue
		}
		if ledger.Nodes[i].RecordedAt.IsZero() {
			return fmt.Errorf("ownership ledger node has a zero recorded_at, so retired_at cannot be clamped to a readable value")
		}
		retiredAt := ledger.Nodes[i].RecordedAt
		ledger.Nodes[i].RetiredAt = &retiredAt
		clockRollbackClamped = true
	}
	sort.Slice(ledger.Nodes, func(i, j int) bool {
		if ledger.Nodes[i].ServiceName == ledger.Nodes[j].ServiceName {
			return ledger.Nodes[i].NodeID < ledger.Nodes[j].NodeID
		}
		return ledger.Nodes[i].ServiceName < ledger.Nodes[j].ServiceName
	})
	data, err := json.MarshalIndent(ledger, "", "  ")
	if err != nil {
		return err
	}
	if err := atomicfile.WriteFile(path, append(data, '\n')); err != nil {
		return err
	}
	if clockRollbackClamped {
		slog.Warn("clock rollback detected while saving ownership ledger; clamped retired_at to recorded_at")
	}
	return nil
}

// RecordOwnedNode idempotently records exact NodeID ownership for a service.
func RecordOwnedNode(path, serviceName, nodeID string, recordedAt time.Time) error {
	if err := registry.ValidateName(serviceName); err != nil {
		return err
	}
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" || len(nodeID) > 256 {
		return fmt.Errorf("cannot record an empty or invalid node ID")
	}
	return withOwnershipLock(path, func() error {
		ledger, err := LoadOwnership(path)
		if err != nil {
			return err
		}
		for i, node := range ledger.Nodes {
			if node.NodeID != nodeID {
				continue
			}
			if node.ServiceName != serviceName {
				return fmt.Errorf("node ownership conflict across services")
			}
			ledger.Nodes[i].RecordedAt = recordedAt.UTC()
			ledger.Nodes[i].RetiredAt = nil
			return saveOwnership(path, ledger)
		}
		ledger.Nodes = append(ledger.Nodes, OwnedNode{ServiceName: serviceName, NodeID: nodeID, RecordedAt: recordedAt.UTC()})
		return saveOwnership(path, ledger)
	})
}

func adoptOwnedNode(ledger OwnershipLedger, serviceName, nodeID string, recordedAt time.Time, retire bool) (OwnershipLedger, error) {
	if err := registry.ValidateName(serviceName); err != nil {
		return OwnershipLedger{}, err
	}
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" || len(nodeID) > 256 {
		return OwnershipLedger{}, fmt.Errorf("cannot adopt an empty or invalid node ID")
	}
	ledger.Nodes = append([]OwnedNode(nil), ledger.Nodes...)
	recordedAt = recordedAt.UTC()
	for i, node := range ledger.Nodes {
		switch {
		case node.ServiceName == serviceName && node.NodeID != nodeID:
			return OwnershipLedger{}, fmt.Errorf("service ownership conflict: name is already bound to a different node ID")
		case node.NodeID == nodeID && node.ServiceName != serviceName:
			return OwnershipLedger{}, fmt.Errorf("node ownership conflict across services")
		case node.NodeID == nodeID:
			ledger.Nodes[i].RecordedAt = recordedAt
			ledger.Nodes[i].RetiredAt = nil
			if retire {
				ledger.Nodes[i].RetiredAt = &recordedAt
			}
			return ledger, nil
		}
	}
	adopted := OwnedNode{
		ServiceName: serviceName,
		NodeID:      nodeID,
		RecordedAt:  recordedAt,
	}
	if retire {
		adopted.RetiredAt = &recordedAt
	}
	ledger.Nodes = append(ledger.Nodes, adopted)
	return ledger, nil
}

// PreviewAdoptOwnedNode returns the exact proof that adoption would persist,
// without writing the ledger. retire is true only for a service absent from the
// registry, allowing cleanup dry-run to preview the corresponding deletion.
func PreviewAdoptOwnedNode(path, serviceName, nodeID string, recordedAt time.Time, retire bool) (OwnershipLedger, error) {
	ledger, err := LoadOwnership(path)
	if err != nil {
		return OwnershipLedger{}, err
	}
	return adoptOwnedNode(ledger, serviceName, nodeID, recordedAt, retire)
}

// AdoptOwnedNode records a one-time reviewed proof for an exact device match.
// Orphans are retired immediately as explicit cleanup authorization; services
// still present in registry.json receive active proof without retired_at.
func AdoptOwnedNode(path, serviceName, nodeID string, recordedAt time.Time, retire bool) error {
	return withOwnershipLock(path, func() error {
		ledger, err := LoadOwnership(path)
		if err != nil {
			return err
		}
		ledger, err = adoptOwnedNode(ledger, serviceName, nodeID, recordedAt, retire)
		if err != nil {
			return err
		}
		return saveOwnership(path, ledger)
	})
}

// MarkOwnedNodeIDsRetired records that remove reviewed the exact ownership
// rows before unregistering their service. Rows remain until remote deletion
// succeeds or proves the device already absent.
func MarkOwnedNodeIDsRetired(path string, nodeIDs []string, retiredAt time.Time) error {
	if len(nodeIDs) == 0 {
		return nil
	}
	retire := make(map[string]struct{}, len(nodeIDs))
	for _, nodeID := range nodeIDs {
		nodeID = strings.TrimSpace(nodeID)
		if nodeID == "" || len(nodeID) > 256 {
			return fmt.Errorf("cannot retire an empty or invalid node ID")
		}
		retire[nodeID] = struct{}{}
	}
	retiredAt = retiredAt.UTC()
	return withOwnershipLock(path, func() error {
		ledger, err := LoadOwnership(path)
		if err != nil {
			return err
		}
		for i := range ledger.Nodes {
			if _, ok := retire[ledger.Nodes[i].NodeID]; ok {
				ledger.Nodes[i].RetiredAt = &retiredAt
			}
		}
		return saveOwnership(path, ledger)
	})
}

// RemoveOwnedNodeIDs forgets only ownership IDs that remote reconciliation has
// proved deleted or already absent.
func RemoveOwnedNodeIDs(path string, nodeIDs []string) error {
	if len(nodeIDs) == 0 {
		return nil
	}
	return withOwnershipLock(path, removeOwnedNodeIDsAction(path, nodeIDs))
}

// TryRemoveOwnedNodesIfUnchanged clears only the exact rows that were proved
// resolved before local cleanup. A newly recorded identity with the same ID
// must survive a writer arriving after the final state deletion.
func TryRemoveOwnedNodesIfUnchanged(path string, oldNodes []OwnedNode) (bool, error) {
	if len(oldNodes) == 0 {
		return true, nil
	}
	byID := make(map[string]OwnedNode, len(oldNodes))
	for _, node := range oldNodes {
		byID[node.NodeID] = node
	}
	return tryWithOwnershipLock(path, func() error {
		ledger, err := LoadOwnership(path)
		if err != nil {
			return err
		}
		kept := ledger.Nodes[:0]
		for _, node := range ledger.Nodes {
			if old, ok := byID[node.NodeID]; !ok || !sameOwnedNode(old, node) {
				kept = append(kept, node)
			}
		}
		ledger.Nodes = kept
		return saveOwnership(path, ledger)
	})
}

func sameOwnedNode(a, b OwnedNode) bool {
	if a.ServiceName != b.ServiceName || a.NodeID != b.NodeID || !a.RecordedAt.Equal(b.RecordedAt) || (a.RetiredAt == nil) != (b.RetiredAt == nil) {
		return false
	}
	return a.RetiredAt == nil || a.RetiredAt.Equal(*b.RetiredAt)
}

func removeOwnedNodeIDsAction(path string, nodeIDs []string) func() error {
	remove := make(map[string]struct{}, len(nodeIDs))
	for _, nodeID := range nodeIDs {
		remove[nodeID] = struct{}{}
	}
	return func() error {
		ledger, err := LoadOwnership(path)
		if err != nil {
			return err
		}
		kept := ledger.Nodes[:0]
		for _, node := range ledger.Nodes {
			if _, ok := remove[node.NodeID]; !ok {
				kept = append(kept, node)
			}
		}
		ledger.Nodes = kept
		return saveOwnership(path, ledger)
	}
}
