package cmd

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/inspect"
	"github.com/anydoor7/tslink/internal/registry"
	tsruntime "github.com/anydoor7/tslink/internal/runtime"
)

func deviceCleanupBlockedServices(result DoctorResult) []string {
	var names []string
	for _, finding := range result.Findings {
		if finding.Code == inspect.WarningCodeDeviceCleanupBlocked {
			names = append(names, finding.Service)
		}
	}
	sort.Strings(names)
	return names
}

// doctor names every service whose remote device deletion the reconciler
// withholds, from local files alone: an ownership record without retired_at
// for a service absent from registry.json. A retired record and a registered
// service are not reported.
func TestDoctorReportsDeviceCleanupBlockedPerService(t *testing.T) {
	env := newDoctorTestEnv(t, []registry.Service{{Name: "keep", Type: registry.TypeProxy, Target: "http://localhost:3000", Tags: []string{"tag:tsmain"}}})
	ownershipPath := filepath.Join(env.dir, "node-ownership.json")
	now := time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)
	for _, name := range []string{"keep", "removed", "handedited", "alpha"} {
		if err := tsruntime.RecordOwnedNode(ownershipPath, name, "n-"+name, now); err != nil {
			t.Fatal(err)
		}
	}
	if err := tsruntime.MarkOwnedNodeIDsRetired(ownershipPath, []string{"n-removed"}, now); err != nil {
		t.Fatal(err)
	}

	result := buildDoctorResult(context.Background(), doctorOptions{})
	if got := strings.Join(deviceCleanupBlockedServices(result), ","); got != "alpha,handedited" {
		t.Fatalf("device_cleanup_blocked services = %q, want alpha,handedited; findings = %+v", got, result.Findings)
	}
	finding := assertDoctorFinding(t, result, inspect.WarningCodeDeviceCleanupBlocked)
	if finding.Severity != "warning" || !strings.Contains(finding.Message, "retired_at") || !strings.Contains(finding.Message, "--adopt") {
		t.Fatalf("finding = %+v, want a warning that says why and what to do", finding)
	}
	assertDoctorCodesRegistered(t, result)
}

// With no unexplained record there is no finding, and an unreadable ledger is
// reported once, without a service, because it stops every deletion.
func TestDoctorDeviceCleanupBlockedControls(t *testing.T) {
	t.Run("all records explained", func(t *testing.T) {
		env := newDoctorTestEnv(t, []registry.Service{{Name: "keep", Type: registry.TypeProxy, Target: "http://localhost:3000", Tags: []string{"tag:tsmain"}}})
		ownershipPath := filepath.Join(env.dir, "node-ownership.json")
		now := time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)
		for _, name := range []string{"keep", "removed"} {
			if err := tsruntime.RecordOwnedNode(ownershipPath, name, "n-"+name, now); err != nil {
				t.Fatal(err)
			}
		}
		if err := tsruntime.MarkOwnedNodeIDsRetired(ownershipPath, []string{"n-removed"}, now); err != nil {
			t.Fatal(err)
		}
		assertDoctorNoFinding(t, buildDoctorResult(context.Background(), doctorOptions{}), inspect.WarningCodeDeviceCleanupBlocked)
	})
	t.Run("ledger unreadable", func(t *testing.T) {
		env := newDoctorTestEnv(t, nil)
		if err := os.WriteFile(filepath.Join(env.dir, "node-ownership.json"), []byte("{ not json"), 0o600); err != nil {
			t.Fatal(err)
		}
		result := buildDoctorResult(context.Background(), doctorOptions{})
		finding := assertDoctorFinding(t, result, inspect.WarningCodeDeviceCleanupBlocked)
		if finding.Service != "" || finding.Evidence["error"] == "" {
			t.Fatalf("finding = %+v, want one install-wide finding with the ledger error", finding)
		}
		assertDoctorCodesRegistered(t, result)
	})
}
