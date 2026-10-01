package lifecycle

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anydoor7/tslink/internal/registry"
	"github.com/anydoor7/tslink/internal/tailapi"
	"github.com/anydoor7/tslink/internal/testenv"
	tailscale "tailscale.com/client/tailscale/v2"
)

// Local absence never proves that the canonical grant is unused across the
// tailnet. The zero-device control also prevents reintroducing a racy
// list-then-delete authorization path.
func TestReconcilePreservesSharedFunnelACLRegardlessOfRemoteSnapshot(t *testing.T) {
	for _, tc := range []struct {
		name    string
		devices []tailscale.Device
		dryRun  bool
	}{
		{name: "foreign offline Funnel device apply", devices: []tailscale.Device{{ID: "foreign-id", NodeID: "foreign-node", Hostname: "foreign", Tags: []string{registry.FunnelTag}}}},
		{name: "zero devices apply"},
		{name: "foreign offline Funnel device dry run", devices: []tailscale.Device{{ID: "foreign-id", NodeID: "foreign-node", Hostname: "foreign", Tags: []string{registry.FunnelTag}}}, dryRun: true},
		{name: "zero devices dry run", dryRun: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testenv.SetHome(t, t.TempDir())
			fake := testenv.NewStatefulTailnet(t)
			t.Setenv(tailapi.APIBaseURLEnv, fake.URL())
			t.Setenv("TSLINK_API_KEY", "test-placeholder")
			fake.SetDevices(tc.devices)
			fake.SetPolicy(`{"tagOwners":{"tag:tsmain":["autogroup:admin"],"tag:tslink-funnel":["tag:tsmain"]},"nodeAttrs":[{"target":["tag:tslink-funnel"],"attr":["funnel"]}]}`)
			before, etagBefore := fake.Policy()
			dir := t.TempDir()
			regPath := filepath.Join(dir, "registry.json")
			if _, err := registry.Add(regPath, registry.Service{Name: "private", Type: registry.TypeProxy, Target: "http://localhost:3000"}); err != nil {
				t.Fatal(err)
			}
			result, err := Reconcile(context.Background(), Options{RegistryPath: regPath, OwnershipPath: filepath.Join(dir, "ownership.json"), ManageACL: true, CheckUnusedACL: true, DryRun: tc.dryRun})
			if err != nil {
				t.Fatal(err)
			}
			after, etagAfter := fake.Policy()
			if result.ACLAction != ACLSkipped || !strings.Contains(strings.Join(result.Warnings, " "), "tailnet-wide nonuse") || after != before || etagAfter != etagBefore {
				t.Fatalf("shared grant changed or skip reason missing: result=%+v before=%q after=%q", result, before, after)
			}
			if len(fake.Requests()) != 0 {
				t.Fatalf("automatic ACL path queried or mutated remote policy: %+v", fake.Requests())
			}
		})
	}
}
