package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/spf13/pflag"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/recipes"
	"github.com/anydoor7/tslink/internal/registry"
)

func stubAppsDetection(t *testing.T) {
	t.Helper()
	old := appsDetectFn
	t.Cleanup(func() { appsDetectFn = old })
	appsDetectFn = func(context.Context, []recipes.RegisteredService) (recipes.Detection, error) {
		return recipes.Detection{SchemaVersion: 1, Listeners: []recipes.Listener{}, Matches: []recipes.Match{}, Complete: true, Warnings: []string{}}, nil
	}
}
func recipeTestRegistry(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv(config.ConfigDirEnv, dir)
	path := filepath.Join(dir, "registry.json")
	withTemplateRegistryPath(t, path)
	old := ensureDaemonFn
	ensureDaemonFn = func(context.Context, io.Writer, bool) error { return nil }
	t.Cleanup(func() { ensureDaemonFn = old })
	return path
}
func TestRecipePlanApply(t *testing.T) {
	path := recipeTestRegistry(t)
	req := recipeRequest{RecipeID: "jellyfin", NoDaemonInstall: true}
	plan, err := applyRecipe(context.Background(), req, path, true, io.Discard)
	if err != nil || plan.Action != "create" || plan.Applied || !plan.DryRun || plan.Requested.Name != "jellyfin" {
		t.Fatalf("plan=%+v err=%v", plan, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("preview wrote registry: %v", err)
	}
	applied, err := applyRecipe(context.Background(), req, path, false, io.Discard)
	if err != nil || !applied.Applied || applied.Action != "created" || applied.DryRun {
		t.Fatalf("apply=%+v err=%v", applied, err)
	}
	reg, err := registry.Load(path)
	if err != nil || len(reg.Services) != 1 || reg.Services[0].Target != "http://127.0.0.1:8096" || reg.Services[0].Funnel {
		t.Fatalf("saved=%+v err=%v", reg, err)
	}
	before, _ := os.ReadFile(path)
	req.Target = "127.0.0.1:9999"
	skipped, err := applyRecipe(context.Background(), req, path, false, io.Discard)
	after, _ := os.ReadFile(path)
	if err != nil || skipped.Applied || skipped.Action != "skip_existing" || string(before) != string(after) || skipped.Service.Backend.Display == skipped.Requested.Backend.Display {
		t.Fatalf("existing replaced or misreported: %+v %v", skipped, err)
	}
}
func TestRecipeSafetyRefusals(t *testing.T) {
	path := recipeTestRegistry(t)
	for _, id := range []string{"ollama", "comfyui", "jupyter", "portainer", "syncthing", "vaultwarden", "generic-web"} {
		t.Run(id, func(t *testing.T) {
			req := recipeRequest{RecipeID: id, Funnel: true, PublicAck: true}
			for _, dry := range []bool{true, false} {
				_, err := applyRecipe(context.Background(), req, path, dry, io.Discard)
				if err == nil || !strings.Contains(err.Error(), "DANGER:") {
					t.Fatalf("never-public guard missing: %v", err)
				}
			}
			req.ForceUnsafePublic = true
			plan, err := applyRecipe(context.Background(), req, path, true, io.Discard)
			if err != nil || !strings.Contains(strings.Join(plan.Warnings, " "), "overrode") {
				t.Fatalf("explicit override: %+v %v", plan, err)
			}
		})
	}
	for _, tc := range []struct {
		req     recipeRequest
		message string
	}{
		{recipeRequest{RecipeID: "missing"}, "not found"},
		{recipeRequest{RecipeID: "ollama", ForceUnsafePublic: true}, "requires Funnel"},
		{recipeRequest{RecipeID: "jellyfin", Target: "192.0.2.1:8096"}, "loopback"},
		{recipeRequest{RecipeID: "jellyfin", Funnel: true}, "public"},
		{recipeRequest{RecipeID: "jellyfin", PublicAck: true}, "only be used"},
		{recipeRequest{RecipeID: "jellyfin", FunnelTTL: recipeTTL("1h")}, "only be used"},
		{recipeRequest{RecipeID: "jellyfin", Name: "BAD NAME"}, "name"},
	} {
		_, err := applyRecipe(context.Background(), tc.req, path, false, io.Discard)
		if err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tc.message)) {
			t.Fatalf("refusal cause %q: %v", tc.message, err)
		}
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("refused operation wrote registry: %v", err)
	}
}
func TestRecipeRaceAndSetupFailure(t *testing.T) {
	path := recipeTestRegistry(t)
	old := recipeAddIfMissingFn
	t.Cleanup(func() { recipeAddIfMissingFn = old })
	recipeAddIfMissingFn = func(p string, s registry.Service) (bool, error) {
		s.Target = "http://127.0.0.1:9999"
		_, err := registry.AddIfMissing(p, s)
		return false, err
	}
	result, err := applyRecipe(context.Background(), recipeRequest{RecipeID: "jellyfin"}, path, false, io.Discard)
	if err != nil || result.Applied || result.Action != "skip_existing" || !strings.Contains(result.Service.Backend.Display, "9999") {
		t.Fatalf("race=%+v %v", result, err)
	}
	recipeAddIfMissingFn = old
	ensureDaemonFn = func(context.Context, io.Writer, bool) error {
		return registry.CodedError{Code: "daemon_setup_failed", Message: "fake setup failure"}
	}
	_, err = applyRecipe(context.Background(), recipeRequest{RecipeID: "ollama"}, path, false, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "Configuration remains") {
		t.Fatalf("retained error: %v", err)
	}
	reg, _ := registry.Load(path)
	if len(reg.Services) != 2 {
		t.Fatalf("setup lost registry: %+v", reg)
	}
}
func TestRecipeDetectionRegistry(t *testing.T) {
	path := recipeTestRegistry(t)
	stubAppsDetection(t)
	_, err := registry.Add(path, registry.Service{Name: "photos", Type: registry.TypeProxy, Target: "http://127.0.0.1:2283"})
	if err != nil {
		t.Fatal(err)
	}
	appsDetectFn = func(_ context.Context, services []recipes.RegisteredService) (recipes.Detection, error) {
		if !reflect.DeepEqual(services, []recipes.RegisteredService{{Name: "photos", Target: "http://127.0.0.1:2283"}}) {
			t.Fatalf("registry=%v", services)
		}
		return recipes.Detection{SchemaVersion: 1, Complete: true}, nil
	}
	if _, err := detectApps(context.Background(), path); err != nil {
		t.Fatal(err)
	}
}
func resetRecipeFlags(t *testing.T) {
	t.Helper()
	for _, args := range [][]string{{"add"}, {"apps", "share"}} {
		c, _, err := rootCmd.Find(args)
		if err != nil {
			t.Fatal(err)
		}
		c.Flags().VisitAll(func(f *pflag.Flag) {
			if err := f.Value.Set(f.DefValue); err != nil {
				t.Fatal(err)
			}
			f.Changed = false
		})
	}
	resetRootJSONFlag(t)
	rootCmd.SetArgs(nil)
}
func runRecipeRoot(t *testing.T, args ...string) (string, error) {
	t.Helper()
	resetRecipeFlags(t)
	t.Cleanup(func() { resetRecipeFlags(t) })
	rootCmd.SetArgs(args)
	var err error
	out := captureStdout(t, func() { err = rootCmd.Execute() })
	return out, err
}
func TestRecipeCLI(t *testing.T) {
	path := recipeTestRegistry(t)
	stubAppsDetection(t)
	for _, args := range [][]string{{"apps", "share", "jellyfin", "--json"}, {"add", "--recipe", "jellyfin", "--json"}, {"add", "movies", "--recipe", "jellyfin", "--json", "--yes", "--dry-run"}} {
		raw, err := runRecipeRoot(t, args...)
		if err != nil {
			t.Fatal(err)
		}
		var frame struct {
			OK            bool         `json:"ok"`
			SchemaVersion int          `json:"schema_version"`
			Data          RecipeResult `json:"data"`
		}
		if err := json.Unmarshal([]byte(raw), &frame); err != nil {
			t.Fatal(err)
		}
		if !frame.OK || frame.SchemaVersion != 1 || !frame.Data.DryRun {
			t.Fatalf("preview envelope: %s", raw)
		}
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("CLI preview wrote: %v", err)
	}
	raw, err := runRecipeRoot(t, "apps", "share", "jellyfin", "--yes", "--no-daemon-install", "--json")
	if err != nil || !strings.Contains(raw, `"applied":true`) {
		t.Fatalf("CLI apply: %s %v", raw, err)
	}
	for _, args := range [][]string{{"apps", "list", "--json"}, {"apps", "detect", "--json"}, {"apps", "list"}, {"apps", "detect"}, {"apps", "share", "home-assistant"}} {
		out, err := runRecipeRoot(t, args...)
		if err != nil || out == "" {
			t.Fatalf("CLI %v: %s %v", args, out, err)
		}
	}
	for _, args := range [][]string{{"add", "--json"}, {"add", "x", "--yes"}, {"add", "x", "--recipe", "jellyfin", "--tcp", "1234"}, {"apps", "share", "ollama", "--funnel", "--public", "--yes"}} {
		_, err := runRecipeRoot(t, args...)
		if err == nil {
			t.Fatalf("invalid CLI accepted: %v", args)
		}
	}
}
func TestRecipeMCP(t *testing.T) {
	path := recipeTestRegistry(t)
	stubAppsDetection(t)
	paths := sharePaths{Registry: path}
	actions := defaultMCPActions(paths, io.Discard)
	for _, name := range []string{"recipe_list", "apps_detect", "recipe_plan", "recipe_apply"} {
		args := json.RawMessage(`{}`)
		if strings.HasPrefix(name, "recipe_") && name != "recipe_list" {
			args = json.RawMessage(`{"recipe_id":"jellyfin","no_daemon_install":true}`)
		}
		result, err := callMCPTool(context.Background(), actions, name, args)
		if err != nil || result.IsError {
			t.Fatalf("%s: %v %+v", name, err, result)
		}
		validateAgainstToolOutputSchema(t, name, result.StructuredContent)
	}
	for _, name := range []string{"recipe_list", "apps_detect", "recipe_plan", "recipe_apply"} {
		result, err := callMCPTool(context.Background(), actions, name, json.RawMessage(`{"wrong":true}`))
		if err != nil || !result.IsError {
			t.Fatalf("bad args %s: %v %+v", name, err, result)
		}
	}
	for _, name := range []string{"recipe_plan", "recipe_apply"} {
		result, err := callMCPTool(context.Background(), actions, name, json.RawMessage(`{}`))
		if err != nil || !result.IsError {
			t.Fatalf("missing recipe_id accepted by %s: %v %+v", name, err, result)
		}
		failure := mcpToolResultFailure(t, result)
		if !strings.Contains(failure.Error.Message, "recipe_id is required") {
			t.Fatalf("missing recipe_id refusal cause: %+v", failure)
		}
	}
	result, _ := callMCPTool(context.Background(), actions, "recipe_apply", json.RawMessage(`{"recipe_id":"ollama","funnel":true,"public_ack":true}`))
	failure := mcpToolResultFailure(t, result)
	if !strings.Contains(failure.Error.Message, "DANGER") {
		t.Fatalf("MCP safety cause: %+v", failure)
	}
	// Exercise the real SDK's declared input and output schemas.
	input := initializedMCPInput(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"recipe_plan","arguments":{"recipe_id":"jupyter"}}}`)
	stdout := runMCPSession(t, input, actions)
	frame := mcpFrameByID(t, decodeMCPResponses(t, stdout), float64(2))
	if frame["result"] == nil || frame["error"] != nil {
		t.Fatalf("SDK recipe tool: %v", frame)
	}
}

func recipeTTL(s string) *string { return &s }

func TestRecipeFailureCauses(t *testing.T) {
	t.Run("bad registry", func(t *testing.T) {
		path := recipeTestRegistry(t)
		if err := os.WriteFile(path, []byte("broken JSON"), 0600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := planRecipe(recipeRequest{RecipeID: "jellyfin"}, path); err == nil {
			t.Fatal("invalid registry accepted")
		}
		if _, err := detectApps(context.Background(), path); err == nil {
			t.Fatal("discovery hid invalid registry")
		}
	})
	t.Run("directory", func(t *testing.T) {
		path := recipeTestRegistry(t)
		ensureDirFn = func() error { return fmt.Errorf("fixture mkdir failed") }
		_, err := applyRecipe(context.Background(), recipeRequest{RecipeID: "jellyfin"}, path, false, io.Discard)
		if err == nil || !strings.Contains(err.Error(), "mkdir failed") {
			t.Fatalf("directory error lost: %v", err)
		}
	})
	t.Run("write", func(t *testing.T) {
		path := recipeTestRegistry(t)
		old := recipeAddIfMissingFn
		t.Cleanup(func() { recipeAddIfMissingFn = old })
		recipeAddIfMissingFn = func(string, registry.Service) (bool, error) { return false, fmt.Errorf("fixture write failed") }
		_, err := applyRecipe(context.Background(), recipeRequest{RecipeID: "jellyfin"}, path, false, io.Discard)
		if err == nil || !strings.Contains(err.Error(), "write failed") {
			t.Fatalf("write error lost: %v", err)
		}
		recipeAddIfMissingFn = func(string, registry.Service) (bool, error) { return true, nil }
		_, err = applyRecipe(context.Background(), recipeRequest{RecipeID: "jellyfin"}, path, false, io.Discard)
		if err == nil || !strings.Contains(err.Error(), "persisted service not found") {
			t.Fatalf("missing persisted service hidden: %v", err)
		}
	})
	t.Run("CLI path", func(t *testing.T) {
		recipeTestRegistry(t)
		registryPathFn = func() (string, error) { return "", fmt.Errorf("fixture registry path failed") }
		for _, args := range [][]string{{"apps", "share", "jellyfin"}, {"apps", "detect"}} {
			_, err := runRecipeRoot(t, args...)
			if err == nil || !strings.Contains(err.Error(), "registry path failed") {
				t.Fatalf("CLI path error lost: %v", err)
			}
		}
	})
}
func TestRecipeCLIOptionsAndDetectionOutput(t *testing.T) {
	recipeTestRegistry(t)
	stubAppsDetection(t)
	appsDetectFn = func(context.Context, []recipes.RegisteredService) (recipes.Detection, error) {
		return recipes.Detection{}, fmt.Errorf("fixture enumeration failed")
	}
	_, err := runRecipeRoot(t, "apps", "detect")
	if err == nil || !strings.Contains(err.Error(), "enumeration failed") {
		t.Fatalf("discovery failure hidden: %v", err)
	}
	appsDetectFn = func(context.Context, []recipes.RegisteredService) (recipes.Detection, error) {
		return recipes.Detection{Matches: []recipes.Match{{RecipeID: "jellyfin", Target: "http://127.0.0.1:8096", Confidence: "high", Registered: []string{"family-tv"}}}, Warnings: []string{"fixture incomplete"}}, nil
	}
	out, err := runRecipeRoot(t, "apps", "detect")
	if err != nil || !strings.Contains(out, "family-tv") || !strings.Contains(out, "high") {
		t.Fatalf("discovery rendering: %s %v", out, err)
	}
	out, err = runRecipeRoot(t, "apps", "share", "jellyfin", "--name", "movies", "--proxy", "127.0.0.1:9999", "--funnel", "--public", "--funnel-ttl", "1h", "--json")
	if err != nil || !strings.Contains(out, "movies") || !strings.Contains(out, "9999") {
		t.Fatalf("CLI overrides: %s %v", out, err)
	}
	_, err = runRecipeRoot(t, "apps", "share", "jellyfin", "--funnel", "--public", "--funnel-ttl", "")
	if err == nil || !strings.Contains(err.Error(), "TTL must be one of") {
		t.Fatalf("explicit empty TTL: %v", err)
	}
	_, err = runRecipeRoot(t, "apps", "share", "ollama", "--yes", "--funnel", "--public", "--force-unsafe-public", "--no-daemon-install")
	if err != nil {
		t.Fatalf("explicit unsafe override: %v", err)
	}
}
