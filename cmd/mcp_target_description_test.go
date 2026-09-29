package cmd

import (
	"strings"
	"testing"

	"github.com/monody0007/tslink/internal/registry"
)

// TestMCPTargetDescriptionsStateTheLiteralOnlyCheck keeps the share and add
// target descriptions to what validation does: it refuses literal addresses
// and one metadata hostname, and never resolves a name. A model reading only
// the tool description must not be told a hostname that resolves to a
// metadata address is refused.
func TestMCPTargetDescriptionsStateTheLiteralOnlyCheck(t *testing.T) {
	for _, tool := range []string{"share", "add"} {
		description := mcpToolByName(t, tool).InputSchema["properties"].(map[string]any)["target"].(map[string]any)["description"].(string)
		for _, want := range []string{"literal", "metadata.google.internal", "hostnames are not resolved"} {
			if !strings.Contains(description, want) {
				t.Errorf("%s target description %q does not say %q", tool, description, want)
			}
		}
	}

	// What the descriptions describe.
	if err := registry.ValidateProxyTarget("http://169.254.169.254:80"); err == nil {
		t.Fatal("literal metadata address accepted")
	}
	if err := registry.ValidateProxyTarget("http://metadata.google.internal:80"); err == nil {
		t.Fatal("metadata.google.internal accepted")
	}
	if err := registry.ValidateProxyTarget("http://169.254.169.254.nip.io:80"); err != nil {
		t.Fatalf("hostname target refused (%v): validation would have to resolve it, and the descriptions say it does not", err)
	}
	if err := registry.ValidateTCPTarget("169.254.169.254.nip.io:80"); err != nil {
		t.Fatalf("hostname tcp target refused: %v", err)
	}
}
