package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/monody0007/tslink/internal/output"
	"github.com/monody0007/tslink/internal/registry"
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
	cmd.Env = append(os.Environ(), "HOME="+home, "TSLINK_DISABLE_KEYRING=1")
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
