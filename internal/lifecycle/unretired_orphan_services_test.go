package lifecycle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/monody0007/tslink/internal/registry"
	tsruntime "github.com/monody0007/tslink/internal/runtime"
)

// UnretiredOrphanServices is the local-only answer doctor reports: the
// services whose device deletion the reconciler withholds, and no others.
func TestUnretiredOrphanServicesNamesOnlyTheWithheldServices(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	ownershipPath := filepath.Join(dir, "node-ownership.json")
	now := time.Date(2030, 8, 31, 12, 0, 0, 0, time.UTC)
	if _, err := registry.Add(regPath, registry.Service{Name: "keep", Type: registry.TypeProxy, Target: "http://localhost:3000"}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"keep", "removed", "zulu", "alpha"} {
		if err := tsruntime.RecordOwnedNode(ownershipPath, name, "n-"+name, now); err != nil {
			t.Fatal(err)
		}
	}
	if err := tsruntime.MarkOwnedNodeIDsRetired(ownershipPath, []string{"n-removed", "n-keep"}, now); err != nil {
		t.Fatal(err)
	}
	names, err := UnretiredOrphanServices(regPath, ownershipPath)
	if err != nil || strings.Join(names, ",") != "alpha,zulu" {
		t.Fatalf("UnretiredOrphanServices() = %v, %v; want [alpha zulu]", names, err)
	}

	// A missing registry makes every deletion dormant for that reason, not
	// because of any one service, so no names are reported for it.
	if err := os.Remove(regPath); err != nil {
		t.Fatal(err)
	}
	names, err = UnretiredOrphanServices(regPath, ownershipPath)
	if err != nil || len(names) != 0 {
		t.Fatalf("UnretiredOrphanServices() with registry.json missing = %v, %v; want none", names, err)
	}

	// An unreadable ledger is an error, not an empty answer.
	if err := os.WriteFile(ownershipPath, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeValidEmptyRegistry(t, regPath)
	if _, err := UnretiredOrphanServices(regPath, ownershipPath); err == nil {
		t.Fatal("UnretiredOrphanServices() with a malformed ledger returned no error")
	}
}
