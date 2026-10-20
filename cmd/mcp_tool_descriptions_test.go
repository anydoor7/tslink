package cmd

import (
	"encoding/json"
	"os"
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
	t.Run("near_duplicates", func(t *testing.T) {
		checkMCPSiblingDescriptionSimilarity(t, mcpToolDefinitions)
	})
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

// The largest current sibling score is 0.723 (people_add/people_update),
// whose shared safety rules are intentional. A 0.80 ceiling leaves room for
// wording changes while rejecting the base guest template (1.0), even with
// a verb changed. This measures shared vocabulary, not semantic equivalence.
const mcpSiblingDescriptionSimilarityLimit = 0.80

func checkMCPSiblingDescriptionSimilarity(t *testing.T, tools []mcpToolDefinition) {
	t.Helper()
	names := make([]string, 0, len(tools))
	descriptions := make(map[string]string, len(tools))
	for _, tool := range tools {
		names = append(names, regexp.QuoteMeta(tool.Name))
		descriptions[tool.Name] = tool.Description
	}
	toolNames := regexp.MustCompile(`\b(?:` + strings.Join(names, "|") + `)\b`)
	wordPattern := regexp.MustCompile(`[a-z0-9_]+`)
	words := func(description string) map[string]bool {
		set := map[string]bool{}
		for _, word := range wordPattern.FindAllString(toolNames.ReplaceAllString(strings.ToLower(description), ""), -1) {
			set[word] = true
		}
		return set
	}
	similarity := func(a, b string) float64 {
		left, right := words(a), words(b)
		intersection := 0
		for word := range left {
			if right[word] {
				intersection++
			}
		}
		union := len(left) + len(right) - intersection
		if union == 0 {
			return 1
		}
		return float64(intersection) / float64(union)
	}
	// Base 9d401ca's guest body, with names changed and then one verb swapped.
	baseGuest := "Owner-only. %s browser guest grants for one app. Uses local state only; Funnel availability is reported separately. Tokens are shown only when explicitly requested; no OIDC or tailnet account is required."
	controlLeft := strings.Replace(baseGuest, "%s", "guest_list", 1)
	controlRight := strings.Replace(baseGuest, "%s", "guest_show", 1)
	controlRight = strings.Replace(controlRight, "Uses", "Reads", 1)
	if score := similarity(controlLeft, controlRight); score < mcpSiblingDescriptionSimilarityLimit {
		t.Fatalf("changed-verb guest template escaped similarity guard: %.6f", score)
	} else {
		t.Logf("changed-verb guest template positive control: %.6f", score)
	}
	// Glama's named sibling families, plus template and recipe selection pairs.
	for _, family := range [][]string{
		{"access_log", "access_summary"},
		{"people_add", "people_update"},
		{"people_grant", "people_revoke"},
		{"guest_create", "guest_list", "guest_show", "guest_revoke"},
		{"template_list", "template_plan", "template_apply"},
		{"recipe_list", "recipe_plan", "recipe_apply"},
	} {
		for i, left := range family {
			for _, right := range family[i+1:] {
				a, haveLeft := descriptions[left]
				b, haveRight := descriptions[right]
				if !haveLeft || !haveRight || a == "" || b == "" {
					t.Errorf("missing sibling description: %s / %s", left, right)
					continue
				}
				score := similarity(a, b)
				t.Logf("%s / %s Jaccard=%.6f (must be < %.2f)", left, right, score, mcpSiblingDescriptionSimilarityLimit)
				if score >= mcpSiblingDescriptionSimilarityLimit {
					t.Errorf("%s and %s have near-duplicate descriptions: %.6f >= %.2f", left, right, score, mcpSiblingDescriptionSimilarityLimit)
				}
			}
		}
	}
}

// Use the same guard on a real tools/list capture when explicitly supplied.
func TestMCPLiveToolDescriptionsDistinct(t *testing.T) {
	path := os.Getenv("MCP_DESCRIPTION_TOOLS_JSON")
	if path == "" {
		t.Skip("set MCP_DESCRIPTION_TOOLS_JSON to a tools/list tools array")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var tools []mcpToolDefinition
	if err := json.Unmarshal(data, &tools); err != nil {
		t.Fatal(err)
	}
	if len(tools) != 48 {
		t.Fatalf("tools/list returned %d tools, want 48", len(tools))
	}
	seen := map[string]string{}
	for _, tool := range tools {
		if tool.Description == "" {
			t.Errorf("%s has an empty description", tool.Name)
		}
		if previous, ok := seen[tool.Description]; ok {
			t.Errorf("%s and %s share a live description", previous, tool.Name)
		}
		seen[tool.Description] = tool.Name
	}
	checkMCPSiblingDescriptionSimilarity(t, tools)
}
