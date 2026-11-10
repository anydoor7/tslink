package registry

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/anydoor7/tslink/internal/errcode"
)

// StartupPriorityFile holds the operator's start-order hints, next to
// registry.json. It is deliberately not a registry.json or config.json field:
// both are decoded strictly, so a TSLink build that predates the hint would
// refuse an entry carrying it (unknown_config_key) and stop serving that
// service, which would make rolling back to such a build an outage. Builds
// that predate the hint never open this file and simply start nodes in
// registry order.
const StartupPriorityFile = "startup-priority.json"

const startupPrioritySchemaVersion = 1

type startupPriorityWire struct {
	SchemaVersion int            `json:"schema_version"`
	Services      map[string]int `json:"services"`
}

// StartupPriorityPath returns the hint file beside the registry at regPath.
func StartupPriorityPath(regPath string) string {
	return filepath.Join(filepath.Dir(regPath), StartupPriorityFile)
}

// LoadStartupPriorities returns the start priority recorded per service name.
// A missing file records none. Names that are no longer registered are kept;
// StartupOrder ignores them.
func LoadStartupPriorities(regPath string) (map[string]int, error) {
	path := StartupPriorityPath(regPath)
	if err := convergeRegistryFile(path); err != nil {
		return nil, err
	}
	data, err := readRegistryBytes(path)
	if os.IsNotExist(err) {
		return map[string]int{}, nil
	}
	if err != nil {
		return nil, err
	}
	var wire startupPriorityWire
	if err := strictJSONDecode(data, &wire); err != nil {
		return nil, fmt.Errorf("load %s: %w", path, err)
	}
	if wire.SchemaVersion != startupPrioritySchemaVersion {
		return nil, fmt.Errorf("load %s: unsupported schema_version %d", path, wire.SchemaVersion)
	}
	if wire.Services == nil {
		wire.Services = map[string]int{}
	}
	return wire.Services, nil
}

// SetStartupPriority records the start priority of one registered service
// under the registry lock. Priority 0 is the default and removes the entry;
// removing is allowed for a name that is no longer registered.
func SetStartupPriority(ctx context.Context, regPath, name string, priority int) error {
	return withLockContext(ctx, regPath, func() error {
		if priority != 0 {
			reg, issues, err := LoadForRuntime(regPath)
			if err != nil {
				return err
			}
			registered := slices.ContainsFunc(reg.Services, func(s Service) bool { return s.Name == name }) ||
				slices.ContainsFunc(issues, func(i ServiceIssue) bool { return i.Name == name })
			if !registered {
				return CodedError{Code: errcode.NotFound, Message: "service not found: " + name}
			}
		}
		priorities, err := LoadStartupPriorities(regPath)
		if err != nil {
			return err
		}
		if priority == 0 {
			delete(priorities, name)
		} else {
			priorities[name] = priority
		}
		data, err := json.MarshalIndent(startupPriorityWire{SchemaVersion: startupPrioritySchemaVersion, Services: priorities}, "", "  ")
		if err != nil {
			return err
		}
		return writeRegistryFile(StartupPriorityPath(regPath), append(data, '\n'))
	})
}

// StartupOrder returns names ordered for node start: higher priority first,
// registry order kept among equal priorities. Unlisted names have priority 0.
func StartupOrder(names []string, priorities map[string]int) []string {
	ordered := slices.Clone(names)
	slices.SortStableFunc(ordered, func(a, b string) int { return cmp.Compare(priorities[b], priorities[a]) })
	return ordered
}
