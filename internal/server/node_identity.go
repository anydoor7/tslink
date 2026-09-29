package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/monody0007/tslink/internal/atomicfile"
	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/registry"
	runtimesnapshot "github.com/monody0007/tslink/internal/runtime"
	"github.com/monody0007/tslink/internal/tailapi"
)

// A nodeIdentity records the requested auth identity used when a node was
// prepared to start. It is deliberately not proof of the identity the tailnet
// ultimately enrolled. In particular, a pre-existing legacy state cannot be
// inspected reliably for its previous ephemeral flag or control URL.
type nodeIdentity struct {
	Version    int      `json:"version"`
	Service    string   `json:"service"`
	Tags       []string `json:"tags"`
	Ephemeral  bool     `json:"ephemeral"`
	ControlURL string   `json:"control_url"`
	Origin     string   `json:"origin"`
}

const (
	nodeIdentityVersion      = 1
	identityPreparedBeforeUp = "requested_before_up"
	identityLegacyAdopted    = "legacy_adopted_unverified"
)

var (
	readNodeIdentityFn  = readNodeIdentity
	writeNodeIdentityFn = writeNodeIdentity
)

// errNodeIdentityUnknown marks a record this build cannot interpret but that
// is not damaged: a newer version, or a field added by a newer build. Its
// identity is unknown, so it is neither compared (no reset) nor rewritten.
var errNodeIdentityUnknown = errors.New("node identity record is from a newer TSLink or has fields this build does not know")

var nodeIdentityFields = map[string]struct{}{
	"version": {}, "service": {}, "tags": {}, "ephemeral": {}, "control_url": {}, "origin": {},
}

// nodeIdentityErrorCode is the stable code of a per-service identity record
// failure. It matches the code the ownership ledger uses for a local durable
// file that cannot be read safely, and no other serviceFailures entry uses it.
const nodeIdentityErrorCode = "internal_error"

// nodeIdentityReadError is a record that cannot be read safely. It fails only
// its own service: that service is not started or restarted from it, and
// neither its node state nor the record is changed.
type nodeIdentityReadError struct {
	service string
	path    string
	err     error
}

func (e *nodeIdentityReadError) Error() string {
	return fmt.Sprintf("cannot safely read node identity record %q for service %q: %v; the service is not started from it and its node state is kept", e.path, e.service, e.err)
}

func (e *nodeIdentityReadError) Unwrap() error { return e.err }

func (e *nodeIdentityReadError) StableCode() string { return nodeIdentityErrorCode }

func (e *nodeIdentityReadError) NextCommands() []string {
	return []string{
		fmt.Sprintf("Back up and inspect %q", e.path),
		fmt.Sprintf("Move %q aside only after preserving it; the next sync adopts the service's existing node state without resetting it", e.path),
		fmt.Sprintf("tslink status --urls --name %s --json", e.service),
	}
}

func nodeIdentityFailure(svc registry.Service, err error) runtimesnapshot.ServiceState {
	var next []string
	var recovery interface{ NextCommands() []string }
	if errors.As(err, &recovery) {
		next = recovery.NextCommands()
	}
	return runtimesnapshot.ServiceState{
		Service:      svc,
		RuntimeState: runtimesnapshot.ServiceRuntimeFailed,
		FunnelState:  funnelFailureState(svc, nodeIdentityErrorCode),
		Error: &runtimesnapshot.ServiceError{
			Code:    nodeIdentityErrorCode,
			Message: err.Error(),
			Next:    next,
		},
	}
}

func isNodeIdentityFailure(failure runtimesnapshot.ServiceState) bool {
	return failure.Error != nil && failure.Error.Code == nodeIdentityErrorCode
}

func (s *Server) nodeIdentityPath(name string) (string, error) {
	if err := registry.ValidateName(name); err != nil {
		return "", fmt.Errorf("node identity service name: %w", err)
	}
	return filepath.Join(s.cfgDir, "node-identities", name+".json"), nil
}

// requestedNodeIdentity describes the node as it is constructed, not as the
// registry stores it: tag:tslink-funnel is derived at construction for a
// public service, so turning Funnel on or off is an auth identity change. A
// Tier 1 node advertises no tags at all; prepareNodeIdentity compares it
// without them.
func requestedNodeIdentity(svc registry.Service, fallbackControlURL, origin string) nodeIdentity {
	svc = serviceForNodeConstruction(svc)
	tags := append([]string(nil), svc.Tags...)
	sort.Strings(tags)
	unique := tags[:0]
	for _, tag := range tags {
		if len(unique) == 0 || unique[len(unique)-1] != tag {
			unique = append(unique, tag)
		}
	}
	return nodeIdentity{
		Version: nodeIdentityVersion, Service: svc.Name, Tags: unique,
		Ephemeral: svc.Ephemeral, ControlURL: effectiveControlURL(svc, fallbackControlURL), Origin: origin,
	}
}

func (a nodeIdentity) sameAuthIdentity(b nodeIdentity) bool {
	return a.sameAuthIdentityExceptControlURL(b) && a.ControlURL == b.ControlURL
}

func (a nodeIdentity) sameAuthIdentityExceptControlURL(b nodeIdentity) bool {
	return a.Service == b.Service && a.Ephemeral == b.Ephemeral && sameStringSet(a.Tags, b.Tags)
}

// sameUntaggedIdentity compares identities as a Tier 1 node is built: without
// advertised tags, so only the service, the ephemeral flag and the control URL
// identify it. An unknown control URL is the unverified fallback, which never
// justifies a reset.
func (a nodeIdentity) sameUntaggedIdentity(b nodeIdentity, controlURLUnknown bool) bool {
	return a.Service == b.Service && a.Ephemeral == b.Ephemeral && (controlURLUnknown || a.ControlURL == b.ControlURL)
}

// controlURLUnknown reports whether svc's effective control URL is only the
// server's unverified fallback, which can never justify an identity reset.
func (s *Server) controlURLUnknown(svc registry.Service) bool {
	return s.controlURLUnverified && svc.ControlURL == ""
}

func readNodeIdentity(path string) (nodeIdentity, bool, error) {
	if err := atomicfile.ConvergePrivateFile(path); err != nil {
		return nodeIdentity{}, false, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nodeIdentity{}, false, nil
	}
	if err != nil {
		return nodeIdentity{}, false, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nodeIdentity{}, false, fmt.Errorf("decode node identity: %w", err)
	}
	var version int
	if raw, ok := fields["version"]; ok {
		if err := json.Unmarshal(raw, &version); err != nil {
			return nodeIdentity{}, false, fmt.Errorf("decode node identity version: %w", err)
		}
	}
	if version > nodeIdentityVersion {
		return nodeIdentity{}, true, fmt.Errorf("%w: version %d", errNodeIdentityUnknown, version)
	}
	// An added field is tolerated only on a record that claims this version;
	// without one the file is not a record any TSLink build wrote.
	for key := range fields {
		if _, known := nodeIdentityFields[key]; !known && version == nodeIdentityVersion {
			return nodeIdentity{}, true, fmt.Errorf("%w: field %q", errNodeIdentityUnknown, key)
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var identity nodeIdentity
	if err := decoder.Decode(&identity); err != nil {
		return nodeIdentity{}, false, fmt.Errorf("decode node identity: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nodeIdentity{}, false, fmt.Errorf("decode node identity: unexpected trailing data: %v", err)
	}
	if identity.Version != nodeIdentityVersion || identity.Origin != identityPreparedBeforeUp && identity.Origin != identityLegacyAdopted {
		return nodeIdentity{}, false, fmt.Errorf("unsupported or invalid node identity metadata")
	}
	if err := registry.ValidateName(identity.Service); err != nil {
		return nodeIdentity{}, false, fmt.Errorf("invalid node identity service: %w", err)
	}
	for _, tag := range identity.Tags {
		if err := registry.ValidateTag(tag); err != nil {
			return nodeIdentity{}, false, fmt.Errorf("invalid node identity tag: %w", err)
		}
	}
	if !sort.StringsAreSorted(identity.Tags) {
		return nodeIdentity{}, false, fmt.Errorf("node identity tags are not normalized")
	}
	for i := 1; i < len(identity.Tags); i++ {
		if identity.Tags[i] == identity.Tags[i-1] {
			return nodeIdentity{}, false, fmt.Errorf("node identity contains a duplicate tag")
		}
	}
	if err := registry.ValidateControlURL(identity.ControlURL); err != nil {
		return nodeIdentity{}, false, fmt.Errorf("invalid node identity control URL: %w", err)
	}
	return identity, true, nil
}

func writeNodeIdentity(path string, identity nodeIdentity) error {
	data, err := json.Marshal(identity)
	if err != nil {
		return err
	}
	return atomicfile.WriteFile(path, append(data, '\n'))
}

func (s *Server) recordRunningIdentitiesLocked() map[string]error {
	failures := make(map[string]error)
	for name, node := range s.nodes {
		path, err := s.nodeIdentityPath(name)
		if err != nil {
			failures[name] = err
			continue
		}
		existing, found, err := readNodeIdentityFn(path)
		if errors.Is(err, errNodeIdentityUnknown) {
			slog.Warn("keeping running node; its identity record is unknown to this build and is left unchanged", "name", name, "path", path, "reason", err)
			continue
		}
		if err != nil {
			failures[name] = &nodeIdentityReadError{service: name, path: path, err: err}
			continue
		}
		requested := requestedNodeIdentity(node.service, s.controlURL, identityPreparedBeforeUp)
		unknownURL := s.controlURLUnknown(node.service)
		if found {
			same := existing.sameAuthIdentity(requested) || unknownURL && existing.sameAuthIdentityExceptControlURL(requested)
			if existing.Service != name || !same {
				failures[name] = fmt.Errorf("running service %q disagrees with durable node identity", name)
			}
			continue
		}
		if unknownURL {
			// Recording the fallback would make a later start with the real
			// control URL look like a change and reset this node.
			continue
		}
		if err := writeNodeIdentityFn(path, requested); err != nil {
			failures[name] = fmt.Errorf("record identity for running service %q: %w", name, err)
		}
	}
	return failures
}

// removeAbsentNodeIdentities prunes the records of services absent from the
// registry once their node state is already gone. It never deletes node state
// itself. Absence from registry.json is not proof that no tailnet node still
// authenticates with that key: the file may be missing or blank, or the
// service may have been removed while no daemon ran and `tslink remove` could
// not confirm the remote side. State of a service that is not running here is
// deleted only by paths that hold remote ownership proof: `tslink remove` and
// the lifecycle reconciler, which require a valid registry file and resolved
// ownership-ledger NodeIDs. Until then the record stays with its state.
func (s *Server) removeAbsentNodeIdentities(desired map[string]registry.Service) error {
	dir := filepath.Join(s.cfgDir, "node-identities")
	info, err := os.Lstat(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect node identities: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("unsafe node identities directory %q", dir)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read node identities: %w", err)
	}
	var absent []string
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".json")
		if err := registry.ValidateName(name); err != nil {
			// Not a record this daemon wrote (for example a Finder copy or an
			// AppleDouble file). It names no service, so it is left alone.
			slog.Warn("ignoring a file in node-identities that is not a service identity record", "path", filepath.Join(dir, entry.Name()), "error", err)
			continue
		}
		if _, exists := desired[name]; !exists {
			absent = append(absent, name)
		}
	}
	if len(absent) == 0 {
		return nil
	}
	registered, trusted := s.trustedRegistryNames()
	if !trusted {
		slog.Warn("keeping node state and identity records for services absent from registry.json; the registry file is missing, blank, or unreadable", "services", absent)
		return nil
	}
	// Pruning is housekeeping for services no longer configured. A record it
	// cannot read or remove is kept and logged; it never fails the sync.
	for _, name := range absent {
		if _, exists := registered[name]; exists {
			continue
		}
		path, err := s.nodeIdentityPath(name)
		if err == nil {
			var identity nodeIdentity
			var found bool
			identity, found, err = readNodeIdentityFn(path)
			if err == nil && (!found || identity.Service != name) {
				err = fmt.Errorf("node identity record does not match service %q", name)
			}
		}
		if err != nil {
			slog.Warn("keeping the identity record of a removed service; it cannot be read safely", "service", name, "path", path, "error", err)
			continue
		}
		stateDir := filepath.Join(config.NodesDirIn(s.cfgDir), name)
		if _, err := os.Lstat(stateDir); err == nil {
			slog.Info("keeping node state and identity record for a removed service; no ownership proof has cleared its tailnet node", "service", name)
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			slog.Warn("keeping the identity record of a removed service; its state directory cannot be inspected", "service", name, "error", err)
			continue
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			slog.Warn("could not remove the identity record of a removed service", "service", name, "path", path, "error", err)
		}
	}
	return nil
}

// trustedRegistryNames re-reads registry.json with the same file-state rule
// the lifecycle reconciler uses before any deletion: only a present, valid
// file can say that a service was removed.
func (s *Server) trustedRegistryNames() (map[string]struct{}, bool) {
	regPath, err := registryPathFn()
	if err != nil {
		return nil, false
	}
	reg, state, err := registry.LoadWithFileState(regPath)
	if err != nil || state != registry.RegistryFileValid {
		return nil, false
	}
	names := make(map[string]struct{}, len(reg.Services))
	for _, svc := range reg.Services {
		names[svc.Name] = struct{}{}
	}
	return names, true
}

// prepareNodeIdentity is the sole transition from an old requested identity
// to a new one. The old record remains in place until the old tsnet state has
// been removed and its absence checked. The new record is written before Up,
// so a process crash after enrollment cannot mistake the new state for old.
// Remote cleanup remains best-effort after local state removal.
func (s *Server) prepareNodeIdentity(ctx context.Context, svc registry.Service) (cleanupErr error, err error) {
	path, err := s.nodeIdentityPath(svc.Name)
	if err != nil {
		return nil, err
	}
	old, found, err := readNodeIdentityFn(path)
	if errors.Is(err, errNodeIdentityUnknown) {
		slog.Warn("node identity record is unknown to this build; starting over the existing node state without a reset and leaving the record unchanged", "name", svc.Name, "path", path, "reason", err)
		return nil, nil
	}
	if err != nil {
		return nil, &nodeIdentityReadError{service: svc.Name, path: path, err: err}
	}
	stateDir := filepath.Join(config.NodesDirIn(s.cfgDir), svc.Name)
	requested := requestedNodeIdentity(svc, s.controlURL, identityPreparedBeforeUp)
	unknownURL := s.controlURLUnknown(svc)
	if !found {
		if _, err := os.Lstat(stateDir); err == nil {
			if unknownURL {
				// Existing state enrolled against a control server this run
				// cannot name. Adopt it without a record; a later start with a
				// loaded config records the real control URL.
				slog.Warn("not recording node identity for existing state; the control URL is an unverified fallback", "name", svc.Name)
				return nil, nil
			}
			// Compatibility baseline for state created before identity records
			// existed. This adopts the requested identity, not a verified old
			// enrollment. Subsequent transitions are tracked strictly.
			requested.Origin = identityLegacyAdopted
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("inspect legacy node state for %q: %w", svc.Name, err)
		}
		if err := writeNodeIdentityFn(path, requested); err != nil {
			return nil, fmt.Errorf("record node identity before start %q: %w", svc.Name, err)
		}
		return nil, nil
	}
	if old.Service != svc.Name {
		return nil, fmt.Errorf("node identity record for %q names %q", svc.Name, old.Service)
	}
	if old.sameAuthIdentity(requested) {
		return nil, nil
	}
	if unknownURL && old.sameAuthIdentityExceptControlURL(requested) {
		slog.Warn("keeping node state; its recorded control URL differs only from an unverified fallback", "name", svc.Name, "recorded_control_url", old.ControlURL)
		return nil, nil
	}
	if !s.credentialed && old.sameUntaggedIdentity(requested, unknownURL) {
		// A Tier 1 node enrolls interactively and advertises no tags
		// (newTSNetServer), so stored tags and Funnel never change the
		// identity it is built with. Keep its state, and record the requested
		// tags so a later Tier 2 start compares against the current registry.
		slog.Info("keeping interactive node state; tag and Funnel changes do not change an untagged Tier 1 node", "name", svc.Name, "recorded_tags", old.Tags, "requested_tags", requested.Tags)
		kept := old
		kept.Tags = requested.Tags
		if err := writeNodeIdentityFn(path, kept); err != nil {
			return nil, fmt.Errorf("record node identity before start %q: %w", svc.Name, err)
		}
		return nil, nil
	}
	if err := removeServiceStateDirFn(svc.Name); err != nil {
		return nil, fmt.Errorf("remove state for auth identity change %q: %w", svc.Name, err)
	}
	if _, err := os.Lstat(stateDir); err == nil {
		return nil, fmt.Errorf("remove state for auth identity change %q: state directory remains", svc.Name)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("verify state removal for auth identity change %q: %w", svc.Name, err)
	}
	cleanupErr = s.cleanupAuthIdentityNodes(ctx, []tailapi.CleanupTarget{
		tailapi.CleanupTargetForService(registry.Service{Name: old.Service, Tags: old.Tags}),
	})
	if err := writeNodeIdentityFn(path, requested); err != nil {
		return cleanupErr, fmt.Errorf("record replacement identity before start %q: %w", svc.Name, err)
	}
	return cleanupErr, nil
}
