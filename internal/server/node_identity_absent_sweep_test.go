package server

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/registry"
	"github.com/anydoor7/tslink/internal/tailapi"
	"github.com/anydoor7/tslink/internal/testenv"
	"github.com/anydoor7/tslink/internal/testenv/localapitest"
)

// identityProbe counts every destructive or remote step an identity
// transition can take, so a test can assert that none happened.
type identityProbe struct {
	mu         sync.Mutex
	removed    []string
	cleanups   [][]tailapi.CleanupTarget
	constructs []string // name|stateDir|tags
}

func (p *identityProbe) removedNames() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.removed...)
}

func (p *identityProbe) cleanupCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.cleanups)
}

func instrumentIdentity(t *testing.T) *identityProbe {
	t.Helper()
	p := &identityProbe{}
	oldRemove := removeServiceStateDirFn
	removeServiceStateDirFn = func(cfgDir, name string) error {
		p.mu.Lock()
		p.removed = append(p.removed, name)
		p.mu.Unlock()
		return oldRemove(cfgDir, name)
	}
	oldNew := newTSNetServerFn
	newTSNetServerFn = func(svc registry.Service, stateDir, _, _ string) tsnetServer {
		tags := append([]string(nil), svc.Tags...)
		sort.Strings(tags)
		p.mu.Lock()
		p.constructs = append(p.constructs, svc.Name+"|"+stateDir+"|"+joinTags(tags))
		p.mu.Unlock()
		return &fakeTSNetServer{localClient: localapitest.NewClient(nil)}
	}
	t.Cleanup(func() {
		removeServiceStateDirFn = oldRemove
		newTSNetServerFn = oldNew
	})
	return p
}

func joinTags(tags []string) string {
	out := ""
	for i, tag := range tags {
		if i > 0 {
			out += ","
		}
		out += tag
	}
	return out
}

func newIdentityProbeServer(t *testing.T, controlURL string, p *identityProbe) *Server {
	t.Helper()
	s, err := New("key", controlURL)
	if err != nil {
		t.Fatal(err)
	}
	s.SetCleanupStaleNodesFn(func(_ context.Context, targets []tailapi.CleanupTarget) (tailapi.CleanupResult, error) {
		p.mu.Lock()
		p.cleanups = append(p.cleanups, targets)
		p.mu.Unlock()
		return tailapi.CleanupResult{}, nil
	})
	return s
}

// legacyLayoutServices is the 2baf2b1-era shape: a file share, a tagged proxy
// with duplicate unsorted tags, and an ephemeral proxy.
func legacyLayoutServices(t *testing.T) []registry.Service {
	t.Helper()
	return []registry.Service{
		{Name: "files", Type: registry.TypeFile, Path: t.TempDir()},
		{Name: "api", Type: registry.TypeProxy, Target: "http://localhost:3000", Tags: []string{"tag:web", "tag:api", "tag:web"}},
		{Name: "eph", Type: registry.TypeProxy, Target: "http://localhost:3001", Tags: []string{"tag:web"}, Ephemeral: true},
	}
}

// writeLegacyLayout creates a config directory as a pre-identity-record build
// left it: a registry and one enrolled state directory per service, and no
// node-identities directory. It returns each service's state marker.
func writeLegacyLayout(t *testing.T, services []registry.Service) map[string]string {
	t.Helper()
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatal(err)
	}
	writeRegistry(t, services)
	nodesDir, err := config.NodesDir()
	if err != nil {
		t.Fatal(err)
	}
	markers := make(map[string]string, len(services))
	for _, svc := range services {
		dir := filepath.Join(nodesDir, svc.Name)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		marker := filepath.Join(dir, "tailscaled.state")
		if err := os.WriteFile(marker, []byte("enrolled-"+svc.Name), 0o600); err != nil {
			t.Fatal(err)
		}
		markers[svc.Name] = marker
	}
	return markers
}

func assertStateIntact(t *testing.T, markers map[string]string, names ...string) {
	t.Helper()
	for _, name := range names {
		data, err := os.ReadFile(markers[name])
		if err != nil || string(data) != "enrolled-"+name {
			t.Fatalf("node state for %q lost: data=%q err=%v", name, data, err)
		}
	}
}

func assertRecordPresent(t *testing.T, s *Server, names ...string) {
	t.Helper()
	for _, name := range names {
		path, err := s.nodeIdentityPath(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("identity record for %q lost: %v", name, err)
		}
	}
}

// startLegacyLayoutOnce runs one daemon start over the legacy layout so every
// service has an identity record, then shuts down as a daemon stop would.
func startLegacyLayoutOnce(t *testing.T, p *identityProbe) {
	t.Helper()
	s := newIdentityProbeServer(t, "", p)
	if err := s.syncNodesAuthoritative(context.Background()); err != nil {
		t.Fatalf("first start: %v", err)
	}
	s.closeAllNodes()
	if removed := p.removedNames(); len(removed) != 0 {
		t.Fatalf("first start removed state: %v", removed)
	}
}

// A registry that is absent or blank at daemon start carries no evidence that
// any service was removed. Every node keeps its state and its record.
func TestNodeIdentityUntrustedRegistryAtStartKeepsEveryIdentity(t *testing.T) {
	for _, tc := range []struct {
		name    string
		corrupt func(t *testing.T, path string)
	}{
		{name: "missing", corrupt: func(t *testing.T, path string) {
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "empty", corrupt: func(t *testing.T, path string) {
			if err := os.WriteFile(path, nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			services := legacyLayoutServices(t)
			markers := writeLegacyLayout(t, services)
			p := instrumentIdentity(t)
			startLegacyLayoutOnce(t, p)
			regPath, err := config.RegistryPath()
			if err != nil {
				t.Fatal(err)
			}
			tc.corrupt(t, regPath)

			s := newIdentityProbeServer(t, "", p)
			err = s.syncNodesAuthoritative(context.Background())
			s.closeAllNodes()
			t.Logf("start with %s registry: sync err=%v", tc.name, err)
			if removed := p.removedNames(); len(removed) != 0 || p.cleanupCount() != 0 {
				t.Fatalf("%s registry at start reset identities: removed=%v cleanups=%d", tc.name, removed, p.cleanupCount())
			}
			assertStateIntact(t, markers, "files", "api", "eph")
			assertRecordPresent(t, s, "files", "api", "eph")
		})
	}
}

// `tslink remove` while no daemon runs keeps node state whenever it cannot
// confirm the remote side. The next daemon start must not delete it either:
// absence from the registry is not proof that no tailnet node uses the key.
func TestNodeIdentityServiceRemovedWhileDownKeepsStateWithoutOwnershipProof(t *testing.T) {
	services := legacyLayoutServices(t)
	markers := writeLegacyLayout(t, services)
	p := instrumentIdentity(t)
	startLegacyLayoutOnce(t, p)
	writeRegistry(t, services[:1])

	s := newIdentityProbeServer(t, "", p)
	if err := s.syncNodesAuthoritative(context.Background()); err != nil {
		t.Fatalf("start after removal while down: %v", err)
	}
	s.closeAllNodes()
	if removed := p.removedNames(); len(removed) != 0 || p.cleanupCount() != 0 {
		t.Fatalf("removed services lost state without remote proof: removed=%v cleanups=%d", removed, p.cleanupCount())
	}
	assertStateIntact(t, markers, "files", "api", "eph")
	assertRecordPresent(t, s, "api", "eph")
}

// Once whatever held the proof (tslink remove, or the lifecycle reconciler's
// ledger-proven cleanup) has deleted a removed service's state, its record
// describes nothing and is pruned, but only against a registry file that is
// present and valid.
func TestNodeIdentityAbsentServicePrunesRecordOnlyAfterStateIsGone(t *testing.T) {
	for _, tc := range []struct {
		name            string
		deleteRegistry  bool
		wantRecordAfter bool
	}{
		{name: "valid registry", wantRecordAfter: false},
		{name: "missing registry", deleteRegistry: true, wantRecordAfter: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			services := legacyLayoutServices(t)
			markers := writeLegacyLayout(t, services)
			p := instrumentIdentity(t)
			startLegacyLayoutOnce(t, p)
			writeRegistry(t, services[:1])
			if err := os.RemoveAll(filepath.Dir(markers["api"])); err != nil {
				t.Fatal(err)
			}
			if tc.deleteRegistry {
				regPath, err := config.RegistryPath()
				if err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(regPath); err != nil {
					t.Fatal(err)
				}
			}

			s := newIdentityProbeServer(t, "", p)
			if err := s.syncNodesAuthoritative(context.Background()); err != nil {
				t.Fatalf("sync: %v", err)
			}
			s.closeAllNodes()
			path, err := s.nodeIdentityPath("api")
			if err != nil {
				t.Fatal(err)
			}
			_, statErr := os.Stat(path)
			if gotRecord := statErr == nil; gotRecord != tc.wantRecordAfter {
				t.Fatalf("api record present = %v (stat %v), want %v", gotRecord, statErr, tc.wantRecordAfter)
			}
			if !tc.wantRecordAfter && !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("api record stat = %v, want not-exist", statErr)
			}
			if removed := p.removedNames(); len(removed) != 0 {
				t.Fatalf("record pruning removed state: %v", removed)
			}
			assertStateIntact(t, markers, "eph")
			assertRecordPresent(t, s, "eph")
		})
	}
}
