package server

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/registry"
	"tailscale.com/tsnet"
)

// Upgrade from a build that predates node identity records (2baf2b1 shape: a
// registry and nodes/<name>/ state, no node-identities/). The first and the
// second start must keep every node's state, construct each node over that
// same state directory with its own hostname, make no remote cleanup call,
// and adopt the existing enrollment as an unverified legacy identity.
func TestNodeIdentityUpgradeFromLegacyLayoutKeepsEveryIdentity(t *testing.T) {
	services := legacyLayoutServices(t)
	markers := writeLegacyLayout(t, services)
	p := instrumentIdentity(t)
	type construction struct{ hostname, dir string }
	var constructed []construction
	fake := newTSNetServerFn
	newTSNetServerFn = func(svc registry.Service, stateDir, authKey, controlURL string) tsnetServer {
		// Build the real tsnet.Server value (a plain struct; nothing starts)
		// to read the hostname and state directory production would use.
		built := newTSNetServer(svc, stateDir, authKey, controlURL).(*tsnet.Server)
		constructed = append(constructed, construction{hostname: built.Hostname, dir: built.Dir})
		return fake(svc, stateDir, authKey, controlURL)
	}
	nodesDir, err := config.NodesDir()
	if err != nil {
		t.Fatal(err)
	}
	identitiesDir := filepath.Join(mustConfigDir(t), "node-identities")
	if _, err := os.Stat(identitiesDir); !os.IsNotExist(err) {
		t.Fatalf("fixture is not a legacy layout: node-identities stat = %v", err)
	}

	for round := 1; round <= 2; round++ {
		constructed = nil
		s := newIdentityProbeServer(t, "", p)
		if err := s.syncNodesAuthoritative(context.Background()); err != nil {
			t.Fatalf("start %d: %v", round, err)
		}
		s.closeAllNodes()
		if removed := p.removedNames(); len(removed) != 0 || p.cleanupCount() != 0 {
			t.Fatalf("start %d reset identity: removed=%v cleanups=%d", round, removed, p.cleanupCount())
		}
		assertStateIntact(t, markers, "files", "api", "eph")
		if len(constructed) != len(services) {
			t.Fatalf("start %d constructed %d nodes, want %d", round, len(constructed), len(services))
		}
		for _, c := range constructed {
			if c.dir != filepath.Join(nodesDir, c.hostname) {
				t.Fatalf("start %d: node %q constructed over %q, want its existing state directory", round, c.hostname, c.dir)
			}
			if _, ok := markers[c.hostname]; !ok {
				t.Fatalf("start %d: unexpected hostname %q (a suffixed or renamed node)", round, c.hostname)
			}
		}
		for _, svc := range services {
			path, err := s.nodeIdentityPath(svc.Name)
			if err != nil {
				t.Fatal(err)
			}
			recorded, found, err := readNodeIdentity(path)
			if err != nil || !found || recorded.Origin != identityLegacyAdopted || recorded.Service != svc.Name {
				t.Fatalf("start %d: %s record = %+v found=%v err=%v, want legacy adoption", round, svc.Name, recorded, found, err)
			}
			if runtime.GOOS != "windows" {
				info, err := os.Stat(path)
				if err != nil || info.Mode().Perm() != 0o600 {
					t.Fatalf("start %d: %s record mode = %v err=%v, want 0600", round, svc.Name, info.Mode().Perm(), err)
				}
			}
		}
		if runtime.GOOS != "windows" {
			info, err := os.Stat(identitiesDir)
			if err != nil || info.Mode().Perm() != 0o700 {
				t.Fatalf("start %d: node-identities mode = %v err=%v, want 0700", round, info.Mode().Perm(), err)
			}
		}
	}
	api, _, err := readNodeIdentity(filepath.Join(identitiesDir, "api.json"))
	if err != nil || joinTags(api.Tags) != "tag:api,tag:web" {
		t.Fatalf("api record tags = %v err=%v, want normalized [tag:api tag:web]", api.Tags, err)
	}
}
