package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monody0007/tslink/internal/inspect"
	"github.com/monody0007/tslink/internal/output"
	"github.com/monody0007/tslink/internal/registry"
)

type templateListJSONResponse struct {
	OK      bool               `json:"ok"`
	Command string             `json:"command"`
	Data    TemplateListResult `json:"data"`
}

type templateShowJSONResponse struct {
	OK      bool               `json:"ok"`
	Command string             `json:"command"`
	Data    TemplateShowResult `json:"data"`
}

type templateApplyJSONResponse struct {
	OK      bool                `json:"ok"`
	Command string              `json:"command"`
	Data    TemplateApplyResult `json:"data"`
}

func resetTemplateCommandFlags(t *testing.T) {
	t.Helper()
	if err := rootCmd.PersistentFlags().Set("json", "false"); err != nil {
		t.Fatalf("reset json flag: %v", err)
	}
	applyCmd, _, err := rootCmd.Find([]string{"template", "apply"})
	if err != nil {
		t.Fatalf("find template apply: %v", err)
	}
	if err := applyCmd.Flags().Set("dry-run", "false"); err != nil {
		t.Fatalf("reset dry-run flag: %v", err)
	}
	if err := applyCmd.Flags().Set("yes", "false"); err != nil {
		t.Fatalf("reset yes flag: %v", err)
	}
	rootCmd.SetArgs(nil)
}

func runTemplateRootCommand(t *testing.T, args ...string) (string, error) {
	t.Helper()
	resetTemplateCommandFlags(t)
	t.Cleanup(func() {
		resetTemplateCommandFlags(t)
	})
	rootCmd.SetArgs(args)
	var err error
	out := captureStdout(t, func() {
		err = rootCmd.Execute()
	})
	return out, err
}

func withTemplateRegistryPath(t *testing.T, regPath string) {
	t.Helper()
	oldRegPath := registryPathFn
	oldEnsureDir := ensureDirFn
	t.Cleanup(func() {
		registryPathFn = oldRegPath
		ensureDirFn = oldEnsureDir
	})
	registryPathFn = func() (string, error) { return regPath, nil }
	ensureDirFn = func() error { return os.MkdirAll(filepath.Dir(regPath), 0o700) }
}

func TestTemplateCommandTree(t *testing.T) {
	cases := [][]string{
		{"template"},
		{"template", "list"},
		{"template", "show", "personal-harness"},
		{"template", "apply", "personal-harness"},
	}
	for _, args := range cases {
		cmd, _, err := rootCmd.Find(args)
		if err != nil {
			t.Fatalf("Find(%v): %v", args, err)
		}
		if cmd == nil {
			t.Fatalf("Find(%v) returned nil command", args)
		}
	}
}

func TestTemplateListJSONIncludesBuiltins(t *testing.T) {
	raw, err := runTemplateRootCommand(t, "template", "list", "--json")
	if err != nil {
		t.Fatalf("template list --json: %v", err)
	}

	var resp templateListJSONResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("unmarshal response: %v\nraw: %s", err, raw)
	}
	if !resp.OK || resp.Command != "template list" {
		t.Fatalf("response = %+v, want ok template list", resp)
	}
	if resp.Data.SchemaVersion != inspect.SchemaVersion {
		t.Fatalf("schema_version = %q, want %q", resp.Data.SchemaVersion, inspect.SchemaVersion)
	}

	found := map[string]bool{}
	for _, tmpl := range resp.Data.Templates {
		found[tmpl.Name] = true
	}
	for _, name := range []string{"personal-harness", "dev-suite", "local-ai-suite"} {
		if !found[name] {
			t.Fatalf("template list missing %q: %+v", name, resp.Data.Templates)
		}
	}
	if resp.Data.Count != 3 {
		t.Fatalf("count = %d, want 3", resp.Data.Count)
	}
}

func TestTemplateShowPersonalHarnessJSONUsesPublicViews(t *testing.T) {
	raw, err := runTemplateRootCommand(t, "template", "show", "personal-harness", "--json")
	if err != nil {
		t.Fatalf("template show --json: %v", err)
	}
	assertTemplateRawJSONHasNoForbiddenFields(t, raw)

	var resp templateShowJSONResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("unmarshal response: %v\nraw: %s", err, raw)
	}
	if !resp.OK || resp.Command != "template show" {
		t.Fatalf("response = %+v, want ok template show", resp)
	}
	if resp.Data.SchemaVersion != inspect.SchemaVersion {
		t.Fatalf("schema_version = %q, want %q", resp.Data.SchemaVersion, inspect.SchemaVersion)
	}
	if resp.Data.Name != "personal-harness" || len(resp.Data.Services) != 2 {
		t.Fatalf("show data = %+v, want personal-harness with 2 services", resp.Data)
	}
	for _, svc := range resp.Data.Services {
		if svc.SchemaVersion != inspect.SchemaVersion {
			t.Fatalf("service schema_version = %q, want %q", svc.SchemaVersion, inspect.SchemaVersion)
		}
		if svc.Funnel || svc.Exposure.Public {
			t.Fatalf("template service is public/funnel: %+v", svc)
		}
		if svc.Endpoint.Kind != inspect.EndpointKindHTTPS {
			t.Fatalf("endpoint kind = %q, want private https", svc.Endpoint.Kind)
		}
		if svc.Allow.Mode != "all_tailnet" {
			t.Fatalf("allow = %+v, want all_tailnet", svc.Allow)
		}
	}
}

func TestTemplateUnknownReturnsNotFound(t *testing.T) {
	if _, err := showTemplateResult("missing-template"); output.ExitCode(err) != output.ExitNotFound {
		t.Fatalf("show ExitCode = %d, want %d (err=%v)", output.ExitCode(err), output.ExitNotFound, err)
	}

	dir := t.TempDir()
	if _, err := applyTemplate("missing-template", filepath.Join(dir, "registry.json"), true); output.ExitCode(err) != output.ExitNotFound {
		t.Fatalf("apply ExitCode = %d, want %d (err=%v)", output.ExitCode(err), output.ExitNotFound, err)
	}
}

func TestTemplateApplyDryRunDefaultDoesNotCreateRegistry(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	withTemplateRegistryPath(t, regPath)

	raw, err := runTemplateRootCommand(t, "template", "apply", "personal-harness", "--json")
	if err != nil {
		t.Fatalf("template apply dry-run: %v", err)
	}
	if _, err := os.Stat(regPath); !os.IsNotExist(err) {
		t.Fatalf("dry-run registry stat err = %v, want not exist", err)
	}

	var resp templateApplyJSONResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("unmarshal response: %v\nraw: %s", err, raw)
	}
	if !resp.Data.DryRun || resp.Data.Applied {
		t.Fatalf("apply data = %+v, want dry-run not applied", resp.Data)
	}
	for _, item := range resp.Data.Services {
		if item.Action != templateActionCreate {
			t.Fatalf("plan item = %+v, want create action", item)
		}
	}
}

func TestTemplateApplyYesWritesOnlyMissingServices(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	withTemplateRegistryPath(t, regPath)

	if _, err := registry.Add(regPath, registry.Service{
		Name:   "harness-web",
		Type:   registry.TypeProxy,
		Target: "http://localhost:9999",
		Tags:   []string{"tag:custom"},
	}); err != nil {
		t.Fatalf("prepopulate registry: %v", err)
	}

	raw, err := runTemplateRootCommand(t, "template", "apply", "personal-harness", "--yes", "--json")
	if err != nil {
		t.Fatalf("template apply --yes: %v", err)
	}
	var resp templateApplyJSONResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("unmarshal response: %v\nraw: %s", err, raw)
	}
	if resp.Data.DryRun || !resp.Data.Applied {
		t.Fatalf("apply data = %+v, want applied write", resp.Data)
	}
	if resp.Data.Created != 1 || resp.Data.Skipped != 1 {
		t.Fatalf("created/skipped = %d/%d, want 1/1", resp.Data.Created, resp.Data.Skipped)
	}

	reg, err := registry.Load(regPath)
	if err != nil {
		t.Fatalf("load registry: %v", err)
	}
	if len(reg.Services) != 2 {
		t.Fatalf("services = %d, want 2", len(reg.Services))
	}
	assertHarnessWebCustomized(t, reg)
	if !hasRegistryService(reg, "harness-api") {
		t.Fatalf("registry missing harness-api: %+v", reg.Services)
	}
}

func TestTemplateReapplySkipsExistingAndDoesNotOverwriteCustomized(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	withTemplateRegistryPath(t, regPath)

	if _, err := registry.Add(regPath, registry.Service{
		Name:   "harness-web",
		Type:   registry.TypeProxy,
		Target: "http://localhost:9999",
		Tags:   []string{"tag:custom"},
	}); err != nil {
		t.Fatalf("prepopulate registry: %v", err)
	}
	if _, err := runTemplateRootCommand(t, "template", "apply", "personal-harness", "--yes", "--json"); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	raw, err := runTemplateRootCommand(t, "template", "apply", "personal-harness", "--yes", "--json")
	if err != nil {
		t.Fatalf("second apply: %v", err)
	}
	var resp templateApplyJSONResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("unmarshal response: %v\nraw: %s", err, raw)
	}
	if resp.Data.Created != 0 || resp.Data.Skipped != 2 {
		t.Fatalf("created/skipped = %d/%d, want 0/2", resp.Data.Created, resp.Data.Skipped)
	}

	reg, err := registry.Load(regPath)
	if err != nil {
		t.Fatalf("load registry: %v", err)
	}
	if len(reg.Services) != 2 {
		t.Fatalf("services = %d, want 2", len(reg.Services))
	}
	assertHarnessWebCustomized(t, reg)
}

func TestTemplateApplyDryRunWinsOverYes(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	withTemplateRegistryPath(t, regPath)

	raw, err := runTemplateRootCommand(t, "template", "apply", "personal-harness", "--dry-run", "--yes", "--json")
	if err != nil {
		t.Fatalf("template apply --dry-run --yes: %v", err)
	}
	if _, err := os.Stat(regPath); !os.IsNotExist(err) {
		t.Fatalf("dry-run --yes registry stat err = %v, want not exist", err)
	}

	var resp templateApplyJSONResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("unmarshal response: %v\nraw: %s", err, raw)
	}
	if !resp.Data.DryRun || resp.Data.Applied {
		t.Fatalf("apply data = %+v, want dry-run to win", resp.Data)
	}
}

func TestTemplatePublicJSONOmitsForbiddenFields(t *testing.T) {
	for _, args := range [][]string{
		{"template", "list", "--json"},
		{"template", "show", "personal-harness", "--json"},
		{"template", "apply", "personal-harness", "--json"},
	} {
		dir := t.TempDir()
		withTemplateRegistryPath(t, filepath.Join(dir, "registry.json"))
		raw, err := runTemplateRootCommand(t, args...)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		assertTemplateRawJSONHasNoForbiddenFields(t, raw)
	}
}

func assertTemplateRawJSONHasNoForbiddenFields(t *testing.T, raw string) {
	t.Helper()
	for _, forbidden := range []string{
		`"funnel":true`,
		`"domain"`,
		`"acme_email"`,
		`"control_url"`,
		`"basic_auth"`,
		`"ip_allow_list"`,
		`"cors_origins"`,
	} {
		if strings.Contains(raw, forbidden) {
			t.Fatalf("template JSON leaked forbidden field %s: %s", forbidden, raw)
		}
	}
}

func hasRegistryService(reg *registry.Registry, name string) bool {
	for _, svc := range reg.Services {
		if svc.Name == name {
			return true
		}
	}
	return false
}

func assertHarnessWebCustomized(t *testing.T, reg *registry.Registry) {
	t.Helper()
	for _, svc := range reg.Services {
		if svc.Name != "harness-web" {
			continue
		}
		if svc.Target != "http://localhost:9999" {
			t.Fatalf("harness-web target = %q, want custom target", svc.Target)
		}
		if len(svc.Tags) != 1 || svc.Tags[0] != "tag:custom" {
			t.Fatalf("harness-web tags = %v, want custom tags", svc.Tags)
		}
		return
	}
	t.Fatalf("harness-web not found in registry: %+v", reg.Services)
}
