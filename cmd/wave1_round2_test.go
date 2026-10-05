package cmd

import (
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/inspect"
	"github.com/anydoor7/tslink/internal/registry"
	"github.com/anydoor7/tslink/internal/testwait"
)

func TestWave1Round2CaptureLargeOutput(t *testing.T) {
	wanted := strings.Repeat("recipe catalog output\n", 8192)
	var writeErr error
	actual := captureStdout(t, func() {
		pipe := os.Stdout
		// A broken capture is unblocked through this test-owned pipe, so the
		// regression reports a write/assertion error instead of hanging the suite.
		guard := time.AfterFunc(testwait.Budget(t), func() { pipe.Close() })
		defer guard.Stop()
		_, writeErr = io.WriteString(pipe, wanted)
	})
	if writeErr != nil || actual != wanted {
		t.Fatalf("output capture blocked or truncated: bytes=%d want=%d write=%v", len(actual), len(wanted), writeErr)
	}
}

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
