package cmd

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/daemon"
	"github.com/monody0007/tslink/internal/output"
	"github.com/monody0007/tslink/internal/registry"
	tsruntime "github.com/monody0007/tslink/internal/runtime"
	"github.com/monody0007/tslink/internal/testenv"
)

var (
	tslinkBinaryOnce sync.Once
	tslinkBinaryPath string
	tslinkBinaryErr  error

	// tslinkBinaryRoot is the single build root shared by every compiled-binary
	// suite in this package. It is created lazily, here, on the first build,
	// and removed by TestMain (cmd/share_test.go) after m.Run returns.
	//
	// It is deliberately NOT a t.TempDir(): tslinkBinaryOnce is package-scoped,
	// so the root would be deleted when whichever test happened to trigger the
	// build finished, and every later test in the package would be handed a
	// dangling path. It is also no longer an unmanaged os.MkdirTemp with no
	// owner: that is what leaked here before Round C-1's review, and Round C-1
	// made the leak materially worse by adding a second real CLI copy
	// (~45.9 MB), the overlay fake daemon (~3.0 MB) and its generated sources
	// into the same never-removed tree — about 47 MiB per test process, six
	// test processes per CI run.
	//
	// Creation stays lazy rather than moving into TestMain because several
	// tests re-execute this test binary as a child process
	// (TestServeSignalContextInstallsRealHandler, and stop_test.go's
	// `-test.run=^$` helpers). Those children run TestMain too, and the signal
	// child terminates itself with a real signal, so nothing after m.Run ever
	// runs in it. Creating the root unconditionally in TestMain therefore
	// leaked one empty directory per package run. Children that never build a
	// binary now never create a root.
	tslinkBinaryRoot string
)

const testDaemonParentLifetimeEnv = "TSLINK_TEST_DAEMON_PARENT_LIFETIME"

func compiledTSLinkBinary(t *testing.T) string {
	t.Helper()
	tslinkBinaryOnce.Do(func() {
		_, file, _, ok := runtime.Caller(0)
		if !ok {
			tslinkBinaryErr = os.ErrInvalid
			return
		}
		repoRoot := filepath.Dir(filepath.Dir(file))
		binDir, err := os.MkdirTemp("", "tslink-bin-*")
		if err != nil {
			tslinkBinaryErr = err
			return
		}
		// Publish the root so TestMain can remove it. Ordering is safe: this
		// write happens inside tslinkBinaryOnce during m.Run, and TestMain only
		// reads it after m.Run has joined every test.
		tslinkBinaryRoot = binDir
		tslinkBinaryPath = filepath.Join(binDir, "tslink")
		if runtime.GOOS == "windows" {
			tslinkBinaryPath += ".exe"
		}
		cmd := exec.Command("go", "build", "-o", tslinkBinaryPath, ".")
		cmd.Dir = repoRoot
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			tslinkBinaryErr = err
			if stderr.Len() > 0 {
				tslinkBinaryErr = output.ErrUsage(stderr.String())
			}
		}
	})
	if tslinkBinaryErr != nil {
		t.Fatalf("build compiled tslink binary: %v", tslinkBinaryErr)
	}
	return tslinkBinaryPath
}

func compiledDaemonIdentityFixture(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate repository root for daemon identity fixture")
	}
	repoRoot := filepath.Dir(filepath.Dir(file))
	fixtureDir := t.TempDir()
	fixtureSourcePath := filepath.Join(fixtureDir, "main.go")
	fixtureSource := `package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/monody0007/tslink/internal/daemon"
)

func main() {
	configDir := os.Getenv("TSLINK_CONFIG_DIR")
	if configDir == "" {
		fmt.Fprintln(os.Stderr, "TSLINK_CONFIG_DIR is required")
		os.Exit(1)
	}
	if err := daemon.WritePID(filepath.Join(configDir, "tslink.pid")); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if _, err := fmt.Fprintln(os.Stdout, "ready"); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	_, _ = io.Copy(io.Discard, os.Stdin)
}
`
	if err := os.WriteFile(fixtureSourcePath, []byte(fixtureSource), 0o600); err != nil {
		t.Fatalf("write daemon identity fixture source: %v", err)
	}
	overlayPath := filepath.Join(fixtureDir, "overlay.json")
	overlay, err := json.Marshal(struct {
		Replace map[string]string `json:"Replace"`
	}{Replace: map[string]string{
		filepath.ToSlash(filepath.Join(repoRoot, "main.go")): filepath.ToSlash(fixtureSourcePath),
	}})
	if err != nil {
		t.Fatalf("encode daemon identity fixture overlay: %v", err)
	}
	if err := os.WriteFile(overlayPath, overlay, 0o600); err != nil {
		t.Fatalf("write daemon identity fixture overlay: %v", err)
	}
	fixtureBinary := filepath.Join(fixtureDir, "tslink-daemon-fixture")
	if runtime.GOOS == "windows" {
		fixtureBinary += ".exe"
	}
	build := exec.Command("go", "build", "-overlay", overlayPath, "-o", fixtureBinary, ".")
	build.Dir = repoRoot
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build daemon identity fixture: %v\n%s", err, output)
	}
	return fixtureBinary
}

func runCompiledTSLink(t *testing.T, home, stdin string, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	configDir := testenv.ConfigDir(home)
	cmd := exec.Command(compiledTSLinkBinary(t), args...)
	cmd.Env = append(os.Environ(),
		"HOME="+home,
		config.ConfigDirEnv+"="+configDir,
		"TSLINK_DISABLE_KEYRING=1",
		testDaemonParentLifetimeEnv+"=1",
	)
	cmd.Stdin = strings.NewReader(stdin)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err := cmd.Run()
	exitCode = 0
	if err != nil {
		exitCode = 1
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			t.Fatalf("run tslink %v: %v", args, err)
		}
	}
	return outBuf.String(), errBuf.String(), exitCode
}

func runCompiledTSLinkWithConfigDir(t *testing.T, configDir, stdin string, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	return runTSLinkBinaryWithConfigDir(t, compiledTSLinkBinary(t), configDir, stdin, args...)
}

func runTSLinkBinaryWithConfigDir(t *testing.T, binary, configDir, stdin string, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	cmd := exec.Command(binary, offlineRegistrationArgs(args)...)
	cmd.Env = append(os.Environ(),
		"TSLINK_CONFIG_DIR="+configDir,
		"TSLINK_DISABLE_KEYRING=1",
		"TSLINK_API_KEY=",
		"TSLINK_CLIENT_SECRET=",
		testDaemonParentLifetimeEnv+"=1",
	)
	cmd.Stdin = strings.NewReader(stdin)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err := cmd.Run()
	exitCode = output.ExitSuccess
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			t.Fatalf("run tslink %v: %v", args, err)
		}
	}
	return outBuf.String(), errBuf.String(), exitCode
}

func parseCompiledJSONLines(t *testing.T, stdout string) []output.Result {
	t.Helper()
	trimmed := strings.TrimSuffix(stdout, "\n")
	if strings.TrimSpace(trimmed) == "" {
		return nil
	}
	lines := strings.Split(trimmed, "\n")
	results := make([]output.Result, 0, len(lines))
	for i, line := range lines {
		var result output.Result
		if err := json.Unmarshal([]byte(line), &result); err != nil {
			t.Fatalf("stdout line %d is not JSON: %v\nstdout:\n%s", i, err, stdout)
		}
		if result.Type != output.SchemaType || result.SchemaVersion != output.SchemaVersion {
			t.Fatalf("stdout line %d schema = %q/%d, want %q/%d", i, result.Type, result.SchemaVersion, output.SchemaType, output.SchemaVersion)
		}
		results = append(results, result)
	}
	return results
}

func TestCompiledJSONCommandGroupsReturnOneUsageEnvelope(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "root"},
		{name: "access", args: []string{"access"}},
		{name: "config", args: []string{"config"}},
		{name: "tags", args: []string{"tags"}},
		{name: "template", args: []string{"template"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string{"--json"}, tc.args...)
			stdout, stderr, exitCode := runCompiledTSLinkWithConfigDir(t, t.TempDir(), "", args...)
			if exitCode != output.ExitUsage || stderr != "" {
				t.Fatalf("exit=%d stderr=%q stdout=%q", exitCode, stderr, stdout)
			}
			results := parseCompiledJSONLines(t, stdout)
			if len(results) != 1 || results[0].OK || results[0].Error == nil || results[0].Error.Code != "usage_error" || len(results[0].Error.Next) == 0 {
				t.Fatalf("result=%+v, want one navigable usage envelope", results)
			}
		})
	}
}

func TestCompiledAddDryRunAndActualShareAdmissionVerdicts(t *testing.T) {
	existingDir := t.TempDir()
	missingDir := filepath.Join(t.TempDir(), "missing")
	tests := []struct {
		name string
		args []string
	}{
		{name: "proxy valid", args: []string{"add", "proxy-ok", "--proxy", "localhost:3000"}},
		{name: "proxy invalid", args: []string{"add", "proxy-bad", "--proxy", "://bad"}},
		{name: "file valid", args: []string{"add", "file-ok", "--dir", existingDir}},
		{name: "file invalid", args: []string{"add", "file-bad", "--dir", missingDir}},
		{name: "tcp valid", args: []string{"add", "tcp-ok", "--tcp", "localhost:5432"}},
		{name: "tcp invalid", args: []string{"add", "tcp-bad", "--tcp", "localhost:99999"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dryArgs := append(append([]string(nil), tc.args...), "--dry-run", "--json")
			actualArgs := append(append([]string(nil), tc.args...), "--json")
			dryOut, dryErr, dryExit := runCompiledTSLinkWithConfigDir(t, t.TempDir(), "", dryArgs...)
			actualOut, actualErr, actualExit := runCompiledTSLinkWithConfigDir(t, t.TempDir(), "", actualArgs...)
			if dryErr != "" || (actualErr != "" && !strings.Contains(actualErr, "--no-daemon-install")) {
				t.Fatalf("dry stderr=%q actual stderr=%q", dryErr, actualErr)
			}
			dryResults := parseCompiledJSONLines(t, dryOut)
			actualResults := parseCompiledJSONLines(t, actualOut)
			if len(dryResults) != 1 || len(actualResults) != 1 {
				t.Fatalf("dry=%q actual=%q", dryOut, actualOut)
			}
			dryResult, actualResult := dryResults[0], actualResults[0]
			dryCode, actualCode := "", ""
			if dryResult.Error != nil {
				dryCode = dryResult.Error.Code
			}
			if actualResult.Error != nil {
				actualCode = actualResult.Error.Code
			}
			if dryExit != actualExit || dryResult.OK != actualResult.OK || dryCode != actualCode {
				t.Fatalf("dry=(exit=%d ok=%v code=%q) actual=(exit=%d ok=%v code=%q)\ndry=%s\nactual=%s", dryExit, dryResult.OK, dryCode, actualExit, actualResult.OK, actualCode, dryOut, actualOut)
			}
		})
	}
}

func TestCompiledUserInputErrorsNeverBecomeInternalError(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantExit int
		wantCode string
		wantNext string
	}{
		{name: "logs level", args: []string{"logs", "--level", "verbose", "--json"}, wantExit: output.ExitUsage, wantCode: "usage_error", wantNext: "tslink --help"},
		{name: "logs source", args: []string{"logs", "--source", "bogus", "--json"}, wantExit: output.ExitUsage, wantCode: "usage_error", wantNext: "tslink --help"},
		{name: "login missing credential", args: []string{"login", "--json"}, wantExit: output.ExitUsage, wantCode: "usage_error", wantNext: "tslink --help"},
		{name: "login invalid key prefix", args: []string{"login", "--api-key", "invalid", "--json"}, wantExit: output.ExitUsage, wantCode: "usage_error", wantNext: "tslink --help"},
		{name: "funnel type conflict", args: []string{"add", "demo", "--dir", t.TempDir(), "--funnel", "--public", "--dry-run", "--json"}, wantExit: output.ExitConflict, wantCode: registry.CodeFunnelTypeConflict, wantNext: "tslink add --help"},
		{name: "public without funnel", args: []string{"add", "demo", "--proxy", "localhost:3000", "--public", "--dry-run", "--json"}, wantExit: output.ExitUsage, wantCode: "usage_error", wantNext: "tslink --help"},
		{name: "proxy target", args: []string{"add", "demo", "--proxy", "://bad", "--dry-run", "--json"}, wantExit: output.ExitUsage, wantCode: "usage_error", wantNext: "tslink --help"},
		{name: "tcp port", args: []string{"add", "demo", "--tcp", "localhost:99999", "--dry-run", "--json"}, wantExit: output.ExitUsage, wantCode: "usage_error", wantNext: "tslink --help"},
		{name: "control url", args: []string{"add", "demo", "--proxy", "localhost:3000", "--control-url", "not-a-url", "--dry-run", "--json"}, wantExit: output.ExitUsage, wantCode: "usage_error", wantNext: "tslink --help"},
		{name: "serve control url", args: []string{"serve", "--control-url", "not-a-url", "--json"}, wantExit: output.ExitUsage, wantCode: "usage_error", wantNext: "tslink --help"},
		{name: "config control url", args: []string{"config", "set", "control-url", "not-a-url", "--json"}, wantExit: output.ExitUsage, wantCode: "usage_error", wantNext: "tslink --help"},
		{name: "invite role enum", args: []string{"invite", "user", "alice@example.com", "--role", "owner", "--json"}, wantExit: output.ExitUsage, wantCode: registry.CodeInviteRoleInvalid, wantNext: "tslink invite user --help"},
		{name: "invite traversal id", args: []string{"invite", "revoke", "../device/nodeid-VICTIM", "--kind", "user", "--json"}, wantExit: output.ExitUsage, wantCode: registry.CodeInviteIDInvalid, wantNext: "tslink invite list --json"},
		{name: "invite missing kind", args: []string{"invite", "revoke", "12346", "--json"}, wantExit: output.ExitUsage, wantCode: registry.CodeInviteKindInvalid, wantNext: "tslink invite --help"},
		{name: "invite service missing", args: []string{"invite", "device", "missing", "alice@example.com", "--json"}, wantExit: output.ExitNotFound, wantCode: "not_found", wantNext: "tslink list --json"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr, exitCode := runCompiledTSLinkWithConfigDir(t, t.TempDir(), "", tc.args...)
			if stderr != "" || exitCode != tc.wantExit {
				t.Fatalf("exit=%d want=%d stderr=%q stdout=%s", exitCode, tc.wantExit, stderr, stdout)
			}
			results := parseCompiledJSONLines(t, stdout)
			if len(results) != 1 || results[0].Error == nil || results[0].Error.Code != tc.wantCode || results[0].Error.Code == "internal_error" || len(results[0].Error.Next) != 1 || results[0].Error.Next[0] != tc.wantNext {
				t.Fatalf("result=%+v, want code=%q exact next=%q", results, tc.wantCode, tc.wantNext)
			}
		})
	}
}

func TestCompiledEveryManifestJSONCommandProducesParseableEnvelope(t *testing.T) {
	groups := map[string]bool{
		"tslink": true, "tslink access": true, "tslink config": true,
		"tslink tags": true, "tslink template": true,
	}
	jsonCommands := 0
	mcpChecked := false
	for _, command := range Manifest().Commands {
		hasJSON := false
		for _, flag := range command.Flags {
			if flag.Name == "json" {
				hasJSON = true
				break
			}
		}
		if command.Path == "tslink mcp" {
			mcpChecked = true
			if hasJSON {
				t.Fatal("tslink mcp must not advertise --json; stdout is JSON-RPC only")
			}
			continue
		}
		if !hasJSON {
			t.Fatalf("manifest command %q neither supports --json nor declares the MCP exclusion", command.Path)
		}

		args := strings.Fields(strings.TrimPrefix(command.Path, "tslink"))
		args = append(args, "--json")
		if !groups[command.Path] {
			// Force Cobra's non-mutating flag-validation path for commands whose
			// normal execution could install, serve, or change local state.
			args = append(args, "--tslink-contract-probe-invalid-flag")
		}
		stdout, _, _ := runCompiledTSLinkWithConfigDir(t, t.TempDir(), "", args...)
		if results := parseCompiledJSONLines(t, stdout); len(results) != 1 {
			t.Fatalf("%s stdout=%q, want exactly one JSON envelope", command.Path, stdout)
		}
		jsonCommands++
	}
	if !mcpChecked || jsonCommands == 0 {
		t.Fatalf("traversal incomplete: json_commands=%d mcp_checked=%v", jsonCommands, mcpChecked)
	}
	t.Logf("manifest_json_commands=%d/%d parseable; tslink mcp explicitly excludes --json", jsonCommands, len(Manifest().Commands))
}

func registryServiceCount(t *testing.T, home string) int {
	t.Helper()
	regPath := filepath.Join(testenv.ConfigDir(home), "registry.json")
	reg, err := registry.Load(regPath)
	if err != nil {
		t.Fatalf("load registry: %v", err)
	}
	return len(reg.Services)
}

// TestCompiledRuntimePersistenceErrorEnvelope covers the envelope a command
// emits when the filesystem itself refuses the read: a directory sitting where
// registry.json belongs is not user input, so it must surface as
// internal_error/1 rather than a usage failure. This ran through `tslink api`
// until the api command was removed; `tslink list` reaches the same registry
// load through the shipped CLI.
func TestCompiledRuntimePersistenceErrorEnvelope(t *testing.T) {
	home := t.TempDir()
	regPath := filepath.Join(home, ".config", "tslink", "registry.json")
	if err := os.MkdirAll(regPath, 0o700); err != nil {
		t.Fatalf("mkdir registry path as directory: %v", err)
	}
	stdout, stderr, code := runCompiledTSLink(t, home, "", "list", "--json")
	if code != output.ExitError {
		t.Fatalf("exit = %d, want %d", code, output.ExitError)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	results := parseCompiledJSONLines(t, stdout)
	if len(results) != 1 || results[0].OK || results[0].Error == nil || results[0].Error.Code != "internal_error" {
		t.Fatalf("runtime failure result = %+v", results)
	}
}

// TestCompiledCLIValidationErrorsUseStableCodes pins each of these five
// rejections to its stable registry error code with a populated next[]. It was
// a CLI-versus-api parity test; the api half is gone with the command, and the
// CLI half is kept because no other compiled-binary test covers these five
// codes (TestCompiledUserInputErrorsNeverBecomeInternalError covers a disjoint
// set).
func TestCompiledCLIValidationErrorsUseStableCodes(t *testing.T) {
	tests := []struct {
		name     string
		cliArgs  []string
		wantCode string
	}{
		{"missing service type", []string{"add", "web", "--json"}, registry.CodeServiceTypeAmbiguous},
		{"invalid service name", []string{"add", "BAD_NAME", "--proxy", "localhost:3000", "--json"}, registry.CodeInvalidServiceName},
		{"invalid tag", []string{"add", "web", "--proxy", "localhost:3000", "--tags", "tag:Web", "--json"}, registry.CodeInvalidTag},
		{"tcp allow unsupported", []string{"add", "db", "--tcp", "localhost:5432", "--allow", "alice@example.com", "--json"}, registry.CodeAllowUnsupportedTCP},
		{"relative path", []string{"add", "docs", "--dir", "relative", "--json"}, registry.CodePathMustBeAbsolute},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cliOut, cliErr, cliExit := runCompiledTSLink(t, t.TempDir(), "", tc.cliArgs...)
			if cliErr != "" {
				t.Fatalf("stderr = %q, want empty", cliErr)
			}
			if cliExit != output.ExitUsage {
				t.Fatalf("exit = %d, want %d\nstdout=%s", cliExit, output.ExitUsage, cliOut)
			}
			cliResults := parseCompiledJSONLines(t, cliOut)
			if len(cliResults) != 1 || cliResults[0].Error == nil {
				t.Fatalf("results = %+v, want one failure envelope", cliResults)
			}
			if cliResults[0].Error.Code != tc.wantCode {
				t.Fatalf("error.code = %q, want %q", cliResults[0].Error.Code, tc.wantCode)
			}
			if len(cliResults[0].Error.Next) == 0 {
				t.Fatalf("next missing: %+v", cliResults[0].Error)
			}
		})
	}
}

func compiledResultDataJSON(t *testing.T, stdout string) []byte {
	t.Helper()
	var envelope struct {
		OK   bool            `json:"ok"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &envelope); err != nil {
		t.Fatalf("decode result: %v\nstdout=%s", err, stdout)
	}
	if !envelope.OK || len(envelope.Data) == 0 {
		t.Fatalf("result = %s, want one success with data", stdout)
	}
	return append([]byte(nil), envelope.Data...)
}

// TestCompiledStatusJSONCarriesDataForASeededRegistry keeps the one thing the
// deleted CLI-versus-api byte-isomorphism test uniquely covered: `status` run
// through the shipped binary against a registry that actually holds a service
// returns a success envelope with a non-empty data object. Every other
// compiled-binary status invocation in this package runs against an empty
// config dir or forces the flag-validation error path.
func TestCompiledStatusJSONCarriesDataForASeededRegistry(t *testing.T) {
	configDir := t.TempDir()
	regPath := filepath.Join(configDir, "registry.json")
	if _, err := registry.Add(regPath, registry.Service{Name: "web", Type: registry.TypeProxy, Target: "http://localhost:3000"}); err != nil {
		t.Fatalf("registry.Add: %v", err)
	}
	for _, tc := range []struct {
		name    string
		cliArgs []string
	}{
		{"status", []string{"status", "--json"}},
		{"status urls", []string{"status", "--urls", "--json"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cliOut, cliErr, cliExit := runCompiledTSLinkWithConfigDir(t, configDir, "", tc.cliArgs...)
			if cliExit != 0 || cliErr != "" {
				t.Fatalf("cli exit=%d err=%q stdout=%s", cliExit, cliErr, cliOut)
			}
			if data := compiledResultDataJSON(t, cliOut); !bytes.Contains(data, []byte(`"web"`)) {
				t.Fatalf("status data = %s, want the seeded service to appear", data)
			}
		})
	}
}

func TestCompiledAgentE2EAddURLListRemove(t *testing.T) {
	configDir := t.TempDir()
	binary := compiledTSLinkBinary(t)
	addOut, addErr, addExit := runTSLinkBinaryWithConfigDir(t, binary, configDir, "", "add", "e2e-app", "--proxy", "localhost:3000", "--json")
	if addExit != 0 || !strings.Contains(addErr, "--no-daemon-install") {
		t.Fatalf("add exit=%d stderr=%q stdout=%s", addExit, addErr, addOut)
	}
	t.Logf("add stdout: %s", strings.TrimSpace(addOut))

	regPath := filepath.Join(configDir, "registry.json")
	reg, err := registry.Load(regPath)
	if err != nil {
		t.Fatalf("load registry: %v", err)
	}
	if len(reg.Services) != 1 {
		t.Fatalf("load registry: services=%d, want 1", len(reg.Services))
	}
	// Build the fixture as the real module main package so E's product check sees
	// the same Go module identity as a released tslink executable. The overlay
	// replaces only main() with a test process that writes its own PID identity
	// sidecar and blocks; argv still identifies the process as a serve daemon.
	daemonFixture := exec.Command(compiledDaemonIdentityFixture(t), "serve")
	daemonFixture.Env = append(os.Environ(), "TSLINK_CONFIG_DIR="+configDir, "TSLINK_DISABLE_KEYRING=1")
	daemonInput, err := daemonFixture.StdinPipe()
	if err != nil {
		t.Fatalf("create daemon fixture input: %v", err)
	}
	daemonOutput, err := daemonFixture.StdoutPipe()
	if err != nil {
		t.Fatalf("create daemon fixture output: %v", err)
	}
	var daemonStderr bytes.Buffer
	daemonFixture.Stderr = &daemonStderr
	if err := daemonFixture.Start(); err != nil {
		t.Fatalf("start daemon identity fixture: %v", err)
	}
	daemonWaited := false
	t.Cleanup(func() {
		_ = daemonInput.Close()
		if !daemonWaited {
			_ = daemonFixture.Wait()
		}
	})

	pidPath := filepath.Join(configDir, "tslink.pid")
	ready, readyErr := bufio.NewReader(daemonOutput).ReadString('\n')
	if readyErr != nil || ready != "ready\n" {
		_ = daemonInput.Close()
		waitErr := daemonFixture.Wait()
		daemonWaited = true
		t.Fatalf("daemon identity fixture ready=%q read_error=%v wait_error=%v stderr=%q", ready, readyErr, waitErr, daemonStderr.String())
	}
	fixturePID, err := daemon.ReadPID(pidPath)
	if err != nil {
		t.Fatalf("read daemon identity fixture PID: %v", err)
	}
	if fixturePID != daemonFixture.Process.Pid {
		t.Fatalf("daemon identity fixture PID = %d, want %d", fixturePID, daemonFixture.Process.Pid)
	}
	if !daemon.IsRunning(pidPath) {
		t.Fatal("daemon identity fixture was not accepted as a running TSLink serve daemon")
	}
	pidInfo, err := os.Stat(pidPath)
	if err != nil {
		t.Fatalf("stat pid fixture: %v", err)
	}
	fingerprint, err := tsruntime.RegistryFingerprint(reg)
	if err != nil {
		t.Fatalf("fingerprint: %v", err)
	}
	snapshot := tsruntime.NewSnapshot(daemonFixture.Process.Pid, pidInfo.ModTime(), fingerprint, pidInfo.ModTime().Add(time.Second), []tsruntime.ServiceState{{
		Service: reg.Services[0], RuntimeHost: "e2e-app.tailnet-example.ts.net",
	}})
	if err := tsruntime.Save(filepath.Join(configDir, "runtime.json"), snapshot); err != nil {
		t.Fatalf("save runtime fixture: %v", err)
	}

	urlOut, urlErr, urlExit := runTSLinkBinaryWithConfigDir(t, binary, configDir, "", "url", "e2e-app", "--raw")
	if urlExit != 0 || urlErr != "" || urlOut != "https://e2e-app.tailnet-example.ts.net\n" || len(urlOut) >= 60 {
		t.Fatalf("url exit=%d stderr=%q stdout=%q bytes=%d", urlExit, urlErr, urlOut, len(urlOut))
	}
	t.Logf("url stdout: %s", strings.TrimSpace(urlOut))

	listOut, listErr, listExit := runTSLinkBinaryWithConfigDir(t, binary, configDir, "", "list", "--name", "e2e-app", "--fields", "name,url", "--json")
	if listExit != 0 || listErr != "" || !strings.Contains(listOut, `"url":"https://e2e-app.tailnet-example.ts.net"`) {
		t.Fatalf("list exit=%d stderr=%q stdout=%s", listExit, listErr, listOut)
	}
	t.Logf("list stdout: %s", strings.TrimSpace(listOut))

	removeOut, removeErr, removeExit := runTSLinkBinaryWithConfigDir(t, binary, configDir, "", "remove", "e2e-app", "--json")
	if removeExit != 0 || removeErr != "" || registryServiceCountAtPath(t, regPath) != 0 {
		t.Fatalf("remove exit=%d stderr=%q stdout=%s", removeExit, removeErr, removeOut)
	}
	t.Logf("remove stdout: %s", strings.TrimSpace(removeOut))
}

func registryServiceCountAtPath(t *testing.T, regPath string) int {
	t.Helper()
	reg, err := registry.Load(regPath)
	if err != nil {
		t.Fatalf("load registry: %v", err)
	}
	return len(reg.Services)
}
