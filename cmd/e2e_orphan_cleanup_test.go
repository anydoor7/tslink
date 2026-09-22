package cmd

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/monody0007/tslink/internal/output"
	"github.com/monody0007/tslink/internal/registry"
	tsruntime "github.com/monody0007/tslink/internal/runtime"
	"github.com/monody0007/tslink/internal/tailapi"
	"github.com/monody0007/tslink/internal/testenv"
	tailscale "tailscale.com/client/tailscale/v2"
)

// Orphan tailnet node convergence, driven end to end through the shipped
// binary against the stateful fake tailnet.
//
// Two incidents are covered. First, the orphan node: `tslink remove` must
// delete exactly the one device whose NodeID this installation durably owns and
// touch nothing else. Second, the degradation family: when the evidence
// this decision rests on is untrustworthy, the number of DELETE calls must be
// zero rather than "whatever the corrupted evidence implies". The dangerous
// direction is unique to this feature: an empty registry file read as "zero
// services" makes every recorded node look like an orphan, which would delete
// the whole tailnet.
//
// The real HTTP client runs inside the child process against a loopback
// httptest server injected via TSLINK_API_BASE_URL. No stubs are installed in
// the tailapi layer; the child performs genuine REST calls and the fake records
// every one of them, so "how many DELETEs happened" is observed, not inferred.
//
// Divergence from the brief, stated plainly: the brief's sequence has a real
// `tslink serve` write the ownership ledger. A real serve would start tsnet
// nodes and contact the Tailscale control plane, and a NodeID only exists once
// a control plane issues one, so the ledger is written here with the product's
// own tsruntime.RecordOwnedNode instead. What that gives up is coverage of the
// daemon-side write path; what it keeps is the entire read-and-authorize path,
// which is where all three incidents actually live.

const e2eFakeAPIKey = "tskey-api-e2e-placeholder-not-a-credential"

func e2eTailnetEnv(configDir, baseURL string) []string {
	env := e2eEnv(configDir)
	// e2eEnv pins TSLINK_API_KEY empty; the later assignment wins in exec.
	return append(env,
		tailapi.APIBaseURLEnv+"="+baseURL,
		"TSLINK_API_KEY="+e2eFakeAPIKey,
	)
}

func e2eCountRequests(requests []testenv.TailnetRequest, method, path string) int {
	count := 0
	for _, r := range requests {
		if r.Method == method && r.Path == path {
			count++
		}
	}
	return count
}

func e2eDeviceHostnames(devices []tailscale.Device) []string {
	names := make([]string, 0, len(devices))
	for _, d := range devices {
		names = append(names, d.Hostname)
	}
	return names
}

// e2eSeedThreeOwnedServices registers three services and records durable exact
// NodeID ownership for each, mirroring the state a daemon leaves behind.
func e2eSeedThreeOwnedServices(t *testing.T, configDir string) (regPath, ownershipPath string) {
	t.Helper()
	regPath = filepath.Join(configDir, "registry.json")
	ownershipPath = filepath.Join(configDir, "node-ownership.json")
	recordedAt := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	for _, svc := range []struct{ name, nodeID string }{
		{"alpha", "node-alpha"},
		{"bravo", "node-bravo"},
		{"charlie", "node-charlie"},
	} {
		if _, err := registry.Add(regPath, registry.Service{
			Name: svc.name, Type: registry.TypeProxy, Target: "http://localhost:3000",
			Tags: []string{"tag:tsmain"},
		}); err != nil {
			t.Fatalf("seed registry %s: %v", svc.name, err)
		}
		if err := tsruntime.RecordOwnedNode(ownershipPath, svc.name, svc.nodeID, recordedAt); err != nil {
			t.Fatalf("seed ownership %s: %v", svc.name, err)
		}
	}
	return regPath, ownershipPath
}

func e2eSeedFakeDevices(t *testing.T, fake *testenv.StatefulTailnet) {
	t.Helper()
	fake.SetDevices([]tailscale.Device{
		{ID: "1", NodeID: "node-alpha", Hostname: "alpha", Tags: []string{"tag:tsmain"}},
		{ID: "2", NodeID: "node-bravo", Hostname: "bravo", Tags: []string{"tag:tsmain"}},
		{ID: "3", NodeID: "node-charlie", Hostname: "charlie", Tags: []string{"tag:tsmain"}},
		{ID: "4", NodeID: "node-unrelated", Hostname: "someone-elses-laptop", Tags: []string{"tag:other"}},
	})
}

func TestE2ERemoveDeletesExactlyTheOwnedDevice(t *testing.T) {
	configDir := t.TempDir()
	binary := compiledTSLinkBinary(t)
	fake := testenv.NewStatefulTailnet(t)
	e2eSeedThreeOwnedServices(t, configDir)
	e2eSeedFakeDevices(t, fake)

	run := e2eRunBinary(t, binary, configDir, "", e2eTailnetEnv(configDir, fake.URL()), "remove", "bravo", "--json")
	if run.ExitCode != output.ExitSuccess {
		t.Fatalf("remove exit=%d stdout=%s stderr=%s", run.ExitCode, run.Stdout, run.Stderr)
	}
	_, data := e2eDecodeEnvelope(t, run, "remove bravo")
	if removed, _ := data["removed"].(bool); !removed {
		t.Fatalf("removed = false, want true; data=%+v", data)
	}
	if cleaned, _ := data["device_cleaned"].(bool); !cleaned {
		t.Fatalf("device_cleaned = false, want true; data=%+v", data)
	}
	if skipped, _ := data["device_cleanup_skipped"].(bool); skipped {
		t.Fatalf("device_cleanup_skipped = true for a fully proven removal; data=%+v", data)
	}
	if warning, _ := data["device_warning"].(string); warning != "" {
		t.Fatalf("device_warning = %q, want none", warning)
	}

	// Exactly one DELETE, aimed at exactly the owned NodeID.
	requests := fake.Requests()
	if got := e2eCountRequests(requests, http.MethodDelete, testenv.TailnetDevicePath("node-bravo")); got != 1 {
		t.Fatalf("DELETE node-bravo count = %d, want 1; requests=%+v", got, requests)
	}
	for _, other := range []string{"node-alpha", "node-charlie", "node-unrelated"} {
		if got := e2eCountRequests(requests, http.MethodDelete, testenv.TailnetDevicePath(other)); got != 0 {
			t.Fatalf("DELETE %s count = %d, want 0", other, got)
		}
	}

	remaining := e2eDeviceHostnames(fake.Devices())
	if len(remaining) != 3 {
		t.Fatalf("devices after removal = %v, want the other three untouched", remaining)
	}
	for _, want := range []string{"alpha", "charlie", "someone-elses-laptop"} {
		found := false
		for _, got := range remaining {
			if got == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("device %q disappeared; remaining=%v", want, remaining)
		}
	}

	// The ledger must also drop the resolved proof so a later reconcile cannot
	// re-target an already deleted node.
	ledger, err := tsruntime.LoadOwnership(filepath.Join(configDir, "node-ownership.json"))
	if err != nil {
		t.Fatalf("load ownership after removal: %v", err)
	}
	for _, node := range ledger.Nodes {
		if node.NodeID == "node-bravo" {
			t.Fatalf("ownership proof for the deleted node survived: %+v", ledger.Nodes)
		}
	}
}

func TestE2ECorruptOwnershipLedgerIssuesZeroDeletes(t *testing.T) {
	configDir := t.TempDir()
	binary := compiledTSLinkBinary(t)
	fake := testenv.NewStatefulTailnet(t)
	_, ownershipPath := e2eSeedThreeOwnedServices(t, configDir)
	e2eSeedFakeDevices(t, fake)

	// Corrupt the one artifact that authorizes deletion.
	if err := os.WriteFile(ownershipPath, []byte("{ this is not json"), 0o600); err != nil {
		t.Fatalf("corrupt ownership ledger: %v", err)
	}

	run := e2eRunBinary(t, binary, configDir, "", e2eTailnetEnv(configDir, fake.URL()), "remove", "bravo", "--json")
	if run.ExitCode != output.ExitSuccess {
		t.Fatalf("remove exit=%d stdout=%s stderr=%s", run.ExitCode, run.Stdout, run.Stderr)
	}
	_, data := e2eDecodeEnvelope(t, run, "remove with corrupt ledger")

	// The local registry edit still succeeds; only the remote action is denied.
	if removed, _ := data["removed"].(bool); !removed {
		t.Fatalf("removed = false; a corrupt ledger must not block the local edit; data=%+v", data)
	}
	if cleaned, _ := data["device_cleaned"].(bool); cleaned {
		t.Fatalf("device_cleaned = true with an unreadable ownership ledger; data=%+v", data)
	}
	if warning, _ := data["device_warning"].(string); warning == "" {
		t.Fatalf("a corrupt ownership ledger must be surfaced, got no device_warning; data=%+v", data)
	}

	// The invariant is stronger than "no DELETE was issued": with unreadable
	// ownership evidence, TSLink must not open a deletion conversation with the
	// tailnet at all. Asserting only "zero DELETEs" would stay green even if the
	// code fell through to a hostname-based cleanup attempt, because a hostname
	// match is separately refused one layer deeper. Asserting "zero requests"
	// pins the guard to this layer, where it actually lives.
	requests := fake.Requests()
	if len(requests) != 0 {
		t.Fatalf("contacted the tailnet %d time(s) with an unreadable ownership ledger; requests=%+v", len(requests), requests)
	}
	if len(fake.Devices()) != 4 {
		t.Fatalf("device count = %d, want all 4 intact", len(fake.Devices()))
	}
}

// The exact-NodeID rule is the single authorization predicate for DELETE. A
// device whose hostname matches a registered service but whose NodeID this
// installation never recorded must be reported as protected and must survive.
// This is the boundary that stops one machine from deleting another machine's
// device just because the names collide.
func TestE2EHostnameMatchNeverAuthorizesDelete(t *testing.T) {
	configDir := t.TempDir()
	binary := compiledTSLinkBinary(t)
	fake := testenv.NewStatefulTailnet(t)

	// "delta" is registered locally but has no ownership proof: the daemon that
	// created the tailnet device was some other installation.
	regPath := filepath.Join(configDir, "registry.json")
	if _, err := registry.Add(regPath, registry.Service{
		Name: "delta", Type: registry.TypeProxy, Target: "http://localhost:3000",
		Tags: []string{"tag:tsmain"},
	}); err != nil {
		t.Fatalf("seed delta: %v", err)
	}
	// A perfectly readable, non-empty ledger that simply contains no proof for
	// delta. The refusal must come from the absence of proof, not from a
	// damaged file: this is the "everything is healthy and it still must not
	// delete" case.
	if err := tsruntime.RecordOwnedNode(filepath.Join(configDir, "node-ownership.json"),
		"epsilon", "node-epsilon", time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("seed unrelated ownership proof: %v", err)
	}
	fake.SetDevices([]tailscale.Device{
		{ID: "9", NodeID: "node-delta-owned-by-someone-else", Hostname: "delta", Tags: []string{"tag:tsmain"}},
	})

	run := e2eRunBinary(t, binary, configDir, "", e2eTailnetEnv(configDir, fake.URL()), "remove", "delta", "--json")
	if run.ExitCode != output.ExitSuccess {
		t.Fatalf("remove exit=%d stdout=%s stderr=%s", run.ExitCode, run.Stdout, run.Stderr)
	}
	_, data := e2eDecodeEnvelope(t, run, "remove hostname-only match")

	if removed, _ := data["removed"].(bool); !removed {
		t.Fatalf("removed = false; the local registry edit must still succeed; data=%+v", data)
	}
	if cleaned, _ := data["device_cleaned"].(bool); cleaned {
		t.Fatalf("device_cleaned = true for a hostname-only match; data=%+v", data)
	}
	if skipped, _ := data["device_cleanup_skipped"].(bool); !skipped {
		t.Fatalf("device_cleanup_skipped = false for a hostname-only match; data=%+v", data)
	}
	reason, _ := data["device_skip_reason"].(string)
	if !strings.Contains(reason, "exact TSLink ownership proof") {
		t.Fatalf("device_skip_reason = %q, want the exact-ownership refusal", reason)
	}

	requests := fake.Requests()
	for _, r := range requests {
		if r.Method == http.MethodDelete {
			t.Fatalf("issued DELETE %s for a hostname-only match; requests=%+v", r.Path, requests)
		}
	}
	devices := fake.Devices()
	if len(devices) != 1 || devices[0].NodeID != "node-delta-owned-by-someone-else" {
		t.Fatalf("hostname-only match was deleted; remaining devices = %+v", devices)
	}
}

func TestE2EStructurallyUntrustedRegistryBlocksMassDelete(t *testing.T) {
	configDir := t.TempDir()
	binary := compiledTSLinkBinary(t)
	fake := testenv.NewStatefulTailnet(t)
	regPath, ownershipPath := e2eSeedThreeOwnedServices(t, configDir)
	e2eSeedFakeDevices(t, fake)

	// Retire every recorded node first. Reconcile has a second, independent
	// guard that refuses to delete orphans of unknown retirement provenance,
	// and while it is armed it produces the same observable outcome as the
	// structural guard. Disarming it is what makes this scenario actually about
	// the structural guard: with retirement recorded, the registry-trust check
	// is the only thing left between a truncated file and a mass delete.
	if err := tsruntime.MarkOwnedNodeIDsRetired(ownershipPath,
		[]string{"node-alpha", "node-bravo", "node-charlie"}, time.Now().UTC()); err != nil {
		t.Fatalf("mark nodes retired: %v", err)
	}

	// Truncate the registry. Every recorded node now looks like an orphan
	// whose service is gone, which is precisely the shape that would authorize
	// deleting the entire tailnet if "empty file" were read as "zero services".
	if err := os.Truncate(regPath, 0); err != nil {
		t.Fatalf("truncate registry: %v", err)
	}

	run := e2eRunBinary(t, binary, configDir, "", e2eTailnetEnv(configDir, fake.URL()),
		"cleanup", "--dry-run=false", "--json")
	if run.ExitCode != output.ExitSuccess {
		t.Fatalf("cleanup exit=%d stdout=%s stderr=%s", run.ExitCode, run.Stdout, run.Stderr)
	}
	_, data := e2eDecodeEnvelope(t, run, "cleanup with truncated registry")

	if skipped, _ := data["device_cleanup_skipped"].(bool); !skipped {
		t.Fatalf("device_cleanup_skipped = false with a structurally untrusted registry; data=%+v", data)
	}
	reason, _ := data["device_skip_reason"].(string)
	// Pin the reason to the structural guard. Accepting any non-empty reason
	// would let a different guard silently stand in for this one, which is
	// exactly how a removed guard stays invisible.
	if !strings.Contains(reason, "structurally untrusted") {
		t.Fatalf("device_skip_reason = %q, want the structural registry-trust refusal; "+
			"a different guard producing the same outcome means this one is untested", reason)
	}
	if provenance, ok := data["device_skip_unknown_provenance"]; ok && provenance != nil {
		t.Fatalf("skip was attributed to unknown retirement provenance (%v), not the structural guard", provenance)
	}
	deleted, _ := data["devices_deleted"].([]any)
	if len(deleted) != 0 {
		t.Fatalf("devices_deleted = %v, want none", deleted)
	}
	t.Logf("structural guard reason: %s", reason)

	requests := fake.Requests()
	for _, r := range requests {
		if r.Method == http.MethodDelete {
			t.Fatalf("issued DELETE %s against a structurally untrusted registry; requests=%+v", r.Path, requests)
		}
	}
	if len(fake.Devices()) != 4 {
		t.Fatalf("device count = %d, want all 4 intact", len(fake.Devices()))
	}
}

// Positive control for the two guard scenarios above. Without it, a cleanup
// implementation that never deletes anything would satisfy both of them, and
// the guards would be indistinguishable from a broken feature.
func TestE2ECleanupDeletesRealOrphansWhenEvidenceIsIntact(t *testing.T) {
	configDir := t.TempDir()
	binary := compiledTSLinkBinary(t)
	fake := testenv.NewStatefulTailnet(t)
	_, ownershipPath := e2eSeedThreeOwnedServices(t, configDir)
	e2eSeedFakeDevices(t, fake)

	// bravo is retired: no registry entry, and a retirement timestamp proving
	// the absence is intentional rather than unexplained.
	regPath := filepath.Join(configDir, "registry.json")
	if _, _, err := registry.RemoveAndReturn(regPath, "bravo"); err != nil {
		t.Fatalf("retire bravo from registry: %v", err)
	}
	if err := tsruntime.MarkOwnedNodeIDsRetired(ownershipPath, []string{"node-bravo"}, time.Now().UTC()); err != nil {
		t.Fatalf("mark bravo retired: %v", err)
	}

	run := e2eRunBinary(t, binary, configDir, "", e2eTailnetEnv(configDir, fake.URL()),
		"cleanup", "--dry-run=false", "--json")
	if run.ExitCode != output.ExitSuccess {
		t.Fatalf("cleanup exit=%d stdout=%s stderr=%s", run.ExitCode, run.Stdout, run.Stderr)
	}
	_, data := e2eDecodeEnvelope(t, run, "cleanup with intact evidence")

	raw, err := json.Marshal(data["devices_deleted"])
	if err != nil {
		t.Fatalf("marshal devices_deleted: %v", err)
	}
	var deleted []string
	if err := json.Unmarshal(raw, &deleted); err != nil {
		t.Fatalf("decode devices_deleted: %v", err)
	}
	if len(deleted) != 1 || deleted[0] != "bravo" {
		t.Fatalf("devices_deleted = %v, want exactly [bravo]; data=%+v", deleted, data)
	}

	requests := fake.Requests()
	if got := e2eCountRequests(requests, http.MethodDelete, testenv.TailnetDevicePath("node-bravo")); got != 1 {
		t.Fatalf("DELETE node-bravo count = %d, want 1", got)
	}
	for _, other := range []string{"node-alpha", "node-charlie", "node-unrelated"} {
		if got := e2eCountRequests(requests, http.MethodDelete, testenv.TailnetDevicePath(other)); got != 0 {
			t.Fatalf("DELETE %s count = %d, want 0", other, got)
		}
	}
	if remaining := e2eDeviceHostnames(fake.Devices()); len(remaining) != 3 {
		t.Fatalf("devices after orphan cleanup = %v, want three", remaining)
	}
}
