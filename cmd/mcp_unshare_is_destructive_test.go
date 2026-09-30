package cmd

import (
	"strings"
	"testing"
)

// TestMCPUnshareSaysWhatItDeletes is A3-6's unshare finding: the unshare
// description said it removed a service from the local registry and "does not
// expose credentials or open a network listener", while the call also deletes
// the service's tailnet device through the Tailscale API and its local node
// state; the instructions' confirmation list left it out; and the manifest's
// high-risk operations named neither tslink remove, nor unshare, nor the
// daemon's own deletions. Each now says so.
func TestMCPUnshareSaysWhatItDeletes(t *testing.T) {
	description := mcpToolByName(t, "unshare").Description
	first, _, _ := strings.Cut(description, ". ")
	for _, want := range []string{"Deletes", "tailnet device", "confirm"} {
		if !strings.Contains(first, want) {
			t.Errorf("unshare first sentence = %q, want %q", first, want)
		}
	}
	for _, want := range []string{"Tailscale API", "exact recorded NodeID", "local node state", "idempotent"} {
		if !strings.Contains(description, want) {
			t.Errorf("unshare description = %q, want %q", description, want)
		}
	}
	if strings.Contains(description, "does not expose credentials or open a network listener") {
		t.Errorf("unshare description still reads as local-only: %q", description)
	}

	confirm := mcpInstructions[strings.Index(mcpInstructions, "Confirm with the user"):]
	if !strings.Contains(confirm, "unshare") {
		t.Errorf("instructions confirmation list = %q, want unshare in it", confirm)
	}
}

func TestManifestListsEveryDeletionAsHighRisk(t *testing.T) {
	m := Manifest()
	find := func(command, operation string) *HighRiskOperation {
		for i := range m.HighRiskOperations {
			op := &m.HighRiskOperations[i]
			if op.Command == command && strings.Contains(op.Operation, operation) {
				return op
			}
		}
		t.Fatalf("no high-risk operation %q for %s in %+v", operation, command, m.HighRiskOperations)
		return nil
	}
	remove := find("tslink remove", "tailnet device")
	unshare := find("tslink mcp", "unshare")
	reconcile := find("tslink serve", "reconciliation")
	for _, op := range []*HighRiskOperation{remove, unshare, reconcile} {
		if op.Default != "enabled" || len(op.RequiredFlags) != 0 {
			t.Errorf("%s %q: default %q, flags %v; these deletions need no flag", op.Command, op.Operation, op.Default, op.RequiredFlags)
		}
		if !strings.Contains(op.Boundary, "NodeID") {
			t.Errorf("%s %q boundary = %q, want the exact NodeID proof named", op.Command, op.Operation, op.Boundary)
		}
	}
	if !strings.Contains(unshare.Boundary, "serve --mcp") {
		t.Errorf("unshare boundary = %q, want the remote control plane named", unshare.Boundary)
	}
}

// TestMCPControlURLSaysTheStoredCredentialNeverReachesIt: the add tool's
// control_url accepts any server, and what keeps the stored Tailscale
// credential from it (B1-2's credential_control_url_mismatch refusal) was
// stated nowhere a model reads.
func TestMCPControlURLSaysTheStoredCredentialNeverReachesIt(t *testing.T) {
	properties := mcpToolByName(t, "add").InputSchema["properties"].(map[string]any)
	description := properties["control_url"].(map[string]any)["description"].(string)
	for _, want := range []string{"never", "stored Tailscale credential", "credential_control_url_mismatch"} {
		if !strings.Contains(description, want) {
			t.Errorf("add control_url description = %q, want %q", description, want)
		}
	}
}
