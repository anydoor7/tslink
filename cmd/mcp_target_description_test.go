package cmd

import (
	"regexp"
	"strings"
	"testing"

	"github.com/anydoor7/tslink/internal/registry"
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

// Check every registered tool, including tools appended by init functions.
// Removing all registered tool names catches templates that differ only by name.
func TestMCPToolDescriptionsDistinct(t *testing.T) {
	names := make([]string, 0, len(mcpToolDefinitions))
	for _, tool := range mcpToolDefinitions {
		names = append(names, regexp.QuoteMeta(tool.Name))
	}
	toolNames := regexp.MustCompile(`\b(?:` + strings.Join(names, "|") + `)\b`)
	normalize := func(description string) string {
		return strings.Join(strings.Fields(toolNames.ReplaceAllString(strings.ToLower(description), "")), " ")
	}
	// Positive control: adding a tool name must not evade the second check.
	if normalize("Owner-only. guest_list browser guest grants.") != normalize("Owner-only. guest_show browser guest grants.") {
		t.Fatal("name normalization did not detect the guest description template")
	}
	for _, check := range []struct {
		name string
		key  func(string) string
	}{
		{"identical", func(s string) string { return s }},
		{"equal_without_tool_names", normalize},
	} {
		t.Run(check.name, func(t *testing.T) {
			seen := map[string]string{}
			for _, tool := range mcpToolDefinitions {
				key := check.key(tool.Description)
				if key == "" {
					t.Errorf("%s has an empty description", tool.Name)
				}
				if previous, ok := seen[key]; ok {
					t.Errorf("%s and %s share a description (%s)", previous, tool.Name, check.name)
				}
				seen[key] = tool.Name
			}
		})
	}
}
