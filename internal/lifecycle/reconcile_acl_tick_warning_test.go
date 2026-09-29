package lifecycle

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/monody0007/tslink/internal/registry"
)

// TestReconcileReportsSharedACLOnlyWhenRequested pins R4-6. `serve
// --manage-acl` reconciles every 30s but asks for the shared-grant check
// (CheckUnusedACL) only on the first tick and on a Funnel-to-none transition.
// The skip warning used to be appended on every tick, which the daemon logs
// at INFO forever. Steady-state ticks are back to not_requested with no
// warning; the requested cases are the control group and still warn, as does
// a Funnel that expires inside this very reconcile.
func TestReconcileReportsSharedACLOnlyWhenRequested(t *testing.T) {
	now := time.Date(2030, 8, 31, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name       string
		registry   string
		requested  bool
		wantAction string
		// wantACLWarning is the shared-grant warning this tick must carry;
		// empty means it must carry none.
		wantACLWarning string
		// wantOtherWarnings counts the rest. An untrusted registry already
		// disabled device deletion with its own warning at 2baf2b1; that one
		// is unchanged and is not the per-tick noise this test is about.
		wantOtherWarnings int
	}{
		{name: "steady tick, no local Funnel", registry: "private", wantAction: ACLNotRequested},
		{name: "steady tick, missing registry", registry: "missing", wantAction: ACLNotRequested, wantOtherWarnings: 1},
		{name: "steady tick, empty registry", registry: "empty", wantAction: ACLNotRequested, wantOtherWarnings: 1},
		{name: "steady tick, active Funnel", registry: "public", wantAction: ACLStillInUse},
		{name: "steady tick, Funnel expires in this reconcile", registry: "expiring", wantAction: ACLSkipped, wantACLWarning: "tailnet-wide nonuse"},
		{name: "requested, no local Funnel", registry: "private", requested: true, wantAction: ACLSkipped, wantACLWarning: "tailnet-wide nonuse"},
		{name: "requested, missing registry", registry: "missing", requested: true, wantAction: ACLSkipped, wantACLWarning: "active Funnel use cannot be established", wantOtherWarnings: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			regPath := filepath.Join(dir, "registry.json")
			switch tc.registry {
			case "private":
				if _, err := registry.Add(regPath, registry.Service{Name: "private", Type: registry.TypeProxy, Target: "http://localhost:3000"}); err != nil {
					t.Fatal(err)
				}
			case "public", "expiring":
				deadline := now.Add(time.Hour)
				if tc.registry == "expiring" {
					deadline = now.Add(-time.Minute)
				}
				if _, err := registry.Add(regPath, registry.Service{Name: "public", Type: registry.TypeProxy, Target: "http://localhost:3000", Funnel: true, PublicAck: true, FunnelExpiresAt: &deadline}); err != nil {
					t.Fatal(err)
				}
			case "empty":
				if err := os.WriteFile(regPath, []byte("\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			oldDelete := deleteTagFn
			t.Cleanup(func() { deleteTagFn = oldDelete })
			deleteTagFn = func(context.Context, string) error {
				t.Fatal("reconcile mutated the shared Funnel grant")
				return nil
			}
			// Three ticks, the way the daemon calls it. Only the expiring
			// fixture changes between ticks (its first tick downgrades it),
			// so it is judged on the first tick alone.
			ticks := 3
			if tc.registry == "expiring" {
				ticks = 1
			}
			for tick := 0; tick < ticks; tick++ {
				result, err := Reconcile(context.Background(), Options{
					RegistryPath: regPath, OwnershipPath: filepath.Join(dir, "ownership.json"), Now: now,
					ManageACL: true, CheckUnusedACL: tc.requested,
				})
				if err != nil {
					t.Fatal(err)
				}
				warnings := strings.Join(result.Warnings, " | ")
				if result.ACLAction != tc.wantAction {
					t.Fatalf("tick %d: acl_action=%q warnings=%q, want %q", tick, result.ACLAction, warnings, tc.wantAction)
				}
				var aclWarnings []string
				for _, warning := range result.Warnings {
					// Both shared-grant skip messages carry this phrase.
					if strings.Contains(warning, "Funnel ACL cleanup skipped") {
						aclWarnings = append(aclWarnings, warning)
					}
				}
				if tc.wantACLWarning == "" && len(aclWarnings) != 0 {
					t.Fatalf("tick %d: warnings=%q, want no shared-grant warning on a tick that did not request the check", tick, warnings)
				}
				if tc.wantACLWarning != "" && (len(aclWarnings) != 1 || !strings.Contains(aclWarnings[0], tc.wantACLWarning)) {
					t.Fatalf("tick %d: warnings=%q, want one shared-grant warning naming %q", tick, warnings, tc.wantACLWarning)
				}
				if others := len(result.Warnings) - len(aclWarnings); others != tc.wantOtherWarnings {
					t.Fatalf("tick %d: warnings=%q, want %d unrelated warnings", tick, warnings, tc.wantOtherWarnings)
				}
			}
		})
	}
}
