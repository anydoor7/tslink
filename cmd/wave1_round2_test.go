package cmd

import (
	"encoding/json"
	"testing"

	"github.com/anydoor7/tslink/internal/inspect"
	"github.com/anydoor7/tslink/internal/registry"
)

func TestWave1Round2InstallationSchemas(t *testing.T) {
	installed := &DaemonInstalled{Manager: "windows-task-scheduler", Path: "isolated/task", Undo: "tslink uninstall"}
	for name, schema := range map[string]map[string]any{
		"add": mcpAddOutputSchema, "share": mcpShareOutputSchema,
		"template_apply": mcpTemplateApplyOutputSchema, "recipe_apply": recipeApplyOutputSchema(),
	} {
		t.Run(name, func(t *testing.T) {
			properties := schema["properties"].(map[string]any)
			data, err := json.Marshal(installed)
			if err != nil {
				t.Fatal(err)
			}
			var value any
			if err := json.Unmarshal(data, &value); err != nil {
				t.Fatal(err)
			}
			if err := validateMCPJSONSchema(properties["daemon_installed"].(map[string]any), value, name); err != nil {
				t.Fatal(err)
			}
			if name == "add" || name == "share" {
				for _, field := range []string{"preserve_host", "request_limits"} {
					if properties[field] == nil {
						t.Fatalf("installation schema lost %s", field)
					}
				}
			} else {
				var serviceSchema map[string]any
				if name == "recipe_apply" {
					serviceSchema = properties["service"].(map[string]any)
				} else {
					serviceSchema = properties["services"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)["service"].(map[string]any)
				}
				for _, field := range []string{"preserve_host", "request_limits"} {
					if serviceSchema["properties"].(map[string]any)[field] == nil {
						t.Fatalf("installation service schema lost %s", field)
					}
				}
			}
		})
	}
	// Use the actual registry service projection to check that the extended
	// fields remain valid when a scheduler installation receipt is available.
	svc := registry.Service{Name: "photos", Type: registry.TypeProxy, Target: "http://localhost:2283", PreserveHost: true, Health: &registry.HealthConfig{Path: "/ready"}, RequestLimits: registry.RecommendedUploadLimits()}
	data, err := json.Marshal(inspect.ServiceViewFor(svc))
	if err != nil {
		t.Fatal(err)
	}
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	if err := validateMCPJSONSchema(mcpServiceViewSchema, value, "service"); err != nil {
		t.Fatal(err)
	}
}
