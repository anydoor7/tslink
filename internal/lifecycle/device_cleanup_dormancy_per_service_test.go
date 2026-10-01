package lifecycle

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/registry"
	tsruntime "github.com/anydoor7/tslink/internal/runtime"
	"github.com/anydoor7/tslink/internal/tailapi"
	"github.com/anydoor7/tslink/internal/testenv"
	tailscale "tailscale.com/client/tailscale/v2"
)

// One ownership record without retired_at, for a service absent from the
// registry, says nothing about any other service: that service's device stays,
// and a service `tslink remove` retired is still deleted by its exact NodeID.
func TestReconcileDormancyIsPerService(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	fake := testenv.NewStatefulTailnet(t)
	t.Setenv(tailapi.APIBaseURLEnv, fake.URL())
	t.Setenv("TSLINK_API_KEY", "test-placeholder")
	fake.SetDevices([]tailscale.Device{
		{ID: "id-keep", NodeID: "n-keep", Hostname: "keep", Tags: []string{"tag:tsmain"}},
		{ID: "id-removed", NodeID: "n-removed", Hostname: "removed", Tags: []string{"tag:tsmain"}},
		{ID: "id-handedited", NodeID: "n-handedited", Hostname: "handedited", Tags: []string{"tag:tsmain"}},
	})
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	ownershipPath := filepath.Join(dir, "node-ownership.json")
	now := time.Date(2030, 8, 31, 12, 0, 0, 0, time.UTC)
	if _, err := registry.Add(regPath, registry.Service{Name: "keep", Type: registry.TypeProxy, Target: "http://localhost:3000"}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"keep", "removed", "handedited"} {
		if err := tsruntime.RecordOwnedNode(ownershipPath, name, "n-"+name, now); err != nil {
			t.Fatal(err)
		}
	}
	// What `tslink remove` records; the hand-edited service was never retired.
	if err := tsruntime.MarkOwnedNodeIDsRetired(ownershipPath, []string{"n-removed"}, now); err != nil {
		t.Fatal(err)
	}

	result, err := Reconcile(context.Background(), Options{RegistryPath: regPath, OwnershipPath: ownershipPath, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(result.DevicesDeleted, ","); got != "removed" {
		t.Fatalf("devices_deleted = %q, want the retired service's device; result = %+v", got, result)
	}
	if got := strings.Join(result.DeviceSkipUnknownProvenance, ","); got != "handedited" {
		t.Fatalf("device_skip_unknown_provenance = %q, want handedited", got)
	}
	if !result.DeviceCleanupSkipped || !strings.Contains(result.DeviceSkipReason, "handedited") {
		t.Fatalf("result = %+v, want the withheld service reported", result)
	}
	var remaining []string
	for _, device := range fake.Devices() {
		remaining = append(remaining, device.NodeID)
	}
	if got := strings.Join(remaining, ","); got != "n-keep,n-handedited" {
		t.Fatalf("remaining devices = %q, want keep and handedited", got)
	}
	ledger, err := tsruntime.LoadOwnership(ownershipPath)
	if err != nil {
		t.Fatal(err)
	}
	var rows []string
	for _, node := range ledger.Nodes {
		rows = append(rows, node.NodeID)
	}
	if got := strings.Join(rows, ","); got != "n-handedited,n-keep" {
		t.Fatalf("ledger rows = %q, want the retired row forgotten and the others kept", got)
	}
}

// The fixture of the former global-freeze test, with the per-service answer:
// the orphan an explicit `cleanup --adopt` reviewed and retired is deleted,
// the unexplained one is withheld and named.
func TestReconcileAdoptedOrphanIsDeletedBesideAnUnretiredOne(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	ownershipPath := filepath.Join(dir, "node-ownership.json")
	now := time.Date(2030, 8, 31, 12, 0, 0, 0, time.UTC)
	if _, err := registry.Add(regPath, registry.Service{Name: "active", Type: registry.TypeProxy, Target: "http://localhost:3000"}); err != nil {
		t.Fatal(err)
	}
	if err := tsruntime.AdoptOwnedNode(ownershipPath, "reviewed-x", "node-x", now, true); err != nil {
		t.Fatal(err)
	}
	if err := tsruntime.RecordOwnedNode(ownershipPath, "unknown-y", "node-y", now); err != nil {
		t.Fatal(err)
	}
	oldCleanup := cleanupDevicesFn
	t.Cleanup(func() { cleanupDevicesFn = oldCleanup })
	var gotTargets []tailapi.CleanupTarget
	cleanupDevicesFn = func(_ context.Context, targets []tailapi.CleanupTarget, _ bool) (tailapi.CleanupResult, error) {
		gotTargets = append([]tailapi.CleanupTarget(nil), targets...)
		return tailapi.CleanupResult{Deleted: []string{"reviewed-x"}, ResolvedOwnershipIDs: []string{"node-x"}}, nil
	}
	result, err := Reconcile(context.Background(), Options{RegistryPath: regPath, OwnershipPath: ownershipPath, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if len(gotTargets) != 1 || gotTargets[0].Hostname != "reviewed-x" || strings.Join(gotTargets[0].NodeIDs, ",") != "node-x" {
		t.Fatalf("cleanup targets = %+v, want only reviewed-x by its exact NodeID", gotTargets)
	}
	if strings.Join(result.DevicesDeleted, ",") != "reviewed-x" || strings.Join(result.DeviceSkipUnknownProvenance, ",") != "unknown-y" || !strings.Contains(result.DeviceSkipReason, "unknown-y") {
		t.Fatalf("result = %+v, want reviewed-x deleted and unknown-y withheld", result)
	}
}
