package cmd

import (
	"encoding/json"
	"testing"

	"github.com/anydoor7/tslink/internal/inspect"
	"github.com/anydoor7/tslink/internal/output"
	"github.com/anydoor7/tslink/internal/registry"
)

// TestViewSchemaVersionIsAnInteger is A3-5: the envelope, registry.json,
// runtime.json and the manifest carry schema_version as an integer, and the
// data views carried the draft string "vnext.1", which would have become the
// permanent identifier of the first public view schema. Every view now says
// 1, and the MCP output schemas declare an integer.
func TestViewSchemaVersionIsAnInteger(t *testing.T) {
	envelope, err := json.Marshal(output.NewSuccess("list", ListResult{SchemaVersion: inspect.SchemaVersion}))
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		SchemaVersion json.RawMessage `json:"schema_version"`
		Data          struct {
			SchemaVersion json.RawMessage `json:"schema_version"`
		} `json:"data"`
	}
	if err := json.Unmarshal(envelope, &decoded); err != nil {
		t.Fatal(err)
	}
	if string(decoded.SchemaVersion) != "1" || string(decoded.Data.SchemaVersion) != "1" {
		t.Fatalf("envelope schema_version %s, list data schema_version %s; want the integer 1 in both", decoded.SchemaVersion, decoded.Data.SchemaVersion)
	}
	view, err := json.Marshal(inspect.ServiceViewFor(registry.Service{Name: "web", Type: registry.TypeProxy, Target: "http://localhost:3000"}))
	if err != nil {
		t.Fatal(err)
	}
	var viewDecoded struct {
		SchemaVersion json.RawMessage `json:"schema_version"`
	}
	if err := json.Unmarshal(view, &viewDecoded); err != nil || string(viewDecoded.SchemaVersion) != "1" {
		t.Fatalf("service view schema_version = %s (%v), want 1", viewDecoded.SchemaVersion, err)
	}

	checked := 0
	for _, tool := range mcpToolDefinitions {
		var walk func(schema map[string]any)
		walk = func(schema map[string]any) {
			if properties, ok := schema["properties"].(map[string]any); ok {
				if version, ok := properties["schema_version"].(map[string]any); ok {
					checked++
					if version["type"] != "integer" {
						t.Errorf("tool %s declares schema_version as %v", tool.Name, version["type"])
					}
				}
				for _, property := range properties {
					if nested, ok := property.(map[string]any); ok {
						walk(nested)
					}
				}
			}
			if items, ok := schema["items"].(map[string]any); ok {
				walk(items)
			}
		}
		walk(tool.OutputSchema)
	}
	if checked < 5 {
		t.Fatalf("found schema_version in %d output schemas; the walk is blind", checked)
	}
}
