package lifecycle

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anydoor7/tslink/internal/registry"
)

func TestReconcileFunnelACLRequiresTrustedRegistry(t *testing.T) {
	for _, tc := range []struct {
		name, registryState, wantAction string
		dryRun                          bool
		wantDeletes                     int
	}{
		{name: "valid no Funnel apply", registryState: "valid", wantAction: ACLSkipped},
		{name: "valid no Funnel dry run", registryState: "valid", wantAction: ACLSkipped, dryRun: true},
		{name: "missing registry apply", registryState: "missing", wantAction: ACLSkipped},
		{name: "empty registry apply", registryState: "empty", wantAction: ACLSkipped},
		{name: "missing registry dry run", registryState: "missing", wantAction: ACLSkipped, dryRun: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			regPath := filepath.Join(dir, "registry.json")
			switch tc.registryState {
			case "valid":
				if _, err := registry.Add(regPath, registry.Service{Name: "private-app", Type: registry.TypeProxy, Target: "http://localhost:3000"}); err != nil {
					t.Fatal(err)
				}
			case "empty":
				if err := os.WriteFile(regPath, []byte("\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			oldDelete := deleteTagFn
			t.Cleanup(func() { deleteTagFn = oldDelete })
			deletes := 0
			deleteTagFn = func(context.Context, string) error { deletes++; return nil }
			result, err := Reconcile(context.Background(), Options{
				RegistryPath: regPath, OwnershipPath: filepath.Join(dir, "ownership.json"),
				ManageACL: true, CheckUnusedACL: true, DryRun: tc.dryRun,
			})
			if err != nil {
				t.Fatal(err)
			}
			if deletes != tc.wantDeletes || result.ACLAction != tc.wantAction {
				t.Fatalf("ACL result = %q, delete calls = %d; want %q, %d", result.ACLAction, deletes, tc.wantAction, tc.wantDeletes)
			}
			if tc.registryState != "valid" && !strings.Contains(strings.Join(result.Warnings, " "), "active Funnel use cannot be established") {
				t.Fatalf("untrusted registry has no ACL-specific warning: %+v", result.Warnings)
			}
			if tc.registryState == "valid" && !strings.Contains(strings.Join(result.Warnings, " "), "tailnet-wide nonuse") {
				t.Fatalf("local no-Funnel registry falsely claimed global unused state: %+v", result.Warnings)
			}
		})
	}
}
