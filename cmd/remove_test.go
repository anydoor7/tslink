package cmd

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/monody0007/tslink/internal/registry"
	tsruntime "github.com/monody0007/tslink/internal/runtime"
	"github.com/monody0007/tslink/internal/tailapi"
)

type deleteDevicesContractFake struct {
	target tailapi.CleanupTarget
	result tailapi.CleanupResult
	err    error
}

func (f deleteDevicesContractFake) Delete(_ context.Context, target tailapi.CleanupTarget) (tailapi.CleanupResult, error) {
	if !reflect.DeepEqual(target, f.target) {
		return tailapi.CleanupResult{}, fmt.Errorf("target = %+v, want %+v", target, f.target)
	}
	return f.result, f.err
}

func testOwnershipPath(regPath string) string {
	return filepath.Join(filepath.Dir(regPath), "node-ownership.json")
}

func TestRemoveUsesExplicitOwnershipPathIndependentOfRegistryLocation(t *testing.T) {
	registryDir := t.TempDir()
	ownershipDir := t.TempDir()
	regPath := filepath.Join(registryDir, "registry.json")
	ownershipPath := filepath.Join(ownershipDir, "node-ownership.json")
	siblingPath := testOwnershipPath(regPath)
	if _, err := registry.Add(regPath, registry.Service{Name: "split-path", Type: registry.TypeProxy, Target: "http://localhost:3000"}); err != nil {
		t.Fatal(err)
	}
	recordedAt := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := tsruntime.RecordOwnedNode(ownershipPath, "split-path", "node-split", recordedAt); err != nil {
		t.Fatal(err)
	}
	if err := tsruntime.RecordOwnedNode(siblingPath, "unrelated", "node-unrelated", recordedAt); err != nil {
		t.Fatal(err)
	}
	siblingBefore, err := os.ReadFile(siblingPath)
	if err != nil {
		t.Fatal(err)
	}

	oldDelete, oldNow := deleteDevicesFn, removeNowFn
	t.Cleanup(func() { deleteDevicesFn, removeNowFn = oldDelete, oldNow })
	removeNowFn = func() time.Time { return recordedAt.Add(time.Hour) }
	deleteDevicesFn = func(_ context.Context, target tailapi.CleanupTarget) (tailapi.CleanupResult, error) {
		if strings.Join(target.NodeIDs, ",") != "node-split" {
			t.Fatalf("cleanup NodeIDs = %v, want proof from explicit ownership path", target.NodeIDs)
		}
		return tailapi.CleanupResult{Skipped: true, SkipReason: tailapi.ErrNoAPIClient.Error()}, nil
	}

	result, err := removeServiceResult(regPath, ownershipPath, "split-path")
	if err != nil || !result.Removed {
		t.Fatalf("removeServiceResult() = %+v, err=%v", result, err)
	}
	ledger, err := tsruntime.LoadOwnership(ownershipPath)
	if err != nil || len(ledger.Nodes) != 1 || ledger.Nodes[0].RetiredAt == nil {
		t.Fatalf("explicit ownership ledger = %+v, err=%v, want retired proof", ledger, err)
	}
	siblingAfter, err := os.ReadFile(siblingPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(siblingAfter) != string(siblingBefore) {
		t.Fatal("remove mutated the registry-sibling ledger instead of the explicit ownership path")
	}
}

func TestRemoveService_NoAPIClientSkipIsReported(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	if _, err := registry.Add(regPath, registry.Service{Name: "web", Type: registry.TypeProxy, Target: "http://localhost:3000"}); err != nil {
		t.Fatalf("Add() error = %v", err)
	}

	oldDelete := deleteDevicesFn
	t.Cleanup(func() { deleteDevicesFn = oldDelete })
	deleteDevicesFn = func(ctx context.Context, target tailapi.CleanupTarget) (tailapi.CleanupResult, error) {
		return tailapi.CleanupResult{Skipped: true, SkipReason: tailapi.ErrNoAPIClient.Error()}, nil
	}

	var out, errOut bytes.Buffer
	if err := removeService(regPath, testOwnershipPath(regPath), "web", &out, &errOut, false); err != nil {
		t.Fatalf("removeService() error = %v", err)
	}
	if !strings.Contains(out.String(), "removed") {
		t.Fatalf("stdout = %q, want removed message", out.String())
	}
	if !strings.Contains(out.String(), "remote tailnet node cleanup skipped") {
		t.Fatalf("stdout = %q, want cleanup skipped message", out.String())
	}
	if errOut.Len() != 0 {
		t.Fatalf("stderr = %q, want no warning for no-client cleanup skip", errOut.String())
	}
}

func TestRemoveClockRollbackWritesReadableClampedLedger(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	ownershipPath := filepath.Join(dir, "node-ownership.json")
	recordedAt := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := registry.Add(regPath, registry.Service{Name: "clocked", Type: registry.TypeProxy, Target: "http://localhost:3000"}); err != nil {
		t.Fatal(err)
	}
	if err := tsruntime.RecordOwnedNode(ownershipPath, "clocked", "node-clocked", recordedAt); err != nil {
		t.Fatal(err)
	}
	oldDelete, oldNow := deleteDevicesFn, removeNowFn
	t.Cleanup(func() { deleteDevicesFn, removeNowFn = oldDelete, oldNow })
	removeNowFn = func() time.Time { return recordedAt.Add(-time.Hour) }
	deleteDevicesFn = func(context.Context, tailapi.CleanupTarget) (tailapi.CleanupResult, error) {
		return tailapi.CleanupResult{}, tailapi.ErrNoAPIClient
	}
	result, err := removeServiceResult(regPath, ownershipPath, "clocked")
	if err != nil {
		t.Fatal(err)
	}
	if !result.Removed || !result.DeviceCleanupSkipped {
		t.Fatalf("result = %+v, want local removal with degraded remote cleanup", result)
	}
	ledger, err := tsruntime.LoadOwnership(ownershipPath)
	if err != nil {
		t.Fatalf("LoadOwnership() after clock rollback remove = %v", err)
	}
	if len(ledger.Nodes) != 1 || ledger.Nodes[0].RetiredAt == nil || !ledger.Nodes[0].RetiredAt.Equal(recordedAt) {
		t.Fatalf("ledger = %+v, want readable retirement clamped to recorded_at", ledger)
	}
}

func TestRemoveService_ProtectedCleanupSkipIsReported(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	if _, err := registry.Add(regPath, registry.Service{
		Name:   "web",
		Type:   registry.TypeProxy,
		Target: "http://localhost:3000",
		Tags:   []string{"tag:tsmain"},
	}); err != nil {
		t.Fatalf("Add() error = %v", err)
	}

	oldDelete := deleteDevicesFn
	t.Cleanup(func() { deleteDevicesFn = oldDelete })
	target := tailapi.CleanupTarget{Hostname: "web", Tags: []string{"tag:tsmain"}}
	fake := deleteDevicesContractFake{
		target: target,
		result: tailapi.CleanupResult{
			Matched:    []string{"web"},
			Protected:  []string{"web"},
			Skipped:    true,
			SkipReason: "ownership could not be proven",
		},
	}
	deleteDevicesFn = fake.Delete

	var out, errOut bytes.Buffer
	if err := removeService(regPath, testOwnershipPath(regPath), "web", &out, &errOut, false); err != nil {
		t.Fatalf("removeService() error = %v", err)
	}
	if !strings.Contains(out.String(), "ownership could not be proven") {
		t.Fatalf("stdout = %q, want protected skip reason", out.String())
	}
}

func TestRemoveServicePassesOnlyNamedServicesOwnershipProof(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	for _, svc := range []registry.Service{
		{Name: "web", Type: registry.TypeProxy, Target: "http://localhost:3000"},
		{Name: "other", Type: registry.TypeProxy, Target: "http://localhost:4000"},
	} {
		if _, err := registry.Add(regPath, svc); err != nil {
			t.Fatal(err)
		}
	}
	ownershipPath := filepath.Join(dir, "node-ownership.json")
	if err := tsruntime.RecordOwnedNode(ownershipPath, "web", "node-web", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := tsruntime.RecordOwnedNode(ownershipPath, "other", "node-other", time.Now()); err != nil {
		t.Fatal(err)
	}
	retiredAt := time.Date(2030, 8, 31, 12, 0, 0, 0, time.UTC)
	oldDelete, oldNow := deleteDevicesFn, removeNowFn
	t.Cleanup(func() {
		deleteDevicesFn = oldDelete
		removeNowFn = oldNow
	})
	removeNowFn = func() time.Time { return retiredAt }
	deleteDevicesFn = func(_ context.Context, target tailapi.CleanupTarget) (tailapi.CleanupResult, error) {
		if target.Hostname != "web" || strings.Join(target.NodeIDs, ",") != "node-web" {
			t.Fatalf("cleanup target = %+v, want only named service proof", target)
		}
		ledger, err := tsruntime.LoadOwnership(ownershipPath)
		if err != nil {
			t.Fatal(err)
		}
		for _, node := range ledger.Nodes {
			switch node.ServiceName {
			case "web":
				if node.RetiredAt == nil || !node.RetiredAt.Equal(retiredAt) {
					t.Fatalf("removed service proof = %+v, want retired_at before remote cleanup", node)
				}
			case "other":
				if node.RetiredAt != nil {
					t.Fatalf("unrelated service proof = %+v, must remain active", node)
				}
			}
		}
		return tailapi.CleanupResult{}, nil
	}
	if _, err := removeServiceResult(regPath, ownershipPath, "web"); err != nil {
		t.Fatal(err)
	}
}
