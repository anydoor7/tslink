package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/anydoor7/tslink/internal/inspect"
	"github.com/anydoor7/tslink/internal/output"
	"github.com/anydoor7/tslink/internal/registry"
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
	resetRootJSONFlag(t)
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
		{"template", "show", "local-web"},
		{"template", "apply", "local-web"},
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
		t.Fatalf("schema_version = %v, want %v", resp.Data.SchemaVersion, inspect.SchemaVersion)
	}

	found := map[string]bool{}
	for _, tmpl := range resp.Data.Templates {
		found[tmpl.Name] = true
	}
	for _, name := range []string{"local-web", "dev-suite", "local-ai-suite"} {
		if !found[name] {
			t.Fatalf("template list missing %q: %+v", name, resp.Data.Templates)
		}
	}
	if resp.Data.Count != 3 {
		t.Fatalf("count = %d, want 3", resp.Data.Count)
	}
}

func TestTemplateShowLocalWebJSONUsesPublicViews(t *testing.T) {
	raw, err := runTemplateRootCommand(t, "template", "show", "local-web", "--json")
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
		t.Fatalf("schema_version = %v, want %v", resp.Data.SchemaVersion, inspect.SchemaVersion)
	}
	if resp.Data.Name != "local-web" || len(resp.Data.Services) != 2 {
		t.Fatalf("show data = %+v, want local-web with 2 services", resp.Data)
	}
	for _, svc := range resp.Data.Services {
		if svc.SchemaVersion != inspect.SchemaVersion {
			t.Fatalf("service schema_version = %v, want %v", svc.SchemaVersion, inspect.SchemaVersion)
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
	_, showErr := showTemplateResult("missing-template")
	if output.ExitCode(showErr) != output.ExitNotFound {
		err := showErr
		t.Fatalf("show ExitCode = %d, want %d (err=%v)", output.ExitCode(err), output.ExitNotFound, err)
	}
	failure := output.NewFailureForError("template show", showErr)
	if failure.Error == nil || failure.Error.Code != "not_found" || !reflect.DeepEqual(failure.Error.Next, []string{"tslink template list --json"}) {
		t.Fatalf("show failure = %+v, want template-specific recovery", failure)
	}

	dir := t.TempDir()
	if _, err := applyTemplate("missing-template", filepath.Join(dir, "registry.json"), true); output.ExitCode(err) != output.ExitNotFound {
		t.Fatalf("apply ExitCode = %d, want %d (err=%v)", output.ExitCode(err), output.ExitNotFound, err)
	}

	stdout, stderr, exitCode := runCompiledTSLinkWithConfigDir(t, t.TempDir(), "", "template", "show", "missing-template", "--json")
	results := parseCompiledJSONLines(t, stdout)
	if stderr != "" || exitCode != output.ExitNotFound || len(results) != 1 || results[0].Error == nil || !reflect.DeepEqual(results[0].Error.Next, []string{"tslink template list --json"}) {
		t.Fatalf("compiled exit=%d stderr=%q result=%+v", exitCode, stderr, results)
	}
}

func TestTemplateApplyDryRunDefaultDoesNotCreateRegistry(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	withTemplateRegistryPath(t, regPath)

	raw, err := runTemplateRootCommand(t, "template", "apply", "local-web", "--json")
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
		Name:   "web",
		Type:   registry.TypeProxy,
		Target: "http://localhost:9999",
		Tags:   []string{"tag:custom"},
	}); err != nil {
		t.Fatalf("prepopulate registry: %v", err)
	}

	raw, err := runTemplateRootCommand(t, "template", "apply", "local-web", "--yes", "--json")
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
	assertWebServiceCustomized(t, reg)
	if !hasRegistryService(reg, "api") {
		t.Fatalf("registry missing api: %+v", reg.Services)
	}
}

func TestTemplateReapplySkipsExistingAndDoesNotOverwriteCustomized(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	withTemplateRegistryPath(t, regPath)

	if _, err := registry.Add(regPath, registry.Service{
		Name:   "web",
		Type:   registry.TypeProxy,
		Target: "http://localhost:9999",
		Tags:   []string{"tag:custom"},
	}); err != nil {
		t.Fatalf("prepopulate registry: %v", err)
	}
	if _, err := runTemplateRootCommand(t, "template", "apply", "local-web", "--yes", "--json"); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	raw, err := runTemplateRootCommand(t, "template", "apply", "local-web", "--yes", "--json")
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
	assertWebServiceCustomized(t, reg)
}

func TestTemplateApplyDryRunWinsOverYes(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	withTemplateRegistryPath(t, regPath)

	raw, err := runTemplateRootCommand(t, "template", "apply", "local-web", "--dry-run", "--yes", "--json")
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
	cases := [][]string{{"template", "list", "--json"}}
	for _, tmpl := range templateSummaries() {
		cases = append(cases,
			[]string{"template", "show", tmpl.Name, "--json"},
			[]string{"template", "apply", tmpl.Name, "--json"},
		)
	}

	for _, args := range cases {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			dir := t.TempDir()
			withTemplateRegistryPath(t, filepath.Join(dir, "registry.json"))
			raw, err := runTemplateRootCommand(t, args...)
			if err != nil {
				t.Fatalf("%v: %v", args, err)
			}
			assertTemplateRawJSONHasNoForbiddenFields(t, raw)
		})
	}
}

func TestTemplateBuiltInServicesAvoidPublicExposureFields(t *testing.T) {
	for _, tmpl := range builtinTemplates() {
		t.Run(tmpl.Name, func(t *testing.T) {
			services, err := buildTemplateServices(tmpl)
			if err != nil {
				t.Fatalf("buildTemplateServices: %v", err)
			}
			for _, svc := range services {
				if svc.Funnel {
					t.Fatalf("%s has Funnel enabled", svc.Name)
				}
				if svc.ControlURL != "" {
					t.Fatalf("%s control_url = %q, want empty", svc.Name, svc.ControlURL)
				}
				if len(svc.AllowedUsers) != 0 {
					t.Fatalf("%s allowed_users = %v, want none", svc.Name, svc.AllowedUsers)
				}
				if svc.Type == registry.TypeFile || svc.Path != "" {
					t.Fatalf("%s has file path assumptions: type=%q path=%q", svc.Name, svc.Type, svc.Path)
				}
				if !strings.Contains(svc.Target, "localhost:") {
					t.Fatalf("%s target = %q, want localhost target", svc.Name, svc.Target)
				}
				if len(svc.Tags) != 1 || svc.Tags[0] != "tag:tslink" {
					t.Fatalf("%s tags = %v, want uniform [tag:tslink]", svc.Name, svc.Tags)
				}
			}
		})
	}
}

func TestTemplateListHumanIncludesAllTemplateNames(t *testing.T) {
	out, err := runTemplateRootCommand(t, "template", "list")
	if err != nil {
		t.Fatalf("template list: %v", err)
	}
	for _, tmpl := range templateSummaries() {
		if !strings.Contains(out, tmpl.Name) {
			t.Fatalf("template list output missing %q:\n%s", tmpl.Name, out)
		}
	}
}

func TestTemplateShowHumanIncludesServicesAndLocalhostBackends(t *testing.T) {
	out, err := runTemplateRootCommand(t, "template", "show", "local-web")
	if err != nil {
		t.Fatalf("template show local-web: %v", err)
	}
	// Assert the whole rendered service line, not the bare name: "web" and
	// "api" also occur inside the service summaries, so a substring check on
	// the name alone would stay green after the name changed.
	for _, want := range []string{
		"  web: proxy -> http://localhost:8080",
		"  api: proxy -> http://localhost:8000",
		"tag:tslink",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("template show output missing %q:\n%s", want, out)
		}
	}
}

func TestTemplateApplyHumanDryRunIncludesReminder(t *testing.T) {
	dir := t.TempDir()
	withTemplateRegistryPath(t, filepath.Join(dir, "registry.json"))

	out, err := runTemplateRootCommand(t, "template", "apply", "local-web")
	if err != nil {
		t.Fatalf("template apply local-web: %v", err)
	}
	for _, want := range []string{
		"No registry changes written",
		"Re-run with --yes",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("template apply dry-run output missing %q:\n%s", want, out)
		}
	}
}

func TestTemplateApplyHumanYesIncludesCreatedSummary(t *testing.T) {
	dir := t.TempDir()
	withTemplateRegistryPath(t, filepath.Join(dir, "registry.json"))

	out, err := runTemplateRootCommand(t, "template", "apply", "local-web", "--yes")
	if err != nil {
		t.Fatalf("template apply local-web --yes: %v", err)
	}
	for _, want := range []string{
		`Template "local-web" applied`,
		"created web",
		"created api",
		"Created 2, skipped 0.",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("template apply --yes output missing %q:\n%s", want, out)
		}
	}
}

func TestTemplateApplyConvertsRaceCreatedServiceToSkipped(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")

	reg, err := registry.Load(regPath)
	if err != nil {
		t.Fatalf("load empty registry: %v", err)
	}
	plan, _, err := planTemplateApply("local-web", reg, false)
	if err != nil {
		t.Fatalf("planTemplateApply: %v", err)
	}
	if plan.Created != 2 || plan.Skipped != 0 {
		t.Fatalf("plan created/skipped = %d/%d, want 2/0", plan.Created, plan.Skipped)
	}

	oldAddIfMissing := templateAddIfMissingFn
	raced := false
	templateAddIfMissingFn = func(path string, svc registry.Service) (bool, error) {
		if svc.Name == "web" && !raced {
			raced = true
			if _, err := registry.Add(path, svc); err != nil {
				return false, err
			}
			return false, nil
		}
		return registry.AddIfMissing(path, svc)
	}
	t.Cleanup(func() {
		templateAddIfMissingFn = oldAddIfMissing
	})

	result, err := applyTemplate("local-web", regPath, false)
	if err != nil {
		t.Fatalf("applyTemplate: %v", err)
	}
	if !result.Applied || result.DryRun {
		t.Fatalf("apply result = %+v, want applied write", result)
	}
	if result.Created != 1 || result.Skipped != 1 {
		t.Fatalf("created/skipped = %d/%d, want 1/1", result.Created, result.Skipped)
	}
	if result.Services[0].Action != templateActionSkipExisting {
		t.Fatalf("first action = %q, want %q", result.Services[0].Action, templateActionSkipExisting)
	}
	if result.Services[1].Action != templateActionCreated {
		t.Fatalf("second action = %q, want %q", result.Services[1].Action, templateActionCreated)
	}

	loaded, err := registry.Load(regPath)
	if err != nil {
		t.Fatalf("load registry: %v", err)
	}
	if len(loaded.Services) != 2 {
		t.Fatalf("services = %+v, want two persisted services", loaded.Services)
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

func assertWebServiceCustomized(t *testing.T, reg *registry.Registry) {
	t.Helper()
	for _, svc := range reg.Services {
		if svc.Name != "web" {
			continue
		}
		if svc.Target != "http://localhost:9999" {
			t.Fatalf("web target = %q, want custom target", svc.Target)
		}
		if len(svc.Tags) != 1 || svc.Tags[0] != "tag:custom" {
			t.Fatalf("web tags = %v, want custom tags", svc.Tags)
		}
		return
	}
	t.Fatalf("web not found in registry: %+v", reg.Services)
}
