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

func TestRemoveService_NoAPIClientSkipIsQuiet(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	if _, err := registry.Add(regPath, registry.Service{Name: "web", Type: registry.TypeProxy, Target: "http://localhost:3000"}); err != nil {
		t.Fatalf("Add() error = %v", err)
	}

	oldDelete := deleteDevicesFn
	t.Cleanup(func() { deleteDevicesFn = oldDelete })
	deleteDevicesFn = func(ctx context.Context, hostname string) error {
		return tailapi.ErrNoAPIClient
	}

	var out, errOut bytes.Buffer
	if err := removeService(regPath, "web", &out, &errOut, false); err != nil {
		t.Fatalf("removeService() error = %v", err)
	}
	if !strings.Contains(out.String(), "removed") {
		t.Fatalf("stdout = %q, want removed message", out.String())
	}
	if strings.Contains(errOut.String(), "warning") {
		t.Fatalf("stderr = %q, want no warning for no-client cleanup skip", errOut.String())
	}
}
