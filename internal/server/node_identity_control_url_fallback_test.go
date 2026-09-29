package server

import (
	"context"
	"os"
	"testing"

	"github.com/monody0007/tslink/internal/registry"
)

// `serve` falls back to the default control URL when config.json fails to
// load. Such a start must not reset any node whose recorded identity differs
// from the fallback only in its control URL. A configured URL change, or a
// real identity change under the fallback, still resets.
func TestNodeIdentityUnverifiedControlURLDoesNotResetIdentity(t *testing.T) {
	const headscale = "https://headscale.example.com"
	for _, tc := range []struct {
		name        string
		unverified  bool
		changeTags  bool
		wantResetOf []string
	}{
		{name: "unverified fallback keeps every node", unverified: true},
		{name: "configured URL change resets every node", unverified: false, wantResetOf: []string{"files", "api", "eph"}},
		{name: "tag change under unverified fallback still resets", unverified: true, changeTags: true, wantResetOf: []string{"api"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			services := legacyLayoutServices(t)
			markers := writeLegacyLayout(t, services)
			p := instrumentIdentity(t)
			first := newIdentityProbeServer(t, headscale, p)
			if err := first.syncNodesAuthoritative(context.Background()); err != nil {
				t.Fatal(err)
			}
			first.closeAllNodes()
			if tc.changeTags {
				changed := append([]registry.Service(nil), services...)
				changed[1].Tags = []string{"tag:other"}
				writeRegistry(t, changed)
			}

			second := newIdentityProbeServer(t, "", p)
			second.SetControlURLUnverified(tc.unverified)
			if err := second.syncNodesAuthoritative(context.Background()); err != nil {
				t.Fatalf("start with fallback control URL: %v", err)
			}
			second.closeAllNodes()
			removed := p.removedNames()
			if joinTags(sortedCopy(removed)) != joinTags(sortedCopy(tc.wantResetOf)) {
				t.Fatalf("reset nodes = %v, want %v", removed, tc.wantResetOf)
			}
			for _, name := range []string{"files", "api", "eph"} {
				reset := containsString(tc.wantResetOf, name)
				if _, err := os.Stat(markers[name]); (err == nil) == reset {
					t.Fatalf("%s state present=%v, want reset=%v", name, err == nil, reset)
				}
				if reset {
					continue
				}
				path, _ := second.nodeIdentityPath(name)
				recorded, found, err := readNodeIdentity(path)
				if err != nil || !found || recorded.ControlURL != headscale {
					t.Fatalf("%s record = %+v found=%v err=%v, want the configured control URL kept", name, recorded, found, err)
				}
			}
		})
	}
}

// Existing state without a record, first seen by a start whose control URL is
// only the fallback, is adopted without recording that fallback; a later start
// with the configured URL then adopts it for real instead of resetting it.
func TestNodeIdentityUnverifiedControlURLDoesNotRecordLegacyState(t *testing.T) {
	services := legacyLayoutServices(t)
	markers := writeLegacyLayout(t, services)
	p := instrumentIdentity(t)
	fallback := newIdentityProbeServer(t, "", p)
	fallback.SetControlURLUnverified(true)
	for round := 0; round < 2; round++ { // the second sync exercises the running-node path
		if err := fallback.syncNodes(context.Background()); err != nil {
			t.Fatalf("fallback sync %d: %v", round, err)
		}
	}
	fallback.closeAllNodes()
	for _, svc := range services {
		path, _ := fallback.nodeIdentityPath(svc.Name)
		if _, found, err := readNodeIdentity(path); err != nil || found {
			t.Fatalf("%s recorded under an unverified control URL: found=%v err=%v", svc.Name, found, err)
		}
	}

	configured := newIdentityProbeServer(t, "https://headscale.example.com", p)
	if err := configured.syncNodesAuthoritative(context.Background()); err != nil {
		t.Fatal(err)
	}
	configured.closeAllNodes()
	if removed := p.removedNames(); len(removed) != 0 || p.cleanupCount() != 0 {
		t.Fatalf("configured start reset legacy state: removed=%v cleanups=%d", removed, p.cleanupCount())
	}
	assertStateIntact(t, markers, "files", "api", "eph")
}
