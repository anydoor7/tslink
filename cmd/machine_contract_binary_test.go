package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/monody0007/tslink/internal/output"
	"github.com/monody0007/tslink/internal/registry"
)

func resultDataAsMap(t *testing.T, result output.Result) map[string]any {
	t.Helper()
	dataBytes, err := json.Marshal(result.Data)
	if err != nil {
		t.Fatalf("marshal result data: %v", err)
	}
	var data map[string]any
	if err := json.Unmarshal(dataBytes, &data); err != nil {
		t.Fatalf("decode result data: %v", err)
	}
	return data
}

func TestCompiledMachineContractRootAndCommandFailures(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		wantExit    int
		wantCommand string
		wantErrCode string
	}{
		{
			name:        "early parse error remains JSON",
			args:        []string{"--json", "--unknown-flag"},
			wantExit:    output.ExitUsage,
			wantCommand: "",
			wantErrCode: "usage_error",
		},
		{
			name:        "known command args failure carries identity",
			args:        []string{"--json", "status", "extra"},
			wantExit:    output.ExitUsage,
			wantCommand: "status",
			wantErrCode: "usage_error",
		},
		{
			name:        "api rejects extra args with identity",
			args:        []string{"--json", "api", "extra"},
			wantExit:    output.ExitUsage,
			wantCommand: "api",
			wantErrCode: "usage_error",
		},
		{
			name:        "invalid log level is usage failure",
			args:        []string{"--json", "logs", "--level", "verbose"},
			wantExit:    output.ExitUsage,
			wantCommand: "logs",
			wantErrCode: "usage_error",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr, code := runCompiledTSLink(t, t.TempDir(), "", tc.args...)
			if code != tc.wantExit {
				t.Fatalf("exit = %d, want %d\nstdout=%s\nstderr=%s", code, tc.wantExit, stdout, stderr)
			}
			if stderr != "" {
				t.Fatalf("stderr = %q, want empty for JSON failure", stderr)
			}
			results := parseCompiledJSONLines(t, stdout)
			if len(results) != 1 {
				t.Fatalf("stdout record count = %d, want 1\nstdout=%s", len(results), stdout)
			}
			result := results[0]
			if result.OK || result.Code != tc.wantExit || result.Command != tc.wantCommand {
				t.Fatalf("result = %+v, want failed command %q code %d", result, tc.wantCommand, tc.wantExit)
			}
			if result.Error == nil || result.Error.Code != tc.wantErrCode {
				t.Fatalf("error = %+v, want code %q", result.Error, tc.wantErrCode)
			}
		})
	}
}

func TestCompiledMachineContractVersionJSON(t *testing.T) {
	humanStdout, humanStderr, humanCode := runCompiledTSLink(t, t.TempDir(), "", "--version")
	if humanCode != output.ExitSuccess || humanStderr != "" {
		t.Fatalf("human version exit=%d stderr=%q stdout=%s", humanCode, humanStderr, humanStdout)
	}
	wantVersion, ok := strings.CutPrefix(strings.TrimSpace(humanStdout), "tslink version ")
	if !ok || wantVersion == "" {
		t.Fatalf("human version output = %q, want tslink version <non-empty>", humanStdout)
	}

	for _, args := range [][]string{
		{"--version", "--json"},
		{"--json", "--version"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			stdout, stderr, code := runCompiledTSLink(t, t.TempDir(), "", args...)
			if code != output.ExitSuccess {
				t.Fatalf("exit = %d, want 0\nstdout=%s\nstderr=%s", code, stdout, stderr)
			}
			if stderr != "" {
				t.Fatalf("stderr = %q, want empty", stderr)
			}
			results := parseCompiledJSONLines(t, stdout)
			if len(results) != 1 || !results[0].OK || results[0].Command != "version" {
				t.Fatalf("version result = %+v", results)
			}
			data := resultDataAsMap(t, results[0])
			version, ok := data["version"].(string)
			if !ok || version != wantVersion {
				t.Fatalf("version data = %+v, want human version %q", data, wantVersion)
			}
		})
	}
}

func TestCompiledRemoveCLIApiSharedResultShape(t *testing.T) {
	home := t.TempDir()
	add := `{"action":"add","name":"web","type":"proxy","target":"http://localhost:3000"}` + "\n"
	stdout, stderr, code := runCompiledTSLink(t, home, add, "api")
	if code != output.ExitSuccess || stderr != "" {
		t.Fatalf("api add exit=%d stderr=%q stdout=%s", code, stderr, stdout)
	}
	if got := registryServiceCount(t, home); got != 1 {
		t.Fatalf("registry count after add = %d, want 1", got)
	}

	stdout, stderr, code = runCompiledTSLink(t, home, "", "--json", "remove", "web")
	if code != output.ExitSuccess || stderr != "" {
		t.Fatalf("cli remove exit=%d stderr=%q stdout=%s", code, stderr, stdout)
	}
	results := parseCompiledJSONLines(t, stdout)
	if len(results) != 1 || !results[0].OK || results[0].Command != "remove" {
		t.Fatalf("cli remove result = %+v", results)
	}
	data := resultDataAsMap(t, results[0])
	if data["name"] != "web" || data["removed"] != true {
		t.Fatalf("cli remove data = %+v, want name=web removed=true", data)
	}
	if got := registryServiceCount(t, home); got != 0 {
		t.Fatalf("registry count after cli remove = %d, want 0", got)
	}

	stdout, stderr, code = runCompiledTSLink(t, home, "", "--json", "remove", "web")
	if code != output.ExitSuccess || stderr != "" {
		t.Fatalf("cli idempotent remove exit=%d stderr=%q stdout=%s", code, stderr, stdout)
	}
	results = parseCompiledJSONLines(t, stdout)
	data = resultDataAsMap(t, results[0])
	if data["name"] != "web" || data["removed"] != false {
		t.Fatalf("cli idempotent remove data = %+v, want removed=false", data)
	}

	removeMissing := `{"action":"remove","name":"web"}` + "\n"
	stdout, stderr, code = runCompiledTSLink(t, home, removeMissing, "api")
	if code != output.ExitSuccess || stderr != "" {
		t.Fatalf("api idempotent remove exit=%d stderr=%q stdout=%s", code, stderr, stdout)
	}
	results = parseCompiledJSONLines(t, stdout)
	if len(results) != 1 || !results[0].OK {
		t.Fatalf("api idempotent remove result = %+v", results)
	}
	data = resultDataAsMap(t, results[0])
	if data["name"] != "web" || data["removed"] != false {
		t.Fatalf("api idempotent remove data = %+v, want removed=false", data)
	}
}

func TestCompiledRemoveDefaultMissingRemainsIdempotent(t *testing.T) {
	stdout, stderr, code := runCompiledTSLink(t, t.TempDir(), "", "remove", "missing", "--json")
	if code != output.ExitSuccess || stderr != "" {
		t.Fatalf("default remove exit=%d stderr=%q stdout=%s", code, stderr, stdout)
	}
	results := parseCompiledJSONLines(t, stdout)
	if len(results) != 1 || !results[0].OK || results[0].Code != output.ExitSuccess {
		t.Fatalf("default remove result = %+v, want ok=true code=0", results)
	}
	data := resultDataAsMap(t, results[0])
	if data["name"] != "missing" || data["removed"] != false {
		t.Fatalf("default remove data = %+v, want name=missing removed=false", data)
	}

	humanOut, humanErr, humanCode := runCompiledTSLink(t, t.TempDir(), "", "remove", "missing")
	if humanCode != output.ExitSuccess || humanErr != "" || humanOut != "→ missing not registered, nothing to remove\n" {
		t.Fatalf("human default remove exit=%d stdout=%q stderr=%q", humanCode, humanOut, humanErr)
	}
}

func TestCompiledRemoveStrictMissingReturnsNotFound(t *testing.T) {
	stdout, stderr, code := runCompiledTSLink(t, t.TempDir(), "", "remove", "missing", "--strict", "--json")
	if code != output.ExitNotFound || stderr != "" {
		t.Fatalf("strict remove exit=%d stderr=%q stdout=%s", code, stderr, stdout)
	}
	results := parseCompiledJSONLines(t, stdout)
	if len(results) != 1 || results[0].OK || results[0].Code != output.ExitNotFound || results[0].Error == nil || results[0].Error.Code != "not_found" {
		t.Fatalf("strict remove result = %+v, want ok=false code=5 error.code=not_found", results)
	}
	if results[0].Error.Message != "service not found: missing" {
		t.Fatalf("strict remove message = %q, want exact missing-service message", results[0].Error.Message)
	}

	humanOut, humanErr, humanCode := runCompiledTSLink(t, t.TempDir(), "", "remove", "missing", "--strict")
	if humanCode != output.ExitNotFound || humanOut != "" || humanErr != "Error: service not found: missing\n" {
		t.Fatalf("human strict remove exit=%d stdout=%q stderr=%q", humanCode, humanOut, humanErr)
	}
}

func TestCompiledRemoveStrictExistingPreservesSuccess(t *testing.T) {
	configDir := t.TempDir()
	regPath := filepath.Join(configDir, "registry.json")
	if _, err := registry.Add(regPath, registry.Service{Name: "web", Type: registry.TypeProxy, Target: "http://localhost:3000"}); err != nil {
		t.Fatalf("add service fixture: %v", err)
	}
	stdout, stderr, code := runCompiledTSLinkWithConfigDir(t, configDir, "", "remove", "web", "--strict", "--json")
	if code != output.ExitSuccess || stderr != "" {
		t.Fatalf("strict existing remove exit=%d stderr=%q stdout=%s", code, stderr, stdout)
	}
	results := parseCompiledJSONLines(t, stdout)
	if len(results) != 1 || !results[0].OK || results[0].Code != output.ExitSuccess {
		t.Fatalf("strict existing result = %+v, want unchanged success", results)
	}
	data := resultDataAsMap(t, results[0])
	if data["name"] != "web" || data["removed"] != true {
		t.Fatalf("strict existing data = %+v, want name=web removed=true", data)
	}
}

func TestCompiledRemoveHelpDocumentsStrictAndIdempotentDefault(t *testing.T) {
	stdout, stderr, code := runCompiledTSLink(t, t.TempDir(), "", "remove", "--help")
	if code != output.ExitSuccess || stderr != "" {
		t.Fatalf("remove help exit=%d stderr=%q stdout=%s", code, stderr, stdout)
	}
	for _, want := range []string{"--strict", "default is idempotent"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("remove help missing %q:\n%s", want, stdout)
		}
	}
}

func TestCompiledAPIManifestSelfDescriptionWithoutDaemon(t *testing.T) {
	configDir := t.TempDir()
	pidPath := filepath.Join(configDir, "tslink.pid")
	if _, err := os.Stat(pidPath); !os.IsNotExist(err) {
		t.Fatalf("daemon pid fixture unexpectedly exists before manifest action: %v", err)
	}
	stdout, stderr, code := runCompiledTSLinkWithConfigDir(t, configDir, `{"action":"manifest"}`+"\n", "api")
	if code != output.ExitSuccess || stderr != "" {
		t.Fatalf("api manifest exit=%d stderr=%q stdout=%s", code, stderr, stdout)
	}
	results := parseCompiledJSONLines(t, stdout)
	if len(results) != 1 || !results[0].OK || results[0].Command != apiActionManifest {
		t.Fatalf("api manifest result = %+v, want one successful manifest record", results)
	}
	data := resultDataAsMap(t, results[0])
	if data["capabilities"] == nil || data["commands"] == nil {
		t.Fatalf("api manifest data lacks generated commands or capabilities: %+v", data)
	}
	dataBytes, err := json.Marshal(results[0].Data)
	if err != nil {
		t.Fatalf("marshal manifest data: %v", err)
	}
	var manifest CLIManifest
	if err := json.Unmarshal(dataBytes, &manifest); err != nil {
		t.Fatalf("decode manifest data: %v", err)
	}
	if manifest.SchemaVersion != 2 || len(manifest.Commands) == 0 {
		t.Fatalf("api manifest content is incomplete: schema=%d commands=%d", manifest.SchemaVersion, len(manifest.Commands))
	}
	if !slices.Equal(manifest.APIActions, apiActionNames()) || len(manifest.APIActions) != 10 || !containsString(manifest.APIActions, apiActionManifest) {
		t.Fatalf("api manifest actions = %v, want generated actions %v", manifest.APIActions, apiActionNames())
	}
	if _, err := os.Stat(pidPath); !os.IsNotExist(err) {
		t.Fatalf("manifest action created or required a daemon pid: %v", err)
	}
}
