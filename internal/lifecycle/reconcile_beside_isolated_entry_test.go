package lifecycle

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
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

// writeRegistryBesideIsolatedEntry writes web, a Funnel service whose deadline
// is deadline, and docs, a file share whose directory is gone: the daemon's
// loader isolates docs as a per-service issue and runs the other two.
func writeRegistryBesideIsolatedEntry(t *testing.T, regPath string, deadline time.Time) []byte {
	t.Helper()
	data, err := json.Marshal(map[string]any{"schema_version": registry.CurrentRegistrySchemaVersion, "services": []map[string]any{
		{"name": "web", "type": "proxy", "target": "http://127.0.0.1:3000", "tags": []string{"tag:tsmain"}},
		{"name": "docs", "type": "file", "path": filepath.Join(t.TempDir(), "gone"), "tags": []string{"tag:tsmain"}},
		{"name": "public", "type": "proxy", "target": "http://127.0.0.1:3001", "funnel": true, "public_ack": true, "funnel_expires_at": deadline.Format(time.RFC3339), "tags": []string{"tag:tsmain"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(regPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, issues, err := registry.LoadForRuntime(regPath); err != nil || len(issues) != 1 || issues[0].Name != "docs" {
		t.Fatalf("control: issues = %+v, err = %v; want docs isolated", issues, err)
	}
	return data
}

// serve starts with a bad entry, so the lifecycle reconciler must run beside
// one: an error here stops the daemon at startup and, on the ticker, withholds
// Funnel expiry and device cleanup from every other service. registry.json is
// rewritten only when every entry is valid, so an expired Funnel is reported
// for the daemon to withdraw in memory instead of written.
func TestReconcileRunsBesideAnIsolatedRegistryEntry(t *testing.T) {
	now := time.Date(2030, 8, 31, 12, 0, 0, 0, time.UTC)

	t.Run("expired Funnel is reported, not written", func(t *testing.T) {
		regPath := filepath.Join(t.TempDir(), "registry.json")
		before := writeRegistryBesideIsolatedEntry(t, regPath, now.Add(-time.Minute))
		result, err := Reconcile(context.Background(), Options{RegistryPath: regPath, OwnershipPath: filepath.Join(t.TempDir(), "node-ownership.json"), Now: now})
		if err != nil {
			t.Fatalf("Reconcile() error = %v, want the other services' lifecycle to run", err)
		}
		if got := strings.Join(result.ExpiredFunnelsNotWritten, ","); got != "public" {
			t.Fatalf("ExpiredFunnelsNotWritten = %q, want public", got)
		}
		if result.RegistryChanged || len(result.ExpiredFunnels) != 0 {
			t.Fatalf("result = %+v, want nothing written", result)
		}
		warned := false
		for _, warning := range result.Warnings {
			warned = warned || (strings.Contains(warning, "public") && strings.Contains(warning, "docs"))
		}
		if !warned {
			t.Fatalf("warnings = %q, want the deadline and the entry that holds it named", result.Warnings)
		}
		after, err := os.ReadFile(regPath)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(before, after) {
			t.Fatal("registry.json was rewritten beside an isolated entry")
		}
	})

	t.Run("no deadline passed", func(t *testing.T) {
		regPath := filepath.Join(t.TempDir(), "registry.json")
		writeRegistryBesideIsolatedEntry(t, regPath, now.Add(time.Hour))
		result, err := Reconcile(context.Background(), Options{RegistryPath: regPath, OwnershipPath: filepath.Join(t.TempDir(), "node-ownership.json"), Now: now})
		if err != nil || len(result.ExpiredFunnelsNotWritten) != 0 || len(result.Warnings) != 0 {
			t.Fatalf("Reconcile() = %+v, %v; want a quiet run", result, err)
		}
	})

	t.Run("a retired service's device is still deleted", func(t *testing.T) {
		testenv.SetHome(t, t.TempDir())
		fake := testenv.NewStatefulTailnet(t)
		t.Setenv(tailapi.APIBaseURLEnv, fake.URL())
		t.Setenv("TSLINK_API_KEY", "test-placeholder")
		fake.SetDevices([]tailscale.Device{
			{ID: "id-docs", NodeID: "n-docs", Hostname: "docs", Tags: []string{"tag:tsmain"}},
			{ID: "id-removed", NodeID: "n-removed", Hostname: "removed", Tags: []string{"tag:tsmain"}},
		})
		dir := t.TempDir()
		regPath := filepath.Join(dir, "registry.json")
		ownershipPath := filepath.Join(dir, "node-ownership.json")
		writeRegistryBesideIsolatedEntry(t, regPath, now.Add(time.Hour))
		for _, name := range []string{"docs", "removed"} {
			if err := tsruntime.RecordOwnedNode(ownershipPath, name, "n-"+name, now); err != nil {
				t.Fatal(err)
			}
		}
		if err := tsruntime.MarkOwnedNodeIDsRetired(ownershipPath, []string{"n-removed"}, now); err != nil {
			t.Fatal(err)
		}
		result, err := Reconcile(context.Background(), Options{RegistryPath: regPath, OwnershipPath: ownershipPath, Now: now})
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Join(result.DevicesDeleted, ","); got != "removed" {
			t.Fatalf("devices_deleted = %q, want the retired service's device only; result = %+v", got, result)
		}
		if devices := fake.Devices(); len(devices) != 1 || devices[0].NodeID != "n-docs" {
			t.Fatalf("devices = %+v, want the isolated entry's device kept", devices)
		}
	})
}
