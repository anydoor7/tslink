package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/monody0007/tslink/internal/atomicfile"
	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/registry"
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

func (s *Server) nodeIdentityPath(name string) (string, error) {
	if err := registry.ValidateName(name); err != nil {
		return "", fmt.Errorf("node identity service name: %w", err)
	}
	return filepath.Join(s.cfgDir, "node-identities", name+".json"), nil
}

func requestedNodeIdentity(svc registry.Service, fallbackControlURL, origin string) nodeIdentity {
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
	return a.Service == b.Service && a.Ephemeral == b.Ephemeral &&
		a.ControlURL == b.ControlURL && sameStringSet(a.Tags, b.Tags)
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
		if err != nil {
			failures[name] = fmt.Errorf("read identity for running service %q: %w", name, err)
			continue
		}
		requested := requestedNodeIdentity(node.service, s.controlURL, identityPreparedBeforeUp)
		if found {
			if existing.Service != name || !existing.sameAuthIdentity(requested) {
				failures[name] = fmt.Errorf("running service %q disagrees with durable node identity", name)
			}
			continue
		}
		if err := writeNodeIdentityFn(path, requested); err != nil {
			failures[name] = fmt.Errorf("record identity for running service %q: %w", name, err)
		}
	}
	return failures
}

// removeAbsentNodeIdentities finishes removal for services whose listener was
// already withdrawn in a previous sync or process. The record remains until
// state removal is verified, so an interrupted deletion is retried safely.
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
	var errs []error
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".json")
		if _, exists := desired[name]; exists {
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
			errs = append(errs, fmt.Errorf("read removed service identity %q: %w", name, err))
			continue
		}
		if err := removeServiceStateDirFn(name); err != nil {
			errs = append(errs, fmt.Errorf("remove state for deleted service %q: %w", name, err))
			continue
		}
		stateDir := filepath.Join(config.NodesDirIn(s.cfgDir), name)
		if _, err := os.Lstat(stateDir); err == nil {
			errs = append(errs, fmt.Errorf("remove state for deleted service %q: state directory remains", name))
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, fmt.Errorf("verify state removal for deleted service %q: %w", name, err))
			continue
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, fmt.Errorf("remove identity for deleted service %q: %w", name, err))
		}
	}
	return errors.Join(errs...)
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
	if err != nil {
		return nil, fmt.Errorf("read node identity for %q: %w", svc.Name, err)
	}
	stateDir := filepath.Join(config.NodesDirIn(s.cfgDir), svc.Name)
	requested := requestedNodeIdentity(svc, s.controlURL, identityPreparedBeforeUp)
	if !found {
		if _, err := os.Lstat(stateDir); err == nil {
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
