package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/monody0007/tslink/internal/output"
	"github.com/monody0007/tslink/internal/registry"
	tsruntime "github.com/monody0007/tslink/internal/runtime"
)

var (
	tslinkBinaryOnce sync.Once
	tslinkBinaryPath string
	tslinkBinaryErr  error
)

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

func runCompiledTSLink(t *testing.T, home, stdin string, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	cmd := exec.Command(compiledTSLinkBinary(t), args...)
	cmd.Env = append(os.Environ(), "HOME="+home, "TSLINK_CONFIG_DIR=", "TSLINK_DISABLE_KEYRING=1")
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
	cmd := exec.Command(binary, args...)
	cmd.Env = append(os.Environ(), "TSLINK_CONFIG_DIR="+configDir, "TSLINK_DISABLE_KEYRING=1")
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

func registryServiceCount(t *testing.T, home string) int {
	t.Helper()
	regPath := filepath.Join(home, ".config", "tslink", "registry.json")
	reg, err := registry.Load(regPath)
	if err != nil {
		t.Fatalf("load registry: %v", err)
	}
	return len(reg.Services)
}

func TestCompiledAPIJSONLProcessStatusAndOrder(t *testing.T) {
	successAdd := `{"action":"add","name":"web","type":"proxy","target":"http://localhost:3000"}`
	unknown := `{"action":"unknown"}`

	tests := []struct {
		name         string
		stdin        string
		wantExit     int
		wantOK       []bool
		wantErrCodes []string
		wantRegCount int
	}{
		{"empty input succeeds silently", "", 0, nil, nil, 0},
		{"blank lines are ignored", "\n  \n\t\n", 0, nil, nil, 0},
		{"one success", `{"action":"list"}` + "\n", 0, []bool{true}, []string{""}, 0},
		{"one failure", unknown + "\n", output.ExitUsage, []bool{false}, []string{"usage_error"}, 0},
		{"only failures", "{not-json}\n" + unknown + "\n", output.ExitUsage, []bool{false, false}, []string{"usage_error", "usage_error"}, 0},
		{"mixed success then failure", successAdd + "\n" + unknown + "\n", output.ExitUsage, []bool{true, false}, []string{"", "usage_error"}, 1},
		{"mixed failure then success", unknown + "\n" + successAdd + "\n", output.ExitUsage, []bool{false, true}, []string{"usage_error", ""}, 1},
		{"unknown field", `{"action":"status","unknown":true}` + "\n", output.ExitUsage, []bool{false}, []string{"usage_error"}, 0},
		{"missing target", `{"action":"add","name":"web","type":"proxy"}` + "\n", output.ExitUsage, []bool{false}, []string{"usage_error"}, 0},
		{"invalid type", `{"action":"add","name":"web","type":"bogus","target":"http://localhost:3000"}` + "\n", output.ExitUsage, []bool{false}, []string{"usage_error"}, 0},
		{"truncated final input", `{"action":"list"`, output.ExitUsage, []bool{false}, []string{"usage_error"}, 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			stdout, stderr, code := runCompiledTSLink(t, home, tc.stdin, "api")
			if code != tc.wantExit {
				t.Fatalf("exit = %d, want %d\nstdout=%s\nstderr=%s", code, tc.wantExit, stdout, stderr)
			}
			if stderr != "" {
				t.Fatalf("stderr = %q, want empty", stderr)
			}
			results := parseCompiledJSONLines(t, stdout)
			if len(results) != len(tc.wantOK) {
				t.Fatalf("record count = %d, want %d\nstdout=%s", len(results), len(tc.wantOK), stdout)
			}
			for i, result := range results {
				if result.OK != tc.wantOK[i] {
					t.Fatalf("record %d ok = %v, want %v", i, result.OK, tc.wantOK[i])
				}
				gotCode := ""
				if result.Error != nil {
					gotCode = result.Error.Code
				}
				if gotCode != tc.wantErrCodes[i] {
					t.Fatalf("record %d error code = %q, want %q", i, gotCode, tc.wantErrCodes[i])
				}
			}
			if got := registryServiceCount(t, home); got != tc.wantRegCount {
				t.Fatalf("registry service count = %d, want %d", got, tc.wantRegCount)
			}
		})
	}
}

func TestCompiledAPIJSONLRecordSizeLimit(t *testing.T) {
	base := `{"action":"list"}`
	makeRecord := func(size int) string {
		return base + strings.Repeat(" ", size-len(base))
	}

	tests := []struct {
		name      string
		recordLen int
		wantExit  int
		wantCount int
		wantOK    bool
	}{
		{"limit minus one", apiMaxRecordBytes - 1, 0, 1, true},
		{"limit", apiMaxRecordBytes, 0, 1, true},
		{"limit plus one", apiMaxRecordBytes + 1, output.ExitUsage, 1, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			stdin := makeRecord(tc.recordLen) + "\n"
			stdout, stderr, code := runCompiledTSLink(t, t.TempDir(), stdin, "api")
			if code != tc.wantExit {
				t.Fatalf("exit = %d, want %d", code, tc.wantExit)
			}
			if stderr != "" {
				t.Fatalf("stderr = %q, want empty", stderr)
			}
			results := parseCompiledJSONLines(t, stdout)
			if len(results) != tc.wantCount {
				t.Fatalf("record count = %d, want %d", len(results), tc.wantCount)
			}
			if results[0].OK != tc.wantOK {
				t.Fatalf("record ok = %v, want %v", results[0].OK, tc.wantOK)
			}
			if !tc.wantOK && (results[0].Error == nil || results[0].Error.Code != "usage_error") {
				t.Fatalf("oversize error = %+v, want usage_error", results[0].Error)
			}
		})
	}
}

func TestCompiledAPIRuntimePersistenceErrorEnvelope(t *testing.T) {
	home := t.TempDir()
	regPath := filepath.Join(home, ".config", "tslink", "registry.json")
	if err := os.MkdirAll(regPath, 0o700); err != nil {
		t.Fatalf("mkdir registry path as directory: %v", err)
	}
	stdout, stderr, code := runCompiledTSLink(t, home, `{"action":"list"}`+"\n", "api")
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

func TestCompiledCLIAndAPIValidationErrorsMatch(t *testing.T) {
	tests := []struct {
		name     string
		cliArgs  []string
		apiInput string
		wantCode string
	}{
		{"missing service type", []string{"add", "web", "--json"}, `{"action":"add","name":"web"}` + "\n", registry.CodeServiceTypeAmbiguous},
		{"invalid service name", []string{"add", "BAD_NAME", "--proxy", "localhost:3000", "--json"}, `{"action":"add","name":"BAD_NAME","type":"proxy","target":"localhost:3000"}` + "\n", registry.CodeInvalidServiceName},
		{"invalid tag", []string{"add", "web", "--proxy", "localhost:3000", "--tags", "tag:Web", "--json"}, `{"action":"add","name":"web","type":"proxy","target":"localhost:3000","tags":["tag:Web"]}` + "\n", registry.CodeInvalidTag},
		{"tcp allow unsupported", []string{"add", "db", "--tcp", "localhost:5432", "--allow", "alice@example.com", "--json"}, `{"action":"add","name":"db","type":"tcp","target":"localhost:5432","allow":["alice@example.com"]}` + "\n", registry.CodeAllowUnsupportedTCP},
		{"relative path", []string{"add", "docs", "--dir", "relative", "--json"}, `{"action":"add","name":"docs","type":"file","path":"relative"}` + "\n", registry.CodePathMustBeAbsolute},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cliOut, cliErr, cliExit := runCompiledTSLink(t, t.TempDir(), "", tc.cliArgs...)
			apiOut, apiErr, apiExit := runCompiledTSLink(t, t.TempDir(), tc.apiInput, "api")
			if cliErr != "" || apiErr != "" {
				t.Fatalf("stderr cli=%q api=%q", cliErr, apiErr)
			}
			if cliExit != output.ExitUsage || apiExit != output.ExitUsage || cliExit != apiExit {
				t.Fatalf("exit cli=%d api=%d, want %d", cliExit, apiExit, output.ExitUsage)
			}
			cliResults := parseCompiledJSONLines(t, cliOut)
			apiResults := parseCompiledJSONLines(t, apiOut)
			if len(cliResults) != 1 || len(apiResults) != 1 || cliResults[0].Error == nil || apiResults[0].Error == nil {
				t.Fatalf("results cli=%+v api=%+v", cliResults, apiResults)
			}
			if cliResults[0].Error.Code != tc.wantCode || apiResults[0].Error.Code != tc.wantCode {
				t.Fatalf("error.code cli=%q api=%q, want %q", cliResults[0].Error.Code, apiResults[0].Error.Code, tc.wantCode)
			}
			if len(cliResults[0].Error.Next) == 0 || len(apiResults[0].Error.Next) == 0 {
				t.Fatalf("next missing cli=%+v api=%+v", cliResults[0].Error, apiResults[0].Error)
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

func TestCompiledCLIAndAPIStatusDataAreByteIsomorphic(t *testing.T) {
	configDir := t.TempDir()
	regPath := filepath.Join(configDir, "registry.json")
	if _, err := registry.Add(regPath, registry.Service{Name: "web", Type: registry.TypeProxy, Target: "http://localhost:3000"}); err != nil {
		t.Fatalf("registry.Add: %v", err)
	}
	for _, tc := range []struct {
		name     string
		cliArgs  []string
		apiInput string
	}{
		{"status", []string{"status", "--json"}, `{"action":"status"}` + "\n"},
		{"status urls", []string{"status", "--urls", "--json"}, `{"action":"status","urls":true}` + "\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cliOut, cliErr, cliExit := runCompiledTSLinkWithConfigDir(t, configDir, "", tc.cliArgs...)
			apiOut, apiErr, apiExit := runCompiledTSLinkWithConfigDir(t, configDir, tc.apiInput, "api")
			if cliExit != 0 || apiExit != 0 || cliErr != "" || apiErr != "" {
				t.Fatalf("cli exit=%d err=%q; api exit=%d err=%q", cliExit, cliErr, apiExit, apiErr)
			}
			cliData := compiledResultDataJSON(t, cliOut)
			apiData := compiledResultDataJSON(t, apiOut)
			if !bytes.Equal(cliData, apiData) {
				t.Fatalf("data mismatch\ncli=%s\napi=%s", cliData, apiData)
			}
		})
	}
}

func TestCompiledAgentE2EAddURLListRemove(t *testing.T) {
	configDir := t.TempDir()
	if requested := os.Getenv("TSLINK_E2E_CONFIG_DIR"); requested != "" {
		if filepath.Clean(requested) != "/tmp/tslink-verify-A" {
			t.Fatalf("TSLINK_E2E_CONFIG_DIR = %q, only /tmp/tslink-verify-A is accepted", requested)
		}
		if err := os.MkdirAll(requested, 0o700); err != nil {
			t.Fatalf("create requested E2E config dir: %v", err)
		}
		entries, err := os.ReadDir(requested)
		if err != nil {
			t.Fatalf("read requested E2E config dir: %v", err)
		}
		if len(entries) != 0 {
			t.Fatalf("requested E2E config dir must start empty: %s", requested)
		}
		configDir = requested
	}
	binary := compiledTSLinkBinary(t)
	if requested := os.Getenv("TSLINK_E2E_BINARY"); requested != "" {
		if !filepath.IsAbs(requested) {
			t.Fatalf("TSLINK_E2E_BINARY must be absolute: %q", requested)
		}
		binary = requested
	}
	addOut, addErr, addExit := runTSLinkBinaryWithConfigDir(t, binary, configDir, "", "add", "e2e-app", "--proxy", "localhost:3000", "--json")
	if addExit != 0 || addErr != "" {
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
	daemonFixture := exec.Command(binary, "api")
	daemonFixture.Env = append(os.Environ(), "TSLINK_CONFIG_DIR="+configDir, "TSLINK_DISABLE_KEYRING=1")
	daemonInput, err := daemonFixture.StdinPipe()
	if err != nil {
		t.Fatalf("create daemon fixture input: %v", err)
	}
	if err := daemonFixture.Start(); err != nil {
		t.Fatalf("start daemon identity fixture: %v", err)
	}
	t.Cleanup(func() {
		_ = daemonInput.Close()
		_ = daemonFixture.Wait()
	})

	pidPath := filepath.Join(configDir, "tslink.pid")
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(daemonFixture.Process.Pid)), 0o600); err != nil {
		t.Fatalf("write pid fixture: %v", err)
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
		Service: reg.Services[0], RuntimeHost: "node.example.ts.net",
	}})
	if err := tsruntime.Save(filepath.Join(configDir, "runtime.json"), snapshot); err != nil {
		t.Fatalf("save runtime fixture: %v", err)
	}

	urlOut, urlErr, urlExit := runTSLinkBinaryWithConfigDir(t, binary, configDir, "", "url", "e2e-app", "--raw")
	if urlExit != 0 || urlErr != "" || urlOut != "https://node.example.ts.net\n" || len(urlOut) >= 60 {
		t.Fatalf("url exit=%d stderr=%q stdout=%q bytes=%d", urlExit, urlErr, urlOut, len(urlOut))
	}
	t.Logf("url stdout: %s", strings.TrimSpace(urlOut))

	listOut, listErr, listExit := runTSLinkBinaryWithConfigDir(t, binary, configDir, "", "list", "--name", "e2e-app", "--fields", "name,url", "--json")
	if listExit != 0 || listErr != "" || !strings.Contains(listOut, `"url":"https://node.example.ts.net"`) {
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
