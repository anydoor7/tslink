package server

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/registry"
	"github.com/anydoor7/tslink/internal/tailapi"
	"github.com/anydoor7/tslink/internal/testenv"
	"github.com/anydoor7/tslink/internal/testenv/localapitest"
)

func funnelPolicySatisfied(context.Context, tailapi.FunnelPolicyRequest) (tailapi.PolicyMutationResult, error) {
	return tailapi.PolicyMutationResult{WriteOutcome: tailapi.PolicyWriteNotAttempted}, nil
}

// syncOnceWithFakeNode runs one fresh-process sync. The fake node stops at Up
// (after the identity is prepared and the node constructed) unless
// startsCleanly is set. It returns the tags each construction advertised.
func syncOnceWithFakeNode(t *testing.T, startsCleanly bool, cleanups *[][]tailapi.CleanupTarget) ([][]string, error) {
	t.Helper()
	var advertised [][]string
	oldNew := newTSNetServerFn
	newTSNetServerFn = func(svc registry.Service, _, _, _ string) tsnetServer {
		tags := append([]string(nil), svc.Tags...)
		sort.Strings(tags)
		advertised = append(advertised, tags)
		if startsCleanly {
			return &fakeTSNetServer{localClient: localapitest.NewClient(nil)}
		}
		return &fakeTSNetServer{upErr: errors.New("intentional fake Up stop")}
	}
	defer func() { newTSNetServerFn = oldNew }()
	s, err := New("key", "")
	if err != nil {
		t.Fatal(err)
	}
	s.SetEnsureFunnelAttrFn(funnelPolicySatisfied)
	s.SetCleanupStaleNodesFn(func(_ context.Context, targets []tailapi.CleanupTarget) (tailapi.CleanupResult, error) {
		*cleanups = append(*cleanups, targets)
		return tailapi.CleanupResult{}, nil
	})
	err = s.syncNodes(context.Background())
	s.closeAllNodes()
	return advertised, err
}

// tsnet ignores the auth key once state exists, so a node keeps whatever tags
// it enrolled with. Turning Funnel on or off changes the constructed tag set
// (tag:tslink-funnel is derived at construction, never stored in the registry
// by `tslink share`), so it must reset the enrollment like any tag change.
func TestNodeIdentityFunnelToggleResetsEnrollment(t *testing.T) {
	private := registry.Service{Name: "app", Type: registry.TypeProxy, Target: "http://localhost:3000", Tags: []string{"tag:web"}}
	public := private
	public.Funnel = true
	public.PublicAck = true // the registry shape `tslink share --funnel --yes` writes
	for _, tc := range []struct {
		name          string
		before, after registry.Service
		oldTags       string
	}{
		{name: "private to public", before: private, after: public, oldTags: "tag:web"},
		{name: "public to private", before: public, after: private, oldTags: registry.FunnelTag + ",tag:web"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testenv.SetHome(t, t.TempDir())
			if err := config.EnsureDir(); err != nil {
				t.Fatal(err)
			}
			writeRegistry(t, []registry.Service{tc.before})
			var cleanups [][]tailapi.CleanupTarget
			// First run prepares and records the identity; its Up may stop.
			_, _ = syncOnceWithFakeNode(t, !tc.before.Funnel, &cleanups)
			marker := filepath.Join(config.NodesDirIn(mustConfigDir(t)), "app", "tailscaled.state")
			if err := os.MkdirAll(filepath.Dir(marker), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(marker, []byte("enrolled-before"), 0o600); err != nil {
				t.Fatal(err)
			}
			cleanups = nil

			writeRegistry(t, []registry.Service{tc.after})
			advertised, _ := syncOnceWithFakeNode(t, !tc.after.Funnel, &cleanups)
			if len(advertised) != 1 {
				t.Fatalf("constructions = %v, want 1", advertised)
			}
			if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("%s reused the old enrollment (advertised %v): stat=%v", tc.name, advertised[0], err)
			}
			if len(cleanups) != 1 || len(cleanups[0]) != 1 || strings.Join(sortedCopy(cleanups[0][0].Tags), ",") != tc.oldTags {
				t.Fatalf("stale-node cleanup = %+v, want one target with the old tags %s", cleanups, tc.oldTags)
			}
			path := filepath.Join(mustConfigDir(t), "node-identities", "app.json")
			recorded, found, err := readNodeIdentity(path)
			if err != nil || !found || strings.Join(recorded.Tags, ",") != strings.Join(advertised[0], ",") {
				t.Fatalf("recorded tags = %v (found=%v err=%v), want the constructed tags %v", recorded.Tags, found, err, advertised[0])
			}
		})
	}
}

func sortedCopy(values []string) []string {
	out := append([]string(nil), values...)
	sort.Strings(out)
	return out
}

// An existing public node adopted at upgrade records the tags it is built
// with, so the next start sees no change and keeps the enrollment.
func TestNodeIdentityLegacyPublicAdoptionDoesNotChurn(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatal(err)
	}
	public := registry.Service{Name: "app", Type: registry.TypeProxy, Target: "http://localhost:3000", Tags: []string{"tag:web"}, Funnel: true, PublicAck: true}
	writeRegistry(t, []registry.Service{public})
	marker := filepath.Join(config.NodesDirIn(mustConfigDir(t)), "app", "tailscaled.state")
	if err := os.MkdirAll(filepath.Dir(marker), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte("enrolled-public"), 0o600); err != nil {
		t.Fatal(err)
	}
	var cleanups [][]tailapi.CleanupTarget
	for start := 1; start <= 2; start++ {
		advertised, _ := syncOnceWithFakeNode(t, false, &cleanups)
		if len(advertised) != 1 || !containsString(advertised[0], registry.FunnelTag) {
			t.Fatalf("start %d constructed %v, want one public node with %s", start, advertised, registry.FunnelTag)
		}
		if data, err := os.ReadFile(marker); err != nil || string(data) != "enrolled-public" {
			t.Fatalf("start %d churned the existing public enrollment: %q err=%v", start, data, err)
		}
		if len(cleanups) != 0 {
			t.Fatalf("start %d requested stale-node cleanup: %+v", start, cleanups)
		}
	}
	recorded, found, err := readNodeIdentity(filepath.Join(mustConfigDir(t), "node-identities", "app.json"))
	if err != nil || !found || recorded.Origin != identityLegacyAdopted || !containsString(recorded.Tags, registry.FunnelTag) {
		t.Fatalf("legacy public record = %+v found=%v err=%v, want adopted constructed tags", recorded, found, err)
	}
}
