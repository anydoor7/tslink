package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/monody0007/tslink/internal/output"
)

// Cross-platform half of the e2e scaffolding: the helpers that run a
// compiled binary once and decode its envelope. Everything that starts a
// long-lived child, enumerates processes by absolute path, or reaps by PID lives
// in e2e_scaffold_unix_test.go, which is POSIX-only. Splitting on that line is
// what keeps the Windows build free of unused symbols.

const (
	// e2eCommandTimeout bounds every non-daemon CLI invocation so a hung child
	// fails the test instead of hanging the package.
	e2eCommandTimeout = 30 * time.Second
)

// e2eEnv builds the isolated child environment shared by every e2e invocation.
// It mirrors runTSLinkBinaryWithConfigDir and adds nothing that could reach a
// real credential store or a real control plane.
func e2eEnv(configDir string, extra ...string) []string {
	env := append(os.Environ(),
		"TSLINK_CONFIG_DIR="+configDir,
		"TSLINK_DISABLE_KEYRING=1",
		"TSLINK_API_KEY=",
		"TSLINK_CLIENT_SECRET=",
		testDaemonParentLifetimeEnv+"=1",
	)
	return append(env, extra...)
}

type e2eRun struct {
	Stdout   string
	Stderr   string
	ExitCode int
	Elapsed  time.Duration
}

// e2eRunBinary invokes a compiled tslink binary with a bounded deadline.
func e2eRunBinary(t *testing.T, binary, configDir, stdin string, env []string, args ...string) e2eRun {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), e2eCommandTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, binary, offlineRegistrationArgs(args)...)
	cmd.Env = env
	cmd.Stdin = strings.NewReader(stdin)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	start := time.Now()
	runErr := cmd.Run()
	elapsed := time.Since(start)

	result := e2eRun{Stdout: outBuf.String(), Stderr: errBuf.String(), Elapsed: elapsed}
	if runErr != nil {
		exitErr, ok := runErr.(*exec.ExitError)
		if !ok {
			t.Fatalf("run %s %v: %v (stderr=%q)", filepath.Base(binary), args, runErr, errBuf.String())
		}
		result.ExitCode = exitErr.ExitCode()
	}
	if ctx.Err() != nil {
		t.Fatalf("run %s %v exceeded %s", filepath.Base(binary), args, e2eCommandTimeout)
	}
	return result
}

// Config isolation alone does not isolate the per-user supervisor slot.
// Existing binary contracts exercise registration, not host installation.
// Bootstrap integration tests use their own explicit fake manager process.
func offlineRegistrationArgs(args []string) []string {
	if len(args) > 0 && (args[0] == "add" || (args[0] == "template" && len(args) > 1 && args[1] == "apply")) {
		result := append(append([]string{}, args...), "--no-daemon-install")
		if args[0] == "add" {
			for _, arg := range args {
				if arg == "--wait" || strings.HasPrefix(arg, "--wait=") {
					return result
				}
			}
			result = append(result, "--wait=0")
		}
		return result
	}
	return args
}

// e2eDecodeEnvelope requires stdout to be exactly one well-formed result
// envelope and returns it together with its raw data object.
func e2eDecodeEnvelope(t *testing.T, run e2eRun, context string) (output.Result, map[string]any) {
	t.Helper()
	results := parseCompiledJSONLines(t, run.Stdout)
	if len(results) != 1 {
		t.Fatalf("%s: stdout envelope count = %d, want 1\nstdout=%s", context, len(results), run.Stdout)
	}
	data := map[string]any{}
	if results[0].Data != nil {
		raw, err := json.Marshal(results[0].Data)
		if err != nil {
			t.Fatalf("%s: marshal data: %v", context, err)
		}
		if err := json.Unmarshal(raw, &data); err != nil {
			t.Fatalf("%s: decode data object: %v\nraw=%s", context, err, raw)
		}
	}
	return results[0], data
}
