package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"testing"

	"github.com/anydoor7/tslink/internal/tailapi"
)

// TestMCPUnshareReturnsWhatRemoveReturns is A3-5's unshare finding: the MCP
// unshare tool answered with a summary of its own that added ok and dropped
// node_state_kept_reason, so an agent was never told that a service's local
// node state was kept because some of its tailnet nodes were neither deleted
// nor confirmed absent. unshare now returns tslink remove's result itself.
func TestMCPUnshareReturnsWhatRemoveReturns(t *testing.T) {
	stubDaemonRunning(t, false)
	for name, cleanup := range map[string]tailapi.CleanupResult{
		"partial resolution": {Deleted: []string{"web"}, ResolvedOwnershipIDs: []string{"node-other"}},
		"deleted":            {Deleted: []string{"web"}, ResolvedOwnershipIDs: []string{"node-web"}},
		"skipped":            {Skipped: true, SkipReason: tailapi.ErrNoAPIClient.Error()},
	} {
		t.Run(name, func(t *testing.T) {
			stubDeleteDevices(t, cleanup, nil)

			regPath, ownershipPath, _ := removeNodeStateFixture(t, "web")
			cli, err := removeServiceResult(regPath, ownershipPath, "web")
			if err != nil {
				t.Fatal(err)
			}
			regPath, ownershipPath, _ = removeNodeStateFixture(t, "web")
			viaMCP, err := defaultMCPActions(sharePaths{Registry: regPath, Ownership: ownershipPath}, io.Discard).unshare(context.Background(), "web")
			if err != nil {
				t.Fatal(err)
			}

			cliJSON, _ := json.Marshal(cli)
			mcpJSON, _ := json.Marshal(viaMCP)
			if !bytes.Equal(cliJSON, mcpJSON) {
				t.Fatalf("MCP unshare = %s\ntslink remove = %s\nwant the same result", mcpJSON, cliJSON)
			}
			if name == "partial resolution" && cli.NodeStateKeptReason == "" {
				t.Fatal("fixture: tslink remove reported no node_state_kept_reason for a partial resolution")
			}
			validateAgainstToolOutputSchema(t, "unshare", viaMCP)
		})
	}

	// An absent service: remove's idempotent success, removed false.
	regPath, ownershipPath, _ := removeNodeStateFixture(t, "web")
	viaMCP, err := defaultMCPActions(sharePaths{Registry: regPath, Ownership: ownershipPath}, io.Discard).unshare(context.Background(), "gone")
	if err != nil {
		t.Fatal(err)
	}
	if mcpJSON, _ := json.Marshal(viaMCP); string(mcpJSON) != `{"name":"gone","removed":false,"device_cleaned":false,"device_cleanup_skipped":false}` {
		t.Fatalf("MCP unshare of an absent service = %s", mcpJSON)
	}
	validateAgainstToolOutputSchema(t, "unshare", viaMCP)
}
