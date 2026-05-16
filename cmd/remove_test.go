package cmd

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monody0007/tslink/internal/registry"
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
