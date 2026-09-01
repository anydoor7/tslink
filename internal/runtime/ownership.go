package runtime

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/monody0007/tslink/internal/atomicfile"
	"github.com/monody0007/tslink/internal/filelock"
	"github.com/monody0007/tslink/internal/registry"
)

const OwnershipSchemaVersion = 1

// OwnedNode is durable proof that a concrete Tailscale StableNodeID was
// observed from a TSLink-managed tsnet node for ServiceName.
type OwnedNode struct {
	ServiceName string    `json:"service_name"`
	NodeID      string    `json:"node_id"`
	RecordedAt  time.Time `json:"recorded_at"`
}

type OwnershipLedger struct {
	SchemaVersion int         `json:"schema_version"`
	Nodes         []OwnedNode `json:"nodes"`
}

func emptyOwnershipLedger() OwnershipLedger {
	return OwnershipLedger{SchemaVersion: OwnershipSchemaVersion, Nodes: []OwnedNode{}}
}

func ownershipLoadError(path string, err error) error {
	return registry.CodedError{
		Code:    "internal_error",
		Message: fmt.Sprintf("cannot safely read node ownership ledger %q: %v; device deletion is disabled until the ledger is repaired or moved aside", path, err),
		Next: []string{
			fmt.Sprintf("Back up and inspect %q", path),
			fmt.Sprintf("Move %q aside only after preserving it for recovery", path),
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
	if ledger.SchemaVersion != OwnershipSchemaVersion {
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

func saveOwnership(path string, ledger OwnershipLedger) error {
	ledger.SchemaVersion = OwnershipSchemaVersion
	if ledger.Nodes == nil {
		ledger.Nodes = []OwnedNode{}
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
	return atomicfile.WriteFile(path, append(data, '\n'))
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
			return saveOwnership(path, ledger)
		}
		ledger.Nodes = append(ledger.Nodes, OwnedNode{ServiceName: serviceName, NodeID: nodeID, RecordedAt: recordedAt.UTC()})
		return saveOwnership(path, ledger)
	})
}

// AdoptOwnedNode records a one-time migration proof for an exact device match.
// Unlike normal startup recording, adoption refuses to attach a service name
// that is already bound to any different NodeID.
func AdoptOwnedNode(path, serviceName, nodeID string, recordedAt time.Time) error {
	if err := registry.ValidateName(serviceName); err != nil {
		return err
	}
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" || len(nodeID) > 256 {
		return fmt.Errorf("cannot adopt an empty or invalid node ID")
	}
	return withOwnershipLock(path, func() error {
		ledger, err := LoadOwnership(path)
		if err != nil {
			return err
		}
		for i, node := range ledger.Nodes {
			switch {
			case node.ServiceName == serviceName && node.NodeID != nodeID:
				return fmt.Errorf("service ownership conflict: name is already bound to a different node ID")
			case node.NodeID == nodeID && node.ServiceName != serviceName:
				return fmt.Errorf("node ownership conflict across services")
			case node.NodeID == nodeID:
				ledger.Nodes[i].RecordedAt = recordedAt.UTC()
				return saveOwnership(path, ledger)
			}
		}
		ledger.Nodes = append(ledger.Nodes, OwnedNode{ServiceName: serviceName, NodeID: nodeID, RecordedAt: recordedAt.UTC()})
		return saveOwnership(path, ledger)
	})
}

// RemoveOwnedNodeIDs forgets only ownership IDs that remote reconciliation has
// proved deleted or already absent.
func RemoveOwnedNodeIDs(path string, nodeIDs []string) error {
	if len(nodeIDs) == 0 {
		return nil
	}
	remove := make(map[string]struct{}, len(nodeIDs))
	for _, nodeID := range nodeIDs {
		remove[nodeID] = struct{}{}
	}
	return withOwnershipLock(path, func() error {
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
	})
}
