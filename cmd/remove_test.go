package cmd

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/monody0007/tslink/internal/registry"
	tsruntime "github.com/monody0007/tslink/internal/runtime"
	"github.com/monody0007/tslink/internal/tailapi"
)

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
	if err := removeService(regPath, "web", &out, &errOut, false); err != nil {
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
	result, err := removeServiceResult(regPath, "clocked")
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
	deleteDevicesFn = func(ctx context.Context, target tailapi.CleanupTarget) (tailapi.CleanupResult, error) {
		if target.Hostname != "web" || len(target.Tags) != 1 || target.Tags[0] != "tag:tsmain" {
			t.Fatalf("cleanup target = %+v, want service hostname and tags", target)
		}
		return tailapi.CleanupResult{
			Matched:    []string{"web"},
			Protected:  []string{"web"},
			Skipped:    true,
			SkipReason: "ownership could not be proven",
		}, nil
	}

	var out, errOut bytes.Buffer
	if err := removeService(regPath, "web", &out, &errOut, false); err != nil {
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
	if _, err := removeServiceResult(regPath, "web"); err != nil {
		t.Fatal(err)
	}
}
