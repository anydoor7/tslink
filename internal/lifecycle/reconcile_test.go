package lifecycle

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/monody0007/tslink/internal/registry"
	tsruntime "github.com/monody0007/tslink/internal/runtime"
	"github.com/monody0007/tslink/internal/tailapi"
)

func saveLifecycleFixture(t *testing.T, now time.Time) (string, string) {
	t.Helper()
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	ownershipPath := filepath.Join(dir, "node-ownership.json")
	expires := now.Add(-time.Minute)
	if _, err := registry.Add(regPath, registry.Service{Name: "expired", Type: registry.TypeProxy, Target: "http://localhost:3000", Funnel: true, PublicAck: true, FunnelExpiresAt: &expires}); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Add(regPath, registry.Service{Name: "active", Type: registry.TypeProxy, Target: "http://localhost:4000"}); err != nil {
		t.Fatal(err)
	}
	if err := tsruntime.RecordOwnedNode(ownershipPath, "orphan", "node-orphan", now); err != nil {
		t.Fatal(err)
	}
	if err := tsruntime.RecordOwnedNode(ownershipPath, "active", "node-active", now); err != nil {
		t.Fatal(err)
	}
	return regPath, ownershipPath
}

func TestReconcileDryRunUsesWallClockWithoutMutation(t *testing.T) {
	now := time.Date(2030, 8, 31, 12, 0, 0, 0, time.UTC)
	regPath, ownershipPath := saveLifecycleFixture(t, now)
	oldCleanup, oldDelete := cleanupDevicesFn, deleteTagFn
	defer func() { cleanupDevicesFn, deleteTagFn = oldCleanup, oldDelete }()
	cleanupDevicesFn = func(_ context.Context, targets []tailapi.CleanupTarget, dryRun bool) (tailapi.CleanupResult, error) {
		if !dryRun || len(targets) != 1 || targets[0].Hostname != "orphan" || strings.Join(targets[0].NodeIDs, ",") != "node-orphan" {
			t.Fatalf("cleanup targets = %+v dryRun=%t", targets, dryRun)
		}
		return tailapi.CleanupResult{Matched: []string{"orphan"}, WouldDelete: []string{"orphan"}}, nil
	}
	deleteTagFn = func(context.Context, string) error {
		t.Fatal("dry-run called ACL mutation")
		return nil
	}

	result, err := Reconcile(context.Background(), Options{RegistryPath: regPath, OwnershipPath: ownershipPath, Now: now, DryRun: true, ManageACL: true, CheckUnusedACL: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(result.ExpiredFunnels, ",") != "expired" || result.ACLAction != ACLWouldDelete || strings.Join(result.DevicesWouldDelete, ",") != "orphan" {
		t.Fatalf("result = %+v", result)
	}
	reg, _ := registry.Load(regPath)
	if !reg.Services[0].Funnel {
		t.Fatal("dry-run downgraded registry")
	}
	ledger, _ := tsruntime.LoadOwnership(ownershipPath)
	if len(ledger.Nodes) != 2 {
		t.Fatal("dry-run consumed ownership proof")
	}
}

func TestReconcileApplyDowngradesDeletesOrphanAndRevokesLastACL(t *testing.T) {
	now := time.Date(2030, 8, 31, 12, 0, 0, 0, time.UTC)
	regPath, ownershipPath := saveLifecycleFixture(t, now)
	oldCleanup, oldDelete := cleanupDevicesFn, deleteTagFn
	defer func() { cleanupDevicesFn, deleteTagFn = oldCleanup, oldDelete }()
	cleanupDevicesFn = func(_ context.Context, targets []tailapi.CleanupTarget, dryRun bool) (tailapi.CleanupResult, error) {
		if dryRun || len(targets) != 1 || targets[0].Hostname != "orphan" {
			t.Fatalf("cleanup targets = %+v dryRun=%t", targets, dryRun)
		}
		return tailapi.CleanupResult{Matched: []string{"orphan"}, Deleted: []string{"orphan"}, ResolvedOwnershipIDs: []string{"node-orphan"}}, nil
	}
	aclCalls := 0
	deleteTagFn = func(_ context.Context, tag string) error {
		aclCalls++
		if tag != registry.FunnelTag {
			t.Fatalf("tag = %q", tag)
		}
		return nil
	}

	result, err := Reconcile(context.Background(), Options{RegistryPath: regPath, OwnershipPath: ownershipPath, Now: now, ManageACL: true})
	if err != nil {
		t.Fatal(err)
	}
	if !result.RegistryChanged || result.ACLAction != ACLDeleted || aclCalls != 1 || strings.Join(result.DevicesDeleted, ",") != "orphan" {
		t.Fatalf("result = %+v aclCalls=%d", result, aclCalls)
	}
	reg, _ := registry.Load(regPath)
	if len(reg.Services) != 2 || reg.Services[0].Funnel {
		t.Fatalf("registry = %+v, want service retained tailnet-only", reg.Services)
	}
	ledger, _ := tsruntime.LoadOwnership(ownershipPath)
	if len(ledger.Nodes) != 1 || ledger.Nodes[0].ServiceName != "active" {
		t.Fatalf("ledger = %+v, want active proof retained", ledger)
	}
}

func TestReconcileRemoteFailuresAreVisibleAndProofIsRetained(t *testing.T) {
	now := time.Date(2030, 8, 31, 12, 0, 0, 0, time.UTC)
	regPath, ownershipPath := saveLifecycleFixture(t, now)
	oldCleanup, oldDelete := cleanupDevicesFn, deleteTagFn
	defer func() { cleanupDevicesFn, deleteTagFn = oldCleanup, oldDelete }()
	cleanupDevicesFn = func(context.Context, []tailapi.CleanupTarget, bool) (tailapi.CleanupResult, error) {
		return tailapi.CleanupResult{Deleted: []string{"orphan"}, ResolvedOwnershipIDs: []string{"node-orphan"}}, errors.New("synthetic device failure")
	}
	deleteTagFn = func(context.Context, string) error { return errors.New("synthetic ACL failure") }
	result, err := Reconcile(context.Background(), Options{RegistryPath: regPath, OwnershipPath: ownershipPath, Now: now, ManageACL: true})
	if err != nil || result.ACLAction != ACLSkipped || len(result.Warnings) != 2 || !result.DeviceCleanupSkipped || result.DeviceSkipReason != cleanupUnavailableReason {
		t.Fatalf("result = %+v err=%v", result, err)
	}
	ledger, _ := tsruntime.LoadOwnership(ownershipPath)
	if len(ledger.Nodes) != 2 {
		t.Fatal("remote failure consumed ownership proof")
	}
}

func TestReconcileCorruptOwnershipContinuesWithDeletionDisabled(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	ownershipPath := filepath.Join(dir, "node-ownership.json")
	if _, err := registry.Add(regPath, registry.Service{Name: "active", Type: registry.TypeProxy, Target: "http://localhost:3000"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ownershipPath, []byte(`{"schema_version":1,"nodes":[`), 0o600); err != nil {
		t.Fatal(err)
	}
	oldCleanup := cleanupDevicesFn
	t.Cleanup(func() { cleanupDevicesFn = oldCleanup })
	cleanupDevicesFn = func(context.Context, []tailapi.CleanupTarget, bool) (tailapi.CleanupResult, error) {
		t.Fatal("corrupt ownership ledger must disable device deletion")
		return tailapi.CleanupResult{}, nil
	}
	result, err := Reconcile(context.Background(), Options{RegistryPath: regPath, OwnershipPath: ownershipPath, Now: time.Now()})
	if err != nil {
		t.Fatalf("Reconcile() error = %v, want service availability preserved", err)
	}
	if !result.DeviceCleanupSkipped || result.DeviceSkipReason != ownershipUnavailableReason || len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], ownershipPath) {
		t.Fatalf("result = %+v, want path-bearing degraded warning", result)
	}
}

func TestReconcileEmptyRegistryWithOwnershipRefusesAllDeviceDeletion(t *testing.T) {
	for _, seed := range []struct {
		name string
		body string
	}{{"missing", ""}, {"empty", " \n"}} {
		t.Run(seed.name, func(t *testing.T) {
			dir := t.TempDir()
			regPath := filepath.Join(dir, "registry.json")
			ownershipPath := filepath.Join(dir, "node-ownership.json")
			if seed.body != "" {
				if err := os.WriteFile(regPath, []byte(seed.body), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if err := tsruntime.RecordOwnedNode(ownershipPath, "orphan", "node-orphan", time.Now()); err != nil {
				t.Fatal(err)
			}
			oldCleanup := cleanupDevicesFn
			t.Cleanup(func() { cleanupDevicesFn = oldCleanup })
			cleanupDevicesFn = func(context.Context, []tailapi.CleanupTarget, bool) (tailapi.CleanupResult, error) {
				t.Fatal("empty registry must not turn all proofs into deletion targets")
				return tailapi.CleanupResult{}, nil
			}
			result, err := Reconcile(context.Background(), Options{RegistryPath: regPath, OwnershipPath: ownershipPath, Now: time.Now()})
			if err != nil {
				t.Fatal(err)
			}
			if !result.DeviceCleanupSkipped || result.DeviceSkipReason != emptyRegistryReason || len(result.DevicesDeleted) != 0 {
				t.Fatalf("result = %+v", result)
			}
		})
	}
}

func TestReconcilePartialRegistryLossRefusesMassDeviceDeletion(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	ownershipPath := filepath.Join(dir, "node-ownership.json")
	now := time.Date(2030, 8, 31, 12, 0, 0, 0, time.UTC)
	if _, err := registry.Add(regPath, registry.Service{Name: "survivor", Type: registry.TypeProxy, Target: "http://localhost:3000"}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"survivor", "alpha", "beta", "gamma"} {
		if err := tsruntime.RecordOwnedNode(ownershipPath, name, "node-"+name, now); err != nil {
			t.Fatal(err)
		}
	}
	oldCleanup := cleanupDevicesFn
	t.Cleanup(func() { cleanupDevicesFn = oldCleanup })
	cleanupDevicesFn = func(context.Context, []tailapi.CleanupTarget, bool) (tailapi.CleanupResult, error) {
		t.Fatal("partial registry safety guard must stop remote deletion")
		return tailapi.CleanupResult{}, nil
	}
	result, err := Reconcile(context.Background(), Options{RegistryPath: regPath, OwnershipPath: ownershipPath, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if !result.DeviceCleanupSkipped || result.DeviceSkipReason != partialRegistryReason || len(result.DevicesDeleted) != 0 {
		t.Fatalf("result = %+v, want incomplete-registry mass-delete guard", result)
	}
	if len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], "restore registry.json") || !strings.Contains(result.Warnings[0], "--adopt") {
		t.Fatalf("warnings = %#v, want actionable recovery", result.Warnings)
	}
}

func TestReconcilePartialRegistryGuardAllowsOneOrTwoOrphans(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	ownershipPath := filepath.Join(dir, "node-ownership.json")
	now := time.Date(2030, 8, 31, 12, 0, 0, 0, time.UTC)
	if _, err := registry.Add(regPath, registry.Service{Name: "survivor", Type: registry.TypeProxy, Target: "http://localhost:3000"}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"orphan-a", "orphan-b"} {
		if err := tsruntime.RecordOwnedNode(ownershipPath, name, "node-"+name, now); err != nil {
			t.Fatal(err)
		}
	}
	oldCleanup := cleanupDevicesFn
	t.Cleanup(func() { cleanupDevicesFn = oldCleanup })
	var gotTargets []tailapi.CleanupTarget
	cleanupDevicesFn = func(_ context.Context, targets []tailapi.CleanupTarget, _ bool) (tailapi.CleanupResult, error) {
		gotTargets = append([]tailapi.CleanupTarget(nil), targets...)
		return tailapi.CleanupResult{}, nil
	}
	result, err := Reconcile(context.Background(), Options{RegistryPath: regPath, OwnershipPath: ownershipPath, Now: now, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.DeviceCleanupSkipped || len(gotTargets) != 2 {
		t.Fatalf("result=%+v targets=%+v, want two normal orphan previews", result, gotTargets)
	}
}

func TestReconcileCleanupFailureSetsSkippedSemantics(t *testing.T) {
	now := time.Date(2030, 8, 31, 12, 0, 0, 0, time.UTC)
	regPath, ownershipPath := saveLifecycleFixture(t, now)
	oldCleanup := cleanupDevicesFn
	t.Cleanup(func() { cleanupDevicesFn = oldCleanup })
	cleanupDevicesFn = func(context.Context, []tailapi.CleanupTarget, bool) (tailapi.CleanupResult, error) {
		return tailapi.CleanupResult{}, errors.New("list devices unavailable")
	}
	result, err := Reconcile(context.Background(), Options{RegistryPath: regPath, OwnershipPath: ownershipPath, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if !result.DeviceCleanupSkipped || result.DeviceSkipReason != cleanupUnavailableReason || len(result.Warnings) != 1 {
		t.Fatalf("result = %+v, want skipped cleanup semantics", result)
	}
}

func TestReconcileACLGatesProtectLiveFunnelAndRequireOptIn(t *testing.T) {
	now := time.Date(2030, 8, 31, 12, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	ownershipPath := filepath.Join(dir, "node-ownership.json")
	deadline := now.Add(time.Hour)
	if _, err := registry.Add(regPath, registry.Service{Name: "public", Type: registry.TypeProxy, Target: "http://localhost:3000", Funnel: true, PublicAck: true, FunnelExpiresAt: &deadline}); err != nil {
		t.Fatal(err)
	}
	oldDelete := deleteTagFn
	t.Cleanup(func() { deleteTagFn = oldDelete })
	deleteCalls := 0
	deleteTagFn = func(context.Context, string) error { deleteCalls++; return nil }

	result, err := Reconcile(context.Background(), Options{RegistryPath: regPath, OwnershipPath: ownershipPath, Now: now, ManageACL: true, CheckUnusedACL: true})
	if err != nil || result.ACLAction != ACLStillInUse || deleteCalls != 0 {
		t.Fatalf("live funnel result=%+v calls=%d err=%v", result, deleteCalls, err)
	}
	result, err = Reconcile(context.Background(), Options{RegistryPath: regPath, OwnershipPath: ownershipPath, Now: now, ManageACL: false, CheckUnusedACL: true})
	if err != nil || result.ACLAction != ACLNotRequested || deleteCalls != 0 {
		t.Fatalf("no opt-in result=%+v calls=%d err=%v", result, deleteCalls, err)
	}
}
