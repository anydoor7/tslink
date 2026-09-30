package cmd

import (
	"os"
	"strings"
	"testing"

	"github.com/monody0007/tslink/internal/registry"
	tsruntime "github.com/monody0007/tslink/internal/runtime"
	"github.com/monody0007/tslink/internal/tailapi"
)

func assertStillRegistered(t *testing.T, regPath, name string) {
	t.Helper()
	reg, err := registry.Load(regPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, svc := range reg.Services {
		if svc.Name == name {
			return
		}
	}
	t.Fatalf("%q was unregistered; services = %+v", name, reg.Services)
}

// remove refuses, with the ledger's coded error, before it changes anything
// when the ownership ledger cannot be read: unregistering the service anyway
// would leave rows nobody retired, which withhold its device deletion.
func TestRemoveRefusesWhenTheOwnershipLedgerCannotBeRead(t *testing.T) {
	regPath, ownershipPath, stateDir := removeNodeStateFixture(t, "web")
	stubDaemonRunning(t, false)
	stubDeleteDevices(t, tailapi.CleanupResult{}, nil)
	if err := os.WriteFile(ownershipPath, []byte("{ not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := removeServiceResult(regPath, ownershipPath, "web")
	code, coded := registry.ErrorCode(err)
	if err == nil || !coded || code != "internal_error" || !strings.Contains(err.Error(), ownershipPath) {
		t.Fatalf("removeServiceResult() error = %v (code %q, coded %v), want the ledger's coded refusal naming %s", err, code, coded, ownershipPath)
	}
	assertStillRegistered(t, regPath, "web")
	assertStateDir(t, stateDir, true)
}

// The retirement is recorded before the registry entry goes: when it cannot be
// recorded, the service stays registered instead of becoming an orphan whose
// rows say nothing about its removal.
func TestRemoveRecordsRetirementBeforeUnregistering(t *testing.T) {
	regPath, ownershipPath, _ := removeNodeStateFixture(t, "web")
	stubDaemonRunning(t, false)
	stubDeleteDevices(t, tailapi.CleanupResult{}, nil)
	// The ledger stays readable, but its lock cannot be taken, so no write to
	// it can succeed.
	if err := os.Remove(ownershipPath + ".lock"); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := os.Mkdir(ownershipPath+".lock", 0o700); err != nil {
		t.Fatal(err)
	}
	result, err := removeServiceResult(regPath, ownershipPath, "web")
	if err == nil || !strings.Contains(err.Error(), "before unregistering") {
		t.Fatalf("removeServiceResult() = %+v, %v; want a refusal because the retirement could not be recorded", result, err)
	}
	assertStillRegistered(t, regPath, "web")
	ledger, err := tsruntime.LoadOwnership(ownershipPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Nodes) != 1 || ledger.Nodes[0].RetiredAt != nil {
		t.Fatalf("ledger = %+v, want web's row unchanged", ledger.Nodes)
	}
}

// When the registry write fails after the retirement was recorded, the rows go
// back to what they were: a still-registered service must not carry a
// retirement that a later lost registry would turn into a deletion.
func TestRemoveRestoresOwnershipRowsWhenUnregisteringFails(t *testing.T) {
	regPath, ownershipPath, _ := removeNodeStateFixture(t, "web")
	stubDaemonRunning(t, false)
	stubDeleteDevices(t, tailapi.CleanupResult{}, nil)
	before, err := tsruntime.LoadOwnership(ownershipPath)
	if err != nil {
		t.Fatal(err)
	}
	// An invalid entry makes every registry mutation refuse the document.
	data, err := os.ReadFile(regPath)
	if err != nil {
		t.Fatal(err)
	}
	broken := strings.Replace(string(data), `"services": [`, `"services": [{"name":"typo","type":"proxy","target":"http://localhost:1","tags":["tag:Bad"]},`, 1)
	if broken == string(data) {
		t.Fatalf("fixture registry has an unexpected shape:\n%s", data)
	}
	if err := os.WriteFile(regPath, []byte(broken), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := removeServiceResult(regPath, ownershipPath, "web"); err == nil {
		t.Fatal("removeServiceResult() succeeded against a registry every mutation refuses")
	}
	assertStillRegistered(t, regPath, "web")
	after, err := tsruntime.LoadOwnership(ownershipPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Nodes) != 1 || after.Nodes[0].RetiredAt != nil || !after.Nodes[0].RecordedAt.Equal(before.Nodes[0].RecordedAt) {
		t.Fatalf("ledger after the failed removal = %+v, want %+v", after.Nodes, before.Nodes)
	}
}
