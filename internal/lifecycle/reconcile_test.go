package lifecycle

import (
	"bytes"
	"context"
	"errors"
	"fmt"
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
	if err := tsruntime.MarkOwnedNodeIDsRetired(ownershipPath, []string{"node-orphan"}, now); err != nil {
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
	if strings.Join(result.ExpiredFunnels, ",") != "expired" || result.ACLAction != ACLSkipped || strings.Join(result.DevicesWouldDelete, ",") != "orphan" {
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

func TestReconcileDryRunUsesInMemoryAdoptionToPreviewApplyDeletion(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	ownershipPath := filepath.Join(dir, "node-ownership.json")
	now := time.Date(2030, 8, 31, 12, 0, 0, 0, time.UTC)
	if _, err := registry.Add(regPath, registry.Service{Name: "active", Type: registry.TypeProxy, Target: "http://localhost:3000"}); err != nil {
		t.Fatal(err)
	}
	if err := tsruntime.RecordOwnedNode(ownershipPath, "active", "node-active", now); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(ownershipPath)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := tsruntime.PreviewAdoptOwnedNode(ownershipPath, "legacy", "node-legacy", now, true)
	if err != nil {
		t.Fatal(err)
	}
	oldCleanup := cleanupDevicesFn
	t.Cleanup(func() { cleanupDevicesFn = oldCleanup })
	cleanupDevicesFn = func(_ context.Context, targets []tailapi.CleanupTarget, dryRun bool) (tailapi.CleanupResult, error) {
		if !dryRun || len(targets) != 1 || targets[0].Hostname != "legacy" || strings.Join(targets[0].NodeIDs, ",") != "node-legacy" {
			t.Fatalf("targets=%+v dryRun=%t", targets, dryRun)
		}
		return tailapi.CleanupResult{WouldDelete: []string{"legacy"}}, nil
	}
	result, err := Reconcile(context.Background(), Options{
		RegistryPath: regPath, OwnershipPath: ownershipPath, OwnershipOverride: &preview, Now: now, DryRun: true,
	})
	if err != nil || result.DeviceCleanupSkipped || strings.Join(result.DevicesWouldDelete, ",") != "legacy" {
		t.Fatalf("result=%+v err=%v, want apply-equivalent deletion preview", result, err)
	}
	after, err := os.ReadFile(ownershipPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("dry-run changed ledger bytes:\nbefore=%s\nafter=%s", before, after)
	}
}

func TestReconcileApplyDowngradesDeletesOrphanAndPreservesSharedACL(t *testing.T) {
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
	if !result.RegistryChanged || result.ACLAction != ACLSkipped || aclCalls != 0 || strings.Join(result.DevicesDeleted, ",") != "orphan" {
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

func writeValidEmptyRegistry(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(`{"schema_version":1,"services":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
}

func recordRetiredLifecycleNode(t *testing.T, path string, now time.Time) {
	t.Helper()
	if err := tsruntime.RecordOwnedNode(path, "orphan", "node-orphan", now); err != nil {
		t.Fatal(err)
	}
	if err := tsruntime.MarkOwnedNodeIDsRetired(path, []string{"node-orphan"}, now); err != nil {
		t.Fatal(err)
	}
}

func refuseLifecycleCleanup(t *testing.T) {
	t.Helper()
	oldCleanup := cleanupDevicesFn
	t.Cleanup(func() { cleanupDevicesFn = oldCleanup })
	cleanupDevicesFn = func(context.Context, []tailapi.CleanupTarget, bool) (tailapi.CleanupResult, error) {
		t.Fatal("structurally untrusted registry must disable remote device deletion")
		return tailapi.CleanupResult{}, nil
	}
}

func TestReconcileMissingRegistryWithAllRowsRetiredRefusesDeviceDeletion(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	ownershipPath := filepath.Join(dir, "node-ownership.json")
	now := time.Date(2030, 8, 31, 12, 0, 0, 0, time.UTC)
	recordRetiredLifecycleNode(t, ownershipPath, now)
	refuseLifecycleCleanup(t)

	result, err := Reconcile(context.Background(), Options{RegistryPath: regPath, OwnershipPath: ownershipPath, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if !result.DeviceCleanupSkipped || !strings.Contains(result.DeviceSkipReason, "structurally untrusted") || !strings.Contains(result.DeviceSkipReason, "remote device deletion disabled") || !strings.Contains(result.DeviceSkipReason, "missing") || len(result.DevicesDeleted) != 0 {
		t.Fatalf("result = %+v, want structural missing-file refusal", result)
	}
}

func TestReconcileBlankRegistryWithAllRowsRetiredRefusesDeviceDeletion(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	ownershipPath := filepath.Join(dir, "node-ownership.json")
	now := time.Date(2030, 8, 31, 12, 0, 0, 0, time.UTC)
	if err := os.WriteFile(regPath, []byte(" \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	recordRetiredLifecycleNode(t, ownershipPath, now)
	refuseLifecycleCleanup(t)

	result, err := Reconcile(context.Background(), Options{RegistryPath: regPath, OwnershipPath: ownershipPath, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if !result.DeviceCleanupSkipped || !strings.Contains(result.DeviceSkipReason, "structurally untrusted") || !strings.Contains(result.DeviceSkipReason, "remote device deletion disabled") || !strings.Contains(result.DeviceSkipReason, "empty") || len(result.DevicesDeleted) != 0 {
		t.Fatalf("result = %+v, want structural blank-file refusal", result)
	}
}

func TestReconcileInvalidRegistryWithAllRowsRetiredRefusesDeviceDeletion(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{name: "truncated", body: `{"schema_version":1,"services":[`},
		{name: "invalid_json", body: `not-json`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			regPath := filepath.Join(dir, "registry.json")
			ownershipPath := filepath.Join(dir, "node-ownership.json")
			now := time.Date(2030, 8, 31, 12, 0, 0, 0, time.UTC)
			if err := os.WriteFile(regPath, []byte(tc.body), 0o600); err != nil {
				t.Fatal(err)
			}
			recordRetiredLifecycleNode(t, ownershipPath, now)
			refuseLifecycleCleanup(t)

			_, err := Reconcile(context.Background(), Options{RegistryPath: regPath, OwnershipPath: ownershipPath, Now: now})
			if err == nil || !strings.Contains(err.Error(), "structurally untrusted") || !strings.Contains(err.Error(), "remote device deletion disabled") {
				t.Fatalf("error = %v, want explicit structural refusal", err)
			}
		})
	}
}

func TestReconcileValidEmptyRegistryWithAllRowsRetiredAllowsDeviceDeletion(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	ownershipPath := filepath.Join(dir, "node-ownership.json")
	now := time.Date(2030, 8, 31, 12, 0, 0, 0, time.UTC)
	writeValidEmptyRegistry(t, regPath)
	recordRetiredLifecycleNode(t, ownershipPath, now)
	oldCleanup := cleanupDevicesFn
	t.Cleanup(func() { cleanupDevicesFn = oldCleanup })
	cleanupDevicesFn = func(_ context.Context, targets []tailapi.CleanupTarget, dryRun bool) (tailapi.CleanupResult, error) {
		if dryRun || len(targets) != 1 || targets[0].Hostname != "orphan" {
			t.Fatalf("targets = %+v dryRun=%t, want reviewed orphan", targets, dryRun)
		}
		return tailapi.CleanupResult{Deleted: []string{"orphan"}}, nil
	}

	result, err := Reconcile(context.Background(), Options{RegistryPath: regPath, OwnershipPath: ownershipPath, Now: now})
	if err != nil || result.DeviceCleanupSkipped || strings.Join(result.DevicesDeleted, ",") != "orphan" {
		t.Fatalf("result = %+v err=%v, want valid whole-shutdown cleanup", result, err)
	}
}

func TestReconcileUnknownRetirementProvenanceRefusesThatServicesDeletion(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	ownershipPath := filepath.Join(dir, "node-ownership.json")
	now := time.Date(2030, 8, 31, 12, 0, 0, 0, time.UTC)
	writeValidEmptyRegistry(t, regPath)
	if err := tsruntime.RecordOwnedNode(ownershipPath, "orphan", "node-orphan", now); err != nil {
		t.Fatal(err)
	}
	oldCleanup := cleanupDevicesFn
	t.Cleanup(func() { cleanupDevicesFn = oldCleanup })
	cleanupDevicesFn = func(context.Context, []tailapi.CleanupTarget, bool) (tailapi.CleanupResult, error) {
		t.Fatal("unknown retirement provenance must withhold that service's remote device deletion")
		return tailapi.CleanupResult{}, nil
	}

	result, err := Reconcile(context.Background(), Options{RegistryPath: regPath, OwnershipPath: ownershipPath, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if !result.DeviceCleanupSkipped || !strings.Contains(result.DeviceSkipReason, unknownRetirementReasonPrefix) || strings.Join(result.DeviceSkipUnknownProvenance, ",") != "orphan" || len(result.DevicesDeleted) != 0 {
		t.Fatalf("result = %+v, want provenance refusal with structured hostname", result)
	}
}

func TestReconcileUnknownRetirementProvenanceNamesAreSorted(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	writeValidEmptyRegistry(t, regPath)
	now := time.Date(2030, 8, 31, 12, 0, 0, 0, time.UTC)
	override := tsruntime.OwnershipLedger{
		SchemaVersion: tsruntime.OwnershipSchemaVersion,
		Nodes: []tsruntime.OwnedNode{
			{ServiceName: "zulu", NodeID: "node-zulu", RecordedAt: now},
			{ServiceName: "alpha", NodeID: "node-alpha", RecordedAt: now},
		},
	}
	oldCleanup := cleanupDevicesFn
	t.Cleanup(func() { cleanupDevicesFn = oldCleanup })
	cleanupDevicesFn = func(context.Context, []tailapi.CleanupTarget, bool) (tailapi.CleanupResult, error) {
		t.Fatal("unknown retirement provenance must disable deletion")
		return tailapi.CleanupResult{}, nil
	}
	result, err := Reconcile(context.Background(), Options{RegistryPath: regPath, OwnershipPath: filepath.Join(dir, "unused.json"), OwnershipOverride: &override, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(result.DeviceSkipUnknownProvenance, ","); got != "alpha,zulu" {
		t.Fatalf("device_skip_unknown_provenance = %q, want sorted alpha,zulu", got)
	}
}

func TestReconcileSecondLoadReplacesRegistryFileState(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	ownershipPath := filepath.Join(dir, "node-ownership.json")
	now := time.Date(2030, 8, 31, 12, 0, 0, 0, time.UTC)
	if _, err := registry.Add(regPath, registry.Service{Name: "active", Type: registry.TypeProxy, Target: "http://localhost:3000"}); err != nil {
		t.Fatal(err)
	}
	recordRetiredLifecycleNode(t, ownershipPath, now)
	oldDowngrade, oldCleanup := downgradeExpiredFunnelsFn, cleanupDevicesFn
	t.Cleanup(func() {
		downgradeExpiredFunnelsFn, cleanupDevicesFn = oldDowngrade, oldCleanup
	})
	downgradeExpiredFunnelsFn = func(path string, _ time.Time, _ bool) ([]registry.Service, error) {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		return nil, nil
	}
	cleanupDevicesFn = func(context.Context, []tailapi.CleanupTarget, bool) (tailapi.CleanupResult, error) {
		t.Fatal("second registry load became untrusted but deletion was attempted")
		return tailapi.CleanupResult{}, nil
	}
	result, err := Reconcile(context.Background(), Options{RegistryPath: regPath, OwnershipPath: ownershipPath, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if !result.DeviceCleanupSkipped || !strings.Contains(result.DeviceSkipReason, "structurally untrusted") || !strings.Contains(result.DeviceSkipReason, "missing") {
		t.Fatalf("result = %+v, want second-load missing state to disable deletion", result)
	}
}

func TestReconcileStructuralRegistryReasonTakesPriorityOverOwnershipFailure(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	ownershipPath := filepath.Join(dir, "node-ownership.json")
	if err := os.WriteFile(ownershipPath, []byte(`{"schema_version":2,"nodes":[`), 0o600); err != nil {
		t.Fatal(err)
	}
	oldCleanup := cleanupDevicesFn
	t.Cleanup(func() { cleanupDevicesFn = oldCleanup })
	cleanupDevicesFn = func(context.Context, []tailapi.CleanupTarget, bool) (tailapi.CleanupResult, error) {
		t.Fatal("combined structural and ownership failure must disable deletion")
		return tailapi.CleanupResult{}, nil
	}
	result, err := Reconcile(context.Background(), Options{RegistryPath: regPath, OwnershipPath: ownershipPath, Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if !result.DeviceCleanupSkipped || !strings.Contains(result.DeviceSkipReason, "structurally untrusted") || !strings.Contains(result.DeviceSkipReason, "missing") || len(result.Warnings) != 2 {
		t.Fatalf("result = %+v, want structural reason priority with both warnings", result)
	}
	if !strings.Contains(strings.Join(result.Warnings, "\n"), "ownership ledger") {
		t.Fatalf("warnings = %#v, want ownership failure retained", result.Warnings)
	}
}

func TestReconcileSkipsFunnelDowngradeWhenRegistryUntrusted(t *testing.T) {
	for _, state := range []string{"missing", "empty"} {
		t.Run(state, func(t *testing.T) {
			dir := t.TempDir()
			regPath := filepath.Join(dir, "registry.json")
			if state == "empty" {
				if err := os.WriteFile(regPath, []byte(" \n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			oldDowngrade := downgradeExpiredFunnelsFn
			t.Cleanup(func() { downgradeExpiredFunnelsFn = oldDowngrade })
			downgradeExpiredFunnelsFn = func(string, time.Time, bool) ([]registry.Service, error) {
				t.Fatal("untrusted registry reached DowngradeExpiredFunnels")
				return nil, nil
			}
			result, err := Reconcile(context.Background(), Options{RegistryPath: regPath, OwnershipPath: filepath.Join(dir, "node-ownership.json"), Now: time.Now()})
			if err != nil {
				t.Fatal(err)
			}
			if !result.DeviceCleanupSkipped || !strings.Contains(result.DeviceSkipReason, state) {
				t.Fatalf("result = %+v, want %s registry refusal", result, state)
			}
		})
	}
}

func TestReconcileRegistryRollbackFrom36To20RefusesAllUnretiredOrphans(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	ownershipPath := filepath.Join(dir, "node-ownership.json")
	now := time.Date(2030, 8, 31, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 36; i++ {
		name := fmt.Sprintf("svc-%02d", i)
		if i < 20 {
			if _, err := registry.Add(regPath, registry.Service{Name: name, Type: registry.TypeProxy, Target: "http://localhost:3000"}); err != nil {
				t.Fatal(err)
			}
		}
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
	if !result.DeviceCleanupSkipped || !strings.Contains(result.DeviceSkipReason, unknownRetirementReasonPrefix) || len(result.DevicesDeleted) != 0 {
		t.Fatalf("result = %+v, want provenance guard to stop all 16 remote deletions", result)
	}
	t.Logf("registry=20 ledger=36 unretired_orphans=16 skipped=%t deleted=%d reason=%q", result.DeviceCleanupSkipped, len(result.DevicesDeleted), result.DeviceSkipReason)
	if len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], "restore registry.json") || !strings.Contains(result.Warnings[0], "--adopt") || strings.Contains(result.Warnings[0], "at least 3") {
		t.Fatalf("warnings = %#v, want actionable recovery", result.Warnings)
	}
}

func TestReconcileThreeRetiredOrphansRemainDeletableInSmallDeployment(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	ownershipPath := filepath.Join(dir, "node-ownership.json")
	now := time.Date(2030, 8, 31, 12, 0, 0, 0, time.UTC)
	for _, name := range []string{"keep-a", "keep-b"} {
		if _, err := registry.Add(regPath, registry.Service{Name: name, Type: registry.TypeProxy, Target: "http://localhost:3000"}); err != nil {
			t.Fatal(err)
		}
	}
	retiredIDs := []string{"node-orphan-a", "node-orphan-b", "node-orphan-c"}
	for _, name := range []string{"orphan-a", "orphan-b", "orphan-c"} {
		if err := tsruntime.RecordOwnedNode(ownershipPath, name, "node-"+name, now); err != nil {
			t.Fatal(err)
		}
	}
	if err := tsruntime.MarkOwnedNodeIDsRetired(ownershipPath, retiredIDs, now); err != nil {
		t.Fatal(err)
	}
	oldCleanup := cleanupDevicesFn
	t.Cleanup(func() { cleanupDevicesFn = oldCleanup })
	var gotTargets []tailapi.CleanupTarget
	cleanupDevicesFn = func(_ context.Context, targets []tailapi.CleanupTarget, dryRun bool) (tailapi.CleanupResult, error) {
		if dryRun {
			t.Fatal("small-deployment apply unexpectedly became dry-run")
		}
		gotTargets = append([]tailapi.CleanupTarget(nil), targets...)
		return tailapi.CleanupResult{Deleted: []string{"orphan-a", "orphan-b", "orphan-c"}, ResolvedOwnershipIDs: retiredIDs}, nil
	}
	result, err := Reconcile(context.Background(), Options{RegistryPath: regPath, OwnershipPath: ownershipPath, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if result.DeviceCleanupSkipped || len(gotTargets) != 3 || len(result.DevicesDeleted) != 3 {
		t.Fatalf("result=%+v targets=%+v, want all three reviewed retirements deleted", result, gotTargets)
	}
	t.Logf("registry=2 retired_orphans=3 skipped=%t targets=%d deleted=%d", result.DeviceCleanupSkipped, len(gotTargets), len(result.DevicesDeleted))
}

func TestReconcileAdoptedUniqueOrphanIsDeletedWithoutGuard(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	ownershipPath := filepath.Join(dir, "node-ownership.json")
	now := time.Date(2030, 8, 31, 12, 0, 0, 0, time.UTC)
	if _, err := registry.Add(regPath, registry.Service{Name: "active", Type: registry.TypeProxy, Target: "http://localhost:3000"}); err != nil {
		t.Fatal(err)
	}
	if err := tsruntime.AdoptOwnedNode(ownershipPath, "legacy", "node-legacy", now, true); err != nil {
		t.Fatal(err)
	}
	oldCleanup := cleanupDevicesFn
	t.Cleanup(func() { cleanupDevicesFn = oldCleanup })
	cleanupDevicesFn = func(_ context.Context, targets []tailapi.CleanupTarget, dryRun bool) (tailapi.CleanupResult, error) {
		if dryRun || len(targets) != 1 || targets[0].Hostname != "legacy" {
			t.Fatalf("targets=%+v dryRun=%t", targets, dryRun)
		}
		return tailapi.CleanupResult{Deleted: []string{"legacy"}, ResolvedOwnershipIDs: []string{"node-legacy"}}, nil
	}
	result, err := Reconcile(context.Background(), Options{RegistryPath: regPath, OwnershipPath: ownershipPath, Now: now})
	if err != nil || result.DeviceCleanupSkipped || strings.Join(result.DevicesDeleted, ",") != "legacy" {
		t.Fatalf("result=%+v err=%v, want adopted orphan deleted", result, err)
	}
	t.Logf("adopted_orphans=1 unknown_orphans=0 skipped=%t deleted=%v", result.DeviceCleanupSkipped, result.DevicesDeleted)
}

func TestReconcileLegacyOwnershipSchemaBlocksWithExecutableRecovery(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	ownershipPath := filepath.Join(dir, "node-ownership.json")
	now := time.Date(2030, 8, 31, 12, 0, 0, 0, time.UTC)
	if _, err := registry.Add(regPath, registry.Service{Name: "active", Type: registry.TypeProxy, Target: "http://localhost:3000"}); err != nil {
		t.Fatal(err)
	}
	legacy := `{"schema_version":1,"nodes":[{"service_name":"legacy","node_id":"node-legacy","recorded_at":"2030-01-01T00:00:00Z"}]}`
	if err := os.WriteFile(ownershipPath, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	oldCleanup := cleanupDevicesFn
	t.Cleanup(func() { cleanupDevicesFn = oldCleanup })
	cleanupDevicesFn = func(context.Context, []tailapi.CleanupTarget, bool) (tailapi.CleanupResult, error) {
		t.Fatal("legacy provenance must block remote deletion")
		return tailapi.CleanupResult{}, nil
	}
	result, err := Reconcile(context.Background(), Options{RegistryPath: regPath, OwnershipPath: ownershipPath, Now: now})
	if err != nil || !result.DeviceCleanupSkipped || !strings.Contains(result.DeviceSkipReason, unknownRetirementReasonPrefix) {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	for _, want := range []string{"retired_at", "restore registry.json", "--adopt <hostname>", "--dry-run=false"} {
		if !strings.Contains(result.DeviceSkipReason, want) {
			t.Fatalf("reason %q missing actionable %q", result.DeviceSkipReason, want)
		}
	}
	t.Logf("legacy_schema=1 retired_at_present=false skipped=%t reason=%q", result.DeviceCleanupSkipped, result.DeviceSkipReason)
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
