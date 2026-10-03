package cmd

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPFunnelTTLMatchesPublishedChoices(t *testing.T) {
	for _, tool := range []string{"share", "add"} {
		t.Run(tool, func(t *testing.T) {
			schema := mcpToolByName(t, tool).InputSchema
			lifetime := schema["properties"].(map[string]any)["funnel_ttl"].(map[string]any)
			choices := lifetime["examples"].([]string)[:5]
			if strings.Join(choices, ",") != "1h,8h,24h,3d,7d" || lifetime["enum"] != nil || !strings.Contains(lifetime["description"].(string), "1h, 8h, 24h, 3d, 7d") {
				t.Fatalf("published choices = %v", choices)
			}
			validValues := append(append([]string{}, choices...), "72h", "90m", "36h", "1w", "1d12h", "168h", "168h0m0s", "1d", "24h0m0s", " 7d")
			for _, ttl := range append(append([]string{}, validValues...), "7D", "garbage", "never", "7d1s", "59m") {
				t.Run(ttl, func(t *testing.T) {
					restoreShareSeams(t)
					paths := mcpSharePaths(t)
					oldEnsure := ensureDaemonFn
					t.Cleanup(func() { ensureDaemonFn = oldEnsure })
					ensureDaemonFn = func(context.Context, io.Writer, bool) error { return nil }
					shareIsRunningFn = func(string) bool { return true }
					shareResolveEndpointOnceFn = func(_ context.Context, _, _, _, name string) (serviceURLResolution, error) {
						return serviceURLResolution{Result: URLResult{Name: name, URL: "https://" + name + ".tail.ts.net"}}, nil
					}
					args := map[string]any{"name": "pub", "target": "localhost:3000", "funnel": true, "public_ack": true, "funnel_ttl": ttl}
					if tool == "add" {
						args["type"] = "proxy"
					}
					raw, _ := json.Marshal(args)
					result, err := callMCPTool(context.Background(), defaultMCPActions(paths, io.Discard), tool, raw)
					if err != nil {
						t.Fatal(err)
					}
					valid := false
					for _, choice := range validValues {
						valid = valid || ttl == choice
					}
					if valid {
						if result.IsError {
							t.Fatalf("schema choice %q refused: %+v", ttl, result.Content)
						}
						validateAgainstToolOutputSchema(t, tool, mcpResultStructured(t, result))
					} else {
						var failure struct {
							Code    string
							Message string
						}
						if err := json.Unmarshal([]byte(result.Content[0].(*mcp.TextContent).Text), &failure); err != nil {
							t.Fatal(err)
						}
						if !result.IsError || result.StructuredContent != nil || failure.Code != "usage_error" || !strings.Contains(failure.Message, "valid examples:") {
							t.Errorf("non-choice %q must be usage_error listing choices; result=%+v failure=%+v", ttl, result, failure)
						}
					}
				})
			}
		})
	}
}
